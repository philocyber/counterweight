package execution

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func writeNewlines(t *testing.T, path string, size int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	chunk := bytes.Repeat([]byte("\n"), 1<<20)
	for written := 0; written < size; written += len(chunk) {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
}

func limitsFixture(t *testing.T) (string, *Files) {
	t.Helper()
	root := t.TempDir()
	f, err := Open(Roots{Read: []string{root}, Write: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return root, f
}

func allocatedDuring(fn func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	fn()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

func TestConfinedGrepSkipsOversizedFiles(t *testing.T) {
	root, f := limitsFixture(t)
	writeNewlines(t, filepath.Join(root, "huge.txt"), 48<<20)
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var r actool.Result
	used := allocatedDuring(func() {
		r = callFile(t, f.Tool(actool.NewGrep()), root, map[string]any{"pattern": "needle"})
	})
	out := r.Flatten()
	if r.IsError || !strings.Contains(out, "small.txt") || strings.Contains(out, "huge.txt") || !strings.Contains(out, "were not searched") {
		t.Fatalf("unexpected grep result (error=%v): %s", r.IsError, out)
	}
	if used > 24<<20 {
		t.Fatalf("grep allocated %d bytes for an oversized file", used)
	}
	r = callFile(t, f.Tool(actool.NewGrep()), root, map[string]any{"pattern": "needle", "path": "huge.txt"})
	if r.IsError || !strings.Contains(r.Flatten(), "No matches found") {
		t.Fatalf("direct grep of an oversized file: %s", r.Flatten())
	}
}

func TestConfinedReadOfHugeFileIsWindowed(t *testing.T) {
	root, f := limitsFixture(t)
	writeNewlines(t, filepath.Join(root, "huge.txt"), 48<<20)
	var r actool.Result
	used := allocatedDuring(func() {
		r = callFile(t, f.Tool(actool.NewRead()), root, map[string]any{"file_path": "huge.txt", "offset": 5, "limit": 3})
	})
	out := r.Flatten()
	if r.IsError || !strings.Contains(out, "     5\t") || !strings.Contains(out, "     7\t") || strings.Contains(out, "     8\t") {
		t.Fatalf("unexpected read result: %s", out)
	}
	if used > 8<<20 {
		t.Fatalf("read allocated %d bytes", used)
	}
	used = allocatedDuring(func() {
		r = callFile(t, f.Tool(actool.NewRead()), root, map[string]any{"file_path": "huge.txt", "limit": 1 << 30})
	})
	if r.IsError || !strings.Contains(r.Flatten(), "Read truncated") || used > 8<<20 {
		t.Fatalf("unbounded limit: error=%v allocated=%d", r.IsError, used)
	}
}

func TestConfinedReadKeepsLineSemantics(t *testing.T) {
	root, f := limitsFixture(t)
	long := strings.Repeat("x", maxLineLen+500)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"+long+"\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte("ab\x00cd"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callFile(t, f.Tool(actool.NewRead()), root, map[string]any{"file_path": "a.txt"}).Flatten()
	want := "     1\tone\n     2\t" + long[:maxLineLen] + "… [line truncated]\n     3\tthree\n     4\t\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	if out := callFile(t, f.Tool(actool.NewRead()), root, map[string]any{"file_path": "bin.dat"}).Flatten(); !strings.Contains(out, "binary file bin.dat not shown — 5 bytes") {
		t.Fatal(out)
	}
	if out := callFile(t, f.Tool(actool.NewRead()), root, map[string]any{"file_path": "a.txt", "offset": 99}).Flatten(); !strings.Contains(out, "no lines in the requested range") {
		t.Fatal(out)
	}
}

func TestConfinedEditRefusesOversizedFile(t *testing.T) {
	root, f := limitsFixture(t)
	writeNewlines(t, filepath.Join(root, "huge.txt"), maxEditBytes+1<<20)
	for name, in := range map[string]map[string]any{
		"Edit":      {"file_path": "huge.txt", "old_string": "a", "new_string": "b"},
		"MultiEdit": {"file_path": "huge.txt", "edits": []map[string]any{{"old_string": "a", "new_string": "b"}}},
	} {
		tool := actool.CoreTool(actool.NewEdit())
		if name == "MultiEdit" {
			tool = actool.NewMultiEdit()
		}
		r := callFile(t, f.Tool(tool), root, in)
		if !r.IsError || !strings.Contains(r.Flatten(), "cannot be edited") {
			t.Fatalf("%s: %s", name, r.Flatten())
		}
	}
}

func TestConfinedMultilineGrepManyMatches(t *testing.T) {
	root, f := limitsFixture(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\nfoo\nbar\nbaz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := callFile(t, f.Tool(actool.NewGrep()), root, map[string]any{"pattern": "foo\nbar", "multiline": true, "output_mode": "content", "-n": true})
	if out := r.Flatten(); out != "a.txt:2:foo\na.txt:3:bar\n" {
		t.Fatalf("got %q", out)
	}
	big := bytes.Repeat([]byte("\n"), 1<<20)
	if err := os.WriteFile(filepath.Join(root, "nl.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	// Every newline is a match; this must not scale with matches times lines.
	callFile(t, f.Tool(actool.NewGrep()), root, map[string]any{"pattern": "\n", "multiline": true, "path": "nl.txt", "output_mode": "count"})
}

func TestConfinedWriteRefusesHardLinkToGitPointer(t *testing.T) {
	root, f := limitsFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: pointer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, ".git"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	for _, tool := range []actool.CoreTool{actool.NewWrite(), actool.NewEdit(), actool.NewMultiEdit()} {
		in := map[string]any{"file_path": "alias", "content": "bad", "old_string": "gitdir", "new_string": "bad", "edits": []map[string]any{{"old_string": "gitdir", "new_string": "bad"}}}
		if r := callFile(t, f.Tool(tool), root, in); !r.IsError {
			t.Errorf("%s through a hard link to the git pointer was accepted", tool.Name())
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".git")); string(b) != "gitdir: pointer" {
		t.Fatal("git pointer was modified")
	}
	if r := callFile(t, f.Tool(actool.NewWrite()), root, map[string]any{"file_path": "ok.txt", "content": "fine"}); r.IsError {
		t.Fatal(r.Flatten())
	}
}

func TestConfinedGrepSkipsUnreadableEntries(t *testing.T) {
	root, f := limitsFixture(t)
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	_ = os.Symlink(filepath.Join(root, "dir"), filepath.Join(root, "dirlink"))
	_ = os.Symlink(t.TempDir(), filepath.Join(root, "outside"))
	r := callFile(t, f.Tool(actool.NewGrep()), root, map[string]any{"pattern": "needle"})
	if r.IsError || !strings.Contains(r.Flatten(), "small.txt") {
		t.Fatalf("links that cannot be read denied the whole search (error=%v): %s", r.IsError, r.Flatten())
	}
	// Naming such an entry as the search path is still refused.
	if r := callFile(t, f.Tool(actool.NewGrep()), root, map[string]any{"pattern": "needle", "path": "dangling"}); !r.IsError {
		t.Fatalf("search of a dangling link: %s", r.Flatten())
	}
}

package codesrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }

func TestParseRepoAcceptsForms(t *testing.T) {
	cases := map[string]Repo{
		"Philocyber/counterweight":                             {Owner: "Philocyber", Name: "counterweight"},
		"github.com/Philocyber/counterweight":                  {Owner: "Philocyber", Name: "counterweight"},
		"https://github.com/Philocyber/counterweight":          {Owner: "Philocyber", Name: "counterweight"},
		"https://github.com/Philocyber/counterweight.git":      {Owner: "Philocyber", Name: "counterweight"},
		"https://github.com/Philocyber/counterweight/":         {Owner: "Philocyber", Name: "counterweight"},
		"https://github.com/Philocyber/counterweight/tree/dev": {Owner: "Philocyber", Name: "counterweight", Ref: "dev"},
		"Philocyber/counterweight#v1.2.0":                      {Owner: "Philocyber", Name: "counterweight", Ref: "v1.2.0"},
	}
	for in, want := range cases {
		got, err := ParseRepo(in)
		if err != nil {
			t.Fatalf("ParseRepo(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("ParseRepo(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestParseRepoRejects(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"justname",
		"owner/name/extra",
		"https://gitlab.com/owner/name",
		"https://evil.com/github.com/owner/name",
		"git@github.com:owner/name.git",
		"ssh://github.com/owner/name",
		"https://user:pass@github.com/owner/name",
		"-flag/name",
		"owner/-flag",
		"owner/na me",
		"owner/name#..\\etc",
		"owner/name#-x",
	}
	for _, in := range bad {
		if _, err := ParseRepo(in); err == nil {
			t.Errorf("ParseRepo(%q) accepted, want error", in)
		}
	}
}

func TestCloneURLFixedHost(t *testing.T) {
	r := Repo{Owner: "a", Name: "b"}
	if got := r.CloneURL(); got != "https://github.com/a/b.git" {
		t.Fatalf("CloneURL = %q", got)
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": ModeBalanced, "light": ModeLight, "BALANCED": ModeBalanced, "Comprehensive": ModeComprehensive} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("deep"); err == nil {
		t.Error("ParseMode(deep) accepted, want error")
	}
}

func TestCloneBuildsSafeArgs(t *testing.T) {
	var gotArgs, gotEnv []string
	run := func(ctx context.Context, name string, args []string, env []string, dir string) error {
		if name != "git" {
			t.Fatalf("command = %q, want git", name)
		}
		gotArgs, gotEnv = args, env
		return nil
	}
	dest := filepath.Join(t.TempDir(), "clone")
	repo := Repo{Owner: "Philocyber", Name: "counterweight", Ref: "main"}

	if _, err := Clone(context.Background(), CloneOptions{Repo: repo, Mode: ModeComprehensive, Dest: dest, Token: "secret", Run: run}); err != nil {
		t.Fatalf("Clone error: %v", err)
	}

	joined := strings.Join(gotArgs, " ")
	// Fixed HTTPS URL, options terminated, branch set, token in extraheader not URL.
	if !strings.Contains(joined, "-- https://github.com/Philocyber/counterweight.git "+dest) {
		t.Errorf("args missing terminated URL+dest: %v", gotArgs)
	}
	if !strings.Contains(joined, "--branch main") {
		t.Errorf("args missing branch: %v", gotArgs)
	}
	if strings.Contains(joined, "secret@") || strings.Contains(joined, "//secret") {
		t.Errorf("token leaked into URL: %v", gotArgs)
	}
	if strings.Contains(joined, "secret") {
		t.Errorf("token leaked into argv: %v", gotArgs)
	}
	envJoined := strings.Join(gotEnv, "\n")
	if !strings.Contains(envJoined, "GIT_CONFIG_KEY_0=http.https://github.com/Philocyber/counterweight.git.extraheader") ||
		!strings.Contains(envJoined, "GIT_CONFIG_VALUE_0=AUTHORIZATION: bearer secret") {
		t.Errorf("token not passed as extraheader via env: %v", gotEnv)
	}
	// Comprehensive = full clone, no --depth.
	if strings.Contains(joined, "--depth") {
		t.Errorf("comprehensive should be full clone: %v", gotArgs)
	}
}

func TestCloneShallowForLight(t *testing.T) {
	var gotArgs []string
	run := func(ctx context.Context, name string, args []string, env []string, dir string) error {
		gotArgs = args
		return nil
	}
	dest := filepath.Join(t.TempDir(), "c")
	if _, err := Clone(context.Background(), CloneOptions{Repo: Repo{Owner: "a", Name: "b"}, Mode: ModeLight, Dest: dest, Run: run}); err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "--depth 1") {
		t.Errorf("light should be shallow: %v", gotArgs)
	}
}

func TestCloneRefusesNonEmptyDest(t *testing.T) {
	dir := t.TempDir() // exists and, after we add a file, non-empty
	if err := writeFile(filepath.Join(dir, "x"), "y"); err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, name string, args []string, env []string, dir string) error {
		t.Fatal("git should not run for a non-empty dest")
		return nil
	}
	if _, err := Clone(context.Background(), CloneOptions{Repo: Repo{Owner: "a", Name: "b"}, Dest: dir, Run: run}); err == nil {
		t.Error("Clone accepted non-empty dest, want error")
	}
}

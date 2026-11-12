package recontools

import (
	"archive/zip"
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSums(t *testing.T) {
	raw := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  subfinder_2.6.3_linux_amd64.zip\n" +
		"cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe *dnsx_1.1.1_windows_amd64.zip\n" +
		"not a checksum line at all\n" +
		"\n"
	sums := parseSums(raw)
	if len(sums) != 2 {
		t.Fatalf("expected 2 parsed entries, got %d: %v", len(sums), sums)
	}
	if sums["subfinder_2.6.3_linux_amd64.zip"] != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("unexpected sum for subfinder entry: %v", sums)
	}
	// Binary-mode "*" prefix on the filename must be stripped.
	if sums["dnsx_1.1.1_windows_amd64.zip"] != "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe" {
		t.Fatalf("unexpected sum for dnsx entry (binary-mode prefix not stripped?): %v", sums)
	}
}

func TestLookupSumCaseInsensitiveFallback(t *testing.T) {
	sums := map[string]string{"HTTPX_1.0.0_Linux_amd64.zip": "abc123"}
	if _, ok := lookupSum(sums, "httpx_1.0.0_linux_amd64.zip"); !ok {
		t.Fatal("expected case-insensitive fallback match")
	}
	if _, ok := lookupSum(sums, "nonexistent.zip"); ok {
		t.Fatal("expected no match for an absent filename")
	}
}

func TestAssetNameDarwinMapsToMacOS(t *testing.T) {
	got := assetName("subfinder", "v2.6.3", "darwin", "arm64")
	want := "subfinder_2.6.3_macOS_arm64.zip"
	if got != want {
		t.Fatalf("assetName(darwin) = %q, want %q", got, want)
	}
	got = assetName("httpx", "v1.3.7", "linux", "amd64")
	want = "httpx_1.3.7_linux_amd64.zip"
	if got != want {
		t.Fatalf("assetName(linux) = %q, want %q", got, want)
	}
}

func TestCheckURLRejectsNonGitHub(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{"https://api.github.com/repos/projectdiscovery/subfinder/releases/latest", true},
		{"https://objects.githubusercontent.com/foo", true},
		{"http://api.github.com/repos/x/y/releases/latest", false}, // not HTTPS
		{"https://evil.example.com/subfinder.zip", false},          // not GitHub
		{"https://github.com.evil.com/x", false},                   // lookalike host
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", c.raw, err)
		}
		err = checkURL(u)
		if c.ok && err != nil {
			t.Errorf("checkURL(%q) unexpectedly rejected: %v", c.raw, err)
		}
		if !c.ok && err == nil {
			t.Errorf("checkURL(%q) should have been rejected", c.raw)
		}
	}
}

func TestExtractBinaryFindsEntryAndSkipsOthers(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	w, err := zw.Create("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("not the binary"))
	w, err = zw.Create("subfinder")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("#!/bin/sh\necho fake-subfinder\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out-bin")
	if err := extractBinary(zipPath, dst, "subfinder"); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\necho fake-subfinder\n" {
		t.Fatalf("extracted wrong content: %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("extracted binary is not executable: mode %v", info.Mode())
	}
}

func TestExtractBinaryMissingEntry(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	w, _ := zw.Create("README.md")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractBinary(zipPath, filepath.Join(dir, "out"), "subfinder"); err == nil {
		t.Fatal("expected an error when the archive has no matching entry")
	}
}

func TestBinDirAndBinPathRoot(t *testing.T) {
	data := "/data/dir"
	if got, want := BinDir(data), filepath.Join(data, "tools", "bin"); got != want {
		t.Fatalf("BinDir = %q, want %q", got, want)
	}
	p := BinPath(data, "subfinder")
	if filepath.Dir(p) != BinDir(data) {
		t.Fatalf("BinPath %q is not inside BinDir %q", p, BinDir(data))
	}
}

func TestFindAssetExactThenCaseInsensitive(t *testing.T) {
	rel := &Release{Assets: []Asset{{Name: "Subfinder_2.6.3_Linux_amd64.zip"}}}
	if _, ok := rel.FindAsset("subfinder_2.6.3_Linux_amd64.zip"); !ok {
		t.Fatal("expected case-insensitive fallback to find the asset")
	}
	if _, ok := rel.FindAsset("nope.zip"); ok {
		t.Fatal("expected no match for an absent asset name")
	}
}

func TestFindChecksumsMatchesEitherNamingConvention(t *testing.T) {
	// goreleaser default naming.
	rel := &Release{Assets: []Asset{{Name: "subfinder_2.6.3_checksums.txt"}}}
	if a, ok := rel.FindChecksums(); !ok || a.Name != "subfinder_2.6.3_checksums.txt" {
		t.Fatalf("expected default checksum naming to match, got %v ok=%v", a, ok)
	}
	// naabu's overridden naming.
	rel = &Release{Assets: []Asset{{Name: "naabu-checksums.txt"}}}
	if a, ok := rel.FindChecksums(); !ok || a.Name != "naabu-checksums.txt" {
		t.Fatalf("expected naabu-style checksum naming to match, got %v ok=%v", a, ok)
	}
	rel = &Release{Assets: []Asset{{Name: "subfinder_2.6.3_linux_amd64.zip"}}}
	if _, ok := rel.FindChecksums(); ok {
		t.Fatal("should not match a non-checksum asset")
	}
}

func TestFindUnknownToolKey(t *testing.T) {
	if _, ok := Find("nmap"); ok {
		t.Fatal("nmap is not in the recon-tools catalog and must not resolve")
	}
	if _, ok := Find("subfinder"); !ok {
		t.Fatal("subfinder must resolve from the catalog")
	}
}

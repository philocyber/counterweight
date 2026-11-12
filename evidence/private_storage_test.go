package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestPrivateEvidencePublicationReadRepairAndExport(t *testing.T) {
	parent := t.TempDir()
	makeEvidenceParentPublic(t, parent)
	before := evidencePermissions(t, parent)
	s := New(nil, nil, filepath.Join(parent, "evidence"))
	body := []byte("raw-sensitive-body\x00\xff")
	hash, err := s.writeBody(bytes.NewReader(body), int64(len(body)), "")
	if err != nil {
		t.Fatal(err)
	}
	path, err := hashPath(s.Dir, hash)
	if err != nil {
		t.Fatal(err)
	}
	assertEvidencePrivate(t, path)
	assertEvidencePrivate(t, s.Dir)
	legacy := []byte("legacy-sensitive-body")
	sum := sha256.Sum256(legacy)
	legacyHash := hex.EncodeToString(sum[:])
	legacyPath, err := hashPath(s.Dir, legacyHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacy, 0644); err != nil {
		t.Fatal(err)
	}
	makeEvidenceFilePublic(t, legacyPath)
	snapshot := db.TrafficEvidenceSnapshot{ReqHash: legacyHash, ReqLen: int64(len(legacy)), RespHash: hash, RespLen: int64(len(body))}
	f, _, err := s.OpenBody(snapshot, "request")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	assertEvidencePrivate(t, legacyPath)
	dest := filepath.Join(parent, "export")
	if err := s.copySnapshots([]db.TrafficEvidenceSnapshot{snapshot}, dest); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		hash string
		raw  []byte
	}{{hash, body}, {legacyHash, legacy}} {
		copyPath, err := hashPath(dest, item.hash)
		if err != nil {
			t.Fatal(err)
		}
		assertEvidencePrivate(t, copyPath)
		raw, err := os.ReadFile(copyPath)
		if err != nil || !bytes.Equal(raw, item.raw) {
			t.Fatalf("raw evidence changed: %v", err)
		}
	}
	if evidencePermissions(t, parent) != before {
		t.Fatal("unrelated parent permissions changed")
	}
	if got, err := s.writeBody(bytes.NewReader(body), int64(len(body)), hash); err != nil || got != hash {
		t.Fatalf("deduplication failed: %v", err)
	}
}

func TestPrivateEvidencePublicationSupportsLongPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), strings.Repeat("nested-", 14), strings.Repeat("nested-", 14))
	s := New(nil, nil, root)
	body := []byte("long-path-evidence")
	if _, err := s.writeBody(bytes.NewReader(body), int64(len(body)), ""); err != nil {
		t.Fatal(err)
	}
}

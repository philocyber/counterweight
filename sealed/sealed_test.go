package sealed

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

func ring(t *testing.T, keys ...[]byte) *Keyring {
	t.Helper()
	k, err := New("test", keys...)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := ring(t, key(1))
	s, err := k.Seal("sk-secret-value", "llm_profiles/api_key/7")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "cwenc:v1:"+k.CurrentID()+":") || strings.Contains(s, "sk-secret-value") {
		t.Fatalf("unexpected sealed form %q", s)
	}
	again, _ := k.Seal("sk-secret-value", "llm_profiles/api_key/7")
	if again == s {
		t.Fatal("nonce reuse: two seals produced the same ciphertext")
	}
	got, err := k.Open(s, "llm_profiles/api_key/7")
	if err != nil || got != "sk-secret-value" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if empty, _ := k.Seal("", "x"); empty != "" {
		t.Fatalf("empty plaintext sealed to %q", empty)
	}
}

func TestOpenWrongKey(t *testing.T) {
	s, _ := ring(t, key(1)).Seal("secret", "a")
	if _, err := ring(t, key(2)).Open(s, "a"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestOpenTampered(t *testing.T) {
	k := ring(t, key(1))
	s, _ := k.Seal("secret", "a")
	i := strings.LastIndexByte(s, ':') + 1
	raw, _ := base64.RawURLEncoding.DecodeString(s[i:])
	raw[len(raw)-1] ^= 1
	tampered := s[:i] + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := k.Open(tampered, "a"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := k.Open(s[:i]+"!!", "a"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("malformed: %v", err)
	}
	if _, err := k.Open("cwenc:v9:"+k.CurrentID()+":AAAA", "a"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unknown version: %v", err)
	}
}

func TestOpenAADMismatch(t *testing.T) {
	k := ring(t, key(1))
	s, _ := k.Seal("secret", "llm_profiles/api_key/1")
	if _, err := k.Open(s, "llm_profiles/api_key/2"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("moved to another row: %v", err)
	}
}

func TestOpenLegacyPlaintextPassthrough(t *testing.T) {
	k := ring(t, key(1))
	for _, v := range []string{"", "sk-plain", "http://user:pass@proxy:8080"} {
		if got, err := k.Open(v, "a"); err != nil || got != v {
			t.Fatalf("passthrough %q = %q, %v", v, got, err)
		}
	}
	if k.Current("sk-plain") || !k.Current("") {
		t.Fatal("Current must flag plaintext and accept empty")
	}
}

func TestRotation(t *testing.T) {
	old := ring(t, key(1))
	s, _ := old.Seal("secret", "a")
	rotated := ring(t, key(1), key(2))
	if rotated.CurrentID() != KeyID(key(2)) || rotated.Current(s) {
		t.Fatal("newest key must be current and old values flagged for rewrite")
	}
	if got, err := rotated.Open(s, "a"); err != nil || got != "secret" {
		t.Fatalf("old value under rotated ring: %q %v", got, err)
	}
	n, _ := rotated.Seal("secret", "a")
	if !rotated.Current(n) {
		t.Fatal("new seal not under current key")
	}
}

func TestLoadCreatesOnceAndFailsClosed(t *testing.T) {
	t.Setenv(KeyEnv, "")
	dir := t.TempDir()
	if _, err := Load(dir, false); !errors.Is(err, ErrKeyMissing) || !strings.Contains(err.Error(), filepath.Join(dir, KeyFile)) {
		t.Fatalf("missing key without create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !os.IsNotExist(err) {
		t.Fatal("key file created although create=false")
	}
	first, err := Load(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(dir, true)
	if err != nil || second.CurrentID() != first.CurrentID() {
		t.Fatalf("reload changed the key: %v", err)
	}
	// An invalid file is reported and never replaced.
	path := filepath.Join(dir, KeyFile)
	if err := os.WriteFile(path, []byte("not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, true); err == nil {
		t.Fatal("invalid key file accepted")
	}
	if b, _ := os.ReadFile(path); string(b) != "not-a-key\n" {
		t.Fatal("invalid key file was overwritten")
	}
}

func TestLoadKeyFileRotationOrder(t *testing.T) {
	t.Setenv(KeyEnv, "")
	dir := t.TempDir()
	content := "# comment\r\n" + base64.StdEncoding.EncodeToString(key(1)) + "\r\n\r\n" + base64.StdEncoding.EncodeToString(key(2)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := Load(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !k.Has(KeyID(key(1))) || k.CurrentID() != KeyID(key(2)) {
		t.Fatal("last key line must be current and earlier lines kept for decryption")
	}
}

func TestLoadEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(KeyEnv, base64.StdEncoding.EncodeToString(key(1))+", "+base64.StdEncoding.EncodeToString(key(3)))
	k, err := Load(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if k.CurrentID() != KeyID(key(3)) || !k.Has(KeyID(key(1))) || !strings.Contains(k.Source, KeyEnv) {
		t.Fatalf("env keyring: current=%s source=%s", k.CurrentID(), k.Source)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !os.IsNotExist(err) {
		t.Fatal("key file written although the environment supplies the key")
	}
	t.Setenv(KeyEnv, "c2hvcnQ=")
	if _, err := Load(dir, true); err == nil || strings.Contains(err.Error(), "c2hvcnQ") {
		t.Fatalf("invalid env key must fail without echoing it: %v", err)
	}
}

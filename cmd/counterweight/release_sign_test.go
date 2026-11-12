package main

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/selfupdate"
)

// keygen runs release-keygen and returns the private key, the public key line and the
// complete stdout and stderr, parsed from the documented output.
func keygen(t *testing.T) (priv, pubLine, out, errOut string) {
	t.Helper()
	var so, se bytes.Buffer
	if code := releaseKeygen(&so, &se); code != 0 {
		t.Fatalf("release-keygen exit %d: %s", code, se.String())
	}
	lines := strings.Split(strings.TrimSpace(so.String()), "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "Private key") && i+1 < len(lines):
			priv = lines[i+1]
		case strings.HasPrefix(l, "Public key line") && i+1 < len(lines):
			pubLine = lines[i+1]
		}
	}
	if priv == "" || pubLine == "" {
		t.Fatalf("could not find keys in output:\n%s", so.String())
	}
	return priv, pubLine, so.String(), se.String()
}

func TestReleaseKeygenWarnsAndPrintsMatchingKeys(t *testing.T) {
	priv, pub, out, errOut := keygen(t)
	if !strings.Contains(errOut, "never commit") || !strings.Contains(errOut, "secret") {
		t.Errorf("missing warning on stderr: %q", errOut)
	}
	if strings.Contains(errOut, priv) {
		t.Error("private key printed on stderr")
	}
	privKey, err := selfupdate.ParsePrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := selfupdate.ParseKeys(pub)
	if err != nil || len(keys) != 1 || !keys[0].Equal(privKey.Public()) {
		t.Fatalf("public line does not match the private key: %v", err)
	}
	if !strings.Contains(out, "Key id: "+selfupdate.KeyID(keys[0])) {
		t.Errorf("key id missing: %s", out)
	}
}

func TestReleaseSignVerifyRoundTrip(t *testing.T) {
	priv, pubLine, _, _ := keygen(t)
	keys, _ := selfupdate.ParseKeys(pubLine)
	getenv := func(k string) string {
		if k == signingKeyEnv {
			return priv
		}
		return ""
	}
	dir := t.TempDir()
	sums := filepath.Join(dir, "SHA256SUMS")
	body := strings.Repeat("1", 64) + "  counterweight-1.0.0-linux-amd64.zip\n"
	if err := os.WriteFile(sums, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var so, se bytes.Buffer
	if code := releaseSign([]string{sums}, getenv, &so, &se); code != 0 {
		t.Fatalf("release-sign exit %d: %s", code, se.String())
	}
	if !strings.Contains(so.String(), selfupdate.KeyID(keys[0])) {
		t.Errorf("sign output lacks the key id: %q", so.String())
	}
	if _, err := os.Stat(sums + ".sig"); err != nil {
		t.Fatalf("signature file not written: %v", err)
	}

	// The private key must not leak through any output of sign or verify.
	var vo, ve bytes.Buffer
	if code := releaseVerify([]string{sums}, keys, &vo, &ve); code != 0 {
		t.Fatalf("release-verify exit %d: %s", code, ve.String())
	}
	for name, s := range map[string]string{"sign stdout": so.String(), "sign stderr": se.String(), "verify stdout": vo.String(), "verify stderr": ve.String()} {
		if strings.Contains(s, priv) {
			t.Errorf("private key leaked in %s", name)
		}
	}

	// Tampered file, wrong key and missing signature all fail.
	if err := os.WriteFile(sums, []byte(body+body), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := releaseVerify([]string{sums}, keys, &vo, &ve); code == 0 {
		t.Error("tampered SHA256SUMS verified")
	}
	if err := os.WriteFile(sums, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	otherPub, _, _ := ed25519.GenerateKey(nil)
	if code := releaseVerify([]string{sums}, []ed25519.PublicKey{otherPub}, &vo, &ve); code == 0 {
		t.Error("verified against the wrong key")
	}
	if err := os.Remove(sums + ".sig"); err != nil {
		t.Fatal(err)
	}
	if code := releaseVerify([]string{sums}, keys, &vo, &ve); code == 0 {
		t.Error("verified without a signature file")
	}
	// A build without embedded keys cannot verify anything.
	if code := releaseVerify([]string{sums}, nil, &vo, &ve); code == 0 {
		t.Error("verify succeeded with no embedded keys")
	}
}

func TestReleaseSignErrorsNeverEchoKey(t *testing.T) {
	const secret = "not-a-valid-key-SECRETVALUE"
	dir := t.TempDir()
	sums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if code := releaseSign([]string{sums}, func(string) string { return secret }, &so, &se); code == 0 {
		t.Fatal("invalid key accepted")
	}
	if strings.Contains(so.String()+se.String(), "SECRETVALUE") {
		t.Errorf("invalid key echoed: %q %q", so.String(), se.String())
	}
	if _, err := os.Stat(sums + ".sig"); err == nil {
		t.Error("signature written despite failure")
	}
	// Unset variable and key passed as an argument are both refused.
	if code := releaseSign([]string{sums}, func(string) string { return "" }, &so, &se); code == 0 {
		t.Error("signing without the environment variable succeeded")
	}
	if code := releaseSign([]string{sums, secret}, func(string) string { return secret }, &so, &se); code != 2 {
		t.Errorf("extra argument accepted, exit %d", code)
	}
}

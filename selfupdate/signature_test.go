package selfupdate

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

const testDigest = "1111111111111111111111111111111111111111111111111111111111111111"

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	priv, pub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ParseKeys(pub)
	if err != nil || len(keys) != 1 {
		t.Fatalf("ParseKeys(%q): %v", pub, err)
	}
	return keys[0], p
}

func setTrusted(t *testing.T, keys ...ed25519.PublicKey) {
	t.Helper()
	old := trustedKeys
	trustedKeys = keys
	t.Cleanup(func() { trustedKeys = old })
}

func sumsFor(names ...string) []byte {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(testDigest + "  " + n + "\n")
	}
	return []byte(b.String())
}

func TestVerifySums(t *testing.T) {
	pubA, privA := newKey(t)
	pubB, privB := newKey(t)
	sums := sumsFor("counterweight-1.0.0-linux-amd64.zip")
	sigA, idA := SignSums(privA, sums)
	if idA != KeyID(pubA) {
		t.Fatalf("key id mismatch: %s vs %s", idA, KeyID(pubA))
	}

	if id, err := VerifySums([]ed25519.PublicKey{pubA}, sums, sigA); err != nil || id != idA {
		t.Fatalf("valid signature rejected: id=%q err=%v", id, err)
	}
	// Rotation: either of two trusted keys is enough.
	sigB, idB := SignSums(privB, sums)
	both := []ed25519.PublicKey{pubA, pubB}
	if id, err := VerifySums(both, sums, sigA); err != nil || id != idA {
		t.Errorf("key A under rotation: id=%q err=%v", id, err)
	}
	if id, err := VerifySums(both, sums, sigB); err != nil || id != idB {
		t.Errorf("key B under rotation: id=%q err=%v", id, err)
	}

	// Wrong key: signed by B, only A trusted.
	if _, err := VerifySums([]ed25519.PublicKey{pubA}, sums, sigB); err == nil {
		t.Error("signature from an untrusted key accepted")
	}
	// Tampered SHA256SUMS.
	if _, err := VerifySums([]ed25519.PublicKey{pubA}, append([]byte(nil), append(sums, '\n')...), sigA); err == nil {
		t.Error("tampered SHA256SUMS accepted")
	}
	other := sumsFor("counterweight-1.0.1-linux-amd64.zip")
	if _, err := VerifySums([]ed25519.PublicKey{pubA}, other, sigA); err == nil {
		t.Error("signature of one release accepted for another release's SHA256SUMS")
	}
	// Tampered signature: flip a byte inside the base64 payload (stays decodable).
	raw := string(sigA)
	i := strings.Index(raw, "signature: ") + len("signature: ")
	flipped := []byte(raw)
	if flipped[i] == 'A' {
		flipped[i] = 'B'
	} else {
		flipped[i] = 'A'
	}
	if _, err := VerifySums([]ed25519.PublicKey{pubA}, sums, flipped); err == nil {
		t.Error("tampered signature accepted")
	}
	// Garbage and version mismatch.
	for name, bad := range map[string]string{
		"empty":   "",
		"garbage": "hello\n",
		"version": strings.Replace(raw, " v1", " v2", 1),
		"no id":   strings.Replace(raw, "key-id: "+idA, "key-id: ", 1),
		"short":   sigHeader + "\nkey-id: " + idA + "\nsignature: " + base64.StdEncoding.EncodeToString([]byte("short")) + "\n",
	} {
		if _, err := VerifySums([]ed25519.PublicKey{pubA}, sums, []byte(bad)); err == nil {
			t.Errorf("%s signature file accepted", name)
		}
	}
	// No keys never verifies.
	if _, err := VerifySums(nil, sums, sigA); err == nil {
		t.Error("VerifySums succeeded with no trusted keys")
	}
}

func TestParseKeys(t *testing.T) {
	pubA, _ := newKey(t)
	pubB, _ := newKey(t)
	a := base64.StdEncoding.EncodeToString(pubA)
	b := base64.StdEncoding.EncodeToString(pubB)

	keys, err := ParseKeys("# comment\n\n  " + a + "  \n# another\n" + b + "\r\n\n")
	if err != nil || len(keys) != 2 || !keys[0].Equal(pubA) || !keys[1].Equal(pubB) {
		t.Fatalf("ParseKeys = %v, %v", keys, err)
	}
	if keys, err := ParseKeys("# only comments\n\n"); err != nil || len(keys) != 0 {
		t.Errorf("comment-only file: %v, %v", keys, err)
	}
	for name, bad := range map[string]string{
		"not base64":   "%%%not-base64%%%",
		"wrong length": base64.StdEncoding.EncodeToString([]byte("too short")),
		"one bad line": a + "\noops\n",
	} {
		_, err := ParseKeys(bad)
		if err == nil || !strings.Contains(err.Error(), "line") {
			t.Errorf("%s: expected an error naming the line, got %v", name, err)
		}
	}
}

// The committed file must always parse: a bad edit must fail CI, not ship a broken build.
func TestEmbeddedKeyFileParses(t *testing.T) {
	if _, err := ParseKeys(releaseKeysFile); err != nil {
		t.Fatalf("selfupdate/release-keys.pub: %v", err)
	}
}

func TestMalformedEmbeddedKeysPanicAtInit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "release-keys.pub") {
			t.Fatalf("expected a panic naming release-keys.pub, got %v", r)
		}
	}()
	mustParseKeys("garbage\n")
}

// releaseServer serves files under /dl/<name> over TLS and returns a client that maps any
// allowlisted GitHub host to it, keeping the real redirect and URL restrictions.
func releaseServer(t *testing.T, files map[string][]byte, rel *Release) *http.Client {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		if name == "evil-redirect" {
			http.Redirect(w, r, "https://evil.example.net/x", http.StatusFound)
			return
		}
		b, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)

	c := NewClient("")
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "example.com" // the httptest certificate covers this name
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	c.Transport = tr
	for name := range files {
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: "https://github.com/dl/" + name, Size: int64(len(files[name]))})
	}
	return c
}

func collect() (MessageProgress, *[]string) {
	var msgs []string
	return func(_ Phase, _ int, m locale.Message) { msgs = append(msgs, m.In(locale.ServerDefault())) }, &msgs
}

func TestFetchSumsSigned(t *testing.T) {
	pub, priv := newKey(t)
	archive := "counterweight-1.0.0-linux-amd64.zip"
	sums := sumsFor(archive)
	sig, id := SignSums(priv, sums)

	rel := &Release{TagName: "v1.0.0"}
	c := releaseServer(t, map[string][]byte{sumsAsset: sums, sigAsset: sig}, rel)
	prog, msgs := collect()
	got, err := fetchSums(context.Background(), c, rel, []ed25519.PublicKey{pub}, prog)
	if err != nil {
		t.Fatalf("signed release rejected: %v", err)
	}
	if got[archive] != testDigest {
		t.Errorf("digest from signed bytes = %q", got[archive])
	}
	if !strings.Contains(strings.Join(*msgs, "|"), "signature verified (key "+id+")") {
		t.Errorf("progress did not report verification: %v", *msgs)
	}
}

func TestFetchSumsRejections(t *testing.T) {
	pub, priv := newKey(t)
	_, otherPriv := newKey(t)
	sums := sumsFor("counterweight-1.0.0-linux-amd64.zip")
	goodSig, _ := SignSums(priv, sums)
	wrongSig, _ := SignSums(otherPriv, sums)
	tampered := append(append([]byte(nil), sums...), []byte(testDigest+"  counterweight-1.0.0-evil.zip\n")...)

	cases := map[string]map[string][]byte{
		"missing signature":   {sumsAsset: sums},
		"wrong key":           {sumsAsset: sums, sigAsset: wrongSig},
		"tampered sums":       {sumsAsset: tampered, sigAsset: goodSig},
		"garbage signature":   {sumsAsset: sums, sigAsset: []byte("nope")},
		"oversized signature": {sumsAsset: sums, sigAsset: []byte(strings.Repeat("x", maxSigSize+1))},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			rel := &Release{TagName: "v1.0.0"}
			c := releaseServer(t, files, rel)
			prog, _ := collect()
			if _, err := fetchSums(context.Background(), c, rel, []ed25519.PublicKey{pub}, prog); err == nil {
				t.Fatal("release accepted")
			}
		})
	}
}

func TestFetchSumsMissingSignatureMessage(t *testing.T) {
	pub, _ := newKey(t)
	rel := &Release{TagName: "v1.0.0"}
	c := releaseServer(t, map[string][]byte{sumsAsset: sumsFor("a.zip")}, rel)
	prog, _ := collect()
	_, err := fetchSums(context.Background(), c, rel, []ed25519.PublicKey{pub}, prog)
	if err == nil || !strings.Contains(err.Error(), sigAsset) || !strings.Contains(err.Error(), "signed releases") {
		t.Fatalf("unclear message: %v", err)
	}
}

func TestFetchSumsNoKeysReportsUnverified(t *testing.T) {
	rel := &Release{TagName: "v1.0.0"}
	c := releaseServer(t, map[string][]byte{sumsAsset: sumsFor("a.zip")}, rel)
	prog, msgs := collect()
	got, err := fetchSums(context.Background(), c, rel, nil, prog)
	if err != nil || got["a.zip"] != testDigest {
		t.Fatalf("no-keys build must behave as before: %v %v", got, err)
	}
	if !strings.Contains(strings.Join(*msgs, "|"), "does not verify release signatures") {
		t.Errorf("state not reported: %v", *msgs)
	}
}

func TestSignatureFetchKeepsURLRestrictions(t *testing.T) {
	pub, priv := newKey(t)
	sums := sumsFor("a.zip")
	sig, _ := SignSums(priv, sums)
	prog, _ := collect()

	// Redirect to a non-GitHub host.
	rel := &Release{TagName: "v1.0.0"}
	c := releaseServer(t, map[string][]byte{sumsAsset: sums, sigAsset: sig}, rel)
	for i := range rel.Assets {
		if rel.Assets[i].Name == sigAsset {
			rel.Assets[i].URL = "https://github.com/dl/evil-redirect"
		}
	}
	if _, err := fetchSums(context.Background(), c, rel, []ed25519.PublicKey{pub}, prog); err == nil {
		t.Error("redirect of the signature to a non-GitHub host was followed")
	}
	// Plain HTTP and foreign hosts.
	for _, u := range []string{"http://github.com/dl/" + sigAsset, "https://evil.example.net/" + sigAsset} {
		rel := &Release{TagName: "v1.0.0"}
		c := releaseServer(t, map[string][]byte{sumsAsset: sums, sigAsset: sig}, rel)
		for i := range rel.Assets {
			if rel.Assets[i].Name == sigAsset {
				rel.Assets[i].URL = u
			}
		}
		if _, err := fetchSums(context.Background(), c, rel, []ed25519.PublicKey{pub}, prog); err == nil {
			t.Errorf("signature fetched from %s", u)
		}
	}
}

// stageRelease runs the real StageMessages against the served release. Staging files
// land next to the test executable, so each case checks that none were created.
func stageRelease(t *testing.T, files map[string][]byte) error {
	t.Helper()
	rel := &Release{TagName: "v1.0.0"}
	c := releaseServer(t, files, rel)
	prog, _ := collect()
	err := StageMessages(context.Background(), c, rel, "0.9.0", prog)
	p, perr := ResolvePaths()
	if perr != nil {
		t.Fatal(perr)
	}
	for _, f := range []string{p.New, p.New + ".part", p.New + ".zip.part", p.Sum, p.Marker} {
		if _, serr := os.Stat(f); serr == nil {
			t.Errorf("%s exists after a refused update", filepath.Base(f))
			_ = os.Remove(f)
		}
	}
	return err
}

func TestStageRefusesUnsignedAndUnlistedArchive(t *testing.T) {
	pub, priv := newKey(t)
	setTrusted(t, pub)
	archive := AssetName("v1.0.0", runtime.GOOS, runtime.GOARCH)
	zipBody := []byte("not a real zip")

	// Signature missing: refused before any archive download.
	err := stageRelease(t, map[string][]byte{sumsAsset: sumsFor(archive), archive: zipBody})
	if err == nil || !strings.Contains(err.Error(), "signed releases") {
		t.Errorf("missing signature: %v", err)
	}

	// Validly signed, but the archive for this platform is not listed in the signed file.
	sums := sumsFor("counterweight-1.0.0-other-arch.zip")
	sig, _ := SignSums(priv, sums)
	err = stageRelease(t, map[string][]byte{sumsAsset: sums, sigAsset: sig, archive: zipBody})
	if err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Errorf("unlisted archive: %v", err)
	}

	// Signed and listed, but the archive does not match the signed digest.
	sums = sumsFor(archive)
	sig, _ = SignSums(priv, sums)
	err = stageRelease(t, map[string][]byte{sumsAsset: sums, sigAsset: sig, archive: zipBody})
	if err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Errorf("digest mismatch: %v", err)
	}
}

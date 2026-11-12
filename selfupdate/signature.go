package selfupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// sigAsset is the detached signature over the exact bytes of SHA256SUMS.
const sigAsset = "SHA256SUMS.sig"

// sigHeader identifies the signature file format and its version.
const sigHeader = "counterweight-release-signature v1"

// maxSigSize bounds the signature file download; a valid file is a few hundred bytes.
const maxSigSize = 4 << 10

// releaseKeysFile holds the trusted public keys compiled into the binary.
//
//go:embed release-keys.pub
var releaseKeysFile string

// trustedKeys are the keys a release signature must verify against. It is a variable
// only so tests can substitute keys; no setting or environment variable changes it.
var trustedKeys = mustParseKeys(releaseKeysFile)

func mustParseKeys(text string) []ed25519.PublicKey {
	keys, err := ParseKeys(text)
	if err != nil {
		panic("selfupdate/release-keys.pub: " + err.Error())
	}
	return keys
}

// TrustedKeys returns the release public keys embedded in this build (possibly none).
func TrustedKeys() []ed25519.PublicKey { return trustedKeys }

// VerifiesSignatures reports whether this build enforces release signatures, which is
// the case exactly when at least one trusted key is embedded.
func VerifiesSignatures() bool { return len(trustedKeys) > 0 }

// ParseKeys parses the key file format: one base64 Ed25519 public key per line, with
// blank lines and #-comments ignored. A malformed key is an error, never skipped, so a
// typo cannot silently weaken verification.
func ParseKeys(text string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("line %d is not a base64 Ed25519 public key (%d bytes expected)", i+1, ed25519.PublicKeySize)
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	return keys, nil
}

// KeyID is a short identifier of a public key: the first 8 bytes of its SHA-256, in hex.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// GenerateKey creates a key pair. The private key is the base64 of the 32-byte seed and
// the public key the base64 line to add to release-keys.pub.
func GenerateKey() (privateKey, publicKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(pub), nil
}

// ParsePrivateKey decodes the base64 seed produced by GenerateKey. Errors never echo the input.
func ParsePrivateKey(b64 string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, locale.NewError("Not a valid release signing key (expected the base64 private key printed by release-keygen)")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// SignSums returns the contents of a SHA256SUMS.sig file for the exact bytes of sums.
func SignSums(priv ed25519.PrivateKey, sums []byte) (sigFile []byte, keyID string) {
	pub := priv.Public().(ed25519.PublicKey)
	keyID = KeyID(pub)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	return []byte(sigHeader + "\nkey-id: " + keyID + "\nsignature: " + sig + "\n"), keyID
}

// VerifySums checks sigFile against the exact bytes of sums using any of keys and
// returns the id of the key that verified. There is no mode in which it succeeds
// without a verifying key, including when keys is empty.
func VerifySums(keys []ed25519.PublicKey, sums, sigFile []byte) (string, error) {
	if len(keys) == 0 {
		return "", locale.NewError("No trusted release keys are embedded in this build")
	}
	id, sig, err := parseSigFile(sigFile)
	if err != nil {
		return "", err
	}
	known := false
	for _, k := range keys {
		if KeyID(k) != id {
			continue
		}
		known = true
		if ed25519.Verify(k, sums, sig) {
			return id, nil
		}
	}
	if !known {
		return "", locale.Errorf("The release is signed with key %s, which this build does not trust", id)
	}
	return "", locale.Errorf("The signature does not match %s (signed with key %s); the release files may have been altered", sumsAsset, id)
}

// parseSigFile parses the three-line signature file strictly.
func parseSigFile(raw []byte) (keyID string, sig []byte, err error) {
	bad := locale.Errorf("%s is not a valid signature file", sigAsset)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[0]) != sigHeader {
		return "", nil, bad
	}
	id, ok1 := strings.CutPrefix(strings.TrimSpace(lines[1]), "key-id: ")
	b64, ok2 := strings.CutPrefix(strings.TrimSpace(lines[2]), "signature: ")
	if !ok1 || !ok2 || id == "" {
		return "", nil, bad
	}
	sig, err = base64.StdEncoding.DecodeString(b64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return "", nil, bad
	}
	return id, sig, nil
}

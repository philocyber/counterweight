// Package sealed encrypts stored credentials at rest. Values are sealed with
// AES-256-GCM under a 256-bit data key that lives outside the database, either
// in a private key file next to the JWT signing key or in an environment
// variable. A sealed value is a self-describing string:
//
//	cwenc:v1:<key-id>:<base64url(nonce || ciphertext)>
//
// The associated data passed to Seal and Open names the value's storage
// location, so a ciphertext copied to another row or column fails to open.
package sealed

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
)

const (
	// Prefix starts every sealed value of any format version.
	Prefix = "cwenc:"
	// prefixV1 starts values in the current format.
	prefixV1 = Prefix + "v1:"
	// KeyFile is the key file name inside the key directory.
	KeyFile = "secrets.key"
	// KeyEnv overrides the key file with base64-encoded keys separated by
	// commas or whitespace; the last one seals new values.
	KeyEnv = "COUNTERWEIGHT_SECRETS_KEY"
	// KeySize is the data key length in bytes (AES-256).
	KeySize = 32
)

var (
	// ErrKeyMissing reports that no key file exists and creation was not allowed.
	ErrKeyMissing = locale.NewError("Credential encryption key not found")
	// ErrUnknownKey reports a sealed value whose key id is not in the keyring.
	ErrUnknownKey = locale.NewError("Stored credential is encrypted with a key that is not available")
	// ErrMalformed reports a value that has the sealed prefix but cannot be parsed.
	ErrMalformed = locale.NewError("Stored credential has an unsupported encrypted format")
	// ErrDecrypt reports a failed authentication: wrong location, tampering or corruption.
	ErrDecrypt = locale.NewError("Stored credential failed to decrypt")
)

// Keyring holds every known data key by id. The last key added is current:
// Seal always uses it, Open accepts any of them.
type Keyring struct {
	aeads   map[string]cipher.AEAD
	current string
	// Source names where the keys came from (a file path or the environment
	// variable), for operator-facing error messages. It never contains key bytes.
	Source string
}

// New builds a keyring from raw 32-byte keys; the last one is current.
func New(source string, keys ...[]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, locale.Errorf("Credential encryption key source %s contains no key", source)
	}
	k := &Keyring{aeads: map[string]cipher.AEAD{}, Source: source}
	for _, key := range keys {
		if len(key) != KeySize {
			return nil, locale.Errorf("Credential encryption key source %s contains a key that is not %d bytes", source, KeySize)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		id := KeyID(key)
		k.aeads[id] = aead
		k.current = id
	}
	return k, nil
}

// KeyID derives the public identifier of a key. It is a truncated, domain
// separated hash, so it identifies the key without revealing it.
func KeyID(key []byte) string {
	sum := sha256.Sum256(append([]byte("counterweight secrets key id\x00"), key...))
	return hex.EncodeToString(sum[:8])
}

// CurrentID returns the id of the key used for new values.
func (k *Keyring) CurrentID() string { return k.current }

// Has reports whether the keyring can open values sealed under id.
func (k *Keyring) Has(id string) bool { _, ok := k.aeads[id]; return ok }

// IsSealed reports whether stored has the sealed prefix of any version.
func IsSealed(stored string) bool { return strings.HasPrefix(stored, Prefix) }

// Header returns the "<version>:<key-id>" part of a sealed value.
func Header(stored string) (version, keyID string, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(stored, Prefix), ":", 3)
	if !IsSealed(stored) || len(parts) != 3 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func header(id string) string { return prefixV1 + id + ":" }

// Seal encrypts plaintext under the current key, bound to aad. An empty
// plaintext stays empty: it carries no secret and keeps "unset" checks working.
func (k *Keyring) Seal(plaintext, aad string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	aead := k.aeads[k.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	h := header(k.current)
	out := aead.Seal(nonce, nonce, []byte(plaintext), []byte(h+aad))
	return h + base64.RawURLEncoding.EncodeToString(out), nil
}

// Open decrypts a sealed value bound to aad. Values without the sealed prefix
// are legacy plaintext and are returned unchanged.
func (k *Keyring) Open(stored, aad string) (string, error) {
	if !IsSealed(stored) {
		return stored, nil
	}
	version, id, ok := Header(stored)
	if !ok || version != "v1" {
		return "", ErrMalformed
	}
	aead, ok := k.aeads[id]
	if !ok {
		return "", locale.Errorf("%w (key %s, expected in %s)", ErrUnknownKey, id, k.Source)
	}
	raw, err := base64.RawURLEncoding.DecodeString(stored[len(header(id)):])
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", ErrMalformed
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(header(id)+aad))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// Current reports whether stored needs no rewrite: it is empty or sealed under
// the current key. Plaintext and values under an older key return false.
func (k *Keyring) Current(stored string) bool {
	if stored == "" {
		return true
	}
	_, id, ok := Header(stored)
	return ok && id == k.current
}

// Load returns the keyring from KeyEnv when set, otherwise from dir/KeyFile.
// When the file does not exist it is created with a new random key only if
// create is true; otherwise ErrKeyMissing is returned with the path. An
// existing file is never replaced, even when it is invalid.
func Load(dir string, create bool) (*Keyring, error) {
	if v := strings.TrimSpace(os.Getenv(KeyEnv)); v != "" {
		keys, err := parseKeys(strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }))
		if err != nil {
			return nil, locale.Errorf("Environment variable %s is invalid: %w", KeyEnv, err)
		}
		return New(locale.Text(locale.ServerDefault(), "environment variable %s", KeyEnv), keys...)
	}
	path := filepath.Join(dir, KeyFile)
	keys, err := readKeyFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil, locale.Errorf("%w: %s", ErrKeyMissing, path)
		}
		key := make([]byte, KeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		content := "# Counterweight credential encryption keys (base64, one per line; the last line encrypts new values).\n" +
			"# Back this file up separately from database backups. Losing it means re-entering stored credentials.\n" +
			base64.StdEncoding.EncodeToString(key) + "\n"
		if err := privatefs.PublishFile(path, []byte(content)); err != nil {
			if !os.IsExist(err) {
				return nil, locale.Errorf("write credential encryption key %s: %w", path, err)
			}
			// Another process created it first; use that one.
			keys, err = readKeyFile(path)
			if err != nil {
				return nil, err
			}
			return New(path, keys...)
		}
		return New(path, key)
	}
	if err != nil {
		return nil, err
	}
	return New(path, keys...)
}

func readKeyFile(path string) ([][]byte, error) {
	if err := privatefs.ProtectFile(path); err != nil {
		return nil, err
	}
	data, err := privatefs.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fields []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			fields = append(fields, line)
		}
	}
	keys, err := parseKeys(fields)
	if err != nil {
		return nil, locale.Errorf("Credential encryption key file %s is invalid; refusing to replace it: %w", path, err)
	}
	return keys, nil
}

func parseKeys(fields []string) ([][]byte, error) {
	if len(fields) == 0 {
		return nil, locale.NewError("no key present")
	}
	keys := make([][]byte, 0, len(fields))
	for _, f := range fields {
		key, err := base64.StdEncoding.DecodeString(f)
		if err != nil || len(key) != KeySize {
			return nil, locale.Errorf("each key must be %d bytes encoded as standard base64", KeySize)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

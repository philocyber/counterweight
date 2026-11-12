package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Philocyber/counterweight/config"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/sealed"
)

// Stored credentials are encrypted at rest with the sealed package. Every
// value is bound to its location (table, column, row and, for JSON maps, the
// entry name), so a ciphertext copied elsewhere does not decrypt. Readers in
// this package open values in their scan function, so callers keep receiving
// plaintext exactly as before; values without the sealed prefix are legacy
// plaintext and pass through until the startup migration seals them.
//
// Sealed locations:
//   - llm_profiles.api_key and llm_profiles.proxy (the proxy URL may carry user:password)
//   - every string value of mcp_servers.env (stdio environment / HTTP headers)
//   - notification_channels.config as a whole (webhook URLs, signing secrets, tokens, passwords),
//     stored as a JSON string while sealed
//   - settings.value for the keys in sealedSettings

// ResetCredentialsEnv, when set to 1/true, makes startup discard stored
// credentials that cannot be decrypted (for example after the key file was
// lost) instead of refusing to start. The affected values must be re-entered.
const ResetCredentialsEnv = "COUNTERWEIGHT_RESET_STORED_CREDENTIALS"

// sealedSettings are the settings keys whose values are credentials.
var sealedSettings = []string{"brave_search_api_key", "tavily_search_api_key", "web_search_proxy", "global_proxy"}

func isSealedSetting(key string) bool {
	for _, k := range sealedSettings {
		if k == key {
			return true
		}
	}
	return false
}

var secretKeys atomic.Pointer[sealed.Keyring]

// testKeyring is a fixed key for test binaries only, so test databases stay
// readable across runs and packages without writing key files into the
// source tree. Production binaries never use it (testing.Testing is false).
var testKey = sha256.Sum256([]byte("counterweight test-only credential key"))

var testKeyring = sync.OnceValue(func() *sealed.Keyring {
	k, err := sealed.New("test keyring", testKey[:])
	if err != nil {
		panic(err)
	}
	return k
})

func useTestKeyring() bool {
	return testing.Testing() && strings.TrimSpace(os.Getenv(sealed.KeyEnv)) == ""
}

// loadSecretKeys loads the keyring that Open installs. The key file lives next
// to the JWT signing key, outside the browsable data directory.
func loadSecretKeys(create bool) (*sealed.Keyring, error) {
	if useTestKeyring() {
		return testKeyring(), nil
	}
	return sealed.Load(config.KeyDir(), create)
}

func currentKeyring() (*sealed.Keyring, error) {
	if k := secretKeys.Load(); k != nil {
		return k, nil
	}
	if useTestKeyring() {
		return testKeyring(), nil
	}
	return nil, locale.NewError("Credential encryption is not initialized; the database was not opened through Open")
}

func secretAAD(table, column string, row any, field ...string) string {
	var id string
	switch v := row.(type) {
	case int64:
		id = strconv.FormatInt(v, 10)
	case string:
		id = v
	}
	return strings.Join(append([]string{"counterweight", table, column, id}, field...), "\x00")
}

func sealSecret(table, column string, row any, plain string, field ...string) (string, error) {
	k, err := currentKeyring()
	if err != nil {
		return "", err
	}
	return k.Seal(plain, secretAAD(table, column, row, field...))
}

func openSecret(table, column string, row any, stored string, field ...string) (string, error) {
	if !sealed.IsSealed(stored) {
		return stored, nil
	}
	k, err := currentKeyring()
	if err != nil {
		return "", err
	}
	plain, err := k.Open(stored, secretAAD(table, column, row, field...))
	if err != nil {
		return "", locale.Errorf("%s.%s row %v: %w", table, column, row, err)
	}
	return plain, nil
}

// sealMCPEnv seals each non-empty string value of an env/header JSON object.
// Non-object JSON and non-string values are stored unchanged.
func sealMCPEnv(id int64, raw string) (string, error) {
	return mapMCPEnv(raw, func(name, v string) (string, error) {
		if sealed.IsSealed(v) {
			// Already sealed for this row (an unchanged value read back): keep it
			// only if it really opens here, never store a foreign ciphertext.
			if _, err := openSecret("mcp_servers", "env", id, v, name); err != nil {
				return "", err
			}
			return v, nil
		}
		return sealSecret("mcp_servers", "env", id, v, name)
	})
}

// openMCPEnv returns the env object with every sealed value decrypted. Rows
// without sealed values are returned byte for byte.
func openMCPEnv(id int64, raw []byte) (json.RawMessage, error) {
	if !strings.Contains(string(raw), sealed.Prefix) {
		return json.RawMessage(raw), nil
	}
	out, err := mapMCPEnv(string(raw), func(name, v string) (string, error) {
		return openSecret("mcp_servers", "env", id, v, name)
	})
	return json.RawMessage(out), err
}

func mapMCPEnv(raw string, fn func(name, value string) (string, error)) (string, error) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &env); err != nil || env == nil {
		return raw, nil
	}
	for name, v := range env {
		var s string
		if json.Unmarshal(v, &s) != nil || s == "" {
			continue
		}
		mapped, err := fn(name, s)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(mapped)
		if err != nil {
			return "", err
		}
		env[name] = b
	}
	b, err := json.Marshal(env)
	return string(b), err
}

// sealChannelConfig seals a whole notification channel config and returns it
// as a JSON string literal for the JSONB column.
func sealChannelConfig(id int64, raw json.RawMessage) (string, error) {
	s, err := sealSecret("notification_channels", "config", id, string(raw))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(s)
	return string(b), err
}

// openChannelConfig returns the plaintext config JSON; legacy object configs pass through.
func openChannelConfig(id int64, raw json.RawMessage) (json.RawMessage, error) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil || !sealed.IsSealed(s) {
		return raw, nil
	}
	plain, err := openSecret("notification_channels", "config", id, s)
	return json.RawMessage(plain), err
}

// secretMigrator is the part of *sql.Conn / *sql.DB used by the startup migration.
type secretMigrator interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// storedSecretHeaders returns the distinct "<version>:<key-id>" headers of all sealed values.
func storedSecretHeaders(ctx context.Context, q secretMigrator) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT split_part(v, ':', 2) || ':' || split_part(v, ':', 3) FROM (
  SELECT api_key FROM llm_profiles
  UNION ALL SELECT proxy FROM llm_profiles
  UNION ALL SELECT e.v FROM mcp_servers m,
    jsonb_each_text(CASE WHEN jsonb_typeof(m.env) = 'object' THEN m.env ELSE '{}'::jsonb END) AS e(k, v)
  UNION ALL SELECT config #>> '{}' FROM notification_channels WHERE jsonb_typeof(config) = 'string'
  UNION ALL SELECT value FROM settings WHERE key = ANY($1::text[])
  UNION ALL SELECT password FROM task_authz_accounts
  UNION ALL SELECT token FROM task_authz_accounts
) s(v) WHERE v LIKE 'cwenc:%'`, sealedSettings)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func resetCredentialsRequested() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(ResetCredentialsEnv)))
	return v == "1" || v == "true" || v == "yes"
}

// initSecrets loads the keyring and migrates stored credentials. It fails
// closed: when sealed values exist and their key is missing or unknown, it
// returns an error naming the key location and changes nothing, unless reset
// is set, in which case unreadable values are cleared.
func initSecrets(ctx context.Context, q secretMigrator, load func(create bool) (*sealed.Keyring, error), reset bool) (*sealed.Keyring, error) {
	headers, err := storedSecretHeaders(ctx, q)
	if err != nil {
		return nil, locale.Errorf("scan stored credentials: %w", err)
	}
	k, err := load(len(headers) == 0 || reset)
	if err != nil {
		if len(headers) > 0 && errors.Is(err, sealed.ErrKeyMissing) {
			return nil, locale.Errorf("The database contains encrypted credentials but their key is missing (%w). Restore the key file from your backup (or set %s) and restart. To discard the stored credentials instead and re-enter them, start once with %s=1", err, sealed.KeyEnv, ResetCredentialsEnv)
		}
		return nil, err
	}
	var unknown []string
	for _, h := range headers {
		version, id, _ := strings.Cut(h, ":")
		if version != "v1" || !k.Has(id) {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 && !reset {
		sort.Strings(unknown)
		return nil, locale.Errorf("The database contains credentials encrypted with key %s, which is not in %s. Restore the matching key file from your backup (or set %s) and restart. To discard the stored credentials instead and re-enter them, start once with %s=1", strings.Join(unknown, ", "), k.Source, sealed.KeyEnv, ResetCredentialsEnv)
	}
	n, cleared, err := migrateSecrets(ctx, q, k, reset)
	if err != nil {
		return nil, locale.Errorf("encrypt stored credentials: %w", err)
	}
	if n > 0 {
		log.Print(locale.Text(locale.ServerDefault(), "[secrets] Encrypted %d stored credential value(s) with key %s", n, k.CurrentID()))
	}
	if cleared > 0 {
		log.Print(locale.Text(locale.ServerDefault(), "[secrets] Discarded %d stored credential value(s) that could not be decrypted; re-enter them in the console", cleared))
	}
	return k, nil
}

// resealer rewrites one stored value: plaintext and values under an older key
// are sealed under the current key. A value that cannot be opened is cleared
// when reset is set, otherwise left untouched and reported.
type resealer struct {
	k       *sealed.Keyring
	reset   bool
	sealed  int
	cleared int
}

func (r *resealer) value(stored, aad, where string) (string, bool, error) {
	if r.k.Current(stored) {
		return stored, false, nil
	}
	plain, err := r.k.Open(stored, aad)
	if err != nil {
		if r.reset {
			r.cleared++
			return "", true, nil
		}
		log.Print(locale.Text(locale.ServerDefault(), "[secrets] Left %s unchanged: %v", where, err))
		return stored, false, nil
	}
	out, err := r.k.Seal(plain, aad)
	if err != nil {
		return "", false, err
	}
	r.sealed++
	return out, true, nil
}

// migrateSecrets seals plaintext values and re-encrypts values under older
// keys, one transaction per table. Running it again changes nothing.
func migrateSecrets(ctx context.Context, q secretMigrator, k *sealed.Keyring, reset bool) (sealedCount, cleared int, err error) {
	r := &resealer{k: k, reset: reset}
	for _, step := range []func(context.Context, *sql.Tx, *resealer) error{
		migrateProfileSecrets, migrateMCPSecrets, migrateChannelSecrets, migrateSettingSecrets, migrateAuthzAccountSecrets,
	} {
		tx, err := q.BeginTx(ctx, nil)
		if err != nil {
			return 0, 0, err
		}
		if err := step(ctx, tx, r); err != nil {
			_ = tx.Rollback()
			return 0, 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, 0, err
		}
	}
	return r.sealed, r.cleared, nil
}

func migrateProfileSecrets(ctx context.Context, tx *sql.Tx, r *resealer) error {
	type row struct {
		id           int64
		key, proxy   string
		keyCleared   bool
		keyChanged   bool
		proxyChanged bool
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(api_key,''), COALESCE(proxy,'') FROM llm_profiles ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.key, &x.proxy); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range all {
		key, keyChanged, err := r.value(x.key, secretAAD("llm_profiles", "api_key", x.id), "llm_profiles.api_key row "+strconv.FormatInt(x.id, 10))
		if err != nil {
			return err
		}
		proxy, proxyChanged, err := r.value(x.proxy, secretAAD("llm_profiles", "proxy", x.id), "llm_profiles.proxy row "+strconv.FormatInt(x.id, 10))
		if err != nil {
			return err
		}
		if !keyChanged && !proxyChanged {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE llm_profiles SET api_key=NULLIF($2,''), proxy=NULLIF($3,''),
api_key_hint=CASE WHEN $4 THEN NULL ELSE api_key_hint END WHERE id=$1`, x.id, key, proxy, keyChanged && key == ""); err != nil {
			return err
		}
	}
	return nil
}

func migrateMCPSecrets(ctx context.Context, tx *sql.Tx, r *resealer) error {
	type row struct {
		id  int64
		env []byte
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, env FROM mcp_servers ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.env); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range all {
		changed := false
		out, err := mapMCPEnv(string(x.env), func(name, v string) (string, error) {
			s, c, err := r.value(v, secretAAD("mcp_servers", "env", x.id, name), "mcp_servers.env row "+strconv.FormatInt(x.id, 10)+" entry "+strconv.Quote(name))
			changed = changed || c
			return s, err
		})
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mcp_servers SET env=$2 WHERE id=$1`, x.id, out); err != nil {
			return err
		}
	}
	return nil
}

func migrateChannelSecrets(ctx context.Context, tx *sql.Tx, r *resealer) error {
	type row struct {
		id  int64
		cfg []byte
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, config FROM notification_channels ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.cfg); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range all {
		stored := string(x.cfg)
		var s string
		if len(x.cfg) > 0 && x.cfg[0] == '"' && json.Unmarshal(x.cfg, &s) == nil && sealed.IsSealed(s) {
			stored = s
		}
		out, changed, err := r.value(stored, secretAAD("notification_channels", "config", x.id), "notification_channels.config row "+strconv.FormatInt(x.id, 10))
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if out == "" {
			// The channel cannot send without its configuration; keep it but disable it.
			if _, err := tx.ExecContext(ctx, `UPDATE notification_channels SET config='{}', enabled=false WHERE id=$1`, x.id); err != nil {
				return err
			}
			continue
		}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_channels SET config=$2 WHERE id=$1`, x.id, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// migrateAuthzAccountSecrets re-encrypts the password and token of task test
// accounts under the current key. They are sealed when written, so there is no
// plaintext to seal here; a value that cannot be opened is removed on reset.
func migrateAuthzAccountSecrets(ctx context.Context, tx *sql.Tx, r *resealer) error {
	type row struct {
		id              int64
		password, token string
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(password,''), COALESCE(token,'') FROM task_authz_accounts
WHERE password IS NOT NULL OR token IS NOT NULL ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.password, &x.token); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range all {
		where := "task_authz_accounts row " + strconv.FormatInt(x.id, 10)
		password, passwordChanged, err := r.value(x.password, secretAAD("task_authz_accounts", "password", x.id), where+" password")
		if err != nil {
			return err
		}
		token, tokenChanged, err := r.value(x.token, secretAAD("task_authz_accounts", "token", x.id), where+" token")
		if err != nil {
			return err
		}
		if !passwordChanged && !tokenChanged {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE task_authz_accounts SET password=NULLIF($2,''), token=NULLIF($3,'') WHERE id=$1`, x.id, password, token); err != nil {
			return err
		}
	}
	return nil
}

func migrateSettingSecrets(ctx context.Context, tx *sql.Tx, r *resealer) error {
	type row struct{ key, value string }
	rows, err := tx.QueryContext(ctx, `SELECT key, value FROM settings WHERE key = ANY($1::text[]) ORDER BY key FOR UPDATE`, sealedSettings)
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.key, &x.value); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range all {
		out, changed, err := r.value(x.value, secretAAD("settings", "value", x.key), "settings.value key "+x.key)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value=$2, updated_at=now() WHERE key=$1`, x.key, out); err != nil {
			return err
		}
	}
	return nil
}

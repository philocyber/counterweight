package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/sealed"
)

func openSecretsTestDB(t *testing.T) *DB {
	t.Helper()
	t.Setenv(sealed.KeyEnv, "")
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func uniqueName(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func saveSecretProfile(t *testing.T, d *DB, apiKey, proxy string) int64 {
	t.Helper()
	id, err := d.SaveProfile(&LLMProfile{Name: uniqueName("sealed-profile"), Format: "openai", Model: "m", APIKey: apiKey, Proxy: proxy, Streaming: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id) })
	return id
}

func rawProfileSecrets(t *testing.T, d *DB, id int64) (apiKey, proxy string) {
	t.Helper()
	if err := d.QueryRow(`SELECT COALESCE(api_key,''), COALESCE(proxy,'') FROM llm_profiles WHERE id=$1`, id).Scan(&apiKey, &proxy); err != nil {
		t.Fatal(err)
	}
	return apiKey, proxy
}

func TestCredentialsSealedAtRest(t *testing.T) {
	d := openSecretsTestDB(t)
	ctx := context.Background()

	const key, proxy = "sk-at-rest-profile-key", "http://proxyuser:proxypass@127.0.0.1:3128"
	pid := saveSecretProfile(t, d, key, proxy)
	rawKey, rawProxy := rawProfileSecrets(t, d, pid)
	if !sealed.IsSealed(rawKey) || !sealed.IsSealed(rawProxy) || strings.Contains(rawKey+rawProxy, "at-rest") || strings.Contains(rawProxy, "proxypass") {
		t.Fatalf("profile secrets stored in plaintext: %q %q", rawKey, rawProxy)
	}
	got, err := d.ProfileByID(pid)
	if err != nil || got.APIKey != key || got.Proxy != proxy {
		t.Fatalf("profile read back: %+v %v", got, err)
	}
	// An update that keeps the key re-seals only the proxy.
	got.APIKey, got.Proxy = "", "socks5://u:p2@127.0.0.1:1080"
	if _, err := d.SaveProfile(got); err != nil {
		t.Fatal(err)
	}
	again, err := d.ProfileByID(pid)
	if err != nil || again.APIKey != key || again.Proxy != "socks5://u:p2@127.0.0.1:1080" {
		t.Fatalf("profile update: %+v %v", again, err)
	}
	listed, err := d.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range listed {
		if p.ID == pid && (p.Proxy != again.Proxy || p.APIKey != "" || p.APIKeyHint != "…-key") {
			t.Fatalf("listed profile: %+v", p)
		}
	}

	mcpID, err := d.SaveMCP(&MCPServer{Name: uniqueName("sealed-mcp"), Transport: "stdio", Command: "true",
		Env: json.RawMessage(`{"API_TOKEN":"mcp-at-rest-token","EMPTY":"","PORT":8080}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM mcp_servers WHERE id=$1`, mcpID) })
	var rawEnv string
	if err := d.QueryRow(`SELECT env::text FROM mcp_servers WHERE id=$1`, mcpID).Scan(&rawEnv); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawEnv, "mcp-at-rest-token") || !strings.Contains(rawEnv, sealed.Prefix) || !strings.Contains(rawEnv, "API_TOKEN") {
		t.Fatalf("mcp env stored in plaintext: %s", rawEnv)
	}
	servers, err := d.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range servers {
		if m.ID == mcpID {
			var env map[string]any
			_ = json.Unmarshal(m.Env, &env)
			found = env["API_TOKEN"] == "mcp-at-rest-token" && env["EMPTY"] == "" && env["PORT"] == float64(8080)
		}
	}
	if !found {
		t.Fatal("mcp env not decrypted on read")
	}

	chID, err := d.SaveNotificationChannel(ctx, &NotificationChannel{Name: uniqueName("sealed-channel"), Kind: "telegram",
		Config: json.RawMessage(`{"bot_token":"tg-at-rest-token","chat_id":"1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM notification_channels WHERE id=$1`, chID) })
	var rawCfg string
	if err := d.QueryRow(`SELECT config::text FROM notification_channels WHERE id=$1`, chID).Scan(&rawCfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawCfg, "tg-at-rest-token") || !strings.Contains(rawCfg, sealed.Prefix) {
		t.Fatalf("channel config stored in plaintext: %s", rawCfg)
	}
	ch, err := d.NotificationChannelByID(ctx, chID)
	if err != nil || !strings.Contains(string(ch.Config), "tg-at-rest-token") {
		t.Fatalf("channel config read back: %s %v", ch.Config, err)
	}

	const setting = "brave_search_api_key"
	old, hadOld, _ := d.GetSetting(setting)
	t.Cleanup(func() {
		if hadOld {
			_ = d.SetSetting(setting, old)
		} else {
			_, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, setting)
		}
	})
	if err := d.SetSetting(setting, "brave-at-rest-key"); err != nil {
		t.Fatal(err)
	}
	var rawSetting string
	if err := d.QueryRow(`SELECT value FROM settings WHERE key=$1`, setting).Scan(&rawSetting); err != nil {
		t.Fatal(err)
	}
	if !sealed.IsSealed(rawSetting) {
		t.Fatalf("setting stored in plaintext: %q", rawSetting)
	}
	if v, ok, err := d.GetSetting(setting); err != nil || !ok || v != "brave-at-rest-key" {
		t.Fatalf("setting read back: %q %v %v", v, ok, err)
	}
}

func TestSealedCredentialBoundToRow(t *testing.T) {
	d := openSecretsTestDB(t)
	a := saveSecretProfile(t, d, "sk-row-a-secret", "")
	b := saveSecretProfile(t, d, "sk-row-b-secret", "")
	rawA, _ := rawProfileSecrets(t, d, a)
	if _, err := d.Exec(`UPDATE llm_profiles SET api_key=$2 WHERE id=$1`, b, rawA); err != nil {
		t.Fatal(err)
	}
	if p, err := d.ProfileByID(b); !errors.Is(err, sealed.ErrDecrypt) {
		t.Fatalf("ciphertext moved to another row must not decrypt: %+v %v", p, err)
	}
	// The same ciphertext in another column fails as well.
	if _, err := d.Exec(`UPDATE llm_profiles SET api_key=NULL, proxy=$2 WHERE id=$1`, b, rawA); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ProfileByID(b); !errors.Is(err, sealed.ErrDecrypt) {
		t.Fatalf("ciphertext moved to another column must not decrypt: %v", err)
	}
}

func TestLegacyPlaintextPassthroughAndMigrationIdempotent(t *testing.T) {
	d := openSecretsTestDB(t)
	ctx := context.Background()
	var pid, mid, cid int64
	if err := d.QueryRow(`INSERT INTO llm_profiles(name,format,model,api_key,proxy) VALUES ($1,'openai','m','sk-legacy-plain','http://u:legacy@h:1') RETURNING id`, uniqueName("legacy")).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, pid) })
	if err := d.QueryRow(`INSERT INTO mcp_servers(name,transport,command,env) VALUES ($1,'stdio','true','{"TOKEN":"legacy-mcp"}') RETURNING id`, uniqueName("legacy-mcp")).Scan(&mid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM mcp_servers WHERE id=$1`, mid) })
	if err := d.QueryRow(`INSERT INTO notification_channels(name,kind,config) VALUES ($1,'wecom','{"webhook":"https://example.invalid/legacy-hook"}') RETURNING id`, uniqueName("legacy-ch")).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM notification_channels WHERE id=$1`, cid) })

	// Plaintext rows read normally before migration.
	if p, err := d.ProfileByID(pid); err != nil || p.APIKey != "sk-legacy-plain" || p.Proxy != "http://u:legacy@h:1" {
		t.Fatalf("legacy profile: %+v %v", p, err)
	}

	raw := func() string {
		var k, px, env, cfg string
		if err := d.QueryRow(`SELECT COALESCE(p.api_key,''), COALESCE(p.proxy,''), m.env::text, c.config::text
FROM llm_profiles p, mcp_servers m, notification_channels c WHERE p.id=$1 AND m.id=$2 AND c.id=$3`, pid, mid, cid).Scan(&k, &px, &env, &cfg); err != nil {
			t.Fatal(err)
		}
		return k + "|" + px + "|" + env + "|" + cfg
	}
	if _, _, err := migrateSecrets(ctx, d.DB, testKeyring(), false); err != nil {
		t.Fatal(err)
	}
	first := raw()
	for _, plain := range []string{"sk-legacy-plain", "legacy@h", "legacy-mcp", "legacy-hook"} {
		if strings.Contains(first, plain) {
			t.Fatalf("migration left %q in plaintext: %s", plain, first)
		}
	}
	if _, _, err := migrateSecrets(ctx, d.DB, testKeyring(), false); err != nil {
		t.Fatal(err)
	}
	if second := raw(); second != first {
		t.Fatalf("second migration rewrote sealed values:\n%s\n%s", first, second)
	}
	if p, err := d.ProfileByID(pid); err != nil || p.APIKey != "sk-legacy-plain" || p.Proxy != "http://u:legacy@h:1" {
		t.Fatalf("migrated profile: %+v %v", p, err)
	}
	ch, err := d.NotificationChannelByID(ctx, cid)
	if err != nil || !strings.Contains(string(ch.Config), "legacy-hook") {
		t.Fatalf("migrated channel: %s %v", ch.Config, err)
	}
}

func TestInitSecretsFailsClosedWithoutKey(t *testing.T) {
	d := openSecretsTestDB(t)
	ctx := context.Background()
	pid := saveSecretProfile(t, d, "sk-fail-closed", "")
	before, _ := rawProfileSecrets(t, d, pid)

	dir := t.TempDir()
	fromDir := func(create bool) (*sealed.Keyring, error) { return sealed.Load(dir, create) }
	_, err := initSecrets(ctx, d.DB, fromDir, false)
	keyPath := filepath.Join(dir, sealed.KeyFile)
	if !errors.Is(err, sealed.ErrKeyMissing) || !strings.Contains(err.Error(), keyPath) || !strings.Contains(err.Error(), ResetCredentialsEnv) {
		t.Fatalf("missing key with ciphertext present: %v", err)
	}
	if _, statErr := os.Stat(keyPath); !os.IsNotExist(statErr) {
		t.Fatal("a new key was created while encrypted credentials exist")
	}

	wrong, _ := sealed.New(keyPath, bytes.Repeat([]byte{9}, sealed.KeySize))
	_, err = initSecrets(ctx, d.DB, func(bool) (*sealed.Keyring, error) { return wrong, nil }, false)
	if err == nil || !strings.Contains(err.Error(), testKeyring().CurrentID()) || !strings.Contains(err.Error(), keyPath) {
		t.Fatalf("wrong key: %v", err)
	}
	if after, _ := rawProfileSecrets(t, d, pid); after != before {
		t.Fatal("failed startup modified stored ciphertext")
	}
}

func TestInitSecretsResetDiscardsUnreadable(t *testing.T) {
	d := openSecretsTestDB(t)
	ctx := context.Background()
	pid := saveSecretProfile(t, d, "sk-will-be-lost", "")
	keep := saveSecretProfile(t, d, "sk-still-readable", "")
	foreign, _ := sealed.New("foreign", bytes.Repeat([]byte{7}, sealed.KeySize))
	lost, _ := foreign.Seal("sk-will-be-lost", secretAAD("llm_profiles", "api_key", pid))
	if _, err := d.Exec(`UPDATE llm_profiles SET api_key=$2 WHERE id=$1`, pid, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := initSecrets(ctx, d.DB, func(bool) (*sealed.Keyring, error) { return testKeyring(), nil }, false); err == nil {
		t.Fatal("unknown key accepted without reset")
	}
	if _, err := initSecrets(ctx, d.DB, func(bool) (*sealed.Keyring, error) { return testKeyring(), nil }, true); err != nil {
		t.Fatal(err)
	}
	p, err := d.ProfileByID(pid)
	if err != nil || p.APIKey != "" {
		t.Fatalf("unreadable key not discarded: %+v %v", p, err)
	}
	var hint *string
	_ = d.QueryRow(`SELECT api_key_hint FROM llm_profiles WHERE id=$1`, pid).Scan(&hint)
	if hint != nil {
		t.Fatalf("hint of a discarded key kept: %q", *hint)
	}
	if k, err := d.ProfileByID(keep); err != nil || k.APIKey != "sk-still-readable" {
		t.Fatalf("readable key affected by reset: %+v %v", k, err)
	}
}

func TestSecretKeyRotation(t *testing.T) {
	d := openSecretsTestDB(t)
	ctx := context.Background()
	pid := saveSecretProfile(t, d, "sk-rotate-me", "http://u:rot@h:1")
	newKey := bytes.Repeat([]byte{5}, sealed.KeySize)
	rotated, _ := sealed.New("rotated", testKey[:], newKey)
	back, _ := sealed.New("back", newKey, testKey[:])
	previous := secretKeys.Load()
	t.Cleanup(func() {
		// Return the shared test database to the test key for later tests and packages.
		if _, _, err := migrateSecrets(ctx, d.DB, back, false); err != nil {
			t.Errorf("restore test key: %v", err)
		}
		secretKeys.Store(previous)
	})

	k, err := initSecrets(ctx, d.DB, func(bool) (*sealed.Keyring, error) { return rotated, nil }, false)
	if err != nil {
		t.Fatal(err)
	}
	rawKey, rawProxy := rawProfileSecrets(t, d, pid)
	for _, v := range []string{rawKey, rawProxy} {
		if _, id, _ := sealed.Header(v); id != sealed.KeyID(newKey) {
			t.Fatalf("value not re-encrypted with the new key: %q", v)
		}
	}
	secretKeys.Store(k)
	if p, err := d.ProfileByID(pid); err != nil || p.APIKey != "sk-rotate-me" || p.Proxy != "http://u:rot@h:1" {
		t.Fatalf("rotated profile: %+v %v", p, err)
	}
	// After rotation the old key is no longer needed.
	only, _ := sealed.New("new only", newKey)
	if _, err := initSecrets(ctx, d.DB, func(bool) (*sealed.Keyring, error) { return only, nil }, false); err != nil {
		t.Fatalf("old key still required after rotation: %v", err)
	}
}

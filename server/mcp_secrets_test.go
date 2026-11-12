package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

// TestMCPEnvValuesAreWriteOnly covers the MCP secret round trip through the real
// mux: responses mask values, the placeholder keeps the stored value, a new value
// replaces it and a removed key disappears.
func TestMCPEnvValuesAreWriteOnly(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("database unavailable (%v) — skipping MCP secret test", err)
	}
	if m.pg == nil {
		t.Skip("postgres unavailable — skipping MCP secret test")
	}
	td := t.TempDir()
	s := New(t.Context(), m, td, td, td)
	h := s.Handler()
	tok, err := signTestSession(t, s)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	const name, renamed = "t-mcp-secret", "t-mcp-secret-renamed"
	cleanup := func() { m.pg.Exec(`DELETE FROM mcp_servers WHERE name IN ($1,$2)`, name, renamed) }
	cleanup()
	t.Cleanup(cleanup)

	do := func(method, path string, body any) (int, string) {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	listed := func(id int64) *db.MCPServer {
		t.Helper()
		code, body := do("GET", "/api/mcp", nil)
		if code != 200 {
			t.Fatalf("list: %d %s", code, body)
		}
		if strings.Contains(body, "s3cret-token") || strings.Contains(body, "other-value") || strings.Contains(body, "replacement-value") {
			t.Fatalf("list exposes a stored secret: %s", body)
		}
		var out struct {
			Servers []*db.MCPServer `json:"servers"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		for _, srv := range out.Servers {
			if srv.ID == id {
				return srv
			}
		}
		t.Fatalf("server %d not listed", id)
		return nil
	}
	stored := func(id int64) (*db.MCPServer, map[string]string) {
		t.Helper()
		all, err := m.pg.ListMCP()
		if err != nil {
			t.Fatal(err)
		}
		for _, srv := range all {
			if srv.ID == id {
				return srv, jsonStrMap(srv.Env)
			}
		}
		t.Fatalf("server %d not stored", id)
		return nil, nil
	}
	envOf := func(srv *db.MCPServer) map[string]string {
		var env map[string]string
		if err := json.Unmarshal(srv.Env, &env); err != nil {
			t.Fatalf("env %s: %v", srv.Env, err)
		}
		return env
	}

	// Create with secrets; disabled so no discovery is attempted.
	code, body := do("POST", "/api/mcp", map[string]any{
		"name": name, "transport": "http", "url": "http://127.0.0.1:9/mcp", "enabled": false,
		"env": map[string]string{"Authorization": "Bearer s3cret-token", "X-Other": "other-value", "X-Empty": ""},
	})
	if code != 200 || strings.Contains(body, "s3cret-token") {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == 0 {
		t.Fatalf("create response %s: %v", body, err)
	}
	id := created.ID

	// The listing keeps the keys and masks every non-empty value.
	got := envOf(listed(id))
	if len(got) != 3 || got["Authorization"] != mcpSecretPlaceholder || got["X-Other"] != mcpSecretPlaceholder || got["X-Empty"] != "" {
		t.Fatalf("masked env=%v", got)
	}

	// Re-sending the listed object (as the enable toggle does) keeps every secret.
	srv := listed(id)
	srv.Enabled = false
	if code, body = do("POST", "/api/mcp", srv); code != 200 {
		t.Fatalf("toggle save: %d %s", code, body)
	}
	if _, env := stored(id); env["Authorization"] != "Bearer s3cret-token" || env["X-Other"] != "other-value" || len(env) != 3 {
		t.Fatalf("toggle save changed secrets: %v", env)
	}

	// Rename; keep Authorization via the placeholder, replace X-Other, drop X-Empty.
	code, body = do("POST", "/api/mcp", map[string]any{
		"id": id, "name": renamed, "transport": "http", "url": "http://127.0.0.1:9/mcp", "enabled": false,
		"env": map[string]string{"Authorization": mcpSecretPlaceholder, "X-Other": "replacement-value"},
	})
	if code != 200 {
		t.Fatalf("update: %d %s", code, body)
	}
	row, env := stored(id)
	if row.Name != renamed || len(env) != 2 || env["Authorization"] != "Bearer s3cret-token" || env["X-Other"] != "replacement-value" {
		t.Fatalf("after update name=%q env=%v", row.Name, env)
	}
	if got := envOf(listed(id)); len(got) != 2 || got["Authorization"] != mcpSecretPlaceholder || got["X-Other"] != mcpSecretPlaceholder {
		t.Fatalf("masked env after update=%v", got)
	}

	// A placeholder for a key with no stored value is rejected and changes nothing.
	code, body = do("POST", "/api/mcp", map[string]any{
		"id": id, "name": renamed, "transport": "http", "url": "http://127.0.0.1:9/mcp",
		"env": map[string]string{"Authorization": mcpSecretPlaceholder, "X-New": mcpSecretPlaceholder},
	})
	if code != 400 {
		t.Fatalf("unknown placeholder accepted: %d %s", code, body)
	}
	if _, env := stored(id); len(env) != 2 || env["Authorization"] != "Bearer s3cret-token" {
		t.Fatalf("rejected update changed env: %v", env)
	}

	// A new server cannot borrow another server's secret through the placeholder.
	code, body = do("POST", "/api/mcp", map[string]any{
		"name": name, "transport": "http", "url": "http://127.0.0.1:9/mcp",
		"env": map[string]string{"Authorization": mcpSecretPlaceholder},
	})
	if code != 400 {
		t.Fatalf("placeholder on create accepted: %d %s", code, body)
	}
}

func TestMaskMCPEnv(t *testing.T) {
	for raw, want := range map[string]string{
		``:                       `{}`,
		`null`:                   `{}`,
		`[]`:                     `{}`,
		`{}`:                     `{}`,
		`{"A":"x","B":"","C":1}`: `{"A":"********","B":"","C":"********"}`,
	} {
		if got := string(maskMCPEnv(json.RawMessage(raw))); got != want {
			t.Errorf("mask(%q)=%s, want %s", raw, got, want)
		}
	}
}

// mcpSecretFixture builds a real mux with a session token for MCP API tests.
func mcpSecretFixture(t *testing.T, names ...string) (*Manager, func(method, path string, body any) (int, string)) {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("database unavailable (%v) — skipping MCP secret test", err)
	}
	if m.pg == nil {
		t.Skip("postgres unavailable — skipping MCP secret test")
	}
	td := t.TempDir()
	s := New(t.Context(), m, td, td, td)
	h := s.Handler()
	tok, err := signTestSession(t, s)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	cleanup := func() {
		for _, n := range names {
			m.pg.Exec(`DELETE FROM mcp_servers WHERE name=$1`, n)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	return m, func(method, path string, body any) (int, string) {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
}

func TestMCPPlaceholderCannotFollowDestinationChange(t *testing.T) {
	const name, renamed = "t-mcp-dest", "t-mcp-dest-renamed"
	m, do := mcpSecretFixture(t, name, renamed)
	var hits atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer attacker.Close()
	const original = "http://127.0.0.1:9/mcp"
	code, body := do("POST", "/api/mcp", map[string]any{
		"name": name, "transport": "http", "url": original, "enabled": false,
		"env": map[string]string{"Authorization": "Bearer dest-secret"},
	})
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	row := func() *db.MCPServer {
		all, err := m.pg.ListMCP()
		if err != nil {
			t.Fatal(err)
		}
		for _, srv := range all {
			if srv.ID == created.ID {
				return srv
			}
		}
		t.Fatal("row missing")
		return nil
	}
	base := map[string]any{"id": created.ID, "name": name, "transport": "http", "url": original, "enabled": true,
		"env": map[string]string{"Authorization": mcpSecretPlaceholder}}
	with := func(k string, v any) map[string]any {
		out := map[string]any{}
		for kk, vv := range base {
			out[kk] = vv
		}
		out[k] = v
		return out
	}
	for label, req := range map[string]map[string]any{
		"url":       with("url", attacker.URL),
		"transport": with("transport", "sse"),
		"insecure":  with("insecure", true),
		"args":      with("args", []string{"--extra"}),
		"command":   with("command", "evil"),
	} {
		code, body := do("POST", "/api/mcp", req)
		if code != 400 || !strings.Contains(body, "Re-enter") {
			t.Errorf("%s change with placeholder: %d %s", label, code, body)
		}
		got := row()
		if got.URL != original || got.Transport != "http" || got.Insecure || got.Enabled || got.Name != name || jsonStrMap(got.Env)["Authorization"] != "Bearer dest-secret" {
			t.Fatalf("%s: rejected save changed the row: %+v env=%s", label, got, got.Env)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("attacker endpoint received %d requests", n)
	}

	// Rename alone keeps the secret through the placeholder.
	if code, body := do("POST", "/api/mcp", with("name", renamed)); code != 200 {
		t.Fatalf("rename: %d %s", code, body)
	}
	if got := row(); got.Name != renamed || jsonStrMap(got.Env)["Authorization"] != "Bearer dest-secret" {
		t.Fatalf("rename lost secret: %+v %s", got, got.Env)
	}

	// Changing the URL with the secret re-entered is allowed.
	req := with("url", "http://127.0.0.1:10/mcp")
	req["name"], req["enabled"] = renamed, false
	req["env"] = map[string]string{"Authorization": "Bearer new-secret"}
	if code, body := do("POST", "/api/mcp", req); code != 200 {
		t.Fatalf("url change with new value: %d %s", code, body)
	}
	if got := row(); got.URL != "http://127.0.0.1:10/mcp" || jsonStrMap(got.Env)["Authorization"] != "Bearer new-secret" {
		t.Fatalf("url change not saved: %+v %s", got, got.Env)
	}
}

func TestMCPDiscoveryErrorsRedactStoredSecrets(t *testing.T) {
	const name = "t-mcp-echo"
	_, do := mcpSecretFixture(t, name)
	const secret = "echo-secret-value-123"
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid API key: " + r.Header.Get("Authorization") + " / " + r.Header.Get("X-Api-Key")))
	}))
	defer echo.Close()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	code, body := do("POST", "/api/mcp", map[string]any{
		"name": name, "transport": "http", "url": echo.URL, "enabled": true,
		"env": map[string]string{"Authorization": "Bearer " + secret, "X-Api-Key": "key-" + secret},
	})
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	code, body = do("POST", "/api/mcp/"+itoaTest(created.ID)+"/refresh", map[string]any{})
	if code != http.StatusBadGateway {
		t.Fatalf("refresh: %d %s", code, body)
	}
	if strings.Contains(body, secret) || !strings.Contains(body, mcpSecretPlaceholder) {
		t.Fatalf("refresh error not redacted: %s", body)
	}
	if !strings.Contains(logs.String(), "invalid API key") || strings.Contains(logs.String(), secret) {
		t.Fatalf("discovery log not redacted: %s", logs.String())
	}
}

func TestRedactMCPSecrets(t *testing.T) {
	env := json.RawMessage(`{"Authorization":"Bearer tok-abc","Short":"abc","Empty":""}`)
	got := redactMCPSecrets("got Bearer tok-abc, then tok-abc and abc", env)
	if want := "got ********, then ******** and abc"; got != want {
		t.Fatalf("redact=%q, want %q", got, want)
	}
}

func TestScopeSentryKeyCannotFollowURLChange(t *testing.T) {
	m, do := mcpSecretFixture(t)
	s := &Server{m: m}
	prior, err := s.findMCPByName(scopeSentryMCPName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if prior != nil {
			_, _ = m.pg.SaveMCP(prior)
		} else {
			m.pg.Exec(`DELETE FROM mcp_servers WHERE name=$1`, scopeSentryMCPName)
		}
	})
	if prior != nil {
		reset := *prior
		reset.URL, reset.Env, reset.Enabled = "", json.RawMessage(`{"X-API-Key":""}`), false
		if _, err := m.pg.SaveMCP(&reset); err != nil {
			t.Fatal(err)
		}
	}
	var hits atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer attacker.Close()
	// Store a key with an unreachable URL (discovery fails fast).
	if code, body := do("POST", "/api/sync/scopesentry/datasource", map[string]string{"url": "http://127.0.0.1:9/mcp", "api_key": "ss-secret-key"}); code != 200 {
		t.Fatalf("configure: %d %s", code, body)
	}
	code, body := do("POST", "/api/sync/scopesentry/datasource", map[string]string{"url": attacker.URL})
	if code != 400 || !strings.Contains(body, "Re-enter the API key") {
		t.Fatalf("url change without key: %d %s", code, body)
	}
	row, err := s.findMCPByName(scopeSentryMCPName)
	if err != nil || row == nil || row.URL != "http://127.0.0.1:9/mcp" || jsonStrMap(row.Env)["X-API-Key"] != "ss-secret-key" {
		t.Fatalf("rejected change altered the row: %+v %v", row, err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("attacker endpoint received %d requests", n)
	}
	// The same URL without a key, and a new URL with a new key, are allowed.
	if code, body := do("POST", "/api/sync/scopesentry/datasource", map[string]string{"url": "http://127.0.0.1:9/mcp"}); code != 200 {
		t.Fatalf("same url: %d %s", code, body)
	}
	if code, body := do("POST", "/api/sync/scopesentry/datasource", map[string]string{"url": attacker.URL, "api_key": "ss-new-key"}); code != 200 {
		t.Fatalf("url change with key: %d %s", code, body)
	}
}

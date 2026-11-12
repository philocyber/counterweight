package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/server"
	"github.com/jackc/pgx/v5"
)

func TestNativeHTTPSManagementAuthentication(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_TLS_E2E") != "1" {
		t.Skip("requires opt-in and a disposable counterweight_transport_* database")
	}
	dsn := os.Getenv("COUNTERWEIGHT_PG_DSN")
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(parsed.Database, "counterweight_transport_") {
		t.Fatal("TLS fixture requires an explicitly selected disposable database")
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "COUNTERWEIGHT_LLM_PROVIDER"} {
		t.Setenv(key, "")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	var profiles int
	if err := pg.QueryRow("SELECT count(*) FROM llm_profiles").Scan(&profiles); err != nil || profiles != 0 {
		t.Fatal("TLS fixture must contain no configured providers")
	}
	if _, err := pg.Exec("UPDATE mcp_servers SET enabled=false"); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec("DELETE FROM settings WHERE key='auth.password_hash'"); err != nil {
		t.Fatal(err)
	}
	m, err := server.NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	s := server.New(ctx, m, dir, dir, dir)
	setup, err := server.ReadSetupToken(dir)
	if err != nil {
		t.Fatal(err)
	}

	certificateFixture := httptest.NewTLSServer(http.NotFoundHandler())
	defer certificateFixture.Close()
	cert := certificateFixture.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "tls-cert.pem"), filepath.Join(dir, "tls-key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := managementTLS(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	https := httptest.NewUnstartedServer(s.Handler())
	https.TLS = tlsConfig
	https.StartTLS()
	defer https.Close()
	client := certificateFixture.Client()
	request := func(method, path string, body any, token string, want int) map[string]any {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.NewRequest(method, https.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.TLS == nil || response.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d, TLS=%v", method, path, response.StatusCode, want, response.TLS != nil)
		}
		var decoded map[string]any
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	const password = "synthetic-tls-fixture-password"
	request("POST", "/api/auth/init", map[string]string{"password": password, "setup_token": "wrong"}, "", http.StatusForbidden)
	enrollment := request("POST", "/api/auth/init", map[string]string{"password": password, "setup_token": setup}, "", http.StatusOK)
	token, _ := enrollment["token"].(string)
	if token == "" {
		t.Fatal("HTTPS enrollment omitted session token")
	}
	request("GET", "/api/settings", nil, "", http.StatusUnauthorized)
	request("GET", "/api/settings", nil, token, http.StatusOK)
	login := request("POST", "/api/auth/login", map[string]string{"username": "COUNTERWEIGHT", "password": password}, "", http.StatusOK)
	loginToken, _ := login["token"].(string)
	if loginToken == "" {
		t.Fatal("HTTPS login omitted session token")
	}
	request("POST", "/api/auth/change-password", map[string]string{"old_password": password, "new_password": "synthetic-new-tls-password"}, loginToken, http.StatusOK)
	request("GET", "/api/settings", nil, token, http.StatusUnauthorized)
	request("GET", "/api/settings", nil, loginToken, http.StatusUnauthorized)
	updated := request("POST", "/api/auth/login", map[string]string{"username": "COUNTERWEIGHT", "password": "synthetic-new-tls-password"}, "", http.StatusOK)
	updatedToken, _ := updated["token"].(string)
	if updatedToken == "" {
		t.Fatal("replacement password login omitted token")
	}
	request("GET", "/api/settings", nil, updatedToken, http.StatusOK)
}

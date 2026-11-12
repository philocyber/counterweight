package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/subscription"
)

func subscriptionTestServer(t *testing.T) *Server {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	dir := t.TempDir()
	credentialDir := filepath.Join(dir, "chatgpt-subscription", "native-codex")
	os.MkdirAll(credentialDir, 0700)
	data, _ := json.Marshal(map[string]any{"7f6cf9ae-3b38-4188-a8f0-b7d5989431f3": map[string]any{"id": "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3", "email": "tester@example.test", "connected": true}})
	if err = os.WriteFile(filepath.Join(credentialDir, "connections.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "fake-codex")
	script := `#!/usr/bin/env python3
import sys,json
for line in sys.stdin:
    p=json.loads(line)
    if p.get("method")=="initialized": continue
    result={}
    if p.get("method")=="account/login/start": result={"loginId":"fixture-login","authUrl":"https://auth.openai.com/oauth/authorize?fixture=true"}
    print(json.dumps({"id":p.get("id"),"result":result}),flush=True)
`
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_CODEX_BINARY", binary)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := New(ctx, m, dir, t.TempDir(), dir)
	return s
}
func TestSubscriptionProfileResolutionAndValidation(t *testing.T) {
	s := subscriptionTestServer(t)
	cfg := agent.ConfigFrom("openai-responses", "account-model", "", "", "")
	if err := s.configureSubscription(&cfg, "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3"); err != nil {
		t.Fatal(err)
	}
	if cfg.SubscriptionNative == nil || cfg.APIKey != "" || cfg.BaseURL != subscription.APIBase {
		t.Fatal("credential source not wired")
	}
	for _, test := range []struct {
		format, url string
		stream      bool
	}{
		{"anthropic", "", true}, {"openai-responses", "https://other.test/v1", true}, {"openai-responses", "", false},
	} {
		cfg := agent.ConfigFrom(test.format, "account-model", test.url, "", "")
		cfg.Stream = test.stream
		if err := s.configureSubscription(&cfg, "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3"); err == nil {
			t.Fatal("unsafe subscription configuration accepted")
		}
	}
	p := &db.LLMProfile{Name: "subscription-resolution", Format: "openai-responses", Model: "account-model", SubscriptionID: "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3", Streaming: true}
	id, err := s.m.pg.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	t.Cleanup(func() {
		s.m.pg.Exec(`UPDATE llm_profiles SET is_default=false WHERE id=$1`, id)
		s.m.pg.DeleteProfile(id)
	})
	provider, cfg, ok := s.providerForProfile(id)
	if !ok || provider == nil || cfg.SubscriptionID != "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3" {
		t.Fatal("profile unavailable without API key")
	}
	if r := s.resolutionFromProfile(p, "task_chain"); !r.Available {
		t.Fatalf("task resolution=%+v", r)
	}
	if err = s.m.pg.SetActiveProfile(id); err != nil {
		t.Fatal(err)
	}
	if cfg, ok = s.loadLLMConfig(); !ok || cfg.SubscriptionNative == nil {
		t.Fatal("active subscription unavailable")
	}
}
func TestSubscriptionAccountsAndAttemptsNeverExposeTokens(t *testing.T) {
	s := subscriptionTestServer(t)
	w := httptest.NewRecorder()
	s.subscriptionAccounts(w, httptest.NewRequest("GET", "/api/llm/subscription/accounts", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unsafe accounts response: %s", w.Body.String())
	}
	request := httptest.NewRequest("POST", "http://localhost:8787/api/llm/subscription/start", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	s.subscriptionStart(w, request)
	if w.Code != 200 {
		t.Fatalf("start: %s", w.Body.String())
	}
	var a subscription.Attempt
	json.Unmarshal(w.Body.Bytes(), &a)
	defer s.subscriptions.Cancel(a.ID)
	w = httptest.NewRecorder()
	request = httptest.NewRequest("GET", "/api/llm/subscription/attempts/"+a.ID, nil)
	request.SetPathValue("id", a.ID)
	s.subscriptionStatus(w, request)
	if strings.Contains(w.Body.String(), "authorize?") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("status returned credentials")
	}
	w = httptest.NewRecorder()
	s.subscriptionStart(w, httptest.NewRequest("POST", "https://remote.example/api/llm/subscription/start", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Fatal("remote-browser loopback flow accepted")
	}
}

func TestSubscriptionRoutesRequireAuthentication(t *testing.T) {
	s := subscriptionTestServer(t)
	handler := s.Handler()
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/llm/subscription/accounts"}, {"POST", "/api/llm/subscription/start"}, {"GET", "/api/llm/subscription/attempts/unknown"}, {"DELETE", "/api/llm/subscription/attempts/unknown"},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)))
		if w.Code != 401 {
			t.Fatalf("%s %s: status %d", route.method, route.path, w.Code)
		}
	}
}

func TestSubscriptionRegisteredRoutesWithAuthenticatedOwner(t *testing.T) {
	s := subscriptionTestServer(t)
	token, err := signTestSession(t, s)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8787"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	accounts := call("GET", "/api/llm/subscription/accounts", "")
	if accounts.Code != 200 || !strings.Contains(accounts.Body.String(), "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3") || strings.Contains(accounts.Body.String(), "secret") {
		t.Fatal("registered account route failed")
	}
	start := call("POST", "/api/llm/subscription/start", `{}`)
	if start.Code != 200 {
		t.Fatalf("start: %d %s", start.Code, start.Body.String())
	}
	var attempt subscription.Attempt
	if err = json.Unmarshal(start.Body.Bytes(), &attempt); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.subscriptions.Cancel(attempt.ID) })
	status := call("GET", "/api/llm/subscription/attempts/"+attempt.ID, "")
	if status.Code != 200 || strings.Contains(status.Body.String(), "http:") {
		t.Fatal("registered status route failed")
	}
	cancelled := call("DELETE", "/api/llm/subscription/attempts/"+attempt.ID, "")
	if cancelled.Code != 200 {
		t.Fatal("registered cancellation route failed")
	}
	final, err := s.subscriptions.Status(attempt.ID)
	if err != nil || final.Status != "cancelled" {
		t.Fatal("attempt not cancelled")
	}
}

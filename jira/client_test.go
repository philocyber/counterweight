package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func init() { AllowLoopbackHTTP = true }

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Site: srv.URL, Email: "appsec@example.com", Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestNormalizeSite(t *testing.T) {
	for in, want := range map[string]string{
		"https://acme.atlassian.net/":  "https://acme.atlassian.net",
		" https://acme.atlassian.net ": "https://acme.atlassian.net",
		"http://127.0.0.1:8080":        "http://127.0.0.1:8080",
	} {
		if got, err := NormalizeSite(in); err != nil || got != want {
			t.Errorf("NormalizeSite(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "acme.atlassian.net", "http://acme.atlassian.net", "https://acme.atlassian.net/jira", "https://u:p@acme.atlassian.net", "ftp://x"} {
		if _, err := NormalizeSite(bad); err == nil {
			t.Errorf("NormalizeSite(%q) accepted", bad)
		}
	}
}

func TestClientAuthRetryAndErrors(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "appsec@example.com" || pass != "test-token" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/rest/api/3/myself":
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"accountId": "abc", "displayName": "AppSec Bot"})
		case "/rest/api/3/issue":
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"errorMessages":[],"errors":{"priority":"Field 'priority' cannot be set."}}`)
		}
	})
	u, err := c.Myself(context.Background())
	if err != nil || u.DisplayName != "AppSec Bot" || calls != 2 {
		t.Fatalf("Myself = %+v, %v after %d calls", u, err, calls)
	}
	_, err = c.CreateIssue(context.Background(), map[string]any{"summary": "x"})
	if err == nil || !strings.Contains(err.Error(), "priority: Field 'priority' cannot be set.") {
		t.Fatalf("CreateIssue error = %v", err)
	}
}

func TestTransitionToPicksStatusCategory(t *testing.T) {
	var posted string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"transitions":[
				{"id":"11","name":"Start","to":{"name":"In Progress","statusCategory":{"key":"indeterminate"}}},
				{"id":"31","name":"Resolve","to":{"name":"Done","statusCategory":{"key":"done"}}}]}`)
			return
		}
		var body struct {
			Transition struct {
				ID string `json:"id"`
			} `json:"transition"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		posted = body.Transition.ID
		w.WriteHeader(204)
	})
	name, err := c.TransitionTo(context.Background(), "SEC-1", "done")
	if err != nil || name != "Done" || posted != "31" {
		t.Fatalf("TransitionTo = %q, %v, posted %q", name, err, posted)
	}
	name, err = c.TransitionTo(context.Background(), "SEC-1", "new")
	if err != nil || name != "" {
		t.Fatalf("missing category must be a no-op, got %q, %v", name, err)
	}
}

func TestFromMarkdownBuildsADF(t *testing.T) {
	md := strings.Join([]string{
		"## Impact",
		"Uses `render_template_string` and **unsafe** input, see [docs](https://example.com/a).",
		"",
		"- first",
		"- second",
		"1. step one",
		"",
		"```python",
		"print('x')",
		"```",
		"| A | B |",
		"| --- | --- |",
		"| 1 | 2 |",
	}, "\n")
	blocks := FromMarkdown(md, 3)
	var types []string
	for _, b := range blocks {
		types = append(types, b["type"].(string))
	}
	if got := strings.Join(types, ","); got != "heading,paragraph,bulletList,orderedList,codeBlock,table" {
		t.Fatalf("blocks = %s", got)
	}
	if lvl := blocks[0]["attrs"].(map[string]any)["level"]; lvl != 3 {
		t.Fatalf("heading level = %v, want 3", lvl)
	}
	raw, _ := json.Marshal(blocks[1])
	for _, want := range []string{`"marks":[{"type":"code"}],"text":"render_template_string"`, `"type":"strong"`, `"href":"https://example.com/a"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("paragraph %s missing %s", raw, want)
		}
	}
	if blocks[4]["attrs"].(map[string]any)["language"] != "python" {
		t.Fatal("code block language lost")
	}
	if DocSize(NewDoc(blocks...)) <= 0 {
		t.Fatal("empty doc size")
	}
}

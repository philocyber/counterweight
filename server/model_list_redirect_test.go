package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModelListDoesNotFollowRedirectsWithTheKey(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer target.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer gateway.Close()
	s := &Server{m: &Manager{}}
	for _, provider := range []string{"openai", "anthropic"} {
		body := `{"provider":"` + provider + `","base_url":"` + gateway.URL + `","api_key":"sk-secret"}`
		w := httptest.NewRecorder()
		s.pgListModels(w, httptest.NewRequest(http.MethodPost, "/api/llm/models", strings.NewReader(body)))
		if strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("%s: redirect was followed: %s", provider, w.Body.String())
		}
	}
	if leaked.Load() != 0 {
		t.Fatalf("redirect target received %d requests", leaked.Load())
	}
}

package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderClientFollowsSameOriginRedirect(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusTemporaryRedirect)
			return
		}
		gotKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := quotaAwareHTTPClient("", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/old", strings.NewReader("prompt"))
	req.Header.Set("x-api-key", "key-123")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("same-origin redirect refused: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || gotKey != "key-123" {
		t.Fatalf("status=%d key=%q", resp.StatusCode, gotKey)
	}
}

func TestProviderClientRefusesCrossOriginRedirect(t *testing.T) {
	var hits atomic.Int32
	var leaked atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("x-api-key") != "" {
			leaked.Store(true)
		}
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	client, err := quotaAwareHTTPClient("", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("prompt"))
	req.Header.Set("x-api-key", "key-123")
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("cross-origin redirect was followed")
	}
	if !strings.Contains(err.Error(), "different origin") || !strings.Contains(err.Error(), "/[redacted]") {
		t.Errorf("unexpected error: %v", err)
	}
	if hits.Load() != 0 || leaked.Load() != nil {
		t.Fatalf("second server was contacted: hits=%d", hits.Load())
	}
}

func TestSameOriginRedirectCompares(t *testing.T) {
	mk := func(raw string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, raw, nil)
		return r
	}
	first := mk("https://gw.example.test/v1")
	for target, ok := range map[string]bool{
		"https://GW.example.test:443/other": true,
		"http://gw.example.test/v1":         false,
		"https://gw.example.test:8443/v1":   false,
		"https://evil.example.test/v1":      false,
	} {
		err := sameOriginRedirect(mk(target), []*http.Request{first})
		if (err == nil) != ok {
			t.Errorf("%s: err=%v want ok=%v", target, err, ok)
		}
	}
}

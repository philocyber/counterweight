package server

import (
	"encoding/json"
	"github.com/Philocyber/counterweight/locale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocaleMiddlewareErrorAndSSE(t *testing.T) {
	locale.Register("language must be en", "language must be en")
	h := withLocale(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if locale.FromContext(r.Context()) != locale.En {
			t.Error("request language missing")
		}
		if _, ok := w.(http.Flusher); !ok {
			t.Error("SSE flushing unavailable")
		}
		writeErr(w, 400, "language must be en")
	}))
	r := httptest.NewRequest("GET", "/api/settings?lang=en", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "language must be en") || w.Header().Get("Content-Language") != "en" {
		t.Fatalf("bad localized response: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Cookie") {
		t.Fatal("cookie language missing cache variation")
	}
}

func TestLocaleMiddlewarePreservesUnknownUserError(t *testing.T) {
	w := httptest.NewRecorder()
	lw := &localeWriter{w, locale.En}
	writeErr(lw, 400, "user-supplied data café")
	if !strings.Contains(w.Body.String(), "user-supplied data café") {
		t.Fatal("unknown content changed")
	}
}

func TestDynamicErrorLocalizesBeforeInterpolation(t *testing.T) {
	const template = "Asset %s is outside the authorized scope (task %d)"
	locale.Register(template, template)
	raw := "user original text/café/%s?q=<raw>"
	err := locale.Errorf(template, raw, 17)
	h := withLocale(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeError(w, 400, err) }))
	r := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var response struct {
		Error string `json:"error"`
	}
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &response); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if response.Error != "Asset "+raw+" is outside the authorized scope (task 17)" {
		t.Fatalf("wrong localized error or modified raw argument: %q", response.Error)
	}
	if err.Error() != "Asset "+raw+" is outside the authorized scope (task 17)" {
		t.Fatal("error identity changed with request language")
	}
}

func TestUpdateProgressRendersPerSubscriber(t *testing.T) {
	p := updateProgress{message: locale.M("Downloading %s (%s)…", "raw-café.zip", "12 MB"), cause: locale.Errorf("Release package does not contain %s", "raw-café.exe")}
	en := p.inLanguage(locale.En)
	if en.Message != "Downloading raw-café.zip (12 MB)…" {
		t.Fatalf("progress locale mismatch: %q", en.Message)
	}
	if !strings.Contains(en.Error, "Release package does not contain") || !strings.Contains(en.Error, "raw-café.exe") {
		t.Fatalf("error locale/raw argument mismatch: %q", en.Error)
	}
	if p.Message != "" || p.Error != "" {
		t.Fatal("subscriber mutated shared event")
	}
}

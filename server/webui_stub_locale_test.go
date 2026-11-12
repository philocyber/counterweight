//go:build !embedui

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebUIStubUsesRequestLanguage(t *testing.T) {
	// An unsupported Accept-Language (here: fr) falls through to
	// the English default, same as any other unsupported language.
	for _, lang := range []string{"en", "fr"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Language", lang)
		rec := httptest.NewRecorder()
		(&Server{}).webuiHandler().ServeHTTP(rec, req)
		if rec.Code != 404 {
			t.Fatalf("status=%d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "The frontend is not embedded") {
			t.Fatalf("%s response=%q", lang, rec.Body.String())
		}
	}
}

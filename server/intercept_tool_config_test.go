package server

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
)

func TestInterceptToolConfigRequiresExplicitList(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test database not configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ic := intercept.New(d)
	original, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ic.SetEnabledTools(original) }()
	seed := []string{"Bash", "shell_open"}
	if err := ic.SetEnabledTools(seed); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{pg: d, interceptor: ic}, jwtKey: []byte("tool-config-fixture-key")}
	token, err := signTestSession(t, s)
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string) int {
		req := httptest.NewRequest("PUT", "/api/intercept/tool-config", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		return w.Code
	}
	for _, body := range []string{``, `{}`, `{"enabled_tools":null}`, `{"prompt":"x"}`, `{"enabled_tools":["Bash"],"extra":1}`, `[]`, `{"enabled_tools":"Bash"}`} {
		if code := put(body); code < 400 || code >= 500 {
			t.Fatalf("body %q: status %d, want 4xx", body, code)
		}
		got, err := ic.GetEnabledTools()
		if err != nil || !reflect.DeepEqual(got, seed) {
			t.Fatalf("body %q changed the setting: %v %v", body, got, err)
		}
	}
	if code := put(`{"enabled_tools":[]}`); code != 200 {
		t.Fatalf("explicit empty list: %d", code)
	}
	if got, err := ic.GetEnabledTools(); err != nil || len(got) != 0 {
		t.Fatalf("explicit empty list not stored: %v %v", got, err)
	}
	if code := put(`{"enabled_tools":["Bash"]}`); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if got, err := ic.GetEnabledTools(); err != nil || !reflect.DeepEqual(got, []string{"Bash"}) {
		t.Fatalf("list not stored: %v %v", got, err)
	}
}

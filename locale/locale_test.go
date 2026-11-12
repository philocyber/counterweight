package locale

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]struct {
		want Lang
		ok   bool
	}{
		"en":    {En, true},
		"EN":    {En, true},
		"en-US": {En, true},
		"en_GB": {En, true},
		"fr":    {Default, false},
		"":      {Default, false},
		"zh-CN": {Default, false},
	}
	for in, want := range cases {
		got, ok := Normalize(in)
		if got != want.want || ok != want.ok {
			t.Errorf("Normalize(%q) = (%q,%v), want (%q,%v)", in, got, ok, want.want, want.ok)
		}
	}
}

func TestFromRequestPrecedence(t *testing.T) {
	SetServerDefault(En)
	// query wins over everything, even when it is the only supported value
	r := newReq("/x?lang=en", "fr-FR,fr;q=0.9", "fr")
	if got := FromRequest(r); got != En {
		t.Errorf("query should win: got %q", got)
	}
	// an unsupported query falls through to Accept-Language
	r = newReq("/x?lang=fr", "en-US,en;q=0.9", "fr")
	if got := FromRequest(r); got != En {
		t.Errorf("unsupported query should fall through: got %q", got)
	}
	// an unsupported Accept-Language falls through to the cookie
	r = newReq("/x", "fr-FR,fr;q=0.9", "en")
	if got := FromRequest(r); got != En {
		t.Errorf("unsupported accept-language should fall through to cookie: got %q", got)
	}
	// an unsupported cookie falls through to the server default
	r = newReq("/x", "", "fr")
	if got := FromRequest(r); got != En {
		t.Errorf("unsupported cookie should fall through to default: got %q", got)
	}
	// SetServerDefault coerces an unsupported value, so the default is always En
	SetServerDefault(Lang("xx"))
	r = newReq("/x", "", "")
	if got := FromRequest(r); got != En {
		t.Errorf("server default must coerce to en: got %q", got)
	}
	SetServerDefault(En)
}

func TestAcceptLanguageQWeights(t *testing.T) {
	// ko has a higher q than en but is not negotiable, so en still wins
	r := newReq("/x", "en;q=0.3, ko;q=0.9", "")
	if got := FromRequest(r); got != En {
		t.Errorf("unsupported language must not win on q-weight: got %q", got)
	}
	// unsupported primary with supported fallback
	r = newReq("/x", "fr, en-US;q=0.8", "")
	if got := FromRequest(r); got != En {
		t.Errorf("fallback to en: got %q", got)
	}
}

func TestFromEnvResolvesEnglishOnly(t *testing.T) {
	env := map[string]string{"COUNTERWEIGHT_LANGUAGE": "en"}
	if l, ok := FromEnv(func(k string) string { return env[k] }); !ok || l != En {
		t.Errorf("COUNTERWEIGHT_LANGUAGE=en should resolve: got %q,%v", l, ok)
	}
	env = map[string]string{"COUNTERWEIGHT_LANGUAGE": "fr"}
	if _, ok := FromEnv(func(k string) string { return env[k] }); ok {
		t.Error("COUNTERWEIGHT_LANGUAGE=fr must not resolve; only supported languages do")
	}
	if _, ok := FromEnv(func(string) string { return "" }); ok {
		t.Errorf("empty env should not resolve")
	}
	// OS LANG must be ignored
	env = map[string]string{"LANG": "ko_KR.UTF-8"}
	if _, ok := FromEnv(func(k string) string { return env[k] }); ok {
		t.Errorf("OS LANG must not be consulted")
	}
}

func TestContextRoundTrip(t *testing.T) {
	SetServerDefault(En)
	// WithLang coerces an unsupported language to the default.
	ctx := WithLang(context.Background(), Lang("xx"))
	if got := FromContext(ctx); got != En {
		t.Errorf("FromContext = %q, want coercion to en", got)
	}
	if got := FromContext(context.Background()); got != En {
		t.Errorf("FromContext default = %q", got)
	}
}

func TestCatalogFallback(t *testing.T) {
	Register("test.hello", "Hello %s")
	if got := T(En, "test.hello", "world"); got != "Hello world" {
		t.Errorf("interpolation: %q", got)
	}
	if got := T(En, "test.missing.key"); got != "test.missing.key" {
		t.Errorf("unknown key returns key: %q", got)
	}
}

func newReq(target, acceptLang, cookie string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://x"+target, nil)
	if acceptLang != "" {
		r.Header.Set("Accept-Language", acceptLang)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	return r
}

func TestRejectedLanguageWeights(t *testing.T) {
	for _, header := range []string{"en;q=0", "en;q=-1", "en;q=2", "en;q=NaN", "en;q=Inf", "en;q=broken"} {
		if got, ok := fromAcceptLanguage(header); ok {
			t.Errorf("accepted excluded/malformed weight %q: %s", header, got)
		}
	}
	// ko is unsupported regardless of its weight, so it never displaces en
	if got, ok := fromAcceptLanguage("ko;q=0.9,en;q=0.1"); !ok || got != En {
		t.Fatal("unsupported language with a higher weight was selected")
	}
	if _, ok := fromAcceptLanguage("ko;q=0.9"); ok {
		t.Fatal("header with only an unsupported language should not resolve")
	}
}

func TestLocalizedErrorPreservesWrapping(t *testing.T) {
	cause := errors.New("user supplied raw text %s")
	Register("Operation %s failed: %w", "Operation %s failed: %w")
	err := Errorf("Operation %s failed: %w", "raw-id", cause)
	if !errors.Is(err, cause) {
		t.Fatal("error wrapping lost")
	}
	if got := ErrorMessage(En, err); got != "Operation raw-id failed: user supplied raw text %s" {
		t.Fatalf("raw cause changed: %q", got)
	}
}

func TestJoinedErrorsPreserveRawCauses(t *testing.T) {
	Register("Missing task", "Missing task")
	raw := errors.New("driver raw text %s")
	joined := errors.Join(NewError("Missing task"), raw)
	if got := ErrorMessage(En, joined); got != "Missing task\ndriver raw text %s" {
		t.Fatalf("joined localization altered raw content: %q", got)
	}
	if !errors.Is(joined, raw) {
		t.Fatal("joined cause identity lost")
	}
}

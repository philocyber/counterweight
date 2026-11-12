package notify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

type redirectProbeTransport func(*http.Request) (*http.Response, error)

func (f redirectProbeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNotificationClientRejectsDowngradeBeforeReplay(t *testing.T) {
	for _, initial := range []string{"https", "http"} {
		t.Run(initial, func(t *testing.T) {
			original := httpClient
			t.Cleanup(func() { httpClient = original })
			client := *original
			var schemes []string
			client.Transport = redirectProbeTransport(func(r *http.Request) (*http.Response, error) {
				schemes = append(schemes, r.URL.Scheme)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				if r.Header.Get("Authorization") != "Bearer fixture-secret" || !strings.Contains(string(body), "fixture-notification") {
					t.Fatal("approved redirect lost its credentials or payload")
				}
				response := &http.Response{StatusCode: 307, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}
				scheme := "http"
				if initial == "http" && len(schemes) == 1 {
					scheme = "https"
				}
				response.Header.Set("Location", scheme+"://notify.example.test/redirect?token=fixture-secret")
				return response, nil
			})
			httpClient = &client
			_, err := doJSON(context.Background(), "POST", initial+"://notify.example.test/hook?token=fixture-secret",
				map[string]string{"Authorization": "Bearer fixture-secret"}, map[string]string{"message": "fixture-notification"})
			if err == nil {
				t.Fatal("HTTPS downgrade was allowed")
			}
			want := 1
			if initial == "http" {
				want = 2
			}
			if len(schemes) != want {
				t.Fatalf("requests reached %v; downgrade must stop before replay", schemes)
			}
			assertNoSecret(t, err.Error(), "fixture-secret")
		})
	}
}

func TestNotificationClientKeepsSecureSameHostRedirect(t *testing.T) {
	original := httpClient
	t.Cleanup(func() { httpClient = original })
	client := *original
	calls := 0
	client.Transport = redirectProbeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" {
			t.Fatal("secure normalization changed scheme")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !strings.Contains(string(body), "fixture-notification") || r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Fatalf("secure replay lost payload/header: %v", err)
		}
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}
		if calls == 1 {
			response.StatusCode = 307
			response.Header.Set("Location", "/hook/")
		}
		return response, nil
	})
	httpClient = &client
	if _, err := doJSON(context.Background(), "POST", "https://notify.example.test/hook", map[string]string{"Authorization": "Bearer fixture-secret"}, map[string]string{"message": "fixture-notification"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("secure normalization used %d requests, want two", calls)
	}
}

func TestSeverityCannotInjectNotificationMarkup(t *testing.T) {
	const raw = "x]\n![probe](https://outside.invalid/mark) **high**"
	const escaped = `x\] \!\[probe\]\(https://outside.invalid/mark\) \*\*high\*\*`
	for _, lang := range []locale.Lang{locale.En} {
		for _, batch := range []bool{false, true} {
			m := Message{Language: lang, Batch: batch, Items: []Item{{Severity: raw, Name: "literal title"}}}
			markdown, kept := markdownBody(m, 0)
			card, cardKept := feishuCard(m)
			if kept != 1 || cardKept != 1 {
				t.Fatal("escaped severity dropped the notification")
			}
			rendered := []string{markdown}
			for _, value := range card["elements"].([]any) {
				if text, ok := value.(map[string]any)["text"].(map[string]any); ok {
					rendered = append(rendered, text["content"].(string))
				}
			}
			found := 0
			for _, output := range rendered {
				if strings.Contains(output, "![probe](") || strings.Contains(output, "**high**") || strings.Contains(output, raw) {
					t.Fatalf("severity injected parsed markup: %q", output)
				}
				if strings.Contains(output, escaped) {
					found++
				}
			}
			if found != 2 {
				t.Fatalf("both Markdown and lark_md must retain escaped severity; found %d", found)
			}
			if m.Items[0].Severity != raw || SeverityLabel(raw, lang) != raw {
				t.Fatal("rendering changed raw evidence or plain label")
			}
			if strings.ContainsAny(markdownTitle(m), "\r\n") {
				t.Fatal("plain title retained a severity line break")
			}
		}
	}
}

package agent

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/llmrec"
)

func TestProviderURLDiagnosticsDoNotExposeSecrets(t *testing.T) {
	var output bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(prior)
	_, capture := llmrec.NewCapture(context.Background())
	for _, raw := range []string{"https://USER_SECRET:PASS_SECRET@example.test/PATH_SECRET?token=QUERY_SECRET#FRAGMENT_SECRET", "https://example.test/%zz?key=QUERY_SECRET"} {
		config := ConfigFrom("openai", "test-model", raw, "fixture-key", "")
		logTestConnection(config, capture)
	}
	response := capture.TeeResponse(429, io.NopCloser(strings.NewReader(`{"error":"rate limit"}`)))
	_, _ = io.ReadAll(response)
	_ = response.Close()
	logTestConnection(ConfigFrom("openai", "test-model", "https://USER_SECRET:PASS_SECRET@example.test/PATH_SECRET?token=QUERY_SECRET", "fixture-key", ""), capture)
	if strings.Contains(output.String(), "SECRET") {
		t.Fatalf("URL credential leaked: %s", output.String())
	}
	if !strings.Contains(output.String(), "example.test") || !strings.Contains(output.String(), "HTTP 429") || !strings.Contains(output.String(), "rate limit") {
		t.Fatal("diagnostic status/body/host lost")
	}
}

func TestProviderProxyErrorsRedactCredentials(t *testing.T) {
	for _, proxy := range []string{"https://USER_SECRET:PASS_SECRET@example.test/%zz?token=QUERY_SECRET", "USER_SECRET:PASS_SECRET@HOST_SECRET", "SECRET_SCHEME://USER_SECRET:PASS_SECRET@example.test"} {
		_, err := quotaAwareHTTPClient(proxy, "")
		if err == nil {
			t.Fatalf("invalid proxy accepted: %s", proxy)
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("proxy error leaked secret: %s", err)
		}
	}
	client, err := quotaAwareHTTPClient("http://user:fixture-password@example.test:8080", "")
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(quotaAwareTransport)
	req, _ := http.NewRequest("GET", "http://target.example.test", nil)
	actual, err := transport.base.(*http.Transport).Proxy(req)
	if err != nil || actual.User.Username() != "user" {
		t.Fatal("proxy credentials changed")
	}
	if password, _ := actual.User.Password(); password != "fixture-password" {
		t.Fatal("proxy password changed")
	}
}

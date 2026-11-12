package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// These tests cover doJSON's HTTP failure classification independently of adapter business codes.
// Without this layer, a gateway 503 might become permanent while a 403 triggers pointless retries.

func replyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoJSONClassifiesHTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		permanent bool
	}{
		{"200 success is not an error", 200, false},
		{"429 rate limit is retryable", 429, false},
		{"408 timeout is retryable", 408, false},
		{"500 server error is retryable", 500, false},
		{"502 gateway error is retryable", 502, false},
		{"503 unavailable is retryable", 503, false},
		{"400 bad arguments are permanent", 400, true},
		{"401 authentication failure is permanent", 401, true},
		{"403 forbidden is permanent", 403, true},
		{"404 missing address is permanent", 404, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := replyServer(t, tc.status, `{"detail":"upstream says no"}`)
			_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
			if tc.status < 300 {
				if err != nil {
					t.Fatalf("2xx must not fail: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Non-2xx must fail")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("HTTP %d permanent classification: want %v, got %v (%v)",
					tc.status, tc.permanent, got, err)
			}
			// Include the numeric status code so operators can distinguish configuration failures from remote
			// outages. Assert the language-independent number rather than localized wording or Go StatusText.
			if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Errorf("Error must include HTTP status %d, got %v", tc.status, err)
			}
		})
	}
}

// TestDoJSONIncludesResponseSnippet preserves the remote explanation instead of returning only a
// generic failure.
func TestDoJSONIncludesResponseSnippet(t *testing.T) {
	srv := replyServer(t, 400, `{"error":"invalid webhook token"}`)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("Expected an error")
	}
	if !strings.Contains(err.Error(), "invalid webhook token") {
		t.Errorf("Error must preserve the remote explanation, got %v", err)
	}
}

// TestDoJSONSnippetIsSingleLineAndBounded protects last_error storage and frontend tables from
// multiline or oversized responses.
func TestDoJSONSnippetIsSingleLineAndBounded(t *testing.T) {
	// Response includes newlines, tabs, and 5000 characters.
	long := strings.Repeat("x", 5000)
	srv := replyServer(t, 500, "line1\nline2\r\n\tline3 "+long)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("Expected an error")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\r\n\t") {
		t.Errorf("Error must fit one line, got %q", msg)
	}
	// The 200-character snippet plus fixed prefix must remain far smaller than the original response.
	if len(msg) > 400 {
		t.Errorf("Error too long (%d bytes); snippet must truncate it: %q", len(msg), msg)
	}
}

// TestDoJSONRejectsOversizedResponse ensures abnormal responses cannot be read unbounded into memory
// or duplicated in each delivery's last_error.
func TestDoJSONRejectsOversizedResponse(t *testing.T) {
	huge := strings.Repeat("A", 1<<20) // 1 MiB
	srv := replyServer(t, 400, huge)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("Expected an error")
	}
	if len(err.Error()) > 400 {
		t.Errorf("Oversized response must be read with a limit and truncated; error length %d", len(err.Error()))
	}
}

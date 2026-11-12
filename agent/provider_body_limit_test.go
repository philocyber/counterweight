package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/llmrec"
	"github.com/Autumn-27/norma/llm"
)

// Generate a large response without allocating it in the test fixture.
type countedProviderBody struct {
	remaining int
	read      int
	closed    bool
}

func (b *countedProviderBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return n, nil
}

func (b *countedProviderBody) Close() error { b.closed = true; return nil }

func TestProvider429ResponseReadIsBounded(t *testing.T) {
	for _, record := range []bool{false, true} {
		name := "unrecorded"
		if record {
			name = "recorded"
		}
		t.Run(name, func(t *testing.T) {
			wire := &countedProviderBody{remaining: 8 * 1024 * 1024}
			transport := quotaAwareTransport{base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Header: make(http.Header), Body: wire}, nil
			})}
			ctx := context.Background()
			var capture *llmrec.Capture
			if record {
				ctx, capture = llmrec.NewCapture(ctx)
			}
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if wire.read > 64*1024+1 || !wire.closed {
				t.Fatalf("read=%d closed=%v; expected bounded inspection and closure", wire.read, wire.closed)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != 429 || len(body) > 1024 || !json.Valid(body) {
				t.Fatalf("status=%d size=%d err=%v; expected small JSON 429", resp.StatusCode, len(body), err)
			}
			if record {
				attempts := capture.Attempts()
				if len(attempts) != 1 || attempts[0].Status != 429 || !attempts[0].Truncated || len(attempts[0].Body) != wire.read {
					t.Fatalf("capture lost bounded wire provenance")
				}
				if !strings.Contains(capture.RawResponse(), "remaining wire bytes not read") {
					t.Fatal("raw capture hides truncation")
				}
			}
		})
	}
}

func TestProvider429InspectionBoundary(t *testing.T) {
	for _, size := range []int{64*1024 - 1, 64 * 1024, 64*1024 + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			// Permanent-quota text at the start must not classify a prefix when
			// a transient metric could occur anywhere in the unread remainder.
			wire := "insufficient_quota" + strings.Repeat(" ", size-len("insufficient_quota"))
			transport := quotaAwareTransport{base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(wire))}, nil
			})}
			req, _ := http.NewRequest("POST", "https://example.invalid", nil)
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if size <= 64*1024 {
				if resp.StatusCode != 402 || string(body) != wire {
					t.Fatal("complete bounded quota response changed")
				}
			} else if resp.StatusCode != 429 || IsQuotaExhaustedMessage(string(body)) {
				t.Fatal("truncated quota prefix incorrectly disables transient retries")
			}
		})
	}
}

func TestProviderRetriesOversized429(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			_, _ = io.WriteString(w, "insufficient_quota"+strings.Repeat(" ", 70*1024)+" requests per minute")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	cfg := ConfigFrom("openai", "test-model", upstream.URL, "test-key", "")
	cfg.Retry = RetryConfig{ConnectAttempts: 1, ConnectInterval: time.Millisecond, EmptyAttempts: -1}
	provider, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, capture := llmrec.NewCapture(ctx)
	for _, err := range provider.Stream(ctx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("ping")}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want failed attempt followed by success", calls.Load())
	}
	attempts := capture.Attempts()
	if len(attempts) != 2 || !attempts[0].Truncated || attempts[1].Truncated || attempts[1].Status != 200 {
		t.Fatal("retry capture lost original status or truncation provenance")
	}
}

func TestProviderSuccessfulStreamIsNotPrefetched(t *testing.T) {
	wire := &countedProviderBody{remaining: 8 * 1024 * 1024}
	ctx, capture := llmrec.NewCapture(context.Background())
	transport := quotaAwareTransport{base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: wire}, nil
	})}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if wire.read != 0 || wire.closed {
		t.Fatal("successful stream buffered before consumer reads")
	}
	buf := make([]byte, 7)
	if n, err := resp.Body.Read(buf); n != 7 || err != nil {
		t.Fatalf("read=%d err=%v", n, err)
	}
	if capture.RawResponse() != "xxxxxxx" {
		t.Fatal("stream capture changed")
	}
}

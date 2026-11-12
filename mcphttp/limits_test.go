package mcphttp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type patternedBody struct {
	pattern         string
	remaining, read int
	closed          bool
}

func (b *patternedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := 0; i < n; i++ {
		p[i] = b.pattern[(b.read+i)%len(b.pattern)]
	}
	b.read += n
	b.remaining -= n
	return n, nil
}
func (b *patternedBody) Close() error { b.closed = true; return nil }

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStreamableJSONResponseBudget(t *testing.T) {
	for _, size := range []int{8 * 1024 * 1024, 8*1024*1024 + 1} {
		const prefix = `{"jsonrpc":"2.0","id":1,"result":"`
		const suffix = `"}`
		payload := &patternedBody{pattern: "x", remaining: size - len(prefix) - len(suffix)}
		closed := false
		body := &trackedReader{Reader: io.MultiReader(strings.NewReader(prefix), payload, strings.NewReader(suffix)), close: func() { closed = true }}
		c := &Client{url: "https://example.invalid/rpc", http: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		})}}
		response, err := c.roundTrip(context.Background(), rpcRequest{ID: 1, Method: "tools/list"}, true)
		if size <= 8*1024*1024 {
			if err != nil || response == nil {
				t.Fatalf("within limit: %v", err)
			}
		} else if err == nil || response != nil {
			t.Fatal("oversized JSON accepted")
		}
		if body.read > 8*1024*1024+1 || !closed {
			t.Fatalf("read=%d closed=%v", body.read, closed)
		}
	}
}

type trackedReader struct {
	io.Reader
	read  int
	close func()
}

func (r *trackedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}
func (r *trackedReader) Close() error { r.close(); return nil }

func TestStreamableSSEAggregateBudget(t *testing.T) {
	for _, mode := range []string{"one multiline event", "many ignored events"} {
		t.Run(mode, func(t *testing.T) {
			pattern := "data: " + strings.Repeat("x", 1000) + "\n"
			prefix := "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"\n"
			suffix := "data: \"}\n\n"
			if mode == "many ignored events" {
				pattern = "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}\n\n"
				prefix = ""
				suffix = "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"
			}
			payload := &patternedBody{pattern: pattern, remaining: len(pattern) * (9*1024*1024/len(pattern) + 1)}
			response, err := parseSSE(io.MultiReader(strings.NewReader(prefix), payload, strings.NewReader(suffix)), 1)
			if err == nil || response != nil {
				t.Fatalf("%s exceeded budget without rejection", mode)
			}
			if payload.read > 8*1024*1024+1 {
				t.Fatalf("%s read=%d", mode, payload.read)
			}
		})
	}
}

func TestLegacyEndpointAndResponseLineBudgets(t *testing.T) {
	for i, input := range []string{strings.Repeat(": comment\n", 8000) + "data: /message\n", "data: /message?padding=" + strings.Repeat("x", 70*1024) + "\n"} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if _, err := readSSEEndpoint(bufio.NewReader(strings.NewReader(input)), "http://example.test/sse"); !errors.Is(err, errResponseLimit) {
				t.Fatalf("oversized endpoint error=%v", err)
			}
		})
	}
}

func TestLegacyResponseLineBudget(t *testing.T) {
	payload := &patternedBody{pattern: "x", remaining: 9 * 1024 * 1024}
	closed := false
	body := &trackedReader{Reader: io.MultiReader(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\""), payload, strings.NewReader("\"}\n")), close: func() { closed = true }}
	var cancelled atomic.Bool
	responses := make(chan *rpcResponse, 1)
	c := &Client{streamDone: make(chan struct{}), streamErr: make(chan error, 1), streamBody: body, streamCancel: func() { cancelled.Store(true) }, pending: map[int]chan *rpcResponse{1: responses}}
	c.readLegacySSE(bufio.NewReader(body))
	if len(responses) != 0 || body.read > 8*1024*1024+4096 || !closed || !cancelled.Load() {
		t.Fatalf("unbounded/accepted legacy response: read=%d delivered=%d closed=%v cancelled=%v", body.read, len(responses), closed, cancelled.Load())
	}
	var posts atomic.Int32
	c.legacySSE = true
	c.messageURL = "https://example.invalid/message"
	c.http = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		posts.Add(1)
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err := c.call(context.Background(), "tools/list", nil); err == nil || posts.Load() != 0 {
		t.Fatalf("terminated stream reused: posts=%d err=%v", posts.Load(), err)
	}
}

func TestLegacySetupFollowsCallerCancellation(t *testing.T) {
	for _, stage := range []string{"headers", "endpoint"} {
		t.Run(stage, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "endpoint" {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer srv.Close()
			defer close(release) // Unblocks the original baseline without leaked goroutines.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				c, err := NewSSE(ctx, "fixture", srv.URL+"/sse", nil, false)
				if c != nil {
					_ = c.Close()
				}
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("fixture never received setup request")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled setup error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("setup ignored caller cancellation")
			}
		})
	}
}

func TestSSEWithinBudgetStillReturnsMatchingMultilineResponse(t *testing.T) {
	response, err := parseSSE(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\n"+"data: \"id\":1,\"result\":{\"text\":\"ok\"}}\n\n"), 1)
	if err != nil || response == nil || string(response.Result) != `{"text":"ok"}` {
		t.Fatalf("response=%v err=%v", response, err)
	}
}

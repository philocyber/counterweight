package mcphttp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMCPNeverDispatchesOutsideConfiguredOrigin(t *testing.T) {
	for _, mode := range []string{"streamable redirect", "legacy GET redirect", "legacy POST redirect", "legacy announcement", "TLS downgrade"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "foreign fixture", 503)
			}))
			defer sink.Close()
			upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "legacy announcement" || mode == "legacy POST redirect" && r.URL.Path == "/sse" {
					endpoint := sink.URL + "/message"
					if mode == "legacy POST redirect" {
						endpoint = "/message"
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpoint)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				http.Redirect(w, r, sink.URL+"/steal", http.StatusTemporaryRedirect)
			})
			upstream := httptest.NewServer(upstreamHandler)
			if mode == "TLS downgrade" {
				upstream.Close()
				upstream = httptest.NewTLSServer(upstreamHandler)
			}
			defer upstream.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			headers := map[string]string{"Authorization": "Bearer fixture-secret", "X-API-Key": "fixture-api-key", "Cookie": "fixture=secret"}
			var c *Client
			var err error
			if strings.HasPrefix(mode, "legacy") {
				c, err = NewSSE(ctx, "fixture", upstream.URL+"/sse", headers, false)
			} else {
				c, err = New(ctx, "fixture", upstream.URL+"/rpc", headers, mode == "TLS downgrade")
			}
			if c != nil {
				_ = c.Close()
			}
			if err == nil {
				t.Fatal("foreign endpoint accepted")
			}
			if calls.Load() != 0 {
				t.Fatalf("sent %d requests to foreign destination", calls.Load())
			}
		})
	}
}

func TestMCPEndpointOriginBoundary(t *testing.T) {
	base := "https://EXAMPLE.test/sse"
	for _, candidate := range []string{"/message?sessionId=abc", "message", "https://example.test:443/message"} {
		endpoint, err := readSSEEndpoint(bufio.NewReader(strings.NewReader("event: endpoint\ndata: "+candidate+"\n\n")), base)
		if err != nil || endpoint == "" {
			t.Fatalf("approved endpoint %q: %v", candidate, err)
		}
	}
	for _, candidate := range []string{"http://example.test/message", "https://example.test:444/message", "https://other.test/message", "//other.test/message", "https://user:secret@example.test/message", "file:///message", "data:text/plain,hello"} {
		if _, err := readSSEEndpoint(bufio.NewReader(strings.NewReader("data: "+candidate+"\n\n")), base); err == nil {
			t.Errorf("unapproved endpoint %q accepted", candidate)
		}
	}
	origin, err := destinationOrigin("http://[2001:db8::1]/sse")
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := destinationOrigin("http://[2001:0db8:0:0:0:0:0:1]:80/message")
	if err != nil || origin != equivalent {
		t.Fatal("equivalent network origins differ")
	}
}

func TestStreamableSameOriginRedirectPreservesHandshakeAndCall(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rpc" {
			http.Redirect(w, r, "/normalized/rpc", http.StatusTemporaryRedirect)
			return
		}
		if r.Header.Get("X-API-Key") != "fixture-key" {
			t.Error("same-origin redirect lost configured credentials")
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		if req.ID == 0 {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Mcp-Session-Id", "fixture-session")
		w.Header().Set("Content-Type", "application/json")
		result := `{"protocolVersion":"2025-06-18","capabilities":{}}`
		if req.Method == "tools/call" {
			result = `{"content":[{"type":"text","text":"retained"}]}`
		}
		_, _ = io.WriteString(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := New(ctx, "fixture", srv.URL+"/rpc", map[string]string{"X-API-Key": "fixture-key"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	text, err := c.Call(ctx, "echo", map[string]string{"input": "retained"})
	if err != nil || text != "retained" || calls.Load() != 3 {
		t.Fatalf("calls=%d text=%q err=%v", calls.Load(), text, err)
	}
}

func TestLegacySameOriginRedirectResolvesRelativeEndpoint(t *testing.T) {
	messages := make(chan []byte, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("configured credential lost")
		}
		switch r.URL.Path {
		case "/sse":
			http.Redirect(w, r, "/normalized/sse", 307)
		case "/normalized/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: endpoint\ndata: message?session=fixture\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case payload := <-messages:
					fmt.Fprintf(w, "data: %s\n\n", payload)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		case "/normalized/message":
			http.Redirect(w, r, "/message?session=fixture", 307)
		case "/message":
			if r.URL.Query().Get("session") != "fixture" {
				t.Error("relative endpoint session lost")
			}
			var req rpcRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(202)
			if req.ID != 0 {
				result := `{"protocolVersion":"2024-11-05","capabilities":{}}`
				if req.Method == "tools/call" {
					result = `{"content":[{"type":"text","text":"retained"}]}`
				}
				messages <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := NewSSE(ctx, "fixture", srv.URL+"/sse", map[string]string{"Authorization": "Bearer fixture"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !strings.Contains(c.messageURL, "/normalized/message?session=fixture") {
		t.Fatal("endpoint did not resolve against redirected SSE URL")
	}
	text, err := c.Call(ctx, "echo", nil)
	if err != nil || text != "retained" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/cert"
)

func bindingRequest(t *testing.T) (*http.Request, *ConnContext) {
	t.Helper()
	conn := &ConnContext{}
	ctx := context.WithValue(t.Context(), connContextKey, conn)
	req, err := http.NewRequestWithContext(ctx, "GET", "https://origin.invalid/fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req, conn
}

func TestCheckedUpstreamRequestContextKeepsConnectionBinding(t *testing.T) {
	req, conn := bindingRequest(t)
	forwarded := req.WithContext(context.WithValue(req.Context(), proxyReqCtxKey, req))
	if forwarded.Context().Value(connContextKey) != conn || forwarded.Context().Value(proxyReqCtxKey) != req {
		t.Fatal("forwarded request overwrote the private connection context")
	}
	p := &Proxy{Opts: &Options{UpstreamDialTarget: func(_ *http.Request, got *ConnContext, _ string) (string, bool, error) {
		if got != conn {
			t.Fatal("connection binding changed while forwarding")
		}
		return "127.0.0.1:443", true, nil
	}}}
	if err := p.checkBoundRequest(forwarded, false); err != nil {
		t.Fatal(err)
	}
}

func TestCheckedUpstreamBindingRequiresNumericPeer(t *testing.T) {
	for _, tc := range []struct {
		address string
		valid   bool
	}{
		{"127.0.0.1:443", true}, {"[::1]:443", true},
		{"origin.invalid:443", false}, {"0.0.0.0:443", false},
		{"[::]:443", false}, {"[fe80::1%eth0]:443", false},
		{"127.0.0.1:0", false}, {"127.0.0.1:65536", false},
		{"127.0.0.1:unknown", false}, {"127.0.0.1", false},
	} {
		t.Run(tc.address, func(t *testing.T) {
			req, conn := bindingRequest(t)
			p := &Proxy{Opts: &Options{UpstreamDialTarget: func(got *http.Request, peer *ConnContext, logical string) (string, bool, error) {
				if got != req || peer != conn || logical != "origin.invalid:443" {
					t.Error("logical request or connection identity changed")
				}
				return tc.address, true, nil
			}}}
			address, bound, err := p.checkedUpstreamAddress(req)
			if !bound || (err == nil) != tc.valid || tc.valid && address != tc.address {
				t.Fatalf("target=%q bound=%v error=%v", address, bound, err)
			}
			if req.URL.Host != "origin.invalid" {
				t.Fatal("numeric dialing changed logical URL")
			}
		})
	}
}

func TestCheckedUpstreamBindingCannotChangeOrDisappear(t *testing.T) {
	for _, mode := range []string{"peer-change", "authority-change", "scheme-change", "host-change", "release", "hook-remove", "policy-deny"} {
		t.Run(mode, func(t *testing.T) {
			req, _ := bindingRequest(t)
			req.URL.Host, req.Host = "origin.invalid:443", "origin.invalid:443"
			address, bound, policyErr := "127.0.0.1:443", true, error(nil)
			p := &Proxy{Opts: &Options{UpstreamDialTarget: func(*http.Request, *ConnContext, string) (string, bool, error) { return address, bound, policyErr }}}
			if _, _, err := p.checkedUpstreamAddress(req); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "peer-change":
				address = "127.0.0.2:443"
			case "authority-change":
				req.URL.Host = "different.invalid"
			case "release":
				bound = false
			case "hook-remove":
				p.Opts.UpstreamDialTarget = nil
			case "scheme-change":
				req.URL.Scheme = "http"
			case "host-change":
				req.Host = "different.invalid:443"
			case "policy-deny":
				policyErr = errors.New("fixture policy denies")
			}
			if err := p.checkBoundRequest(req, false); err == nil {
				t.Fatal("bound connection silently lost its peer authorization")
			}
		})
	}
}

func TestCheckedUpstreamBindingRefusesUnattestedTransports(t *testing.T) {
	for _, bound := range []bool{false, true} {
		req, _ := bindingRequest(t)
		p := &Proxy{Opts: &Options{UpstreamDialTarget: func(*http.Request, *ConnContext, string) (string, bool, error) { return "127.0.0.1:443", bound, nil }}}
		if err := p.checkBoundRequest(req, false); err != nil {
			t.Fatal(err)
		}
		if err := p.checkBoundRequest(req, true); (err != nil) != bound {
			t.Fatalf("alternate transport bound=%v error=%v", bound, err)
		}
		req.Header.Set("Upgrade", "websocket")
		if err := p.checkBoundRequest(req, false); (err != nil) != bound {
			t.Fatalf("websocket bound=%v error=%v", bound, err)
		}
	}
}

func TestCheckedUpstreamNumericDialPreservesTLSIdentity(t *testing.T) {
	ca, err := cert.NewSelfSignCAMemory()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.GetCert("origin.invalid")
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 1)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "origin.invalid:") {
			t.Errorf("logical Host lost: %s", r.Host)
		}
		_, _ = io.WriteString(w, "bound response")
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { observed <- hello.ServerName; return nil, nil }}
	target.StartTLS()
	defer target.Close()
	numeric := target.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(numeric)
	logical := net.JoinHostPort("origin.invalid", port)
	roots := x509.NewCertPool()
	roots.AddCert(ca.GetRootCA())
	req, conn := bindingRequest(t)
	req.URL.Host, req.Host = logical, logical
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	p := &Proxy{Opts: &Options{UpstreamTLSConfig: func(address string) *tls.Config {
		if address != logical {
			t.Errorf("TLS policy selected numeric peer identity: %s", address)
		}
		return &tls.Config{RootCAs: roots}
	}, UpstreamDialTarget: func(_ *http.Request, c *ConnContext, address string) (string, bool, error) {
		if c != conn || address != logical {
			t.Error("logical binding changed")
		}
		return numeric, true, nil
	}}}
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return nil, nil })
	raw, err := p.getUpstreamConn(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if raw.RemoteAddr().String() != numeric {
		t.Fatalf("unchecked peer: %s", raw.RemoteAddr())
	}
	secure := tls.Client(raw, p.upstreamTLS(logical))
	if err := secure.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	if name := <-observed; name != "origin.invalid" {
		t.Fatalf("numeric SNI replaced logical identity: %s", name)
	}
	if err := req.Write(secure); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(secure), req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "bound response" {
		t.Fatalf("response changed: %q %v", body, err)
	}
}

func TestCheckedUpstreamEgressCONNECTUsesNumericPeer(t *testing.T) {
	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			t.Errorf("unexpected proxy operation: %s", r.Method)
		}
		seen <- r.Host
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(buffer, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
	}))
	defer upstream.Close()
	proxyURL, _ := url.Parse(upstream.URL)
	req, _ := bindingRequest(t)
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	p := &Proxy{Opts: &Options{UpstreamDialTarget: func(*http.Request, *ConnContext, string) (string, bool, error) { return "127.0.0.1:443", true, nil }}}
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return proxyURL, nil })
	conn, err := p.getUpstreamConn(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if address := <-seen; address != "127.0.0.1:443" {
		t.Fatalf("proxy can resolve logical name again: %s", address)
	}
}

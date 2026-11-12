package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPPeerDialPinsResolutionAndFiltersBlockedAddress(t *testing.T) {
	lookups, dials := 0, 0
	transport := &httpPeerTransport{
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			lookups++
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.2")}, {IP: net.ParseIP("127.0.0.1")}}, nil
		},
		check: func(_ context.Context, ip net.IP) error {
			if ip.Equal(net.ParseIP("127.0.0.2")) {
				return errors.New("fixture IP denied")
			}
			return nil
		},
	}
	conn, err := transport.dial(t.Context(), "tcp", "fixture.invalid:80", func(_ context.Context, network, address string) (net.Conn, error) {
		dials++
		if network != "tcp" || address != "127.0.0.1:80" {
			t.Fatalf("unapproved/unresolved dial: %s %s", network, address)
		}
		left, right := net.Pipe()
		right.Close()
		return left, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if lookups != 1 || dials != 1 {
		t.Fatalf("resolution/dial repeated: %d %d", lookups, dials)
	}
}

func TestHTTPPeerDialPreservesDualStackFallback(t *testing.T) {
	primaryStopped := make(chan struct{})
	transport := &httpPeerTransport{
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("::1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
		},
		check: func(context.Context, net.IP) error { return nil },
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	conn, err := transport.dial(ctx, "tcp", "fixture.invalid:80", func(ctx context.Context, _, address string) (net.Conn, error) {
		if address == "[::1]:80" {
			<-ctx.Done()
			close(primaryStopped)
			return nil, ctx.Err()
		}
		if address != "127.0.0.1:80" {
			return nil, errors.New("wrong fallback address")
		}
		left, right := net.Pipe()
		right.Close()
		return left, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	select {
	case <-primaryStopped:
	case <-ctx.Done():
		t.Fatal("losing family dial was not canceled")
	}
}

func TestHTTPPeerTransportPreservesTLSHostnameVerification(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.TLS.ServerName != "example.com" {
			t.Error("original TLS server name changed")
		}
		w.Write([]byte("fixture"))
	}))
	defer target.Close()
	base := target.Client().Transport.(*http.Transport).Clone()
	defer base.CloseIdleConnections()
	if base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("fixture does not verify TLS")
	}
	transport := &httpPeerTransport{base: base, check: func(context.Context, net.IP) error { return nil }, lookup: func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "example.com" && host != "wrong.invalid" {
			t.Errorf("unexpected lookup %s", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	endpoint := strings.Replace(target.URL, "127.0.0.1", "example.com", 1)
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response, err := client.Get(strings.Replace(endpoint, "example.com", "wrong.invalid", 1)); err == nil {
		response.Body.Close()
		t.Fatal("wrong TLS hostname accepted")
	}
	if received.Load() != 1 {
		t.Fatal("unexpected HTTP requests after hostname verification")
	}
}

func TestHTTPPeerTransportPreservesProxyRouting(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Host != "fixture.invalid" {
			t.Errorf("proxy target changed: %s", r.URL.Host)
		}
		w.Write([]byte("proxy fixture"))
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	base := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer base.CloseIdleConnections()
	transport := &httpPeerTransport{base: base, check: func(context.Context, net.IP) error { return errors.New("proxy infrastructure checked as target") }, lookup: func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("proxied target resolved locally")
	}}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get("http://fixture.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if requests.Load() != 1 {
		t.Fatal("configured proxy was not used")
	}
}

func TestHTTPPeerTransportProxyPolicyStopsBeforeProxyRequest(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("proxy fixture"))
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	base := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer base.CloseIdleConnections()
	blocked := true
	transport := &httpPeerTransport{base: base,
		check: func(context.Context, net.IP) error { t.Fatal("proxy infrastructure checked as target"); return nil },
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			t.Fatal("proxy target resolved locally")
			return nil, nil
		},
		proxyDestination: func(req *http.Request) error {
			if req.URL.Hostname() != "fixture.invalid" {
				t.Fatal("actual proxy destination lost")
			}
			if blocked {
				return errors.New("fixture remote DNS cannot enforce IP policy")
			}
			return nil
		},
	}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get("http://fixture.invalid/")
	if response != nil {
		response.Body.Close()
	}
	var policy *httpPeerPolicyError
	if !errors.As(err, &policy) || requests.Load() != 0 {
		t.Fatalf("blocked target reached proxy: %d %v", requests.Load(), err)
	}
	blocked = false
	response, err = client.Get("http://fixture.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if requests.Load() != 1 {
		t.Fatal("unrestricted hostname proxy routing broken")
	}
}

func TestHTTPPeerTransportRechecksURLAfterResolution(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer target.Close()
	base := &http.Transport{}
	defer base.CloseIdleConnections()
	revoked := false
	transport := &httpPeerTransport{base: base,
		check: func(context.Context, net.IP) error { return nil },
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			revoked = true
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		},
		destination: func(*http.Request) error {
			if revoked {
				return errors.New("fixture URL grant revoked during lookup")
			}
			return nil
		},
	}
	endpoint := strings.Replace(target.URL, "127.0.0.1", "fixture.invalid", 1)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(endpoint)
	if response != nil {
		response.Body.Close()
	}
	var policy *httpPeerPolicyError
	if !errors.As(err, &policy) || requests.Load() != 0 {
		t.Fatalf("revoked URL reached peer: requests=%d err=%v", requests.Load(), err)
	}
}

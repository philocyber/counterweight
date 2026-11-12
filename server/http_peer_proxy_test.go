package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func TestPinnedHTTPProxyPreservesLogicalAuthority(t *testing.T) {
	var targetRequests atomic.Int32
	var authority string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Host != authority || r.RequestURI != "/a%2Fb?x=%2F" || r.Method != "POST" || string(body) != "fixture body" {
			t.Errorf("logical request changed: %s %s %s %q %v", r.Host, r.Method, r.RequestURI, body, err)
		}
		w.Write([]byte("target fixture"))
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	authority = "fixture.invalid:" + targetURL.Port()
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(pinnedTunnelFixture(t, targetURL.Host, &proxyRequests))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	lookups := 0
	blocked := false
	transport := &httpPeerTransport{base: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		proxyDestination: func(*http.Request) error { return db.ErrHTTPProxyPeerRequired },
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			lookups++
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.2")}, {IP: net.ParseIP("127.0.0.1")}}, nil
		},
		destination: func(r *http.Request) error {
			if r.URL.Host != authority {
				return errors.New("logical policy destination changed")
			}
			return nil
		},
		check: func(_ context.Context, ip net.IP) error {
			if blocked || !ip.Equal(net.ParseIP("127.0.0.1")) {
				return errors.New("fixture IP blocked")
			}
			return nil
		},
	}
	defer transport.base.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	endpoint := "http://" + authority + "/a%2Fb?x=%2F"
	request, err := http.NewRequestWithContext(t.Context(), "POST", endpoint, strings.NewReader("fixture body"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "target fixture" || response.Request.URL.String() != endpoint || lookups != 1 || targetRequests.Load() != 1 {
		t.Fatalf("pinned request failed: %q %v %d %d", body, err, lookups, targetRequests.Load())
	}
	blocked = true
	request, err = http.NewRequestWithContext(t.Context(), "POST", endpoint, strings.NewReader("fixture body"))
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	var policy *httpPeerPolicyError
	if !errors.As(err, &policy) || targetRequests.Load() != 1 || proxyRequests.Load() != 1 {
		t.Fatalf("blocked IP reached proxy/origin: %v %d %d", err, proxyRequests.Load(), targetRequests.Load())
	}
}

// A real tunnel preserves the wire Host independently of the CONNECT address.
func pinnedTunnelFixture(t *testing.T, target string, requests *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodConnect || r.Host != target {
			t.Errorf("unapproved CONNECT target: %s %s", r.Method, r.Host)
			http.Error(w, "bad target", 500)
			return
		}
		upstream, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			t.Error(err)
			http.Error(w, "dial failed", 502)
			return
		}
		defer upstream.Close()
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		client.SetDeadline(time.Now().Add(5 * time.Second))
		upstream.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = buffer.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { io.Copy(upstream, buffer); upstream.Close(); close(done) }()
		io.Copy(client, upstream)
		client.Close()
		<-done
	})
}

func TestPinnedHTTPProxyTLSIdentity(t *testing.T) {
	for _, secureProxy := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP_proxy", true: "HTTPS_proxy"}[secureProxy], func(t *testing.T) {
			var received, connects atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if r.TLS.ServerName != "example.com" || !strings.HasPrefix(r.Host, "example.com:") || r.Header.Get("Proxy-Authorization") != "" {
					t.Errorf("TLS/Host identity changed: %s %s", r.TLS.ServerName, r.Host)
				}
				w.Write([]byte("verified"))
			}))
			defer target.Close()
			targetURL, _ := url.Parse(target.URL)
			tunnel := pinnedTunnelFixture(t, targetURL.Host, &connects)
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "Basic Zml4dHVyZTp0ZXN0" {
					t.Error("proxy credentials missing or changed")
				}
				tunnel.ServeHTTP(w, r)
			}))
			if secureProxy {
				proxy.StartTLS()
			} else {
				proxy.Start()
			}
			defer proxy.Close()
			base := target.Client().Transport.(*http.Transport).Clone()
			if secureProxy {
				base.TLSClientConfig.RootCAs = base.TLSClientConfig.RootCAs.Clone()
				base.TLSClientConfig.RootCAs.AddCert(proxy.Certificate())
			}
			if base.TLSClientConfig.InsecureSkipVerify {
				t.Fatal("fixture must verify TLS")
			}
			proxyURL, _ := url.Parse(proxy.URL)
			proxyURL.User = url.UserPassword("fixture", "test")
			base.Proxy = http.ProxyURL(proxyURL)
			defer base.CloseIdleConnections()
			transport := &httpPeerTransport{base: base, proxyDestination: func(*http.Request) error { return db.ErrHTTPProxyPeerRequired }, check: func(context.Context, net.IP) error { return nil }, lookup: func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			}}
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			endpoint := strings.Replace(target.URL, "127.0.0.1", "example.com", 1)
			response, err := client.Get(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			response, err = client.Get(strings.Replace(endpoint, "example.com", "wrong.invalid", 1))
			if response != nil {
				response.Body.Close()
			}
			if err == nil || received.Load() != 1 || connects.Load() != 2 {
				t.Fatalf("wrong TLS hostname accepted or route changed: %v %d %d", err, received.Load(), connects.Load())
			}
		})
	}
}

func TestPinnedHTTPProxyRecordingRouteRemainsClosed(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	base := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer base.CloseIdleConnections()
	transport := &httpPeerTransport{base: base, recordingProxy: proxyURL, proxyDestination: func(*http.Request) error { return db.ErrHTTPProxyPeerRequired }, lookup: func(context.Context, string) ([]net.IPAddr, error) {
		t.Error("unsupported recorder must not resolve")
		return nil, nil
	}}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("http://fixture.invalid/")
	if response != nil {
		response.Body.Close()
	}
	var policy *httpPeerPolicyError
	if !errors.As(err, &policy) || requests.Load() != 0 {
		t.Fatalf("unsupported recorder reached: %v %d", err, requests.Load())
	}
}

func TestPinnedHTTPProxyRechecksBeforeConnect(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "unexpected", 500) }))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	blocked := false
	base := &http.Transport{Proxy: http.ProxyURL(proxyURL), GetProxyConnectHeader: func(context.Context, *url.URL, string) (http.Header, error) { blocked = true; return nil, nil }}
	defer base.CloseIdleConnections()
	transport := &httpPeerTransport{base: base, proxyDestination: func(*http.Request) error { return db.ErrHTTPProxyPeerRequired }, lookup: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}, check: func(context.Context, net.IP) error {
		if blocked {
			return errors.New("new IP block")
		}
		return nil
	}}
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get("http://fixture.invalid/")
	if response != nil {
		response.Body.Close()
	}
	var policy *httpPeerPolicyError
	if !errors.As(err, &policy) || requests.Load() != 0 {
		t.Fatalf("changed policy reached CONNECT: %v %d", err, requests.Load())
	}
}

func TestRecordingHTTPProxyURLIsNotPrefixedTwice(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte("proxy fixture")) }))
	defer proxy.Close()
	s := &Server{m: &Manager{globalProxy: proxy.URL}}
	raw, _ := json.Marshal(httpExec{URL: "http://fixture.invalid/", UseRecordingProxy: true})
	res, err := s.runHTTPTool(t.Context(), raw, nil, nil)
	if err != nil || res.IsError || requests.Load() != 1 {
		t.Fatalf("configured route broken: %s %v %d", res.Flatten(), err, requests.Load())
	}
}

package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/lqqyt2423/go-mitmproxy/cert"
)

func TestAlternateTLSSeparatesHTTPSProxyFromTargetException(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "untrusted-proxy", true: "trusted-proxy"}[trusted], func(t *testing.T) {
			var requests, tunnels atomic.Int32
			ca, err := cert.NewSelfSignCAMemory()
			if err != nil {
				t.Fatal(err)
			}
			leaf, err := ca.GetCert("127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, "target exception")
			}))
			target.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}}
			target.StartTLS()
			defer target.Close()
			targetURL, _ := url.Parse(target.URL)
			proxyServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tunnels.Add(1)
				if r.Method != "CONNECT" || r.Host != targetURL.Host {
					t.Error("unexpected proxy target")
					http.Error(w, "invalid", 400)
					return
				}
				upstream, err := net.Dial("tcp", r.Host)
				if err != nil {
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
				_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
				_ = buffer.Flush()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(client, upstream); _ = client.Close(); close(done) }()
				_, _ = io.Copy(upstream, client)
				_ = upstream.Close()
				<-done
			}))
			defer proxyServer.Close()
			proxyURL, _ := url.Parse(proxyServer.URL)
			roots := x509.NewCertPool()
			if trusted {
				roots.AddCert(proxyServer.Certificate())
			}
			proxy := &Proxy{Opts: &Options{UpstreamTLSConfig: func(address string) *tls.Config {
				return &tls.Config{RootCAs: roots, InsecureSkipVerify: address == targetURL.Host}
			}}}
			transport := &targetTLSTransport{proxy: proxy, base: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
			client := &http.Client{Transport: transport}
			response, err := client.Get(target.URL)
			if !trusted {
				if err == nil {
					response.Body.Close()
					t.Fatal("target exception disabled proxy authentication")
				}
				if tunnels.Load() != 0 || requests.Load() != 0 {
					t.Fatal("HTTP delivered before proxy TLS validation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(body) != "target exception" || requests.Load() != 1 || tunnels.Load() != 1 {
				t.Fatalf("trusted HTTPS proxy lost target behavior: body=%q err=%v", body, err)
			}
		})
	}
}

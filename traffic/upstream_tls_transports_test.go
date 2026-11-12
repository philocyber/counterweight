package traffic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	mitmcert "github.com/lqqyt2423/go-mitmproxy/cert"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

type separateTLSRequest struct{ mproxy.BaseAddon }

func (*separateTLSRequest) Request(flow *mproxy.Flow) { flow.UseSeparateClient = true }

func nativeRecordingFixture(t *testing.T, options TLSOptions) (*Traffic, *url.URL, *x509.CertPool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	tr, err := Open(t.TempDir(), addr, options)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- tr.Start() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tr.proxy.Shutdown(ctx)
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("recording proxy did not stop")
		}
		_ = tr.Close()
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recording proxy did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	root := tr.proxy.GetCertificate()
	roots := x509.NewCertPool()
	roots.AddCert(&root)
	proxyURL, _ := url.Parse("http://" + addr)
	return tr, proxyURL, roots
}

func TestNativeTLSProtectsWSSAndAlternateConnections(t *testing.T) {
	for _, transport := range []string{"wss", "alternate-http"} {
		for _, mode := range []string{"default", "private-ca", "second-certificate-swap", "explicit-origin"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				seed := httptest.NewTLSServer(http.NotFoundHandler())
				good := seed.TLS.Certificates[0]
				root := seed.Certificate()
				seed.Close()
				otherCA, err := mitmcert.NewSelfSignCAMemory()
				if err != nil {
					t.Fatal(err)
				}
				bad, err := otherCA.GetCert("127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				var handshakes, requests atomic.Int32
				target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if transport == "wss" {
						upgrader := websocket.Upgrader{}
						conn, err := upgrader.Upgrade(w, r, nil)
						if err != nil {
							return
						}
						defer conn.Close()
						kind, body, err := conn.ReadMessage()
						if err == nil {
							_ = conn.WriteMessage(kind, body)
						}
					} else {
						_, _ = io.WriteString(w, "alternate HTTPS")
					}
				}))
				target.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
					certificate := good
					if handshakes.Add(1) > 1 && mode == "second-certificate-swap" {
						certificate = *bad
					}
					return &tls.Config{Certificates: []tls.Certificate{certificate}}, nil
				}}
				target.StartTLS()
				defer target.Close()
				options := TLSOptions{}
				if mode == "private-ca" || mode == "second-certificate-swap" {
					options.CAFile = filepath.Join(t.TempDir(), "private-ca.pem")
					if err := os.WriteFile(options.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}), 0600); err != nil {
						t.Fatal(err)
					}
				} else if mode == "explicit-origin" {
					options.InsecureOrigins = []string{target.URL}
				}
				tr, proxyURL, roots := nativeRecordingFixture(t, options)
				allowed := mode == "private-ca" || mode == "explicit-origin"
				if transport == "wss" {
					dialer := websocket.Dialer{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots}, HandshakeTimeout: 3 * time.Second}
					conn, _, err := dialer.Dial(strings.Replace(target.URL, "https:", "wss:", 1), nil)
					if !allowed {
						if err == nil {
							_ = conn.Close()
							t.Fatal("unverified WSS connection accepted")
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
						if err := conn.WriteMessage(websocket.TextMessage, []byte("fixture echo")); err != nil {
							t.Fatal(err)
						}
						_, body, err := conn.ReadMessage()
						if err != nil || string(body) != "fixture echo" {
							t.Fatalf("WSS behavior changed: body=%q err=%v", body, err)
						}
					}
				} else {
					tr.proxy.AddAddon(&separateTLSRequest{})
					transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true}
					defer transport.CloseIdleConnections()
					client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
					response, err := client.Get(target.URL)
					if !allowed {
						if err == nil {
							_ = response.Body.Close()
							if response.StatusCode < 400 {
								t.Fatal("unverified alternate connection accepted")
							}
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(response.Body)
						_ = response.Body.Close()
						if err != nil || string(body) != "alternate HTTPS" {
							t.Fatalf("alternate behavior changed: %q err=%v", body, err)
						}
					}
				}
				want := int32(0)
				if allowed {
					want = 1
				}
				if requests.Load() != want {
					t.Fatalf("HTTP delivery count=%d want=%d", requests.Load(), want)
				}
				if mode == "second-certificate-swap" && handshakes.Load() < 2 {
					t.Fatal("test did not reach the independent second TLS connection")
				}
			})
		}
	}
}

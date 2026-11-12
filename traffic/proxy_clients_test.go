package traffic

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
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
)

func TestProxyClientCIDRPolicy(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_PROXY_CLIENT_CIDRS", "192.0.2.8/32,2001:db8:42::/48")
	allow, err := proxyClientPolicy()
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"127.0.0.1": true, "127.0.0.2": true, "::1": true, "::ffff:127.0.0.1": true, "192.0.2.8": true, "::ffff:192.0.2.8": true, "192.0.2.9": false, "2001:db8:42::1": true, "2001:db8:43::1": false, "0.0.0.0": false} {
		if got := allow(&net.TCPAddr{IP: net.ParseIP(host), Port: 45000}); got != want {
			t.Fatalf("peer %s allowed=%v want=%v", host, got, want)
		}
	}
	if allow(nil) {
		t.Fatal("missing transport peer allowed")
	}
	if !allow(&net.TCPAddr{IP: net.ParseIP("2001:db8:42::8"), Port: 45000, Zone: "fixture"}) {
		t.Fatal("IPv6 zone changed authorized identity")
	}
}

func TestProxyClientInvalidConfigurationBeforeStoreCreation(t *testing.T) {
	for _, raw := range []string{"example.invalid", "192.0.2.8", "192.0.2.8/33", "192.0.2.8/32,", "SECRET_INPUT"} {
		t.Setenv("COUNTERWEIGHT_PROXY_CLIENT_CIDRS", raw)
		dir := filepath.Join(t.TempDir(), "uncreated")
		tr, err := Open(dir, "127.0.0.1:0")
		if tr != nil {
			tr.Close()
		}
		if err == nil || strings.Contains(err.Error(), raw) {
			t.Fatalf("invalid client config accepted or echoed: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid client policy created store")
		}
	}
}

// Explicit non-loopback local interface fixture, never an external target.
func TestProxyClientTransportAdmission(t *testing.T) {
	clientIP := net.ParseIP(os.Getenv("COUNTERWEIGHT_PROXY_TEST_CLIENT_IP"))
	if clientIP == nil || clientIP.IsLoopback() {
		t.Skip("requires explicit non-loopback local IPv4 interface")
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	local := false
	for _, address := range addresses {
		if host, _, err := net.ParseCIDR(address.String()); err == nil && host.Equal(clientIP) {
			local = true
		}
	}
	if !local || clientIP.To4() == nil {
		t.Fatal("test client address must be a local IPv4 interface")
	}
	for _, remoteAllowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote-allowed=%v", remoteAllowed), func(t *testing.T) {
			cidrs := ""
			if remoteAllowed {
				cidrs = clientIP.String() + "/32"
			}
			t.Setenv("COUNTERWEIGHT_PROXY_CLIENT_CIDRS", cidrs)
			var received, targetConnections atomic.Int32
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if websocket.IsWebSocketUpgrade(r) {
					connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					connection.Close()
					return
				}
				w.Write([]byte("local target response"))
			}))
			target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					targetConnections.Add(1)
				}
			}
			target.Start()
			defer target.Close()
			tlsTarget := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.Write([]byte("local TLS response")) }))
			tlsTarget.Config.ConnState = target.Config.ConnState
			tlsTarget.StartTLS()
			defer tlsTarget.Close()
			caFile := filepath.Join(t.TempDir(), "target-ca.pem")
			if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsTarget.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "0.0.0.0:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			tr, err := Open(t.TempDir(), net.JoinHostPort("0.0.0.0", fmt.Sprint(port)), TLSOptions{CAFile: caFile})
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			done := make(chan error, 1)
			go func() { done <- tr.Start() }()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				tr.proxy.Shutdown(ctx)
				select {
				case <-done:
				case <-ctx.Done():
					t.Error("proxy did not stop")
				}
			}()
			for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
				conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), 50*time.Millisecond)
				if err == nil {
					conn.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("proxy did not start")
				}
			}
			address := net.JoinHostPort(clientIP.String(), fmt.Sprint(port))
			for _, mode := range []string{"http", "connect", "passthrough", "websocket"} {
				dialer := net.Dialer{Timeout: time.Second, LocalAddr: &net.TCPAddr{IP: clientIP}}
				conn, err := dialer.Dial("tcp", address)
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(2 * time.Second))
				before := targetConnections.Load()
				destination, _ := url.Parse(target.URL)
				if mode == "connect" {
					destination, _ = url.Parse(tlsTarget.URL)
				}
				request := fmt.Sprintf("GET %s/ HTTP/1.1\r\nHost: %s\r\nConnection: close\r\nX-Forwarded-For: 127.0.0.1\r\nForwarded: for=127.0.0.1\r\n\r\n", target.URL, destination.Host)
				if mode == "connect" || mode == "passthrough" {
					if mode == "passthrough" {
						tr.pass.Store(hostOnly(destination.Host), struct{}{})
					}
					request = fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nX-Forwarded-For: 127.0.0.1\r\n\r\n", destination.Host, destination.Host)
				} else if mode == "websocket" {
					request = fmt.Sprintf("GET %s/ HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", target.URL, destination.Host)
				}
				_, writeErr := io.WriteString(conn, request)
				response, readErr := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
				if remoteAllowed {
					if writeErr != nil || readErr != nil {
						t.Fatalf("authorized %s client refused: %v %v", mode, writeErr, readErr)
					}
					wantStatus := 200
					if mode == "websocket" {
						wantStatus = 101
					}
					if response.StatusCode != wantStatus {
						t.Fatalf("authorized %s status=%d", mode, response.StatusCode)
					}
					if mode == "connect" {
						roots := x509.NewCertPool()
						cert := tr.proxy.GetCertificate()
						roots.AddCert(&cert)
						secure := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
						if err := secure.Handshake(); err != nil {
							t.Fatal(err)
						}
						io.WriteString(secure, "GET / HTTP/1.1\r\nHost: "+destination.Host+"\r\nConnection: close\r\n\r\n")
						final, err := http.ReadResponse(bufio.NewReader(secure), &http.Request{Method: "GET"})
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(final.Body)
						final.Body.Close()
						if err != nil || string(body) != "local TLS response" {
							t.Fatal("authorized remote HTTPS capture lost")
						}
						secure.Close()
					} else if mode == "http" {
						body, err := io.ReadAll(response.Body)
						response.Body.Close()
						if err != nil || string(body) != "local target response" {
							t.Fatal("authorized remote HTTP response changed")
						}
					}
				} else {
					if readErr == nil {
						response.Body.Close()
						t.Fatalf("unauthorized %s client reached HTTP proxy", mode)
					}
					if targetConnections.Load() != before {
						t.Fatalf("unauthorized %s caused target dial", mode)
					}
				}
				conn.Close()
			}
			// A denied peer must not stop the listener for ordinary worker traffic.
			proxyURL, _ := url.Parse(tr.ProxyAddr())
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			defer transport.CloseIdleConnections()
			client := http.Client{Transport: transport, Timeout: 2 * time.Second}
			response, err := client.Get(target.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "local target response" {
				t.Fatal("loopback capture workflow lost")
			}
			if !remoteAllowed && received.Load() != 1 {
				t.Fatal("unapproved requests reached target")
			}
		})
	}
}

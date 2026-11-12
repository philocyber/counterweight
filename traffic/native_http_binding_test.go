package traffic

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mitmcert "github.com/lqqyt2423/go-mitmproxy/cert"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

func TestNativeHTTPRecordingPeerRequiresLiveListener(t *testing.T) {
	unstarted := &Traffic{}
	if unstarted.OwnsRecordingPeer(net.ParseIP("127.0.0.1"), "8788") {
		t.Fatal("unstarted recorder claims a socket")
	}
	tr, proxyURL, _ := nativeRecordingFixture(t, TLSOptions{})
	peer := net.ParseIP(proxyURL.Hostname())
	if !tr.OwnsRecordingPeer(peer, proxyURL.Port()) {
		t.Fatal("live socket is not owned")
	}
	port, _ := strconv.Atoi(proxyURL.Port())
	if tr.OwnsRecordingPeer(peer, strconv.Itoa(port+1)) || tr.OwnsRecordingPeer(net.ParseIP("127.0.0.2"), proxyURL.Port()) || tr.OwnsRecordingPeer(net.IPv4zero, proxyURL.Port()) {
		t.Fatal("foreign socket is owned")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := tr.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for tr.OwnsRecordingPeer(peer, proxyURL.Port()) {
		select {
		case <-ctx.Done():
			t.Fatal("closed listener remains owned")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestNativeHTTPBindingRejectsInvalidOwnershipAndCancellation(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { received.Add(1); w.Write([]byte("unexpected")) }))
	defer target.Close()
	tr, proxyURL, _ := nativeRecordingFixture(t, TLSOptions{})
	origin, _ := url.Parse(target.URL)
	origin.Host = net.JoinHostPort("bound.invalid", origin.Port())
	check := func(context.Context) error { return nil }
	foreign, err := net.DialTimeout("tcp", strings.TrimPrefix(target.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.BindNativeHTTPConnection(t.Context(), foreign, origin, net.ParseIP("127.0.0.1"), check); err == nil {
		t.Error("foreign destination socket acquired recorder ownership")
	}
	foreign.Close()
	conn, err := net.DialTimeout("tcp", proxyURL.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	bound, err := tr.BindNativeHTTPConnection(ctx, conn, origin, net.ParseIP("127.0.0.1"), check)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer bound.Close()
	if _, err := tr.BindNativeHTTPConnection(ctx, conn, origin, net.ParseIP("127.0.0.2"), check); err == nil {
		t.Fatal("existing private socket binding was replaced")
	}
	cancel()
	// Deliberately write over the already-connected socket after cancellation;
	// this tests proxy-side cancellation rather than http.Client cancellation.
	request, _ := http.NewRequest(http.MethodGet, origin.String(), nil)
	bound.SetDeadline(time.Now().Add(2 * time.Second))
	if err := request.WriteProxy(bound); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(bound), request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 502 || received.Load() != 0 {
		t.Fatalf("cancelled private request escaped: status=%d received=%d", response.StatusCode, received.Load())
	}
}

type revokeNativeRequest struct {
	mproxy.BaseAddon
	revoked *atomic.Bool
}

func (a *revokeNativeRequest) Request(*mproxy.Flow) { a.revoked.Store(true) }

func TestNativeHTTPBindingPreservesCaptureAndAuthority(t *testing.T) {
	for _, mode := range []string{"http", "https", "https-passthrough", "alternate", "revoke-at-dispatch", "wrong-host"} {
		t.Run(mode, func(t *testing.T) {
			var received, checks atomic.Int32
			var revoked atomic.Bool
			var gotSNI atomic.Value
			gotSNI.Store("")
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if !strings.HasPrefix(r.Host, "bound.invalid:") {
					t.Errorf("origin Host changed: %q", r.Host)
				}
				if r.Header.Get("X-Research") != "preserved" {
					t.Error("ordinary header lost")
				}
				w.Write([]byte("captured native response"))
			}))
			options := TLSOptions{}
			isTLS := strings.HasPrefix(mode, "https")
			if isTLS {
				ca, err := mitmcert.NewSelfSignCAMemory()
				if err != nil {
					t.Fatal(err)
				}
				leaf, err := ca.GetCert("bound.invalid")
				if err != nil {
					t.Fatal(err)
				}
				root := ca.GetRootCA()
				caFile := filepath.Join(t.TempDir(), "origin-ca.pem")
				if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}), 0600); err != nil {
					t.Fatal(err)
				}
				options.CAFile = caFile
				target.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { gotSNI.Store(hello.ServerName); return nil, nil }}
				target.StartTLS()
			} else {
				target.Start()
			}
			defer target.Close()
			tr, proxyURL, roots := nativeRecordingFixture(t, options)
			tr.SetCaptureEnabled(true)
			if mode == "https-passthrough" {
				tr.pass.Store("bound.invalid", struct{}{})
			}
			if mode == "alternate" {
				tr.proxy.AddAddon(&separateTLSRequest{})
			}
			if mode == "revoke-at-dispatch" {
				tr.proxy.AddAddon(&revokeNativeRequest{revoked: &revoked})
			}
			origin, _ := url.Parse(target.URL + "/evidence?q=preserved")
			origin.Host = net.JoinHostPort("bound.invalid", origin.Port())
			type contextKey struct{}
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "original-task"), 3*time.Second)
			defer cancel()
			check := func(got context.Context) error {
				checks.Add(1)
				if got.Value(contextKey{}) != "original-task" {
					t.Error("policy lost original task context")
				}
				if revoked.Load() {
					return errors.New("origin grant revoked")
				}
				return nil
			}
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots}}
			transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(dialCtx, network, address)
				if err != nil {
					return nil, err
				}
				bound, err := tr.BindNativeHTTPConnection(ctx, conn, origin, net.ParseIP("127.0.0.1"), check)
				if err != nil {
					conn.Close()
				}
				return bound, err
			}
			defer transport.CloseIdleConnections()
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin.String(), nil)
			request.Header.Set("X-Research", "preserved")
			if mode == "wrong-host" {
				request.Host = "unapproved.invalid"
			}
			response, err := (&http.Client{Transport: transport}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			blocked := mode == "alternate" || mode == "revoke-at-dispatch" || mode == "wrong-host"
			if blocked {
				if response.StatusCode != 502 || received.Load() != 0 {
					t.Fatalf("unattested request escaped: status=%d received=%d", response.StatusCode, received.Load())
				}
			} else {
				if response.StatusCode != 200 || string(body) != "captured native response" || received.Load() != 1 {
					t.Fatalf("native forwarding failed: %d %q %d", response.StatusCode, body, received.Load())
				}
				if checks.Load() < 3 {
					t.Fatalf("dispatch did not recheck policy: %d", checks.Load())
				}
				if isTLS && gotSNI.Load() != "bound.invalid" {
					t.Fatalf("TLS identity changed: %v", gotSNI.Load())
				}
				rows, err := tr.Search("bound.invalid", 0, 10)
				if err != nil || len(rows) != 1 || rows[0].URL != origin.String() {
					t.Fatalf("logical capture metadata lost: %+v %v", rows, err)
				}
				requestData, responseData, err := tr.Get(rows[0].ID)
				if err != nil || !strings.Contains(requestData, "X-Research: preserved") || !strings.Contains(responseData, "captured native response") {
					t.Fatalf("captured evidence lost: %q %q %v", requestData, responseData, err)
				}
			}
			deadline := time.Now().Add(time.Second)
			for {
				tr.nativeMu.Lock()
				remaining := len(tr.nativeBindings)
				tr.nativeMu.Unlock()
				if remaining == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("closed native connection retained %d bindings", remaining)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

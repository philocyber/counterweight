package enrich

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mitmcert "github.com/lqqyt2423/go-mitmproxy/cert"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

func TestDirectProbeVerifiesTLSBeforeSendingRequest(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "target")
	}))
	defer target.Close()
	if response, err := buildClient(nil).Get(target.URL); err == nil {
		response.Body.Close()
		t.Fatal("direct probe accepted an untrusted target")
	}
	if calls.Load() != 0 {
		t.Fatal("HTTP request reached target before certificate validation")
	}
}

func TestProbeTrustIsBoundToRecordingRouteAndCaptureToggle(t *testing.T) {
	caDir := t.TempDir()
	ca, err := mitmcert.NewSelfSignCA(caDir)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.GetCert("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "captured HTTPS probe")
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}, MinVersion: tls.VersionTLS12}
	target.StartTLS()
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	proxy, err := mproxy.NewProxy(&mproxy.Options{Addr: addr, CaRootPath: caDir, SslInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	proxy.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return nil, nil })
	started := make(chan error, 1)
	go func() { started <- proxy.Start() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = proxy.Shutdown(ctx)
		select {
		case <-started:
		case <-ctx.Done():
			t.Error("recording-proxy fixture did not stop")
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recording-proxy fixture did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var route atomic.Value
	proxyURL := "http://" + addr
	route.Store(proxyURL)
	client := buildClient(func() string { return route.Load().(string) }, ProxyTrust{
		URL: proxyURL, CAFile: filepath.Join(caDir, "mitmproxy-ca-cert.pem"),
	})
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "captured HTTPS probe" || response.TLS == nil {
		t.Fatal("verified local-MITM probe lost its response or TLS state")
	}
	route.Store("")
	if response, err := client.Get(target.URL); err == nil {
		response.Body.Close()
		t.Fatal("recording CA leaked into direct-target trust after capture was disabled")
	}
	if calls.Load() != 1 {
		t.Fatal("direct route delivered an unverified HTTP request")
	}
	route.Store(proxyURL)
	response, err = client.Get(target.URL)
	if err != nil {
		t.Fatal("capture could not be enabled again", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(strings.Replace(target.URL, "127.0.0.1", "localhost", 1))
	if err != nil {
		t.Fatal("verified recording probe failed for a DNS target", err)
	}
	_ = response.Body.Close()
	unrelated := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted egress proxy received HTTP before TLS verification")
	}))
	defer unrelated.Close()
	route.Store(unrelated.URL)
	if response, err := client.Get(target.URL); err == nil {
		response.Body.Close()
		t.Fatal("recording CA exception escaped to unrelated egress proxy")
	}
}

func TestRecordingIPVerificationRejectsWrongIssuerAndNamedIdentity(t *testing.T) {
	ca, err := mitmcert.NewSelfSignCAMemory()
	if err != nil {
		t.Fatal(err)
	}
	other, err := mitmcert.NewSelfSignCAMemory()
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.GetRootCA())
	for _, tc := range []struct {
		name string
		ca   mitmcert.CA
		want bool
	}{
		{"", ca, false}, {"127.0.0.1", ca, true}, {"foreign.example", ca, false},
		{"127.0.0.2", ca, false}, {"", other, false},
	} {
		certificate, err := tc.ca.GetCert(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := func() error {
			_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "127.0.0.1"})
			return err
		}(); (err == nil) != tc.want {
			t.Fatalf("recording IP verification name=%q want=%v err=%v", tc.name, tc.want, err)
		}
	}

}

func TestMissingRecordingCAFailsClosedOnlyForItsRoute(t *testing.T) {
	const proxyURL = "http://127.0.0.1:9"
	var route atomic.Value
	route.Store(proxyURL)
	client := buildClient(func() string { return route.Load().(string) }, ProxyTrust{
		URL: proxyURL, CAFile: filepath.Join(t.TempDir(), "missing.pem"),
	})
	if _, err := client.Get("https://fixture.invalid"); err == nil {
		t.Fatal("missing recording CA did not fail closed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "direct HTTP") }))
	defer server.Close()
	route.Store("")
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal("bad recording CA disabled unrelated direct HTTP", err)
	}
	_ = response.Body.Close()
}

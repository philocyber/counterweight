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
)

func TestRecordingUpstreamTLSBeforeHTTP(t *testing.T) {
	for _, mode := range []string{"default", "private-ca", "explicit-origin", "different-port", "wrong-hostname"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, "verified or explicitly authorized target")
			}))
			defer target.Close()
			options := TLSOptions{}
			switch mode {
			case "private-ca", "wrong-hostname":
				options.CAFile = filepath.Join(t.TempDir(), "target-ca.pem")
				if err := os.WriteFile(options.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.Certificate().Raw}), 0600); err != nil {
					t.Fatal(err)
				}
			case "explicit-origin":
				options.InsecureOrigins = []string{target.URL}
			case "different-port":
				options.InsecureOrigins = []string{"https://127.0.0.1:1"}
			}
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
			defer tr.Close()
			started := make(chan error, 1)
			go func() { started <- tr.Start() }()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tr.proxy.Shutdown(ctx)
				select {
				case <-started:
				case <-ctx.Done():
					t.Error("recording fixture did not shut down")
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
					t.Fatal("recording fixture did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			root := tr.proxy.GetCertificate()
			roots := x509.NewCertPool()
			roots.AddCert(&root)
			proxyURL, _ := url.Parse("http://" + addr)
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true,
				TLSClientConfig: &tls.Config{RootCAs: roots}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			targetURL := target.URL
			if mode == "wrong-hostname" {
				targetURL = strings.Replace(targetURL, "127.0.0.1", "localhost", 1)
			}
			response, err := client.Get(targetURL)
			allowed := mode == "private-ca" || mode == "explicit-origin"
			if !allowed {
				if err == nil {
					response.Body.Close()
					t.Fatal("proxy accepted an untrusted origin")
				}
				if calls.Load() != 0 {
					t.Fatal("untrusted target received HTTP before validation")
				}
				if _, tunneled := tr.pass.Load("127.0.0.1"); tunneled {
					t.Fatal("certificate failure enabled passthrough")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(body) != "verified or explicitly authorized target" || calls.Load() != 1 {
				t.Fatal("authorized target response changed")
			}
			if n, err := tr.Count(); err != nil || n != 1 {
				t.Fatalf("authorized HTTPS was not captured: count=%d err=%v", n, err)
			}
		})
	}
}

func TestRecordingTLSOptionsRejectAmbiguousExceptions(t *testing.T) {
	for _, raw := range []string{"http://fixture.test", "https://user:secret@fixture.test", "https://fixture.test/private", "https://fixture.test?key=secret", "https://fixture.test/#fragment", "https://fixture.test:bad", "https://fixture.test:0", "https://fixture.test:65536"} {
		if err := ValidateTLSOptions(TLSOptions{InsecureOrigins: []string{raw}}); err == nil {
			t.Fatalf("ambiguous exception accepted: %s", raw)
		}
	}
	if err := ValidateTLSOptions(TLSOptions{CAFile: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing CA file accepted")
	}
}

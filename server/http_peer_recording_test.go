package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
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

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/traffic"
	"github.com/jackc/pgx/v5"
	mitmcert "github.com/lqqyt2423/go-mitmproxy/cert"
)

func nativeServerRecorder(t *testing.T, options traffic.TLSOptions) (*traffic.Traffic, *url.URL, *x509.CertPool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	recorder, err := traffic.Open(t.TempDir(), addr, options)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- recorder.Start() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := recorder.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("recorder did not stop")
		}
		if err := recorder.Close(); err != nil {
			t.Error(err)
		}
	})
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recorder did not start")
		}
	}
	rootPEM, err := os.ReadFile(recorder.CACertPath())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("invalid recorder CA")
	}
	proxyURL, _ := url.Parse(recorder.ProxyAddr())
	return recorder, proxyURL, roots
}

func TestNativeRecordedHTTPUsesTaskAssetIPPolicy(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_DESTINATION_E2E") != "1" {
		t.Skip("requires explicit disposable transport database")
	}
	config, err := pgx.ParseConfig(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil || !strings.HasPrefix(config.Database, "counterweight_transport_") {
		t.Fatal("explicit disposable transport database required")
	}
	d, err := db.Open(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	prior, err := d.ListAssetInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range prior {
		if rule.Enabled && (rule.Kind == "exact_ip" || rule.Kind == "fuzzy_ip" || rule.Kind == "cidr") {
			if err := d.ToggleAssetInterceptRule(rule.ID, false); err != nil {
				t.Fatal(err)
			}
			defer d.ToggleAssetInterceptRule(rule.ID, true)
		}
	}
	task, err := d.CreateTask("native recorder policy fixture", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	// Require a checked peer even though the hostname is allowed. The IPv6 rule
	// makes localhost deterministic against this fixture's IPv4-only listener.
	ipv6, err := d.Assets().CreateTaskInterceptRule(task.ID, "block", "cidr", "::1/128", "fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Assets().DeleteTaskInterceptRule(task.ID, ipv6.ID)
	for _, lang := range []locale.Lang{locale.En} {
		for _, explicit := range []bool{false, true} {
			t.Run(string(lang)+"/"+map[bool]string{false: "automatic", true: "explicit"}[explicit], func(t *testing.T) {
				var requests atomic.Int32
				target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if !strings.HasPrefix(r.Host, "localhost:") {
						t.Errorf("Host changed: %s", r.Host)
					}
					w.Write([]byte("asset-authorized recorded response"))
				}))
				options := traffic.TLSOptions{}
				if explicit {
					target.Start()
				} else {
					ca, err := mitmcert.NewSelfSignCAMemory()
					if err != nil {
						t.Fatal(err)
					}
					leaf, err := ca.GetCert("localhost")
					if err != nil {
						t.Fatal(err)
					}
					root := ca.GetRootCA()
					options.CAFile = filepath.Join(t.TempDir(), "native-origin-ca.pem")
					if err := os.WriteFile(options.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}), 0600); err != nil {
						t.Fatal(err)
					}
					target.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}}
					target.StartTLS()
				}
				defer target.Close()
				u, _ := url.Parse(target.URL)
				endpoint := u.Scheme + "://localhost:" + u.Port() + "/native"
				allow, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_url", endpoint, "fixture", true)
				if err != nil {
					t.Fatal(err)
				}
				defer d.Assets().DeleteTaskInterceptRule(task.ID, allow.ID)
				recorder, _, _ := nativeServerRecorder(t, options)
				recorder.SetCaptureEnabled(true)
				s := &Server{m: &Manager{assets: d.Assets(), traffic: recorder, trafficOn: true}}
				ctx, cancel := context.WithTimeout(agent.WithRunInfo(locale.WithLang(t.Context(), lang), agent.RunInfo{TaskID: task.ID}), 3*time.Second)
				defer cancel()
				spec := httpExec{URL: endpoint, UseRecordingProxy: !explicit}
				if explicit {
					spec.Proxy = recorder.ProxyAddr()
				}
				raw, _ := json.Marshal(spec)
				result, err := s.runHTTPTool(ctx, raw, nil, nil)
				if err != nil || result.IsError || requests.Load() != 1 || !strings.Contains(result.Flatten(), "asset-authorized recorded response") {
					t.Fatalf("native asset-authorized capture failed: %v requests=%d result=%s", err, requests.Load(), result.Flatten())
				}
				rows, err := recorder.Search("localhost", 0, 10)
				if err != nil || len(rows) != 1 || rows[0].URL != endpoint {
					t.Fatalf("native capture metadata changed: %+v %v", rows, err)
				}
				blocked, err := d.Assets().CreateTaskInterceptRule(task.ID, "block", "exact_ip", "127.0.0.1", "fixture", true)
				if err != nil {
					t.Fatal(err)
				}
				defer d.Assets().DeleteTaskInterceptRule(task.ID, blocked.ID)
				result, err = s.runHTTPTool(ctx, raw, nil, nil)
				if err != nil || !result.IsError || requests.Load() != 1 {
					t.Fatalf("revoked IP reached native recording route: %v requests=%d result=%s", err, requests.Load(), result.Flatten())
				}
				count, err := recorder.Count()
				if err != nil || count != 1 {
					t.Fatalf("denied native request was captured: %d %v", count, err)
				}
			})
		}
	}
}

func TestRecordedHTTPPeerTransportUsesCheckedNumericOrigin(t *testing.T) {
	for _, mode := range []string{"http", "https", "blocked-ip", "revoke-before-bind", "dns-error", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var requests, resolutions, checks, destinations atomic.Int32
			var gotSNI atomic.Value
			gotSNI.Store("")
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if !strings.HasPrefix(r.Host, "recorded.invalid:") {
					t.Errorf("logical Host changed: %s", r.Host)
				}
				if mode == "redirect" && r.URL.Path == "/fixture" {
					w.Header().Set("Location", "/redirected")
					w.WriteHeader(302)
					return
				}
				w.Write([]byte("native checked capture"))
			}))
			options := traffic.TLSOptions{}
			if mode == "https" {
				ca, err := mitmcert.NewSelfSignCAMemory()
				if err != nil {
					t.Fatal(err)
				}
				leaf, err := ca.GetCert("recorded.invalid")
				if err != nil {
					t.Fatal(err)
				}
				root := ca.GetRootCA()
				options.CAFile = filepath.Join(t.TempDir(), "origin-ca.pem")
				if err := os.WriteFile(options.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}), 0600); err != nil {
					t.Fatal(err)
				}
				target.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { gotSNI.Store(hello.ServerName); return nil, nil }}
				target.StartTLS()
			} else {
				target.Start()
			}
			defer target.Close()
			recorder, proxyURL, roots := nativeServerRecorder(t, options)
			recorder.SetCaptureEnabled(true)
			origin, _ := url.Parse(target.URL + "/fixture")
			origin.Host = net.JoinHostPort("recorded.invalid", origin.Port())
			type contextKey struct{}
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "native-task"), 3*time.Second)
			defer cancel()
			base := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots}}
			transport := &httpPeerTransport{base: base, recordingProxy: proxyURL, bindRecording: recorder.BindNativeHTTPConnection,
				lookup: func(got context.Context, host string) ([]net.IPAddr, error) {
					resolutions.Add(1)
					if host != "recorded.invalid" {
						t.Errorf("wrong DNS hostname: %s", host)
					}
					if mode == "dns-error" {
						return nil, errors.New("fixture DNS failure")
					}
					return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
				},
				proxyDestination: func(*http.Request) error { return db.ErrHTTPProxyPeerRequired },
				destination:      func(*http.Request) error { destinations.Add(1); return nil },
				check: func(got context.Context, ip net.IP) error {
					n := checks.Add(1)
					if got.Value(contextKey{}) != "native-task" {
						t.Error("original task context lost")
					}
					if !ip.Equal(net.ParseIP("127.0.0.1")) {
						t.Errorf("unchecked peer: %s", ip)
					}
					if mode == "blocked-ip" || mode == "revoke-before-bind" && n >= 3 {
						return errors.New("fixture peer revoked")
					}
					return nil
				}}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin.String(), nil)
			response, err := (&http.Client{Transport: transport}).Do(request)
			blocked := mode == "blocked-ip" || mode == "revoke-before-bind" || mode == "dns-error"
			if blocked {
				if response != nil {
					response.Body.Close()
				}
				var policy *httpPeerPolicyError
				if !errors.As(err, &policy) || requests.Load() != 0 {
					t.Fatalf("refused native route escaped: %v requests=%d", err, requests.Load())
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				want := int32(1)
				if mode == "redirect" {
					want = 2
				}
				if readErr != nil || response.StatusCode != 200 || string(body) != "native checked capture" || requests.Load() != want || resolutions.Load() != want {
					t.Fatalf("native route/capture changed: %d %q requests=%d resolutions=%d err=%v", response.StatusCode, body, requests.Load(), resolutions.Load(), readErr)
				}
				if mode == "https" && gotSNI.Load() != "recorded.invalid" {
					t.Fatalf("TLS SNI changed: %v", gotSNI.Load())
				}
				rows, err := recorder.Search("recorded.invalid", 0, 10)
				if err != nil || len(rows) != int(want) {
					t.Fatalf("missing native recording: %+v %v", rows, err)
				}
				for _, row := range rows {
					if !strings.HasPrefix(row.URL, origin.Scheme+"://"+origin.Host+"/") {
						t.Fatalf("numeric peer leaked into metadata: %s", row.URL)
					}
				}
				if checks.Load() < 5 || destinations.Load() < 4 {
					t.Fatalf("dispatch policy was not rechecked: checks=%d destinations=%d", checks.Load(), destinations.Load())
				}
			}
		})
	}
}

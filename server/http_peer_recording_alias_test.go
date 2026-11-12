package server

import (
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/traffic"
	mitmcert "github.com/lqqyt2423/go-mitmproxy/cert"
)

func TestHTTPPeerRecordingLoopbackAliasKeepsCheckedOrigin(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) { testHTTPPeerRecordingAlias(t, scheme) })
	}
}

func testHTTPPeerRecordingAlias(t *testing.T, scheme string) {
	t.Helper()
	var requests atomic.Int32
	originServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Host != "alias-origin.invalid:"+r.URL.Query().Get("port") {
			t.Errorf("logical Host changed: %s", r.Host)
		}
		if scheme == "https" && (r.TLS == nil || r.TLS.ServerName != "alias-origin.invalid") {
			t.Error("logical TLS server name changed")
		}
		w.Write([]byte("alias checked capture"))
	}))
	options := traffic.TLSOptions{}
	if scheme == "https" {
		ca, err := mitmcert.NewSelfSignCAMemory()
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := ca.GetCert("alias-origin.invalid")
		if err != nil {
			t.Fatal(err)
		}
		options.CAFile = filepath.Join(t.TempDir(), "alias-origin-ca.pem")
		if err := os.WriteFile(options.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.GetRootCA().Raw}), 0600); err != nil {
			t.Fatal(err)
		}
		originServer.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}}
		originServer.StartTLS()
	} else {
		originServer.Start()
	}
	defer originServer.Close()
	origin, _ := url.Parse(originServer.URL)
	origin.Host = net.JoinHostPort("alias-origin.invalid", origin.Port())
	origin.Path = "/"
	origin.RawQuery = "port=" + origin.Port()
	recorder, canonical, roots := nativeServerRecorder(t, options)
	recorder.SetCaptureEnabled(true)
	alias := *canonical
	alias.Host = net.JoinHostPort("localhost", canonical.Port())
	var lookups atomic.Int32
	transport := &httpPeerTransport{
		base:           &http.Transport{Proxy: http.ProxyURL(&alias), TLSClientConfig: &tls.Config{RootCAs: roots}},
		recordingProxy: canonical,
		recordingPeer:  recorder.OwnsRecordingPeer,
		bindRecording:  recorder.BindNativeHTTPConnection,
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			lookups.Add(1)
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		},
		proxyDestination: func(*http.Request) error { return db.ErrHTTPProxyPeerRequired },
		check:            func(context.Context, net.IP) error { return nil },
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin.String(), nil)
	response, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil || response.StatusCode != http.StatusOK || string(body) != "alias checked capture" || requests.Load() != 1 || lookups.Load() != 1 {
		t.Fatalf("recording alias lost checked destination: status=%d body=%q requests=%d lookups=%d error=%v", response.StatusCode, body, requests.Load(), lookups.Load(), readErr)
	}
	rows, err := recorder.Search("alias-origin.invalid", 0, 10)
	if err != nil || len(rows) != 1 || rows[0].URL != origin.String() {
		t.Fatalf("recording alias lost logical capture: rows=%+v error=%v", rows, err)
	}
}

func TestHTTPPeerRecordingAliasPinsInfrastructureResolution(t *testing.T) {
	recorder, canonical, _ := nativeServerRecorder(t, traffic.TLSOptions{})
	alias := *canonical
	alias.Host = net.JoinHostPort("recorder-alias.invalid", canonical.Port())
	alias.User = url.UserPassword("fixture", "fixture-password")
	for _, mode := range []string{"owned", "foreign", "zoned", "dns-error"} {
		t.Run(mode, func(t *testing.T) {
			var lookups int
			transport := &httpPeerTransport{
				recordingProxy: canonical,
				recordingPeer:  recorder.OwnsRecordingPeer,
				proxyLookup: func(context.Context, string) ([]net.IPAddr, error) {
					lookups++
					if mode == "dns-error" {
						return nil, errors.New("fixture resolution failure")
					}
					candidate := net.IPAddr{IP: net.ParseIP("127.0.0.1")}
					if mode == "foreign" {
						candidate.IP = net.ParseIP("127.0.0.2")
					}
					if mode == "zoned" {
						candidate.Zone = "fixture"
					}
					return []net.IPAddr{candidate}, nil
				},
			}
			selected, owned, err := transport.checkedRecordingProxy(t.Context(), &alias)
			if lookups != 1 {
				t.Fatalf("proxy lookups=%d", lookups)
			}
			if mode == "dns-error" {
				if err == nil {
					t.Fatal("failed resolution was accepted")
				}
				return
			}
			if err != nil || owned != (mode == "owned") {
				t.Fatalf("owned=%v error=%v", owned, err)
			}
			if owned && (selected.Host != canonical.Host || selected.User.String() != alias.User.String()) {
				t.Fatalf("numeric endpoint or credentials changed: %s", selected)
			}
			if !owned && selected != &alias {
				t.Fatal("external proxy selection changed")
			}
			if alias.Hostname() != "recorder-alias.invalid" {
				t.Fatal("caller URL was mutated")
			}
		})
	}
}

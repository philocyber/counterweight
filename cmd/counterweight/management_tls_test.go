package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestManagementTLSAndLocalDefault(t *testing.T) {
	host, _, err := net.SplitHostPort(defaultManagementAddress)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("management default exposes HTTP beyond loopback: %q", defaultManagementAddress)
	}
	if config, err := managementTLS("", ""); config != nil || err != nil {
		t.Fatalf("local HTTP setup changed: %v %v", config, err)
	}
	for _, pair := range [][2]string{{"missing", ""}, {"", "missing"}, {"missing", "missing"}} {
		if _, err := managementTLS(pair[0], pair[1]); err == nil {
			t.Fatal("invalid TLS configuration silently fell back to HTTP")
		}
	}
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	defer fixture.Close()
	cert := fixture.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := managementTLS(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion != tls.VersionTLS12 {
		t.Fatal("unexpected minimum TLS version")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := &http.Server{TLSConfig: config, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.TLS == nil || r.TLS.Version < tls.VersionTLS12 {
			t.Error("credentials reached handler without TLS")
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	address := listener.Addr().String()
	response, err := fixture.Client().Post("https://"+address, "application/json", strings.NewReader(`{"password":"synthetic-fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || calls.Load() != 1 {
		t.Fatal("native HTTPS management request failed")
	}
	response, err = http.Get("http://" + address)
	if err == nil {
		response.Body.Close()
	}
	if calls.Load() != 1 {
		t.Fatal("plaintext request reached HTTPS management handler")
	}
	server.Close()
	if err := <-done; err != http.ErrServerClosed {
		t.Fatalf("TLS listener shutdown: %v", err)
	}
}

func TestManagementAddressRequiresRemoteTLS(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8787", "[::1]:8787", "[::ffff:127.0.0.1]:8787", "localhost:8787"} {
		got, err := managementAddress(address, nil)
		if err != nil {
			t.Fatalf("local HTTP rejected: %s: %v", address, err)
		}
		host, _, err := net.SplitHostPort(got)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			t.Fatalf("HTTP hostname not pinned to loopback: %s", got)
		}
	}
	for _, address := range []string{":8787", "0.0.0.0:8787", "[::]:8787", "192.0.2.1:8787", "[2001:db8::1]:8787", "[::ffff:192.0.2.1]:8787"} {
		if _, err := managementAddress(address, nil); err == nil {
			t.Fatalf("remote plaintext listener accepted: %s", address)
		}
		if got, err := managementAddress(address, &tls.Config{MinVersion: tls.VersionTLS12}); err != nil || got != address {
			t.Fatalf("HTTPS deployment listener changed: %s %s %v", address, got, err)
		}
	}
	for _, address := range []string{"not-an-address", "127.0.0.1:not-a-port"} {
		if _, err := managementAddress(address, nil); err == nil {
			t.Fatalf("malformed listener accepted: %s", address)
		}
	}
}

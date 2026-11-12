package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRecordingClientPolicySurvivesCaptureToggle(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_PROXY_CLIENT_E2E") != "1" {
		t.Skip("requires explicit disposable transport database and local interface")
	}
	config, err := pgx.ParseConfig(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil || !strings.HasPrefix(config.Database, "counterweight_transport_") {
		t.Fatal("explicit disposable transport database required")
	}
	clientIP := net.ParseIP(os.Getenv("COUNTERWEIGHT_PROXY_TEST_CLIENT_IP"))
	if clientIP == nil || clientIP.IsLoopback() || clientIP.To4() == nil {
		t.Fatal("explicit local non-loopback IPv4 interface required")
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
	if !local {
		t.Fatal("test interface must belong to this host")
	}
	t.Setenv("COUNTERWEIGHT_PROXY_CLIENT_CIDRS", "")
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "COUNTERWEIGHT_LLM_PROVIDER", "COUNTERWEIGHT_LLM_MODEL", "COUNTERWEIGHT_LLM_BASE_URL", "COUNTERWEIGHT_LLM_PROXY"} {
		t.Setenv(key, "")
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	m, err := NewManager(t.TempDir(), net.JoinHostPort("0.0.0.0", fmt.Sprint(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.Traffic() == nil {
		t.Fatal("proxy initialization failed")
	}
	prior := m.TrafficEnabled()
	defer m.SetTrafficEnabled(prior)
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.Write([]byte("fixture")) }))
	defer target.Close()
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
	for _, enabled := range []bool{false, true, false} {
		if err := m.SetTrafficEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		dialer := net.Dialer{Timeout: time.Second, LocalAddr: &net.TCPAddr{IP: clientIP}}
		conn, err := dialer.Dial("tcp", net.JoinHostPort(clientIP.String(), fmt.Sprint(port)))
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		io.WriteString(conn, "GET "+target.URL+"/ HTTP/1.1\r\nHost: fixture\r\nX-Forwarded-For: 127.0.0.1\r\n\r\n")
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
		conn.Close()
		if err == nil {
			response.Body.Close()
			t.Fatalf("capture=%v disabled client policy", enabled)
		}
		if received.Load() != 0 {
			t.Fatal("unapproved traffic reached target")
		}
		if enabled && m.ProxyAddr() != m.Traffic().ProxyAddr() {
			t.Fatal("enabled worker capture routing changed")
		}
	}
	proxyURL, _ := url.Parse("http://" + net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if received.Load() != 1 {
		t.Fatal("trusted loopback client workflow lost")
	}
}

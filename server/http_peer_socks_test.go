package server

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func TestPinnedSOCKSProxyUsesNumericDestination(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			var received atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if !strings.HasPrefix(r.Host, "fixture.invalid:") || r.Header.Get("Proxy-Authorization") != "" {
					t.Errorf("logical Host or proxy credential boundary changed: %s", r.Host)
				}
				w.Write([]byte("SOCKS fixture"))
			}))
			defer target.Close()
			targetURL, _ := url.Parse(target.URL)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() { done <- serveNumericSOCKSFixture(listener, targetURL.Host) }()
			proxyURL, _ := url.Parse(scheme + "://" + listener.Addr().String())
			base := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			defer base.CloseIdleConnections()
			transport := &httpPeerTransport{base: base, proxyDestination: func(*http.Request) error { return db.ErrHTTPProxyPeerRequired }, check: func(context.Context, net.IP) error { return nil }, lookup: func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			}}
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Get(strings.Replace(target.URL, "127.0.0.1", "fixture.invalid", 1))
			if err != nil {
				listener.Close()
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "SOCKS fixture" || received.Load() != 1 {
				t.Fatalf("SOCKS request failed: %q %v %d", body, err, received.Load())
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("SOCKS tunnel did not close")
			}
		})
	}
}

func serveNumericSOCKSFixture(listener net.Listener, target string) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 2)
	if _, err = io.ReadFull(conn, header); err != nil {
		return err
	}
	methods := make([]byte, int(header[1]))
	if _, err = io.ReadFull(conn, methods); err != nil {
		return err
	}
	if _, err = conn.Write([]byte{5, 0}); err != nil {
		return err
	}
	request := make([]byte, 4)
	if _, err = io.ReadFull(conn, request); err != nil {
		return err
	}
	// Domain-form requests would hand DNS back to the proxy and defeat pinning.
	if request[0] != 5 || request[1] != 1 || request[3] != 1 {
		return &net.AddrError{Err: "SOCKS destination was not numeric IPv4", Addr: "fixture"}
	}
	address := make([]byte, 6)
	if _, err = io.ReadFull(conn, address); err != nil {
		return err
	}
	actual := net.JoinHostPort(net.IP(address[:4]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(address[4:]))))
	if actual != target {
		return &net.AddrError{Err: "SOCKS destination changed", Addr: actual}
	}
	upstream, err := net.DialTimeout("tcp", actual, time.Second)
	if err != nil {
		return err
	}
	defer upstream.Close()
	upstream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { io.Copy(upstream, conn); upstream.Close(); close(done) }()
	io.Copy(conn, upstream)
	conn.Close()
	<-done
	return nil
}

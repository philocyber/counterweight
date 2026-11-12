package traffic

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestCaptureOffDirectProxyForwardsWithoutRecording(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "http"
		if secure {
			name = "https"
		}
		t.Run(name, func(t *testing.T) { testCaptureOffDirectProxy(t, secure) })
	}
}

func testCaptureOffDirectProxy(t *testing.T, secure bool) {
	body := bytes.Repeat([]byte{0x7b}, maxInlineBody+1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	})
	var target *httptest.Server
	options := TLSOptions{}
	if secure {
		target = httptest.NewTLSServer(handler)
		// Exercise the supported operator-approved local TLS exception without
		// altering trust for other origins or weakening client proxy verification.
		options.InsecureOrigins = []string{target.URL}
	} else {
		target = httptest.NewServer(handler)
	}
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	tr, err := Open(t.TempDir(), addr, options)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.SetCaptureEnabled(false)
	finished := make(chan error, 1)
	go func() { finished <- tr.Start() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tr.proxy.Shutdown(ctx)
		select {
		case <-finished:
		case <-ctx.Done():
			t.Error("proxy did not shut down")
		}
	}()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("proxy did not start")
		}
	}
	proxyURL, _ := url.Parse("http://" + addr)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	if secure {
		root := tr.proxy.GetCertificate()
		roots := x509.NewCertPool()
		roots.AddCert(&root)
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for i, on := range []bool{false, true, false} {
		tr.SetCaptureEnabled(on)
		response, err := client.Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !bytes.Equal(got, body) {
			t.Fatalf("toggle %v changed proxy response: %v", on, err)
		}
		want := 1
		if i == 0 {
			want = 0
		}
		if n, err := tr.Count(); n != want || err != nil {
			t.Fatalf("toggle %v captured or removed evidence: %d %v", on, n, err)
		}
		status := tr.BlobStorageStatus()
		if status.DroppedCaptures != 0 || (i == 0 && status.UsedBytes != 0) {
			t.Fatalf("intentional off allocated bodies or reported quota loss: %+v", status)
		}
	}
	if n, err := tr.ExportHosts([]string{"127.0.0.1"}, t.TempDir()); n != 1 || err != nil {
		t.Fatalf("capture off prevented saved export: %d %v", n, err)
	}
}

func TestCaptureDisableWaitsForAdmittedWriteLock(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.wmu.Lock()
	started, done := make(chan struct{}), make(chan struct{})
	go func() { close(started); tr.SetCaptureEnabled(false); close(done) }()
	<-started
	select {
	case <-done:
		tr.wmu.Unlock()
		t.Fatal("disable returned before pending write released")
	case <-time.After(20 * time.Millisecond):
	}
	tr.wmu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disable did not finish")
	}
	tr.record(newFlow("toggle.fixture", "GET", "/", nil, []byte("response")))
	if n, err := tr.Count(); n != 0 || err != nil {
		t.Fatalf("post-disable capture: %d %v", n, err)
	}
}

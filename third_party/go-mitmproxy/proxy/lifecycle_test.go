package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type closeOriginAtBodyEOF struct {
	BaseAddon
	closed atomic.Int32
}

func (*closeOriginAtBodyEOF) Requestheaders(f *Flow)  { f.Stream = true }
func (*closeOriginAtBodyEOF) Responseheaders(f *Flow) { f.Response.Header.Del("Content-Length") }
func (a *closeOriginAtBodyEOF) StreamResponseModifier(f *Flow, reader io.Reader) io.Reader {
	return &originEOFReader{Reader: reader, close: func() { a.closed.Add(1); _ = f.ConnContext.ServerConn.Conn.Close() }}
}

type originEOFReader struct {
	io.Reader
	once  sync.Once
	close func()
}

func (r *originEOFReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	if err == io.EOF {
		r.once.Do(r.close)
	}
	return n, err
}

func TestOriginClosureFinishesChunkedHTTPResponse(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			handleError(t, err)
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			helper := &testProxyHelper{server: &http.Server{}, proxyAddr: ":" + strconv.Itoa(port)}
			helper.init(t)
			addon := &closeOriginAtBodyEOF{}
			helper.testProxy.AddAddon(addon)
			go helper.server.Serve(helper.ln)
			go helper.server.Serve(helper.tlsLn)
			go helper.testProxy.Start()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = helper.testProxy.Shutdown(ctx)
				_ = helper.server.Close()
				_ = helper.ln.Close()
				_ = helper.tlsPlainLn.Close()
			})
			for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(time.Millisecond) {
				conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 20*time.Millisecond)
				if err == nil {
					conn.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("proxy did not start")
				}
			}
			client := helper.getProxyClient()
			client.Timeout = 2 * time.Second
			defer client.CloseIdleConnections()
			endpoint := helper.httpEndpoint
			if scheme == "https" {
				endpoint = helper.httpsEndpoint
			}
			for n := 0; n < 2; n++ {
				response, err := client.Get(endpoint)
				handleError(t, err)
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				handleError(t, readErr)
				if response.StatusCode != 200 || string(body) != "ok" || len(response.TransferEncoding) != 1 || response.TransferEncoding[0] != "chunked" {
					t.Fatalf("response framing changed: status=%d body=%q encoding=%v", response.StatusCode, body, response.TransferEncoding)
				}
			}
			if addon.closed.Load() != 2 {
				t.Fatalf("origin closure not exercised: %d", addon.closed.Load())
			}
		})
	}
}

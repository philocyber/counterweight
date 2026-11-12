package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type originLog struct {
	mu   sync.Mutex
	seen []string
}

func (o *originLog) add(s string) {
	o.mu.Lock()
	o.seen = append(o.seen, s)
	o.mu.Unlock()
}

func (o *originLog) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.seen...)
}

type recordedResponses struct {
	BaseAddon
	mu     sync.Mutex
	byHost map[string]string
}

func (r *recordedResponses) Response(f *Flow) {
	r.mu.Lock()
	r.byHost[f.Request.URL.Host] = string(f.Response.Body)
	r.mu.Unlock()
}

func TestKeepAliveRequestsForTwoOriginsStayOnTheirOwnUpstream(t *testing.T) {
	var logA, logB originLog
	var srvB *httptest.Server
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logA.add(r.URL.Path + "|" + r.Header.Get("Authorization"))
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, srvB.URL+"/landing", http.StatusFound)
			return
		}
		io.WriteString(w, "reply-from-A")
	}))
	defer srvA.Close()
	srvB = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logB.add(r.URL.Path + "|" + r.Header.Get("Authorization"))
		io.WriteString(w, "reply-from-B")
	}))
	defer srvB.Close()

	p, err := NewProxy(&Options{Addr: ":29210", SslInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordedResponses{byHost: map[string]string{}}
	p.AddAddon(rec)
	go p.Start()
	defer p.Close()
	time.Sleep(200 * time.Millisecond)

	conn, err := net.Dial("tcp", "127.0.0.1:29210")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)

	do := func(target, extra string) (*http.Response, string) {
		host := strings.TrimPrefix(target, "http://")
		host = host[:strings.Index(host, "/")]
		fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, host, extra)
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		res, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(body)
	}

	res, body := do(srvA.URL+"/redirect", "")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != srvB.URL+"/landing" {
		t.Fatalf("unexpected first response %d %q", res.StatusCode, body)
	}
	// Follow the redirect on the same client connection, with a credential.
	res, body = do(srvB.URL+"/landing", "Authorization: Bearer secret\r\n")
	if res.StatusCode != 200 || body != "reply-from-B" {
		t.Fatalf("second origin answered %d %q", res.StatusCode, body)
	}
	// And back to the first origin again on the same connection.
	res, body = do(srvA.URL+"/again", "")
	if res.StatusCode != 200 || body != "reply-from-A" {
		t.Fatalf("first origin answered %d %q", res.StatusCode, body)
	}

	if got := logA.list(); len(got) != 2 || got[0] != "/redirect|" || got[1] != "/again|" {
		t.Fatalf("first origin saw %v", got)
	}
	if got := logB.list(); len(got) != 1 || got[0] != "/landing|Bearer secret" {
		t.Fatalf("second origin saw %v", got)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	hostB := strings.TrimPrefix(srvB.URL, "http://")
	if rec.byHost[hostB] != "reply-from-B" {
		t.Fatalf("recorded reply for second host: %q", rec.byHost[hostB])
	}
}

func TestSameAuthority(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"example.com:80", "EXAMPLE.com:80", true},
		{"example.com:80", "example.com:8080", false},
		{"a.test:80", "b.test:80", false},
		{"bücher.example:80", "xn--bcher-kva.example:80", true},
		{"nohostport", "nohostport", false},
	}
	for _, c := range cases {
		if got := sameAuthority(c.a, c.b); got != c.want {
			t.Errorf("sameAuthority(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

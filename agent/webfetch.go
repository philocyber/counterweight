package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/locale"
)

const (
	webFetchTimeout   = 30 * time.Second
	webFetchBodyLimit = 2 << 20
	webFetchMaxHops   = 10
)

var errWebFetchHops = errors.New("too many redirects")

// reviewedWebFetch is the WebFetch tool every session gets in place of the
// SDK's own. It guarantees that:
//   - every redirect is reviewed, as a WebFetch call to the new location,
//     before any request is sent there, and a refused one ends the call;
//   - a request that fails through the configured proxy is reported as failed
//     and is never repeated without the proxy.
//
// The embedded tool is an SDK WebFetch without network settings. It supplies
// the name, description, schema and permission text, checks the input, and
// renders the response; the only address it is ever given is the relay's.
type reviewedWebFetch struct {
	actool.CoreTool
	transport http.RoundTripper // nil: the process default, as the SDK uses without settings
	proxied   bool
}

// newReviewedWebFetch applies the proxy and TLS settings the way the SDK does,
// except that a proxy address that cannot be read fails every request instead
// of letting it leave directly.
func newReviewedWebFetch(proxy, caCert string, insecureTLS bool) *reviewedWebFetch {
	t := &reviewedWebFetch{CoreTool: actool.NewWebFetch(actool.WebFetchConfig{}), proxied: proxy != ""}
	if proxy == "" && !insecureTLS && caCert == "" {
		return t
	}
	// The transport lives as long as its session and is never closed, so idle
	// connections to the proxy must not outlive it indefinitely.
	tr := &http.Transport{IdleConnTimeout: 90 * time.Second}
	if proxy != "" {
		address, err := url.Parse(proxy)
		tr.Proxy = func(*http.Request) (*url.URL, error) { return address, err }
	}
	switch {
	case insecureTLS:
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	case caCert != "":
		if pem, err := os.ReadFile(caCert); err == nil {
			pool, _ := x509.SystemCertPool()
			if pool == nil {
				pool = x509.NewCertPool()
			}
			if pool.AppendCertsFromPEM(pem) {
				tr.TLSClientConfig = &tls.Config{RootCAs: pool}
			}
		}
	}
	t.transport = tr
	return t
}

func (t *reviewedWebFetch) Call(ctx context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return actool.Result{}, err
	}
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return actool.Errorf("Error: url must start with http:// or https://"), nil
	}
	target, err := url.Parse(in.URL)
	if err != nil {
		return actool.Errorf("Error: " + err.Error()), nil
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(input, &fields)
	relay, err := startWebFetchRelay()
	if err != nil {
		return actool.Errorf("Error fetching URL: " + err.Error()), nil
	}
	defer relay.close()
	// The loopback address keeps the path of the requested URL, which is all
	// the SDK's own input checks read, so they decide exactly as they would
	// for that URL. The query stays out: a raw space in one would be refused
	// by the relay before the SDK's request reached it.
	local := relay.base + target.EscapedPath()
	localCall := webFetchCall(fields, local)
	// First pass: the SDK checks the input and, if it accepts it, reaches the
	// relay, which has no response yet. A call the SDK refuses never gets
	// there, and nothing is sent to the target.
	checked, err := t.CoreTool.Call(ctx, localCall, tc)
	userAgent, accepted := relay.asked()
	if err == nil && !accepted && !checked.IsError {
		// Something other than the relay's handler answered; that is not the
		// target's response and must not be shown as one.
		return actool.Errorf("Error fetching URL: " + locale.Text(locale.FromContext(ctx), "The request could not be prepared from this URL")), nil
	}
	if err != nil || !accepted {
		return webFetchShow(checked, local, in.URL), err
	}
	page, final, err := t.fetch(ctx, in.URL, userAgent, fields, tc)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	// Second pass: the SDK renders the response the relay now holds.
	relay.offer(page)
	rendered, err := t.CoreTool.Call(ctx, localCall, tc)
	if err != nil {
		return actool.Result{}, err
	}
	if rendered.IsError {
		return actool.Errorf("Error fetching URL: " + locale.Text(locale.FromContext(ctx), "The response was received but could not be prepared for display")), nil
	}
	return webFetchShow(rendered, local, final), nil
}

// fetch requests start and follows at most webFetchMaxHops redirects, each one
// only after the session's review allowed it. It returns the last response and
// the URL that produced it. The timeout covers network time only: the time a
// review takes, which may include a person's decision, does not count.
func (t *reviewedWebFetch) fetch(ctx context.Context, start, userAgent string, fields map[string]json.RawMessage, tc *actool.ToolContext) (*webFetchPage, string, error) {
	lang := locale.FromContext(ctx)
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	left, since := webFetchTimeout, time.Now()
	timer := time.AfterFunc(left, func() { cancel(context.DeadlineExceeded) })
	defer timer.Stop()
	final, refused := start, ""
	var refusal error
	client := &http.Client{Transport: t.transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= webFetchMaxHops {
			return errWebFetchHops
		}
		timer.Stop()
		left -= time.Since(since)
		err := ReviewOperation(next.Context(), "WebFetch", webFetchCall(fields, next.URL.String()), tc)
		since = time.Now()
		timer.Reset(left)
		if err != nil {
			refused, refusal = next.URL.String(), err
			return err
		}
		final = next.URL.String()
		return nil
	}}
	req, err := http.NewRequestWithContext(runCtx, http.MethodGet, start, nil)
	if err != nil {
		return nil, "", errors.New("Error: " + err.Error())
	}
	req.Header.Set("user-agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		timedOut := errors.Is(context.Cause(runCtx), context.DeadlineExceeded)
		switch {
		case refusal != nil && !timedOut:
			return nil, "", errors.New(locale.Text(lang, "Redirect to %s was not followed: %s", refused, locale.ErrorMessage(lang, refusal)))
		case errors.Is(err, errWebFetchHops):
			return nil, "", errors.New("Error fetching URL: " + locale.Text(lang, "Stopped after %d redirects without reaching a final response", webFetchMaxHops))
		}
		var requestErr *url.Error
		if timedOut && errors.As(err, &requestErr) {
			requestErr.Err = context.DeadlineExceeded
		}
		message := "Error fetching URL: " + err.Error()
		if t.proxied {
			message += ". " + locale.Text(lang, "The request goes through the configured proxy (the recording proxy while traffic capture is on), which could not complete it. It was not retried without the proxy.")
		}
		return nil, "", errors.New(message)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 {
		return nil, "", errors.New("Error fetching URL: " + locale.Text(lang, "The server answered with status %s, which is not a final response", resp.Status))
	}
	page := &webFetchPage{status: resp.StatusCode, header: http.Header{}}
	for _, name := range []string{"Server", "X-Powered-By", "Content-Type"} {
		if value := resp.Header.Get(name); value != "" {
			page.header.Set(name, value)
		}
	}
	page.body, _ = io.ReadAll(io.LimitReader(resp.Body, webFetchBodyLimit))
	return page, final, nil
}

// webFetchCall returns the call the agent made with its URL replaced, so a
// redirect is reviewed as the same call sent to the new location. Characters
// are kept as written, the way a model sends them, for rules that read the text.
func webFetchCall(fields map[string]json.RawMessage, address string) json.RawMessage {
	call := maps.Clone(fields)
	if call == nil {
		call = map[string]json.RawMessage{}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(address)
	call["url"] = bytes.TrimSpace(out.Bytes())
	out = bytes.Buffer{}
	encoder = json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(call)
	return bytes.TrimSpace(out.Bytes())
}

// webFetchShow returns the SDK's text with the relay address in its first line
// replaced by the URL the content came from.
func webFetchShow(result actool.Result, local, shown string) actool.Result {
	out := actool.Text(strings.Replace(result.Flatten(), local, shown, 1))
	out.IsError = result.IsError
	return out
}

// webFetchPage is the part of a fetched response the SDK's rendering reads.
type webFetchPage struct {
	status int
	header http.Header
	body   []byte
}

// webFetchRelay hands one fetched response to the SDK's WebFetch for
// rendering. It listens on a loopback port chosen by the system for the length
// of one call, answers only requests that carry a random path token created
// for that call, serves the response it was given once, and never makes a
// request of its own. close ends the listener and every connection.
type webFetchRelay struct {
	server *http.Server
	done   chan struct{}
	base   string // http://127.0.0.1:<port>/<token>
	token  string

	mu        sync.Mutex
	reached   bool
	userAgent string
	page      *webFetchPage
}

func startWebFetchRelay() (*webFetchRelay, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &webFetchRelay{done: make(chan struct{}), token: "/" + rand.Text()}
	r.base = "http://" + listener.Addr().String() + r.token
	r.server = &http.Server{Handler: http.HandlerFunc(r.serve)}
	r.server.SetKeepAlivesEnabled(false)
	go func() {
		defer close(r.done)
		_ = r.server.Serve(listener)
	}()
	return r, nil
}

func (r *webFetchRelay) close() {
	_ = r.server.Close()
	<-r.done
}

// asked reports whether the SDK reached the relay, and the user agent it sent.
func (r *webFetchRelay) asked() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userAgent, r.reached
}

func (r *webFetchRelay) offer(page *webFetchPage) {
	r.mu.Lock()
	r.page = page
	r.mu.Unlock()
}

func (r *webFetchRelay) serve(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || req.URL.Path != r.token && !strings.HasPrefix(req.URL.Path, r.token+"/") {
		http.NotFound(w, req)
		return
	}
	r.mu.Lock()
	page := r.page
	r.page = nil
	r.reached, r.userAgent = true, req.Header.Get("User-Agent")
	r.mu.Unlock()
	if page == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Only the headers the fetched response had are sent: no content type is
	// guessed, and no Location is ever sent, so the reader cannot be redirected.
	header := w.Header()
	header["Content-Type"] = nil
	maps.Copy(header, page.header)
	w.WriteHeader(page.status)
	_, _ = w.Write(page.body)
}

package llmrec

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type captureContextKey struct{}

// Capture collects the untouched wire bodies of one logical LLM call. The
// Recorder creates it and puts it on the context; the HTTP transport that norma
// dials through (agent.quotaAwareTransport) finds it there and fills it in.
//
// This exists because everything the Recorder itself sees is already normalized:
// llm.CompletionRequest is re-serialized rather than the body buildBody() sent,
// and the response arrives as decoded StreamEvents, not the SSE frames. For
// debugging a live provider, the bytes on the wire are the only ground truth.
//
// norma's doStream retries the request-establishment phase, so one Stream can
// issue several HTTP attempts. Each attempt is kept: the discarded ones (see
// norma/llm/retry.go, which closes non-final bodies unread) are exactly what
// makes rate-limit and gateway failures diagnosable.
//
// The transport writes from norma's stream-reading goroutine while the Recorder
// snapshots at stream end, so all state is mutex-guarded.
type Capture struct {
	mu       sync.Mutex
	request  string
	attempts []*attempt
}

type attempt struct {
	status    int
	body      strings.Builder
	truncated bool
}

// MarkResponseTruncated records intentional early termination of a captured
// body. It does not modify captured wire bytes or affect uncaptured responses.
func MarkResponseTruncated(body io.ReadCloser) {
	if tee, ok := body.(*teeBody); ok {
		tee.c.mu.Lock()
		tee.a.truncated = true
		tee.c.mu.Unlock()
	}
}

func responseText(a *attempt) string {
	if a.truncated {
		return a.body.String() + "\n===== response truncated; remaining wire bytes not read =====\n"
	}
	return a.body.String()
}

// NewCapture returns a context carrying a fresh Capture, plus the Capture itself.
func NewCapture(ctx context.Context) (context.Context, *Capture) {
	c := &Capture{}
	return context.WithValue(ctx, captureContextKey{}, c), c
}

// CaptureFrom returns the Capture attached to ctx, or nil when raw capture is
// off. Callers must tolerate nil — recording is a toggle, and non-recorded
// providers dial through the same transport.
func CaptureFrom(ctx context.Context) *Capture {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(captureContextKey{}).(*Capture)
	return c
}

// SetRequest stores the outgoing request body. Retries re-send identical bytes,
// so only the first attempt's body is kept.
func (c *Capture) SetRequest(body string) {
	if c == nil || body == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.request == "" {
		c.request = body
	}
}

// TeeResponse opens a new attempt and wraps rc so everything read from it is
// mirrored into that attempt. It tees rather than reads because a successful
// response is an SSE stream that must keep streaming to the caller.
func (c *Capture) TeeResponse(status int, rc io.ReadCloser) io.ReadCloser {
	if c == nil || rc == nil {
		return rc
	}
	a := &attempt{status: status}
	c.mu.Lock()
	c.attempts = append(c.attempts, a)
	c.mu.Unlock()
	return &teeBody{rc: rc, c: c, a: a}
}

// RawRequest returns the request body as sent, or "" if nothing was captured.
func (c *Capture) RawRequest() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request
}

// RawResponse returns the response bytes as received. A single attempt yields
// the untouched original (copy-pasteable straight into a replay) unless the
// transport marked it truncated, in which case an explicit annotation follows
// the captured prefix. Multiple
// attempts are concatenated behind per-attempt header lines so a retry sequence
// stays readable.
func (c *Capture) RawResponse() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch len(c.attempts) {
	case 0:
		return ""
	case 1:
		return responseText(c.attempts[0])
	}
	var b strings.Builder
	for i, a := range c.attempts {
		fmt.Fprintf(&b, "===== attempt %d/%d — HTTP %d =====\n", i+1, len(c.attempts), a.status)
		body := responseText(a)
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Attempt is one HTTP round trip's status code and captured response bytes.
// Truncated distinguishes intentionally incomplete bodies from replayable ones.
type Attempt struct {
	Status    int
	Body      string
	Truncated bool
}

// Attempts returns every round trip in order. Unlike RawResponse — which drops
// the header line for a lone attempt so the bytes stay replayable — this always
// carries the status code, for callers that must report "HTTP 401 + body".
func (c *Capture) Attempts() []Attempt {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Attempt, 0, len(c.attempts))
	for _, a := range c.attempts {
		out = append(out, Attempt{Status: a.status, Body: a.body.String(), Truncated: a.truncated})
	}
	return out
}

// teeBody mirrors reads into a Capture attempt, guarded by the Capture's mutex
// so a snapshot taken mid-stream never races the writer.
type teeBody struct {
	rc io.ReadCloser
	c  *Capture
	a  *attempt
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		t.c.mu.Lock()
		t.a.body.Write(p[:n])
		t.c.mu.Unlock()
	}
	return n, err
}

func (t *teeBody) Close() error { return t.rc.Close() }

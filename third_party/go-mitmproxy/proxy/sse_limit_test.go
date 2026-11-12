package proxy

import (
	"bytes"
	"io"
	"net/url"
	"strings"
	"testing"
)

func newTestSSEFlow() *Flow {
	f := newFlow()
	f.Request = &Request{URL: &url.URL{Scheme: "http", Host: "example.test", Path: "/"}}
	f.ConnContext = &ConnContext{proxy: &Proxy{}}
	f.SSE = newSSEData()
	return f
}

func drain(t *testing.T, r io.Reader) []byte {
	t.Helper()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSSEReaderCapsRetainedEvents(t *testing.T) {
	f := newTestSSEFlow()
	var in bytes.Buffer
	for i := 0; i < 5000; i++ {
		in.WriteString("data: x\n\n")
	}
	want := in.Len()
	got := drain(t, newSSEReader(f, &in))
	if len(got) != want {
		t.Fatalf("relayed %d bytes, want %d", len(got), want)
	}
	if n := len(f.SSE.Events); n != maxRetainedStreamEntries {
		t.Fatalf("retained %d events, want %d", n, maxRetainedStreamEntries)
	}
	if f.SSE.Events[len(f.SSE.Events)-1].Data != "x" {
		t.Fatal("newest event not last")
	}
}

func TestSSEReaderParsesCRLFFraming(t *testing.T) {
	f := newTestSSEFlow()
	in := strings.NewReader("data: a\r\n\r\ndata: b\r\n\r\n")
	drain(t, newSSEReader(f, in))
	if len(f.SSE.Events) != 2 || f.SSE.Events[0].Data != "a" || f.SSE.Events[1].Data != "b" {
		t.Fatalf("unexpected events: %+v", f.SSE.Events)
	}
}

func TestSSEReaderBoundsDelimiterlessStream(t *testing.T) {
	f := newTestSSEFlow()
	chunk := strings.Repeat("data: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", 20)
	total := 0
	sr := newSSEReader(f, readerFunc(func(p []byte) (int, error) {
		if total > 8*maxSSEEventBytes {
			return 0, io.EOF
		}
		total += len(chunk)
		return copy(p, chunk), nil
	})).(*sseReader)
	buf := make([]byte, 4096)
	maxPending := 0
	for {
		_, err := sr.Read(buf)
		if len(sr.buffer) > maxPending {
			maxPending = len(sr.buffer)
		}
		if err != nil {
			break
		}
	}
	if maxPending > maxSSEEventBytes+len(buf) {
		t.Fatalf("pending buffer reached %d bytes", maxPending)
	}
	if len(f.SSE.Events) != 0 {
		t.Fatalf("oversized event was retained")
	}
}

func TestSSEReaderResumesAfterOversizedEvent(t *testing.T) {
	f := newTestSSEFlow()
	in := strings.NewReader(strings.Repeat("data: big\n", maxSSEEventBytes/8) + "\n\ndata: after\n\n")
	drain(t, newSSEReader(f, in))
	if n := len(f.SSE.Events); n != 1 || f.SSE.Events[0].Data != "after" {
		t.Fatalf("unexpected events: %d", n)
	}
}

func TestSSEFindEventEndDoesNotRescan(t *testing.T) {
	sr := &sseReader{}
	sr.buffer = []byte(strings.Repeat("a\n", 100))
	if end, _ := sr.findEventEnd(); end != -1 {
		t.Fatal("unexpected boundary")
	}
	if sr.scanned < 190 {
		t.Fatalf("scan position %d, expected to resume near the end", sr.scanned)
	}
}

func TestWebSocketHistoryIsBounded(t *testing.T) {
	d := newWebSocketData()
	for i := 0; i < 5000; i++ {
		d.addMessage(1, []byte("x"), true)
	}
	if len(d.Messages) != maxRetainedStreamEntries {
		t.Fatalf("retained %d messages", len(d.Messages))
	}
	big := bytes.Repeat([]byte("y"), maxRetainedStreamBytes/2)
	for i := 0; i < 10; i++ {
		d.addMessage(2, big, false)
	}
	if d.retainedBytes > maxRetainedStreamBytes || len(d.Messages) < 1 {
		t.Fatalf("retained %d bytes in %d messages", d.retainedBytes, len(d.Messages))
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

package server

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

// newActivityStreamFixture creates a task backed by the test database and a
// server whose engine is not running, so the only activity writers are the test's.
func newActivityStreamFixture(t *testing.T) (*Server, *Task) {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	t.Cleanup(func() { m.Close() })
	task, err := m.CreateTask("activity stream", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { m.pg.DeleteTask(taskID) })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Server{m: m, engine: NewEngine(m), ctx: ctx}, task
}

func taskActivityIDs(t *testing.T, task *Task) []int64 {
	t.Helper()
	var ids []int64
	var since int64
	for {
		items, cursor, err := task.Store.ActivityList(nil, since, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range items {
			ids = append(ids, a.ID)
		}
		if len(items) < 500 {
			return ids
		}
		since = cursor
	}
}

func subscriberCount(b *Broadcaster, task string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[task])
}

func waitForSubscriber(t *testing.T, b *Broadcaster, task string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for subscriberCount(b, task) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("stream handler never subscribed")
		}
		time.Sleep(time.Millisecond)
	}
}

func sseIDs(body string) []int64 {
	var ids []int64
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, "id: "); ok {
			n, _ := strconv.ParseInt(v, 10, 64)
			ids = append(ids, n)
		}
	}
	return ids
}

// startStreamClient connects a slow, EventSource-like client to the stream
// handler: whenever the stream ends it reconnects with Last-Event-ID. The
// returned wait func blocks until want ids arrived (or a timeout), stops the
// client and returns every id it received, in arrival order.
func startStreamClient(t *testing.T, s *Server, task *Task) (wait func(want int) []int64) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.streamActivity))
	t.Cleanup(srv.Close)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop) // runs before srv.Close, which waits for open streams

	var mu sync.Mutex
	var got []int64
	done := make(chan struct{})
	ready := make(chan struct{})
	go func() {
		defer close(done)
		last := ""
		first := true
		for ctx.Err() == nil {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?task="+task.ID+"&since=0", nil)
			if last != "" {
				req.Header.Set("Last-Event-ID", last)
			}
			resp, err := http.DefaultClient.Do(req)
			if first {
				first = false
				close(ready)
			}
			if err != nil {
				return
			}
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 64*1024), 1<<20)
			for sc.Scan() {
				if v, ok := strings.CutPrefix(sc.Text(), "id: "); ok {
					n, _ := strconv.ParseInt(v, 10, 64)
					mu.Lock()
					got = append(got, n)
					mu.Unlock()
					last = v
					time.Sleep(time.Millisecond) // slow consumer
				}
			}
			resp.Body.Close()
		}
	}()
	<-ready
	waitForSubscriber(t, s.engine.Broadcaster(), task.ID)
	return func(want int) []int64 {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(got)
			mu.Unlock()
			if n >= want {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond) // surface any duplicate still in flight
		stop()
		<-done
		mu.Lock()
		defer mu.Unlock()
		return append([]int64(nil), got...)
	}
}

// TestActivityStreamConcurrentEmittersDeliverEveryIDInOrder drives the real SSE
// handler with a slow, reconnecting client while several goroutines emit
// concurrently. Every persisted id must arrive exactly once and in order.
func TestActivityStreamConcurrentEmittersDeliverEveryIDInOrder(t *testing.T) {
	s, task := newActivityStreamFixture(t)
	wait := startStreamClient(t, s, task)

	const emitters, perEmitter = 8, 40
	total := emitters * perEmitter
	var wg sync.WaitGroup
	for w := 0; w < emitters; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perEmitter; i++ {
				s.engine.emitActivity(task, db.Activity{Worker: fmt.Sprintf("worker-%d", w), Kind: "text", Summary: fmt.Sprintf("step %d", i)})
			}
		}(w)
	}
	wg.Wait()

	want := taskActivityIDs(t, task)
	if len(want) != total {
		t.Fatalf("persisted %d rows, want %d", len(want), total)
	}
	if got := wait(len(want)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stream delivered %d ids (want %d) or out of order:\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
}

// gatedWriter is an SSE ResponseWriter whose writes block until the gate opens,
// standing in for a client that has stopped reading.
type gatedWriter struct {
	header http.Header
	gate   chan struct{}
	mu     sync.Mutex
	buf    strings.Builder
}

func (w *gatedWriter) Header() http.Header { return w.header }
func (w *gatedWriter) WriteHeader(int)     {}
func (w *gatedWriter) Flush()              {}
func (w *gatedWriter) Write(p []byte) (int, error) {
	<-w.gate
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// TestActivityStreamOverflowClosesStreamAndReplayFillsGap stalls a subscriber
// until its buffer overflows. The handler must end the stream once it has
// drained what was buffered, and a reconnect from the last delivered id must
// replay exactly the events that were not delivered.
func TestActivityStreamOverflowClosesStreamAndReplayFillsGap(t *testing.T) {
	s, task := newActivityStreamFixture(t)
	bc := s.engine.Broadcaster()

	w := &gatedWriter{header: http.Header{}, gate: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/api/exploration/activity/stream?task="+task.ID+"&since=0", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.streamActivity(w, req)
	}()
	waitForSubscriber(t, bc, task.ID)

	// One event to park the handler inside a blocked write, then enough to overflow
	// the 256-slot buffer by a few.
	const total = 1 + 256 + 5
	for i := 0; i < total; i++ {
		s.engine.emitActivity(task, db.Activity{Worker: "worker", Kind: "text", Summary: fmt.Sprintf("step %d", i)})
	}
	if n := subscriberCount(bc, task.ID); n != 0 {
		t.Fatalf("overflowing subscriber still registered (%d subscribers)", n)
	}

	close(w.gate)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stream handler did not return after its subscription was closed")
	}

	all := taskActivityIDs(t, task)
	if len(all) != total {
		t.Fatalf("persisted %d rows, want %d", len(all), total)
	}
	first := sseIDs(w.buf.String())
	if len(first) == 0 || len(first) >= total {
		t.Fatalf("first stream delivered %d events, want a strict prefix of %d", len(first), total)
	}
	if fmt.Sprint(first) != fmt.Sprint(all[:len(first)]) {
		t.Fatalf("first stream is not an in-order prefix:\n got=%v\nwant=%v", first, all[:len(first)])
	}

	// Reconnect the way EventSource does and collect the replay.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req2 := httptest.NewRequest(http.MethodGet, "/api/exploration/activity/stream?task="+task.ID+"&since=0", nil).WithContext(ctx)
	req2.Header.Set("Last-Event-ID", strconv.FormatInt(first[len(first)-1], 10))
	rec := httptest.NewRecorder()
	s.streamActivity(rec, req2)
	rest := sseIDs(rec.Body.String())
	if fmt.Sprint(rest) != fmt.Sprint(all[len(first):]) {
		t.Fatalf("replay after reconnect:\n got=%v\nwant=%v", rest, all[len(first):])
	}
}

// TestBroadcasterUnsubscribeAfterOverflowClose checks that a subscriber closed
// by an overflowing Publish can still be unsubscribed, and that later publishes
// neither panic nor reach it.
func TestBroadcasterUnsubscribeAfterOverflowClose(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe("t")
	for i := 1; i <= 257; i++ {
		b.Publish("t", db.Activity{ID: int64(i)})
	}
	b.Publish("t", db.Activity{ID: 258})
	n := 0
	for range ch {
		n++
	}
	if n != 256 {
		t.Fatalf("drained %d buffered events before close, want 256", n)
	}
	unsub()
	unsub()
	b.Publish("t", db.Activity{ID: 259})

	// A healthy subscriber is unaffected and its unsubscribe closes the channel.
	ch2, unsub2 := b.Subscribe("t")
	b.Publish("t", db.Activity{ID: 260})
	unsub2()
	var got []int64
	for a := range ch2 {
		got = append(got, a.ID)
	}
	if fmt.Sprint(got) != "[260]" {
		t.Fatalf("healthy subscriber got %v", got)
	}
}

// TestActivityStreamFollowUpAuditInterleavesInOrder runs finding follow-ups
// (whose audit row commits inside the intent transaction) concurrently with
// worker emitters, through the real stream handler: every id arrives once, in order.
func TestActivityStreamFollowUpAuditInterleavesInOrder(t *testing.T) {
	s, task := newActivityStreamFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // admission is exercised without starting background planner/worker work
	s.ctx = ctx
	nodeID, err := task.Store.AddNode(db.KindFinding, map[string]any{"summary": "source"}, 5, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	findingID, err := s.m.pg.AddFinding(taskID, nodeID, "test", "source", db.SeverityHigh, "source", "", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.DeleteFinding(findingID) })
	if err := s.m.SetTaskStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	wait := startStreamClient(t, s, task)

	const followUps, emitters, perEmitter = 6, 4, 30
	var wg sync.WaitGroup
	codes := make(chan int, followUps)
	for i := 0; i < followUps; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/exploration/findings/1/deepen",
				strings.NewReader(fmt.Sprintf(`{"description":"Follow-up %d: confirm the chain"}`, i)))
			req.SetPathValue("id", strconv.FormatInt(findingID, 10))
			rec := httptest.NewRecorder()
			s.deepenFinding(rec, req)
			codes <- rec.Code
		}(i)
	}
	for w := 0; w < emitters; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perEmitter; i++ {
				s.engine.emitActivity(task, db.Activity{Worker: fmt.Sprintf("worker-%d", w), Kind: "text", Summary: fmt.Sprintf("step %d", i)})
			}
		}(w)
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("follow-up status = %d", code)
		}
	}

	want := taskActivityIDs(t, task)
	if len(want) < followUps+emitters*perEmitter {
		t.Fatalf("persisted %d rows, want at least %d", len(want), followUps+emitters*perEmitter)
	}
	if got := wait(len(want)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stream delivered %d ids (want %d) or out of order:\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
}

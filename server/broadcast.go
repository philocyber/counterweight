package server

import (
	"sync"

	"github.com/Philocyber/counterweight/db"
)

// Broadcaster is a per-task in-process pub/sub for live activity events. The
// engine publishes each appended activity at its single emit point; SSE handlers
// subscribe per task. Storage (activity table) and the live stream come from the
// same Publish call, so they never diverge.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[string]map[chan db.Activity]struct{} // task id -> set of subscriber channels
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[string]map[chan db.Activity]struct{}{}}
}

// Subscribe returns a buffered channel of activities for a task plus an
// unsubscribe func the caller must invoke (defer) to release it.
func (b *Broadcaster) Subscribe(task string) (<-chan db.Activity, func()) {
	ch := make(chan db.Activity, 256)
	b.mu.Lock()
	if b.subs[task] == nil {
		b.subs[task] = map[chan db.Activity]struct{}{}
	}
	b.subs[task][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.removeLocked(task, ch)
		})
	}
}

// removeLocked detaches and closes a subscriber channel if it is still
// registered. Channels are only sent to and closed under b.mu while present in
// the set, so a channel is closed exactly once and never sent to after close.
func (b *Broadcaster) removeLocked(task string, ch chan db.Activity) {
	m := b.subs[task]
	if _, ok := m[ch]; !ok {
		return
	}
	delete(m, ch)
	if len(m) == 0 {
		delete(b.subs, task)
	}
	close(ch)
}

// Publish fans an activity out to all subscribers of a task. Non-blocking: if a
// subscriber's buffer is full, that subscriber is detached and its channel is
// closed instead of silently losing the event. The SSE handler then ends the
// stream, the browser reconnects with its Last-Event-ID, and the replay from the
// DB fills the gap, so a slow client never stalls the engine.
func (b *Broadcaster) Publish(task string, a db.Activity) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[task] {
		select {
		case ch <- a:
		default:
			b.removeLocked(task, ch)
		}
	}
}

package agent

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// Native Codex owns network retries. Preserve Counterweight's profile request pacing
// around both streaming inference and synchronous summaries.
type nativePacedProvider struct {
	llm.Provider
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func (p *nativePacedProvider) wait(ctx context.Context) error {
	p.mu.Lock()
	now := time.Now()
	start := p.next
	if start.Before(now) {
		start = now
	}
	p.next = start.Add(p.interval)
	p.mu.Unlock()
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (p *nativePacedProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if err := p.wait(ctx); err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		for event, err := range p.Provider.Stream(ctx, req) {
			if !yield(event, err) {
				return
			}
		}
	}
}
func (p *nativePacedProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	if err := p.wait(ctx); err != nil {
		return llm.Message{}, "", llm.Usage{}, err
	}
	return p.Provider.Complete(ctx, req)
}

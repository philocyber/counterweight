package server

import (
	"io"
	"sync"
	"testing"
	"time"
)

func TestLogSinkDisconnectCannotRacePublication(t *testing.T) {
	s := &logSinkT{cap: 128, subs: make(map[chan LogLine]struct{}), out: io.Discard}
	const writers, messages, clients, disconnects = 4, 10000, 4, 5000
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() {
				if problem := recover(); problem != nil {
					t.Errorf("publication panicked during disconnect: %v", problem)
				}
			}()
			<-start
			for j := 0; j < messages; j++ {
				s.add(LogLine{Text: "synthetic log"})
			}
		}()
	}
	for i := 0; i < clients; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for j := 0; j < disconnects; j++ {
				stream, unsubscribe := s.subscribe()
				unsubscribe()
				unsubscribe() // Cleanup remains safe when called more than once.
				var previous int64
				for line := range stream {
					if line.Seq <= previous {
						t.Errorf("stream sequence regressed: %d after %d", line.Seq, previous)
					}
					previous = line.Seq
				}
			}
		}()
	}
	close(start)
	group.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subs) != 0 || s.seq != writers*messages || len(s.ring) != s.cap {
		t.Fatalf("publication/subscription state lost: subscribers=%d seq=%d ring=%d", len(s.subs), s.seq, len(s.ring))
	}
}

func TestLogSinkSlowSubscriberDoesNotBlock(t *testing.T) {
	s := &logSinkT{cap: 64, subs: make(map[chan LogLine]struct{}), out: io.Discard}
	stream, unsubscribe := s.subscribe()
	defer unsubscribe()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s.add(LogLine{Text: "synthetic log"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a full subscriber buffer blocked publication")
	}
	lines, cursor := s.recent(0, 0)
	if cursor != 1000 || len(lines) != 64 || lines[0].Seq != 937 || lines[63].Seq != 1000 {
		t.Fatalf("ring history lost after drops: cursor=%d lines=%v", cursor, lines)
	}
	if len(stream) != 256 {
		t.Fatalf("subscriber buffer changed: %d", len(stream))
	}
	unsubscribe()
	previous := int64(0)
	for line := range stream {
		if line.Seq != previous+1 {
			t.Fatalf("buffered stream lost ordering: %d after %d", line.Seq, previous)
		}
		previous = line.Seq
	}
	if previous != 256 {
		t.Fatalf("closed stream lost buffered entries: %d", previous)
	}
}

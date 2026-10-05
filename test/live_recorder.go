// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Live channel recorder (TEST-009). Production realtime code emits events
// to channels; tests need to capture those events deterministically so they
// can assert on order and content without races. The recorder is a
// bounded-buffer collector: Emit appends, Events returns the slice, WaitFor
// blocks until N events are seen or the deadline passes.

package test

import (
	"context"
	"sync"
	"time"
)

// RecordedEvent captures a single realtime event for assertions.
type RecordedEvent struct {
	Topic   string
	Payload []byte
	At      time.Time
}

// LiveRecorder is the per-test realtime collector. Tests pass it to the
// production broadcast layer in place of a real wire transport; the layer
// calls Emit for each message it would have sent.
type LiveRecorder struct {
	mu     sync.Mutex
	events []RecordedEvent
	subs   map[string]bool // set of subscribed topics
	cond   *sync.Cond
	closed bool
}

// NewLiveRecorder constructs an empty recorder.
func NewLiveRecorder() *LiveRecorder {
	r := &LiveRecorder{subs: map[string]bool{}}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// Emit appends an event and broadcasts to any waiter.
func (r *LiveRecorder) Emit(topic string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.events = append(r.events, RecordedEvent{
		Topic:   topic,
		Payload: append([]byte(nil), payload...),
		At:      time.Now(),
	})
	r.cond.Broadcast()
}

// Subscribe records a topic subscription. The production layer may check
// this to decide whether to fan out. Returns true if the subscription
// was newly added.
func (r *LiveRecorder) Subscribe(topic string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subs[topic] {
		return false
	}
	r.subs[topic] = true
	return true
}

// Unsubscribe removes a topic subscription.
func (r *LiveRecorder) Unsubscribe(topic string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, topic)
}

// Events returns a copy of the recorded events.
func (r *LiveRecorder) Events() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

// WaitFor blocks until at least n events have been recorded or ctx is
// cancelled. Returns the events seen so far.
func (r *LiveRecorder) WaitFor(ctx context.Context, n int) ([]RecordedEvent, error) {
	// Polling condition loop with a 5ms backoff; avoids spinning while
	// still being responsive.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		r.mu.Lock()
		count := len(r.events)
		events := make([]RecordedEvent, count)
		copy(events, r.events)
		r.mu.Unlock()
		if count >= n {
			return events, nil
		}
		select {
		case <-ctx.Done():
			return events, ctx.Err()
		case <-ticker.C:
		}
	}
}

// AssertTopic fails the test if no event on topic was recorded.
func (r *LiveRecorder) AssertTopic(t interface{ Fatalf(string, ...any) }, topic string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Topic == topic {
			return
		}
	}
	t.Fatalf("ogontest: no event recorded on topic %q", topic)
}

// Close stops further emits. Subsequent Emit calls are no-ops.
func (r *LiveRecorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.cond.Broadcast()
}

// Count returns the number of events recorded.
func (r *LiveRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// Subscribed reports whether topic is currently subscribed.
func (r *LiveRecorder) Subscribed(topic string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.subs[topic]
}

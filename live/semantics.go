// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/semantics — delivery semantics (LIVE-016/017).
//
// OGON-LIVE default is at-most-once: the server publishes, the
// subscriber receives at most once, no ack required. This is the
// lowest-latency option and the one most applications should use.
//
// Optional at-least-once mode requires the client to ACK every message
// (using the AckID envelope field). The server keeps an in-flight map
// of unacked messages per connection; if a connection drops with
// unacked messages, the server re-publishes them on resume (provided
// they are still in the resume window). This is heavier and is opt-in
// per channel via the Handle DSL: live.Handle("/room", Comp,
// live.WithDelivery(live.DeliveryAtLeastOnce)).

package live

import (
	"sync"
	"sync/atomic"
	"time"
)

// DeliveryMode enumerates the two supported delivery semantics.
type DeliveryMode int

const (
	// DeliveryAtMostOnce is the default. No ack; messages may be dropped
	// under backpressure but are never redelivered.
	DeliveryAtMostOnce DeliveryMode = iota
	// DeliveryAtLeastOnce requires client ACK. Unacked messages are
	// redelivered on resume. The application MUST be idempotent.
	DeliveryAtLeastOnce
)

// AckTracker tracks per-connection unacked message IDs for
// at-least-once delivery (LIVE-017).
//
// Each entry holds the AckID, the topic, the payload, and the timestamp
// so the server can re-publish on resume. Entries age out after a
// configurable TTL (default 60s) to bound memory.
type AckTracker struct {
	mu      sync.Mutex
	pending map[string]ackEntry
	ttl     time.Duration
}

type ackEntry struct {
	ackID    string
	topic    string
	payload  []byte
	enqueued time.Time
}

// NewAckTracker constructs a tracker. ttl bounds how long an unacked
// message is tracked before being abandoned (treated as acked for
// memory purposes — the application must accept loss after this window).
func NewAckTracker(ttl time.Duration) *AckTracker {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &AckTracker{pending: make(map[string]ackEntry), ttl: ttl}
}

// Track records a new unacked delivery.
func (t *AckTracker) Track(ackID, topic string, payload []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[ackID] = ackEntry{
		ackID:    ackID,
		topic:    topic,
		payload:  append([]byte(nil), payload...),
		enqueued: time.Now(),
	}
}

// Ack removes ackID from pending. Returns true if the ack was tracked
// (i.e. the message had not already been acked or evicted).
func (t *AckTracker) Ack(ackID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.pending[ackID]; !ok {
		return false
	}
	delete(t.pending, ackID)
	return true
}

// Pending returns a snapshot of all unacked entries (for re-publish on resume).
func (t *AckTracker) Pending() []ackEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	out := make([]ackEntry, 0, len(t.pending))
	for id, e := range t.pending {
		if now.Sub(e.enqueued) > t.ttl {
			delete(t.pending, id)
			continue
		}
		out = append(out, e)
	}
	return out
}

// Size returns the number of unacked entries currently tracked.
func (t *AckTracker) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

// global atomic ack counter for generating unique IDs
var ackCounter atomic.Uint64

// NewAckID generates a unique, sortable ack ID.
func NewAckID() string {
	n := ackCounter.Add(1)
	return formatAckID(n)
}

func formatAckID(n uint64) string {
	// Simple base36-ish; collisions impossible under a single process.
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%36]
		n /= 36
	}
	return string(buf[i:])
}

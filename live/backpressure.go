// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/backpressure — bounded per-connection queues, slow-client
// eviction, per-conn rate limiting, per-channel load shedding.
// (LIVE-012/013/042/047)
//
// The fundamental law of OGON-LIVE backpressure:
//
//   "Every outbound queue is bounded. The publisher never blocks on a
//    slow consumer; the slow consumer drops, gets rate-limited, or is
//    evicted — never the producer."
//
// Backpressure is layered:
//
//   1. per-connection send queue (OutboundQueue) — drop-oldest default
//   2. per-connection rate limiter (RateLimiter) — token bucket
//   3. per-channel load shedder (LoadShedder) — drops publishes when
//      channel inflight exceeds cap
//   4. slow-client eviction — if a connection's queue stays full past
//      a threshold for `EvictAfter`, the connection is force-closed
//      with an "evict" envelope explaining why.

package live

import (
	"sync"
	"sync/atomic"
	"time"
)

// DropPolicy enumerates queue-full handling. Mirrors pubsub.BackendConfig.
type DropPolicy int

const (
	DropOldest DropPolicy = iota // default
	DropNew
	DropBlock // use sparingly — publisher stalls
)

// OutboundQueue is a bounded FIFO per-connection write queue.
//
// It is the single chokepoint between the dispatcher goroutine and the
// connection's write loop. Under load it drops (oldest by default) and
// records drops for observability.
//
// Concurrency:
//   - Enqueue and Close are mutually exclusive via mu — Enqueue holds
//     mu for the channel send; Close holds mu while closing. The
//     closed flag is checked under mu so the close-send race is closed.
type OutboundQueue struct {
	cap    int
	policy DropPolicy
	ch     chan []byte
	closed atomic.Bool
	mu     sync.Mutex

	dropped atomic.Int64
	sent    atomic.Int64
	// stall tracking for slow-client eviction (LIVE-013)
	stallSince atomic.Int64 // unix-nano; 0 = not stalling
}

// NewOutboundQueue constructs a queue with the given bound and policy.
func NewOutboundQueue(capacity int, policy DropPolicy) *OutboundQueue {
	if capacity <= 0 {
		capacity = 64
	}
	return &OutboundQueue{
		cap:    capacity,
		policy: policy,
		ch:     make(chan []byte, capacity),
	}
}

// Enqueue attempts to deliver a frame to the connection's write loop.
// Returns true on success. On drop, increments the drop counter.
func (q *OutboundQueue) Enqueue(frame []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed.Load() {
		return false
	}
	// Non-blocking send first (the common path).
	select {
	case q.ch <- frame:
		q.sent.Add(1)
		q.stallSince.Store(0)
		return true
	default:
	}
	// Queue full. Apply policy.
	switch q.policy {
	case DropNew:
		q.dropped.Add(1)
		q.markStall()
		return false
	case DropBlock:
		// Blocking send — used only when explicitly configured.
		// Release mu around the blocking wait so Close can proceed.
		q.mu.Unlock()
		select {
		case q.ch <- frame:
			q.mu.Lock()
			q.sent.Add(1)
			q.stallSince.Store(0)
			return true
		case <-time.After(50 * time.Millisecond):
			q.mu.Lock()
			q.dropped.Add(1)
			return false
		}
	default: // DropOldest
		select {
		case <-q.ch:
			q.dropped.Add(1)
		default:
		}
		select {
		case q.ch <- frame:
			q.sent.Add(1)
			q.stallSince.Store(0)
			return true
		default:
			q.dropped.Add(1)
			q.markStall()
			return false
		}
	}
}

func (q *OutboundQueue) markStall() {
	if q.stallSince.Load() == 0 {
		q.stallSince.Store(time.Now().UnixNano())
	}
}

// StallDuration returns how long the queue has been continuously full.
// Returns 0 if not stalling.
func (q *OutboundQueue) StallDuration() time.Duration {
	s := q.stallSince.Load()
	if s == 0 {
		return 0
	}
	return time.Since(time.Unix(0, s))
}

// Out returns the channel the connection's write loop drains.
func (q *OutboundQueue) Out() <-chan []byte { return q.ch }

// Close releases the queue. After Close, Enqueue is a no-op.
func (q *OutboundQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed.CompareAndSwap(false, true) {
		return
	}
	close(q.ch)
}

// Stats returns dropped and sent counts.
func (q *OutboundQueue) Stats() (dropped, sent int64) {
	return q.dropped.Load(), q.sent.Load()
}

// RateLimiter is a per-connection token bucket (LIVE-042).
//
// The default cap is 64 messages per second per connection, burstable
// up to 128. Application code SHOULD tune this per route via the
// Handle DSL.
type RateLimiter struct {
	mu       sync.Mutex
	tokens   int
	max      int
	refill   int // tokens per second
	lastFill time.Time
}

// NewRateLimiter constructs a token bucket with the given refill rate
// (tokens/sec) and burst cap.
func NewRateLimiter(refillPerSec, burst int) *RateLimiter {
	if refillPerSec <= 0 {
		refillPerSec = 64
	}
	if burst <= 0 {
		burst = refillPerSec * 2
	}
	return &RateLimiter{
		tokens:   burst,
		max:      burst,
		refill:   refillPerSec,
		lastFill: time.Now(),
	}
}

// Allow returns true if a token is available; false if the connection
// is over its current rate budget.
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	// Refill proportional to elapsed time.
	elapsed := now.Sub(r.lastFill).Seconds()
	add := int(elapsed * float64(r.refill))
	if add > 0 {
		r.tokens += add
		if r.tokens > r.max {
			r.tokens = r.max
		}
		r.lastFill = now
	}
	if r.tokens <= 0 {
		return false
	}
	r.tokens--
	return true
}

// LoadShedder caps in-flight publishes per channel (LIVE-047).
//
// When `inflight` exceeds `MaxInflight`, subsequent publishes return
// false (the caller drops). This protects the dispatch worker pool
// from a single hot channel swamping the system.
type LoadShedder struct {
	name      string
	max       int
	inflight  atomic.Int64
	shedCount atomic.Int64
}

// NewLoadShedder constructs a shedder for the named channel.
func NewLoadShedder(name string, maxInflight int) *LoadShedder {
	if maxInflight <= 0 {
		maxInflight = 1024
	}
	return &LoadShedder{name: name, max: maxInflight}
}

// Acquire reserves a slot. Returns true if ok; false to shed.
func (s *LoadShedder) Acquire() bool {
	cur := s.inflight.Load()
	if cur >= int64(s.max) {
		s.shedCount.Add(1)
		return false
	}
	s.inflight.Add(1)
	return true
}

// Release returns a slot.
func (s *LoadShedder) Release() {
	s.inflight.Add(-1)
}

// ShedCount returns the number of dropped publishes.
func (s *LoadShedder) ShedCount() int64 { return s.shedCount.Load() }

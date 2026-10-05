// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — priority queues (JOBS-018), per-queue rate limit (JOBS-019).
//
// Priority: drivers MAY support priority natively (DB via the
// priority column; Redis via multiple lists). For drivers that
// don't, we layer a priority shim that maintains N buckets and
// dequeues from the highest non-empty bucket with weighted fairness.
//
// Rate limit: token-bucket per queue. Dequeue acquires a token
// before claiming; tokens refill at the configured rate.

package jobs

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// PriorityQueue wraps a base Queue with priority buckets. For
// drivers that already honour Envelope.Priority (DB), this is a
// pass-through. For drivers that don't (in-proc uses priority in
// sort, but Redis FIFO does not), this shim keeps N underlying
// queues and serves higher priority first.
type PriorityQueue struct {
	buckets [10]Queue // 0..9 priority
	mu      sync.Mutex
}

// NewPriorityQueue returns a priority shim backed by 10 queues
// constructed by the supplied factory.
func NewPriorityQueue(factory func(priority int) Queue) *PriorityQueue {
	p := &PriorityQueue{}
	for i := range p.buckets {
		p.buckets[i] = factory(i)
	}
	return p
}

// Enqueue routes env to the bucket at env.Priority (clamped to 0..9).
func (p *PriorityQueue) Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error {
	idx := env.Priority
	if idx < 0 {
		idx = 0
	}
	if idx > 9 {
		idx = 9
	}
	opts.Priority = idx
	return p.buckets[idx].Enqueue(ctx, env, opts)
}

// Dequeue scans buckets from highest priority to lowest. Higher
// priority buckets are checked first; lower priority is served
// only when higher is empty (with a small jitter to avoid
// starvation: 5% of dispatches go to a random lower bucket if non-empty).
func (p *PriorityQueue) Dequeue(ctx context.Context) (*Envelope, Receipt, error) {
	// 5% jitter: serve a lower-priority bucket if non-empty
	if rand.Float64() < 0.05 {
		for i := 0; i < 9; i++ {
			env, rcpt, err := p.buckets[i].Dequeue(ctx)
			if err == nil {
				return env, rcpt, nil
			}
		}
	}
	for i := 9; i >= 0; i-- {
		env, rcpt, err := p.buckets[i].Dequeue(ctx)
		if err == nil {
			return env, rcpt, nil
		}
	}
	return nil, nil, ErrEmpty
}

// Ack dispatches to the bucket that owns the envelope. We try each
// bucket since the receipt doesn't carry priority.
func (p *PriorityQueue) Ack(ctx context.Context, r Receipt) error {
	var firstErr error
	for i := range p.buckets {
		if err := p.buckets[i].Ack(ctx, r); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Nack mirrors Ack.
func (p *PriorityQueue) Nack(ctx context.Context, r Receipt, requeue bool, nextVisibleAt time.Time, lastErr string) error {
	var firstErr error
	for i := range p.buckets {
		if err := p.buckets[i].Nack(ctx, r, requeue, nextVisibleAt, lastErr); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Depth sums all buckets.
func (p *PriorityQueue) Depth(ctx context.Context) (int64, error) {
	var sum int64
	for i := range p.buckets {
		n, err := p.buckets[i].Depth(ctx)
		if err != nil {
			return 0, err
		}
		sum += n
	}
	return sum, nil
}

// Peek aggregates the highest N visible envelopes across buckets.
func (p *PriorityQueue) Peek(ctx context.Context, n int) ([]*Envelope, error) {
	out := make([]*Envelope, 0, n)
	for i := 9; i >= 0; i-- {
		if n > 0 && len(out) >= n {
			break
		}
		got, err := p.buckets[i].Peek(ctx, n-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// Close closes all buckets.
func (p *PriorityQueue) Close() error {
	var firstErr error
	for i := range p.buckets {
		if err := p.buckets[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RateLimiter is a token-bucket per-queue rate limit (JOBS-019).
// The worker calls Wait() before Dequeue; if the bucket is empty,
// Wait blocks for the next refill tick.
type RateLimiter struct {
	mu         sync.Mutex
	tokens     int
	max        int
	refillRate time.Duration // per token
	last       time.Time
}

// NewRateLimiter returns a token bucket with max tokens, refilling
// at one-per-refillRate.
func NewRateLimiter(max int, refillEvery time.Duration) *RateLimiter {
	if max <= 0 {
		max = 1
	}
	if refillEvery <= 0 {
		refillEvery = 10 * time.Millisecond
	}
	return &RateLimiter{tokens: max, max: max, refillRate: refillEvery, last: time.Now()}
}

// Wait blocks until a token is available or ctx is cancelled.
func (r *RateLimiter) Wait(ctx context.Context) error {
	for {
		r.mu.Lock()
		now := time.Now()
		// refill
		elapsed := now.Sub(r.last)
		refill := int(elapsed / r.refillRate)
		if refill > 0 {
			r.tokens += refill
			if r.tokens > r.max {
				r.tokens = r.max
			}
			r.last = r.last.Add(time.Duration(refill) * r.refillRate)
			if r.last.After(now) {
				r.last = now
			}
		}
		if r.tokens > 0 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}
		// compute next refill wait
		next := r.refillRate - (now.Sub(r.last))
		r.mu.Unlock()
		if next <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(next):
		}
	}
}

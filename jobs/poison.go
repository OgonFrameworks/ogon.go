// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — poison-message quarantine (JOBS-025), retry-storm guard (JOBS-054).
//
// A poison message is one whose payload cannot be decoded, or whose
// handler returns ErrPoison. Such envelopes are quarantined instead
// of being retried (because retrying a poison will always fail).
//
// The retry-storm guard throttles a queue when the failure rate
// exceeds a threshold over a moving window. When armed, the worker
// backs off dequeue for a cooldown to let downstreams recover.

package jobs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// PoisonQuarantine stores envelopes removed due to poison. Backed
// by an in-memory slice for dev; production wraps a DLQDriver.
type PoisonQuarantine struct {
	dlq DLQDriver
}

// NewPoisonQuarantine wraps a DLQDriver. nil falls back to an
// in-memory DLQ that does not enqueue on Replay (caller wires the
// queue explicitly when needed).
func NewPoisonQuarantine(dlq DLQDriver) *PoisonQuarantine {
	if dlq == nil {
		dlq = NewInMemoryDLQ(nil, DefaultRetryPolicy)
	}
	return &PoisonQuarantine{dlq: dlq}
}

// Quarantine moves env into the DLQ with reason "poison".
func (p *PoisonQuarantine) Quarantine(ctx context.Context, env *Envelope, reason string) {
	if p == nil || p.dlq == nil {
		return
	}
	SendToDLQ(ctx, p.dlq, env, reason)
}

// RetryStormGuard arms a global cooldown when the rolling failure
// rate exceeds the configured threshold. Once armed, Wait blocks
// workers from dequeue for the cooldown duration.
type RetryStormGuard struct {
	mu         sync.Mutex
	window     time.Duration
	threshold  float64
	cooldown   time.Duration
	history    []event
	armed      atomic.Bool
	armedUntil time.Time
}

type event struct {
	t  time.Time
	ok bool
}

// NewRetryStormGuard returns a guard that triggers when failure
// rate exceeds threshold over window. Defaults: window 1m,
// threshold 0.5, cooldown 30s.
func NewRetryStormGuard(window time.Duration, threshold float64, cooldown time.Duration) *RetryStormGuard {
	if window <= 0 {
		window = time.Minute
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &RetryStormGuard{window: window, threshold: threshold, cooldown: cooldown}
}

// Record adds an event to the rolling window.
func (g *RetryStormGuard) Record(ok bool) {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.history = append(g.history, event{t: now, ok: ok})
	cutoff := now.Add(-g.window)
	// trim
	for len(g.history) > 0 && g.history[0].t.Before(cutoff) {
		g.history = g.history[1:]
	}
	if g.shouldArmLocked(now) {
		g.armed.Store(true)
		g.armedUntil = now.Add(g.cooldown)
	}
}

// Wait blocks if the guard is armed; returns when it disarms.
func (g *RetryStormGuard) Wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		now := time.Now()
		if g.armed.Load() && now.Before(g.armedUntil) {
			remaining := g.armedUntil.Sub(now)
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(remaining):
			}
		} else {
			if g.armed.Load() {
				g.armed.Store(false)
			}
			g.mu.Unlock()
			return nil
		}
	}
}

func (g *RetryStormGuard) shouldArmLocked(now time.Time) bool {
	if len(g.history) < 10 {
		return false
	}
	fail := 0
	for _, e := range g.history {
		if !e.ok {
			fail++
		}
	}
	rate := float64(fail) / float64(len(g.history))
	return rate > g.threshold
}

// Armed reports whether the guard is currently armed.
func (g *RetryStormGuard) Armed() bool {
	return g.armed.Load()
}

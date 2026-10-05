// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — idempotency keys + dedup window (JOBS-013/014).
//
// An idempotency key is a caller-supplied opaque string. Within the
// dedup window, a second Enqueue with the same key returns
// ErrAlreadyExists and makes no new envelope.
//
// Drivers that already enforce this server-side (Redis SETNX, PG
// unique index) should call Enforcer.Seen before persisting.

package jobs

import (
	"sync"
	"time"
)

// Enforcer is the in-memory dedup tracker used by the in-proc driver
// and as a fallback for drivers without native support. Production
// deployments use the DB unique index or Redis SETNX.
type Enforcer struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

// NewEnforcer returns a new dedup enforcer with the supplied window.
// ttl <= 0 falls back to 5 minutes.
func NewEnforcer(ttl time.Duration) *Enforcer {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Enforcer{seen: make(map[string]time.Time), ttl: ttl}
}

// Claim attempts to mark key as in-flight. Returns true if this
// caller is the first to claim; false (and the caller should return
// ErrAlreadyExists) if the key is already in-flight or recently
// completed. Empty key always returns true (dedup disabled).
func (e *Enforcer) Claim(key string) bool {
	if key == "" {
		return true
	}
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, ok := e.seen[key]; ok && now.Sub(t) < e.ttl {
		return false
	}
	e.seen[key] = now
	return true
}

// Release removes a key from the in-flight set. Called when the
// enqueue itself failed (e.g. driver error) so the caller can retry.
func (e *Enforcer) Release(key string) {
	if key == "" {
		return
	}
	e.mu.Lock()
	delete(e.seen, key)
	e.mu.Unlock()
}

// GC removes expired entries. Call periodically from the worker pool.
func (e *Enforcer) GC(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, t := range e.seen {
		if now.Sub(t) >= e.ttl {
			delete(e.seen, k)
		}
	}
}

// Size returns the count of tracked keys (for metrics/tests).
func (e *Enforcer) Size() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.seen)
}

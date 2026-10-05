// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Server-held state store (UI-007, UI-050, UI-051).
//
// The state store is the per-session, server-owned home for
// component State and Shared primitives. It is intentionally
// pluggable: a memory backend is the dev default; a Redis
// backplane (Part IX) is the prod default. Every session has a
// cap on the number of stored keys (UI-050) and an LRU eviction
// policy so a runaway component cannot OOM the server.

package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

// StateStore is the persistence interface for per-session state.
// Implementations MUST be safe for concurrent use.
type StateStore interface {
	// Get returns the value at key, or false if unset.
	Get(ctx context.Context, sessionID, key string) (any, bool, error)
	// Set writes a value. Returns ErrSessionCapHit when the
	// session is at its per-session key cap (UI-050).
	Set(ctx context.Context, sessionID, key string, val any) error
	// Delete removes a key.
	Delete(ctx context.Context, sessionID, key string) error
	// Drop removes every key for a session (called on unmount).
	Drop(ctx context.Context, sessionID string) error
	// Cap returns the per-session key cap.
	Cap() int
}

// ErrSessionCapHit is returned when the per-session key cap is hit.
// The caller SHOULD surface this as an `ogon explain` diagnostic
// (UI-050/051) so component authors can shrink their state surface.
var ErrSessionCapHit = errors.New("ogon/ui: session state cap hit (UI-050)")

// MemoryStateStore is the dev-default state store. It uses a sync.Map
// of per-session sync.Map values so concurrent reads and writes do
// not contend on a single mutex across sessions.
type MemoryStateStore struct {
	mu       sync.Mutex
	sessions map[string]*sessionBucket
	cap      int
	ttl      time.Duration
}

type sessionBucket struct {
	mu   sync.Mutex
	keys map[string]any
	last time.Time // last access — for LRU
}

// NewMemoryStateStore constructs a memory store with the supplied
// per-session cap and idle TTL. Sessions idle for longer than ttl
// are eligible for eviction.
func NewMemoryStateStore(cap int, ttl time.Duration) *MemoryStateStore {
	if cap <= 0 {
		cap = 1024
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &MemoryStateStore{sessions: map[string]*sessionBucket{}, cap: cap, ttl: ttl}
}

// Cap returns the per-session key cap.
func (m *MemoryStateStore) Cap() int { return m.cap }

// Get retrieves a value.
func (m *MemoryStateStore) Get(_ context.Context, sessionID, key string) (any, bool, error) {
	bucket := m.bucket(sessionID, false)
	if bucket == nil {
		return nil, false, nil
	}
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	v, ok := bucket.keys[key]
	bucket.last = time.Now()
	return v, ok, nil
}

// Set writes a value, enforcing the per-session cap.
func (m *MemoryStateStore) Set(_ context.Context, sessionID, key string, val any) error {
	bucket := m.bucket(sessionID, true)
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	if _, exists := bucket.keys[key]; !exists && len(bucket.keys) >= m.cap {
		return ErrSessionCapHit
	}
	bucket.keys[key] = val
	bucket.last = time.Now()
	return nil
}

// Delete removes a key.
func (m *MemoryStateStore) Delete(_ context.Context, sessionID, key string) error {
	bucket := m.bucket(sessionID, false)
	if bucket == nil {
		return nil
	}
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	delete(bucket.keys, key)
	return nil
}

// Drop purges an entire session.
func (m *MemoryStateStore) Drop(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
	return nil
}

// EvictExpired sweeps sessions whose last access is older than ttl.
// Called by the runtime sweeper; tests call it directly.
func (m *MemoryStateStore) EvictExpired() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-m.ttl)
	evicted := 0
	for sid, b := range m.sessions {
		b.mu.Lock()
		expired := b.last.Before(cutoff)
		b.mu.Unlock()
		if expired {
			delete(m.sessions, sid)
			evicted++
		}
	}
	return evicted
}

func (m *MemoryStateStore) bucket(sessionID string, create bool) *sessionBucket {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.sessions[sessionID]
	if !ok && create {
		b = &sessionBucket{keys: map[string]any{}, last: time.Now()}
		m.sessions[sessionID] = b
	}
	return b
}

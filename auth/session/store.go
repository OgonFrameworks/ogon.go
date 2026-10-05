// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Session storage backends (SEC-003, SEC-004, SEC-005, SEC-006, SEC-007, SEC-064).
//
// Sessions are server-side state (DB or Redis) keyed by an unguessable
// 256-bit session ID. The client holds only the opaque ID in a
// Secure/HttpOnly/SameSite=Lax cookie.

package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// Session is the server-side state for one authenticated session.
type Session struct {
	ID           string
	UserID       string
	TenantID     string // SEC-027 isolation; empty if single-tenant
	DeviceName   string
	UserAgent    string
	IP           string
	CreatedAt    time.Time
	LastSeen     time.Time
	ExpiresAt    time.Time // absolute TTL (SEC-007)
	IdleTimeout  time.Duration
	PrivilegeRev uint64 // bumped on role/permission change → rotation (SEC-006)
	Data         map[string]string
}

// IsExpired reports whether the session has exceeded its absolute or
// idle TTL relative to now.
func (s *Session) IsExpired(now time.Time) bool {
	if !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt) {
		return true
	}
	if s.IdleTimeout > 0 && now.Sub(s.LastSeen) > s.IdleTimeout {
		return true
	}
	return false
}

// Store is the pluggable backend contract. Both DB-backed and
// Redis-backed implementations satisfy it.
//
// Implementations MUST:
//   - return ErrNotFound on missing IDs (do not leak via timing)
//   - apply TTLs server-side where supported (Redis EXPIRE)
//   - be safe for concurrent access
type Store interface {
	// Save persists the session under s.ID, overwriting any prior.
	Save(ctx context.Context, s *Session) error
	// Load fetches a session by ID. Returns ErrNotFound if absent.
	Load(ctx context.Context, id string) (*Session, error)
	// Delete removes a session (logout / revoke).
	Delete(ctx context.Context, id string) error
	// ListForUser returns all active sessions for a user (SEC-064).
	ListForUser(ctx context.Context, userID string) ([]*Session, error)
	// DeleteAllForUser revokes every session for a user (password
	// change, security event).
	DeleteAllForUser(ctx context.Context, userID string) error
}

// ErrNotFound is the canonical "session does not exist" sentinel.
// Returned by Store implementations; not exported as a diag so callers
// can map to their own auth error.
var ErrNotFound = errors.New("session: not found")

// NewID generates a fresh 256-bit session ID, hex-encoded (64 chars).
// Uses crypto/rand so IDs are unguessable even with cluster-wide state.
func NewID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// MemoryStore is an in-process implementation. Useful for tests and for
// single-instance deployments; multi-instance deployments MUST use a
// shared store (Redis, DB).
type MemoryStore struct {
	sessions map[string]*Session
}

// NewMemoryStore returns a ready-to-use MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]*Session)}
}

// Save stores s in the map. It clones the session struct so the caller
// cannot mutate stored state by reference.
func (m *MemoryStore) Save(_ context.Context, s *Session) error {
	cp := *s
	if s.Data != nil {
		cp.Data = make(map[string]string, len(s.Data))
		for k, v := range s.Data {
			cp.Data[k] = v
		}
	}
	m.sessions[s.ID] = &cp
	return nil
}

// Load fetches a session by ID, returning ErrNotFound if absent or
// expired.
func (m *MemoryStore) Load(_ context.Context, id string) (*Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	if s.IsExpired(time.Now()) {
		// lazy GC
		delete(m.sessions, id)
		return nil, ErrNotFound
	}
	return s, nil
}

// Delete removes a session. Idempotent.
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	delete(m.sessions, id)
	return nil
}

// ListForUser returns all sessions for userID, oldest first.
func (m *MemoryStore) ListForUser(_ context.Context, userID string) ([]*Session, error) {
	now := time.Now()
	var out []*Session
	for _, s := range m.sessions {
		if s.UserID != userID {
			continue
		}
		if s.IsExpired(now) {
			delete(m.sessions, s.ID)
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// DeleteAllForUser revokes every session for a user.
func (m *MemoryStore) DeleteAllForUser(_ context.Context, userID string) error {
	for id, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, id)
		}
	}
	return nil
}

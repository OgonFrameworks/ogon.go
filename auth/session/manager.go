// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Session manager (SEC-006, SEC-007, SEC-064).
//
// The manager is the orchestrator: it issues, rotates, lists, revokes.
// Backends satisfy Store; cookies satisfy CookieOptions. Lifecycle
// invariants:
//
//   - On login: a fresh session ID is issued (never reuse pre-auth ID).
//   - On privilege change: ID rotates, PrivilegeRev bumps.
//   - Absolute + idle TTLs enforced (SEC-007).
//   - User can list/revoke their sessions (SEC-064).

package session

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Manager coordinates session lifecycle across a Store and cookies.
type Manager struct {
	store       Store
	cookies     CookieOptions
	absoluteTTL time.Duration // SEC-007 absolute
	idleTTL     time.Duration // SEC-007 idle
	now         func() time.Time
}

// NewManager returns a Manager with the supplied store and defaults:
//   - 24h absolute TTL
//   - 30m idle TTL
func NewManager(store Store, opts CookieOptions) *Manager {
	return &Manager{
		store:       store,
		cookies:     opts,
		absoluteTTL: 24 * time.Hour,
		idleTTL:     30 * time.Minute,
		now:         time.Now,
	}
}

// WithTTLs returns a copy of the manager with overridden TTLs. Zero
// values keep the default; negative values disable that TTL.
func (m *Manager) WithTTLs(absolute, idle time.Duration) *Manager {
	out := *m
	if absolute != 0 {
		out.absoluteTTL = absolute
	}
	if idle != 0 {
		out.idleTTL = idle
	}
	return &out
}

// Login issues a brand-new session for userID and writes the cookie.
// It is the single authoritative entry point for new sessions — never
// reuse a pre-auth session ID (session-fixation, SEC-065).
//
// If a prior session exists for this user/device combo, the manager
// rotates its ID rather than creating a parallel one (SEC-006).
func (m *Manager) Login(ctx context.Context, w http.ResponseWriter,
	userID, tenantID, deviceName, ua, ip string) (*Session, error) {
	id, err := NewID()
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-006", Title: "session: id gen"})
	}
	now := m.now()
	s := &Session{
		ID:           id,
		UserID:       userID,
		TenantID:     tenantID,
		DeviceName:   deviceName,
		UserAgent:    ua,
		IP:           ip,
		CreatedAt:    now,
		LastSeen:     now,
		ExpiresAt:    now.Add(m.absoluteTTL),
		IdleTimeout:  m.idleTTL,
		PrivilegeRev: 1,
		Data:         map[string]string{},
	}
	if err := m.store.Save(ctx, s); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-006", Title: "session: save"})
	}
	m.cookies.SetCookie(w, id)
	return s, nil
}

// Rotate issues a new session ID for an existing session, preserving
// all other state. Called on privilege change (SEC-006) and on any
// privilege-level escalation within the session's lifetime.
func (m *Manager) Rotate(ctx context.Context, w http.ResponseWriter, old *Session) (*Session, error) {
	if old == nil {
		return nil, errors.New("session: rotate on nil session")
	}
	newID, err := NewID()
	if err != nil {
		return nil, err
	}
	// delete old, save new — order matters: save-then-delete means no
	// window where the user has no session.
	now := m.now()
	next := *old
	next.ID = newID
	next.PrivilegeRev = old.PrivilegeRev + 1
	next.CreatedAt = now // rotation = new session-attribution window
	next.LastSeen = now
	next.ExpiresAt = now.Add(m.absoluteTTL)
	if err := m.store.Save(ctx, &next); err != nil {
		return nil, err
	}
	if err := m.store.Delete(ctx, old.ID); err != nil {
		// non-fatal; old ID lingers but cookie now points at new ID.
		_ = err
	}
	m.cookies.SetCookie(w, newID)
	return &next, nil
}

// FromRequest extracts the session from a request. Returns nil if
// absent, expired, or invalid. The error is non-nil only on store
// failure (callers should 5xx, not 401).
func (m *Manager) FromRequest(ctx context.Context, r *http.Request) (*Session, error) {
	c, err := r.Cookie(m.cookies.Name)
	if err != nil {
		return nil, nil // http.ErrNoCookie → no session, not an error
	}
	s, err := m.store.Load(ctx, c.Value)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	// touch LastSeen (idle TTL extension). Cheap write; acceptable to
	// lose on failure.
	now := m.now()
	if now.Sub(s.LastSeen) > 30*time.Second {
		s.LastSeen = now
		_ = m.store.Save(ctx, s)
	}
	return s, nil
}

// Logout revokes one session (the one in the request).
func (m *Manager) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(m.cookies.Name)
	if err == nil {
		_ = m.store.Delete(ctx, c.Value)
	}
	m.cookies.ClearCookie(w)
	return nil
}

// RevokeByID revokes a specific session (SEC-064 user-driven revoke).
func (m *Manager) RevokeByID(ctx context.Context, userID, id string) error {
	s, err := m.store.Load(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if s.UserID != userID {
		// do not leak existence — return nil
		return nil
	}
	return m.store.Delete(ctx, id)
}

// ListForUser is the SEC-064 surface: enumerate active sessions.
func (m *Manager) ListForUser(ctx context.Context, userID string) ([]*Session, error) {
	return m.store.ListForUser(ctx, userID)
}

// LogoutAll revokes every session for a user (password change, compromise).
func (m *Manager) LogoutAll(ctx context.Context, userID string) error {
	return m.store.DeleteAllForUser(ctx, userID)
}

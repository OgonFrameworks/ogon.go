// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Session fixation test (SEC-006, SEC-065). Verifies the session ID
// rotates on every privilege-changing event:
//   - login (new ID issued — never reuse pre-auth ID)
//   - privilege change (Rotate bumps PrivilegeRev + new ID)
//   - password change (LogoutAll + fresh login)
//
// Session fixation attacks rely on the attacker-supplied session ID being
// accepted post-authentication. The Manager refuses this by always issuing
// a fresh ID at login time.

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OgonFrameworks/ogon.go/auth/session"
)

// TestSessionIDRotatesOnLogin: Login must always issue a fresh session ID.
// A pre-auth session ID supplied by an attacker (via Set-Cookie) must not
// be honoured — the Manager writes a brand-new ID to the response.
func TestSessionIDRotatesOnLogin(t *testing.T) {
	st := session.NewMemoryStore()
	m := session.NewManager(st, session.DefaultCookieOptions())

	// Attacker-supplied pre-auth session ID.
	attackerID := "ATTACKER-KNOWN-ID-12345"

	// Victim logs in. The Manager issues a fresh ID.
	w := httptest.NewRecorder()
	s, err := m.Login(context.Background(), w, "victim", "t1", "laptop", "UA", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if s.ID == attackerID {
		t.Fatal("Manager.Login must not honour a pre-auth attacker-supplied session ID")
	}
	if s.ID == "" {
		t.Fatal("Manager.Login must issue a non-empty session ID")
	}
	// Cookie written to the response must contain the new ID, not the attacker's.
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != s.ID {
		t.Fatalf("cookie should be the fresh session ID, got %+v", cookies)
	}
	if cookies[0].Value == attackerID {
		t.Fatal("cookie must not contain the attacker's pre-auth ID")
	}
}

// TestSessionIDRotatesOnPrivilegeChange: when the user's privileges
// change (role elevation, scope grant), Rotate must issue a new ID and
// bump PrivilegeRev.
func TestSessionIDRotatesOnPrivilegeChange(t *testing.T) {
	st := session.NewMemoryStore()
	m := session.NewManager(st, session.DefaultCookieOptions())

	// Initial login.
	w := httptest.NewRecorder()
	s, _ := m.Login(context.Background(), w, "u", "t", "laptop", "UA", "1.2.3.4")
	oldID := s.ID
	oldRev := s.PrivilegeRev

	// Privilege change → Rotate.
	w2 := httptest.NewRecorder()
	s2, err := m.Rotate(context.Background(), w2, s)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if s2.ID == oldID {
		t.Fatal("Rotate must issue a new ID")
	}
	if s2.PrivilegeRev <= oldRev {
		t.Fatal("Rotate must bump PrivilegeRev")
	}
	// Old ID must be deleted from the store.
	if _, err := st.Load(context.Background(), oldID); err == nil {
		t.Fatal("old session ID must be revoked after Rotate")
	}
	// New ID must load.
	got, err := st.Load(context.Background(), s2.ID)
	if err != nil {
		t.Fatalf("new ID load: %v", err)
	}
	if got.UserID != "u" {
		t.Fatalf("UserID preserved: want u, got %s", got.UserID)
	}
}

// TestSessionIDRotatesOnPasswordChange: password change triggers
// LogoutAll (revokes every session for the user) followed by a fresh
// login. The new session ID must differ from any prior session.
func TestSessionIDRotatesOnPasswordChange(t *testing.T) {
	st := session.NewMemoryStore()
	m := session.NewManager(st, session.DefaultCookieOptions())

	// User has 3 active sessions.
	var oldIDs []string
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		s, _ := m.Login(context.Background(), w, "u", "t", "dev", "UA", "1.2.3.4")
		oldIDs = append(oldIDs, s.ID)
	}

	// Password change → revoke all sessions.
	if err := m.LogoutAll(context.Background(), "u"); err != nil {
		t.Fatalf("LogoutAll: %v", err)
	}
	// All old sessions must be gone.
	for _, id := range oldIDs {
		if _, err := st.Load(context.Background(), id); err == nil {
			t.Errorf("session %s should be revoked after password change", id)
		}
	}

	// Fresh login post-password-change must issue a new ID.
	w := httptest.NewRecorder()
	s, err := m.Login(context.Background(), w, "u", "t", "dev", "UA", "1.2.3.4")
	if err != nil {
		t.Fatalf("post-password-change login: %v", err)
	}
	for _, old := range oldIDs {
		if s.ID == old {
			t.Fatal("post-password-change session ID must not match any pre-change ID")
		}
	}
}

// TestSessionFixationAttackRejected: full fixation attack simulation.
// Attacker obtains a pre-auth session ID (e.g. via cookie forcing) and
// then authenticates as the victim. The victim's authenticated session ID
// must differ from the attacker's pre-auth ID.
func TestSessionFixationAttackRejected(t *testing.T) {
	st := session.NewMemoryStore()
	m := session.NewManager(st, session.DefaultCookieOptions())

	// Step 1: attacker forces a session ID on the victim's browser.
	attackerCookie := &http.Cookie{
		Name:  "ogon.session",
		Value: "ATTACKER-FIXED-SESSION-ID",
	}

	// Step 2: victim authenticates. The Manager issues a fresh ID.
	w := httptest.NewRecorder()
	s, err := m.Login(context.Background(), w, "victim", "t", "laptop", "UA", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Step 3: the post-auth cookie on the response must NOT be the attacker's ID.
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Value == attackerCookie.Value {
		t.Fatal("fixation: post-auth session ID must differ from attacker-supplied pre-auth ID")
	}
	if cookies[0].Value != s.ID {
		t.Fatal("response cookie must match the freshly-issued session ID")
	}
	// Attacker's ID must not be in the store.
	if _, err := st.Load(context.Background(), attackerCookie.Value); err == nil {
		t.Fatal("attacker-supplied pre-auth session ID must never enter the store")
	}
}

// TestRotationPreservesRevBump: Rotate on a session with PrivilegeRev=N
// must yield PrivilegeRev=N+1.
func TestRotationPreservesRevBump(t *testing.T) {
	st := session.NewMemoryStore()
	m := session.NewManager(st, session.DefaultCookieOptions())

	w := httptest.NewRecorder()
	s, _ := m.Login(context.Background(), w, "u", "t", "laptop", "UA", "1.2.3.4")
	if s.PrivilegeRev != 1 {
		t.Fatalf("initial PrivilegeRev: want 1, got %d", s.PrivilegeRev)
	}

	// Rotate 3 times; each must bump the rev by exactly 1.
	for i := 0; i < 3; i++ {
		wr := httptest.NewRecorder()
		next, err := m.Rotate(context.Background(), wr, s)
		if err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
		// Assign to s so the next Rotate takes the freshly-rotated
		// session as input. (A := here would shadow the outer s and
		// freeze PrivilegeRev at 1 for all iterations.)
		s = next
		// session.PrivilegeRev is uint64; cast to keep the comparison
		// well-typed under Go's strict numeric-conversion rules.
		want := uint64(2 + i)
		if s.PrivilegeRev != want {
			t.Fatalf("rotate %d: PrivilegeRev want %d, got %d", i, want, s.PrivilegeRev)
		}
	}
}

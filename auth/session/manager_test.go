// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagerLoginRotateLogout(t *testing.T) {
	st := NewMemoryStore()
	m := NewManager(st, DefaultCookieOptions())

	w := httptest.NewRecorder()
	s, err := m.Login(context.Background(), w, "u1", "t1", "laptop", "UA", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if s.ID == "" || s.UserID != "u1" {
		t.Fatalf("bad session: %+v", s)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "ogon.session" {
		t.Fatalf("cookie not set: %v", cookies)
	}
	if !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("cookie flags wrong: %+v", cookies[0])
	}

	// rotate
	w2 := httptest.NewRecorder()
	next, err := m.Rotate(context.Background(), w2, s)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if next.ID == s.ID {
		t.Fatal("rotation did not change ID")
	}
	if next.PrivilegeRev <= s.PrivilegeRev {
		t.Fatal("rotation should bump PrivilegeRev")
	}

	// old session should be gone
	if _, err := st.Load(context.Background(), s.ID); err == nil {
		t.Fatal("old session should be deleted after rotation")
	}

	// new session should load
	got, err := st.Load(context.Background(), next.ID)
	if err != nil {
		t.Fatalf("load new: %v", err)
	}
	if got.UserID != "u1" {
		t.Fatalf("wrong user: %s", got.UserID)
	}

	// logout
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "ogon.session", Value: next.ID})
	w3 := httptest.NewRecorder()
	_ = m.Logout(context.Background(), w3, r)
	if _, err := st.Load(context.Background(), next.ID); err == nil {
		t.Fatal("session should be deleted on logout")
	}
}

func TestSessionIsExpired(t *testing.T) {
	now := time.Now()
	s := &Session{CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(time.Hour), IdleTimeout: time.Minute}
	if s.IsExpired(now) {
		t.Fatal("fresh session should not be expired")
	}
	if !s.IsExpired(now.Add(2 * time.Hour)) {
		t.Fatal("past absolute expiry should be expired")
	}
	if !s.IsExpired(now.Add(2 * time.Minute)) {
		t.Fatal("past idle expiry should be expired")
	}
}

func TestRevokeByIDDoesntLeak(t *testing.T) {
	st := NewMemoryStore()
	m := NewManager(st, DefaultCookieOptions())
	w := httptest.NewRecorder()
	s, _ := m.Login(context.Background(), w, "alice", "t1", "phone", "UA", "1.1.1.1")

	// attacker tries to revoke alice's session while posing as bob
	if err := m.RevokeByID(context.Background(), "bob", s.ID); err != nil {
		t.Fatal(err)
	}
	// session should still exist (revoke did nothing for wrong user)
	if _, err := st.Load(context.Background(), s.ID); err != nil {
		t.Fatal("session should still be alive (revoke by wrong user must not delete)")
	}
	// alice revokes her own
	if err := m.RevokeByID(context.Background(), "alice", s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(context.Background(), s.ID); err == nil {
		t.Fatal("session should be deleted")
	}
}

func TestListForUser(t *testing.T) {
	st := NewMemoryStore()
	m := NewManager(st, DefaultCookieOptions())
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		_, _ = m.Login(context.Background(), w, "u", "t", "d", "ua", "ip")
	}
	list, err := m.ListForUser(context.Background(), "u")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(list))
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Edge cases for the CSRF middleware (P14 bug-bounty / SEC-008).
// Each test exercises one adversarial input shape: missing token,
// malformed token, expired token, double-submit mismatch, etc.
// The contract: no panic on any input; rejection is via a CSRF
// diagnostic, never a 500.

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestEdgeCSRFMissingToken — POST with no cookie and no header must
// be rejected cleanly, never panic.
func TestEdgeCSRFMissingToken(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify(missing) panicked: %v", r)
		}
	}()
	if err := c.Verify(r, "session-edge"); err == nil {
		t.Fatal("Verify(missing) returned nil; want CSRF diag")
	}
}

// TestEdgeCSRFMissingCookie — POST with header but no cookie (the
// header half of double-submit). Must reject, not panic.
func TestEdgeCSRFMissingCookie(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	tok, _ := c.Issue(w, "session-edge-mc")

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("X-CSRF-Token", tok) // header but no cookie
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify(missing cookie) panicked: %v", r)
		}
	}()
	if err := c.Verify(r, "session-edge-mc"); err == nil {
		t.Fatal("Verify(missing cookie) returned nil; want CSRF diag")
	}
}

// TestEdgeCSRFMalformedCookie — POST with a malformed cookie value
// (not the hex token format the issuer produced). Must reject, not panic.
func TestEdgeCSRFMalformedCookie(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	tok, _ := c.Issue(w, "session-edge-mc2")

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	// Cookie present but value is garbage.
	r.AddCookie(&http.Cookie{Name: "ogon.csrf", Value: "not-a-real-token"})
	r.Header.Set("X-CSRF-Token", tok)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify(malformed cookie) panicked: %v", r)
		}
	}()
	if err := c.Verify(r, "session-edge-mc2"); err == nil {
		t.Fatal("Verify(malformed cookie) returned nil; want CSRF diag")
	}
}

// TestEdgeCSRFExpiredToken — POST with a cookie that was issued
// beyond the TTL window. Must reject, not panic.
func TestEdgeCSRFExpiredToken(t *testing.T) {
	cfg := DefaultCSRFConfig()
	cfg.TTL = 1 * time.Millisecond // tiny TTL for the test
	c := NewCSRF(cfg)
	w := httptest.NewRecorder()
	tok, _ := c.Issue(w, "session-edge-exp")

	// sleep past TTL
	time.Sleep(10 * time.Millisecond)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set on Issue")
	}
	r.AddCookie(cookies[0])
	r.Header.Set("X-CSRF-Token", tok)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify(expired) panicked: %v", r)
		}
	}()
	if err := c.Verify(r, "session-edge-exp"); err == nil {
		t.Fatal("Verify(expired) returned nil; want CSRF diag")
	}
}

// TestEdgeCSRFDoubleSubmitMismatch — POST with cookie and header
// both present but holding different values. Must reject, not panic.
func TestEdgeCSRFDoubleSubmitMismatch(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	tok, _ := c.Issue(w, "session-edge-dsm")

	// Two different real tokens (different sessions).
	w2 := httptest.NewRecorder()
	tok2, _ := c.Issue(w2, "session-edge-dsm2")

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	// cookie = tok's cookie, header = tok2 — they don't match.
	r.AddCookie(w.Result().Cookies()[0])
	r.Header.Set("X-CSRF-Token", tok2)
	_ = tok // tok is intentionally unused beyond Issue's side-effect

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify(double-submit mismatch) panicked: %v", r)
		}
	}()
	if err := c.Verify(r, "session-edge-dsm"); err == nil {
		t.Fatal("Verify(mismatch) returned nil; want CSRF diag")
	}
}

// TestEdgeCSRFSafeMethodBypass — GET/HEAD/OPTIONS must bypass
// the CSRF check (per spec — safe methods don't need CSRF protection).
func TestEdgeCSRFSafeMethodBypass(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	safeMethods := []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
	}
	for _, method := range safeMethods {
		r := httptest.NewRequest(method, "/", nil)
		// No cookie, no header — should still pass for safe methods.
		if err := c.Verify(r, "session-edge-safe"); err != nil {
			t.Fatalf("Verify(%s) without token returned err: %v", method, err)
		}
	}
}

// TestEdgeCSRFNoSession — POST with no session id must reject, not panic.
func TestEdgeCSRFNoSession(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify(no-session) panicked: %v", r)
		}
	}()
	if err := c.Verify(r, ""); err == nil {
		t.Fatal("Verify(no-session) returned nil; want CSRF diag")
	}
}

// TestEdgeCSRFMiddlewareReject — The Middleware wrapper must write
// 403 on a bad token, never 500.
func TestEdgeCSRFMiddlewareReject(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	mw := c.Middleware(func(r *http.Request) string { return "session-edge-mw" },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Middleware(bad) status = %d; want 403", rec.Code)
	}
	if rec.Code >= 500 {
		t.Fatalf("Middleware(bad) status = %d; must not be 5xx", rec.Code)
	}
}

// TestEdgeCSRFClearCookie — ClearCookie must not panic on a fresh
// ResponseWriter, must set a Max-Age=-1 cookie to invalidate.
func TestEdgeCSRFClearCookie(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ClearCookie panicked: %v", r)
		}
	}()
	w := httptest.NewRecorder()
	c.ClearCookie(w)
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("ClearCookie set no cookie")
	}
	if cookies[0].MaxAge != -1 {
		t.Fatalf("ClearCookie MaxAge = %d; want -1", cookies[0].MaxAge)
	}
}

// TestEdgeCSRFCookieFlagDefaults — Default CSRF cookie must be
// Secure + HttpOnly=false (the latter is intentional: the client
// needs to read the cookie to echo it in a header for double-submit).
func TestEdgeCSRFCookieFlagDefaults(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	if _, err := c.Issue(w, "session-edge-flags"); err != nil {
		t.Fatalf("Issue err: %v", err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	ck := cookies[0]
	if !ck.Secure {
		t.Error("Secure flag not set")
	}
	if ck.HttpOnly {
		t.Error("HttpOnly=true blocks client-side read (double-submit needs JS access)")
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v; want Lax", ck.SameSite)
	}
}

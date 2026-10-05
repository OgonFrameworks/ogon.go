// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCSRFVerifyMatches(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	tok, err := c.Issue(w, "session-1")
	if err != nil {
		t.Fatal(err)
	}

	// cookie was set
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "ogon.csrf" {
		t.Fatalf("cookie not set: %v", cookies)
	}
	if !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags wrong: %+v", cookies[0])
	}

	// a POST with matching cookie + header passes
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(cookies[0])
	r.Header.Set("X-CSRF-Token", tok)
	if err := c.Verify(r, "session-1"); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestCSRFRejectsMismatch(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	_, _ = c.Issue(w, "session-1")

	// POST with cookie but wrong header
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(w.Result().Cookies()[0])
	r.Header.Set("X-CSRF-Token", "WRONG")
	if err := c.Verify(r, "session-1"); err == nil {
		t.Fatal("should reject mismatched token")
	}

	// POST with no cookie → reject
	r2 := httptest.NewRequest(http.MethodPost, "/", nil)
	r2.Header.Set("X-CSRF-Token", "anything")
	if err := c.Verify(r2, "session-1"); err == nil {
		t.Fatal("should reject missing cookie")
	}
}

func TestCSRFGETAllowedWithoutToken(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := c.Verify(r, "session-1"); err != nil {
		t.Fatalf("GET should bypass CSRF: %v", err)
	}
}

func TestCSRFMiddleware(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	tok, _ := c.Issue(w, "s1")

	mw := c.Middleware(func(*http.Request) string { return "s1" },
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	// bad token → 403
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(w.Result().Cookies()[0])
	r.Header.Set("X-CSRF-Token", "WRONG")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w2.Code)
	}

	// good token → 200
	r2 := httptest.NewRequest(http.MethodPost, "/", nil)
	r2.AddCookie(w.Result().Cookies()[0])
	r2.Header.Set("X-CSRF-Token", tok)
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, r2)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w3.Code)
	}
}

// ---- Full CSRF suite (P15 — SEC-008) ----

// TestCSRFDoubleSubmitCookieAndHeaderEqual: the double-submit cookie
// pattern requires the cookie value AND the request header to BOTH equal
// the stored token. A request that has the right cookie but a wrong
// header must be rejected; vice versa.
func TestCSRFDoubleSubmitCookieAndHeaderEqual(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	tok, _ := c.Issue(w, "sess-double")
	cookie := w.Result().Cookies()[0]

	cases := []struct {
		name        string
		cookieValue string
		hdrValue    string
		wantOK      bool
	}{
		{"both match", tok, tok, true},
		{"cookie match only", tok, "WRONG", false},
		{"header match only", "WRONG", tok, false},
		{"both wrong", "WRONG", "WRONG", false},
		{"both empty", "", "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.cookieValue != "" {
				r.AddCookie(&http.Cookie{Name: "ogon.csrf", Value: tc.cookieValue})
			} else {
				// missing cookie → should fail
			}
			if tc.hdrValue != "" {
				r.Header.Set("X-CSRF-Token", tc.hdrValue)
			}
			// explicitly set the cookie if we mutated it
			_ = cookie
			err := c.Verify(r, "sess-double")
			if tc.wantOK && err != nil {
				t.Fatalf("expected OK, got %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("expected reject, got OK")
			}
		})
	}
}

// TestCSRFSameSiteLaxDefault: the default config sets SameSite=Lax. Lax
// blocks cross-site state-changing requests (POST/PUT/DELETE) while
// allowing top-level GET navigations. The cookie emitted by Issue must
// carry SameSite=Lax.
func TestCSRFSameSiteLaxDefault(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	_, _ = c.Issue(w, "sess-lax")
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(cookies))
	}
	if cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite: want Lax, got %v", cookies[0].SameSite)
	}
	if !cookies[0].Secure {
		t.Fatal("CSRF cookie must be Secure")
	}
	if cookies[0].HttpOnly {
		t.Fatal("CSRF cookie must NOT be HttpOnly — the client reads it for double-submit")
	}
}

// TestCSRFTokenRotationOnRotate: Rotate must delete the old token and
// issue a new one. The new token must be different from the old; the old
// must no longer verify.
func TestCSRFTokenRotationOnRotate(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	tok1, _ := c.Issue(w, "sess-rotate")

	w2 := httptest.NewRecorder()
	tok2, err := c.Rotate(w2, "sess-rotate")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if tok1 == tok2 {
		t.Fatal("rotation must change the token")
	}
	// old token should no longer verify
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(w.Result().Cookies()[0])
	r.Header.Set("X-CSRF-Token", tok1)
	if err := c.Verify(r, "sess-rotate"); err == nil {
		t.Fatal("old token should be rejected after rotation")
	}
	// new token should verify
	r2 := httptest.NewRequest(http.MethodPost, "/", nil)
	r2.AddCookie(w2.Result().Cookies()[0])
	r2.Header.Set("X-CSRF-Token", tok2)
	if err := c.Verify(r2, "sess-rotate"); err != nil {
		t.Fatalf("new token should verify: %v", err)
	}
}

// TestCSRFFixationRejected: an attacker who plants a CSRF cookie on a
// victim's browser cannot make the victim's POST verify against the
// attacker-known token. The server-side stored map is keyed by sessionID,
// so a planted cookie whose value does not match the server-stored token
// for that session is rejected.
func TestCSRFFixationRejected(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	// Server issues a token for the victim's session.
	w := httptest.NewRecorder()
	_, _ = c.Issue(w, "sess-victim")
	// Attacker plants a DIFFERENT cookie value on the victim's browser.
	// The request echoes the attacker's value in both cookie AND header
	// (the double-submit pattern). It must still fail because the
	// server-stored token for sess-victim is different.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: "ogon.csrf", Value: "ATTACKER_VALUE"})
	r.Header.Set("X-CSRF-Token", "ATTACKER_VALUE")
	if err := c.Verify(r, "sess-victim"); err == nil {
		t.Fatal("fixation attack must fail: cookie+header matching the attacker value should not verify against the server-stored token")
	}
}

// TestCSRFGETExempt: GET/HEAD/OPTIONS are safe methods and must bypass
// CSRF verification.
func TestCSRFGETExempt(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		r := httptest.NewRequest(m, "/", nil)
		if err := c.Verify(r, "any-session"); err != nil {
			t.Errorf("%s should bypass CSRF, got %v", m, err)
		}
	}
}

// TestCSRFSafeMethodExempt: explicit safe-method list (TRACE is NOT
// safe per RFC 7231).
func TestCSRFSafeMethodExempt(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	// POST must not bypass.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := c.Verify(r, "s1"); err == nil {
		t.Fatal("POST must not bypass CSRF")
	}
}

// TestCSRFCookieFlags: the cookie emitted by Issue must have Secure=true
// and SameSite=Lax. HttpOnly is false (client must read it for double-
// submit) but Path is "/" and MaxAge is the configured TTL.
func TestCSRFCookieFlags(t *testing.T) {
	c := NewCSRF(DefaultCSRFConfig())
	w := httptest.NewRecorder()
	_, _ = c.Issue(w, "sess-flags")
	ck := w.Result().Cookies()[0]
	if !ck.Secure {
		t.Error("Secure flag missing")
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite: want Lax, got %v", ck.SameSite)
	}
	if ck.HttpOnly {
		t.Error("HttpOnly should be false on CSRF cookie")
	}
	if ck.Path != "/" {
		t.Errorf("Path: want /, got %q", ck.Path)
	}
	// 24h TTL → 86400 seconds
	if ck.MaxAge != 86400 {
		t.Errorf("MaxAge: want 86400, got %d", ck.MaxAge)
	}
}

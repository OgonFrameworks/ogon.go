// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// CSRF protection: double-submit + SameSite (SEC-008).
//
// Strategy:
//   - Set a random CSRF token in a HttpOnly cookie (the "session" cookie).
//   - Require the form/JSON request to echo the same token in a header
//     or body field. Mismatch → 403.
//   - SameSite=Lax already blocks most cross-site state-changing
//     requests; CSRF is defense-in-depth (SEC-008).
//
// Tokens are random 256-bit values, hex-encoded. They are tied to the
// request's authenticated session (rotation on session rotation).

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// CSRFConfig configures the CSRF middleware.
type CSRFConfig struct {
	CookieName string        // default "ogon.csrf"
	HeaderName string        // default "X-CSRF-Token"
	FormField  string        // default "ogon_csrf_token"
	Path       string        // default "/"
	SameSite   http.SameSite // default Lax
	Insecure   bool          // toggle Secure flag off (local dev only)
	TTL        time.Duration // default 24h
}

// DefaultCSRFConfig returns the production-safe default.
func DefaultCSRFConfig() CSRFConfig {
	return CSRFConfig{
		CookieName: "ogon.csrf",
		HeaderName: "X-CSRF-Token",
		FormField:  "ogon_csrf_token",
		Path:       "/",
		SameSite:   http.SameSiteLaxMode,
		Insecure:   false,
		TTL:        24 * time.Hour,
	}
}

// CSRF is the middleware.
type CSRF struct {
	cfg    CSRFConfig
	mu     sync.Mutex
	stored map[string]csrfEntry // sessionID → token (server-side state for double-submit)
}

type csrfEntry struct {
	token     string
	createdAt time.Time
}

// NewCSRF returns a CSRF middleware with the supplied config.
func NewCSRF(cfg CSRFConfig) *CSRF {
	if cfg.CookieName == "" {
		cfg = DefaultCSRFConfig()
	}
	return &CSRF{cfg: cfg, stored: map[string]csrfEntry{}}
}

// Issue generates (and persists) a CSRF token for the session. The
// token is also written to the response cookie. Called at session
// creation.
func (c *CSRF) Issue(w http.ResponseWriter, sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("csrf: empty session id")
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-008", Title: "csrf: rand"})
	}
	tok := hex.EncodeToString(b[:])
	c.mu.Lock()
	c.stored[sessionID] = csrfEntry{token: tok, createdAt: time.Now()}
	c.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     c.cfg.CookieName,
		Value:    tok,
		Path:     c.cfg.Path,
		Secure:   !c.cfg.Insecure,
		HttpOnly: false, // client must read this for double-submit
		SameSite: c.cfg.SameSite,
		MaxAge:   int(c.cfg.TTL.Seconds()),
	})
	return tok, nil
}

// Rotate deletes the prior token and issues a new one. Called on
// session rotation (SEC-006).
func (c *CSRF) Rotate(w http.ResponseWriter, sessionID string) (string, error) {
	c.mu.Lock()
	delete(c.stored, sessionID)
	c.mu.Unlock()
	return c.Issue(w, sessionID)
}

// Verify checks the request for a matching token. Returns nil on match,
// else a CSRF diagnostic. Used as http middleware via Middleware().
func (c *CSRF) Verify(r *http.Request, sessionID string) error {
	// GET/HEAD/OPTIONS do not require CSRF (safe methods).
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	if sessionID == "" {
		return diag.New("OGON-SEC-008", "csrf: no session", "")
	}
	c.mu.Lock()
	e, ok := c.stored[sessionID]
	c.mu.Unlock()
	if !ok || time.Since(e.createdAt) > c.cfg.TTL {
		return diag.New("OGON-SEC-008", "csrf: stale or missing", "")
	}
	// cookie + header must both be present and equal (double-submit)
	ck, err := r.Cookie(c.cfg.CookieName)
	if err != nil || ck.Value == "" {
		return diag.New("OGON-SEC-008", "csrf: missing cookie", "")
	}
	hdr := r.Header.Get(c.cfg.HeaderName)
	if hdr == "" {
		// also accept form field for plain HTML forms
		hdr = r.FormValue(c.cfg.FormField)
	}
	if hdr == "" {
		return diag.New("OGON-SEC-008", "csrf: missing token", "")
	}
	// both must equal stored, and each other
	if subtle.ConstantTimeCompare([]byte(ck.Value), []byte(e.token)) != 1 {
		return diag.New("OGON-SEC-008", "csrf: cookie mismatch", "")
	}
	if subtle.ConstantTimeCompare([]byte(hdr), []byte(e.token)) != 1 {
		return diag.New("OGON-SEC-008", "csrf: token mismatch", "")
	}
	return nil
}

// Middleware wraps next with CSRF verification. The sessionID func
// reads the session ID from the request (or empty if none).
func (c *CSRF) Middleware(sessionID func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := c.Verify(r, sessionID(r)); err != nil {
			http.Error(w, "csrf check failed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClearCookie revokes the client-side token cookie (on logout).
func (c *CSRF) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.cfg.CookieName, Value: "", Path: c.cfg.Path,
		Secure: !c.cfg.Insecure, HttpOnly: false, SameSite: c.cfg.SameSite,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

// noop-context import to ensure ctx appears for future use
var _ = context.Background

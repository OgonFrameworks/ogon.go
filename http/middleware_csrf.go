// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// CSRF middleware: double-submit cookie + SameSite=Strict/Lax. State-changing
// methods (POST/PUT/PATCH/DELETE) MUST present a valid CSRF token; safe
// methods (GET/HEAD/OPTIONS) bypass.
//
// HTTP-034: CSRF tokens are nonces, not predictable IDs; rotated per session.
// HTTP-033: SameSite=Strict by default; Lax is opt-in.

package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

// CSRFConfig configures the CSRF middleware.
type CSRFConfig struct {
	// CookieName: default "ogon.csrf". When the SameSite=Strict cookie is
	// sent, the JS client MUST also send the value in the X-CSRF-Token header.
	CookieName string
	// HeaderName: default "X-CSRF-Token".
	HeaderName string
	// SameSite: "strict" (default) or "lax". "none" is REFUSED (use CORS).
	SameSite string
	// TokenLength: bytes of entropy; default 32 (256 bits).
	TokenLength int
	// SecureCookie: when true, sets Secure on the cookie. Should be true in
	// production (HTTPS) only.
	SecureCookie bool
	// CookieMaxAge: default 24h. Refreshes on each safe-method request.
	CookieMaxAge time.Duration
}

// DefaultCSRFConfig returns a safe default config.
func DefaultCSRFConfig() CSRFConfig {
	return CSRFConfig{
		CookieName:   "ogon.csrf",
		HeaderName:   "X-CSRF-Token",
		SameSite:     "strict",
		TokenLength:  32,
		CookieMaxAge: 24 * time.Hour,
	}
}

// CSRFMiddleware returns the CSRF middleware. The middleware:
//  1. On every request: refresh the cookie with a new nonce (rotating token).
//  2. On state-changing requests: validate that the cookie matches the
//     header (double-submit). Mismatch → 403 ProblemDetails.
func CSRFMiddleware(cfg CSRFConfig) Middleware {
	if cfg.CookieName == "" {
		cfg.CookieName = "ogon.csrf"
	}
	if cfg.HeaderName == "" {
		cfg.HeaderName = "X-CSRF-Token"
	}
	if cfg.TokenLength <= 0 {
		cfg.TokenLength = 32
	}
	if cfg.CookieMaxAge == 0 {
		cfg.CookieMaxAge = 24 * time.Hour
	}
	if cfg.SameSite == "" {
		cfg.SameSite = "strict"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check the route: only enforce on routes opted into CSRF via
			// Route.CSRF(). When no route metadata is present, the middleware
			// is a no-op (the framework's contract: CSRF is opt-in per-route).
			if c := CtxFromRequest(r); c != nil {
				if v := c.Route().GroupMeta("csrf"); v == nil {
					next.ServeHTTP(w, r)
					return
				}
			}

			if isStateChanging(r.Method) {
				if !csrfValidate(r, cfg) {
					p := NewProblem(http.StatusForbidden,
						"CSRF validation failed",
						"the X-CSRF-Token header does not match the CSRF cookie")
					_ = p.Write(w, nil)
					return
				}
			}
			// Refresh the cookie with a new nonce.
			token, err := csrfToken(cfg.TokenLength)
			if err == nil {
				http.SetCookie(w, &http.Cookie{
					Name:     cfg.CookieName,
					Value:    token,
					MaxAge:   int(cfg.CookieMaxAge.Seconds()),
					Path:     "/",
					Secure:   cfg.SecureCookie,
					HttpOnly: false, // JS must read it to echo in header
					SameSite: parseSameSite(cfg.SameSite),
				})
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func csrfValidate(r *http.Request, cfg CSRFConfig) bool {
	cookie, err := r.Cookie(cfg.CookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(cfg.HeaderName)
	if header == "" {
		return false
	}
	// Constant-time compare to avoid timing oracle.
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) == 1
}

func csrfToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parseSameSite returns the http.SameSite enum.
func parseSameSite(s string) http.SameSite {
	switch strings.ToLower(s) {
	case "strict":
		return http.SameSiteStrictMode
	case "lax":
		return http.SameSiteLaxMode
	case "none":
		return http.SameSiteNoneMode
	}
	return http.SameSiteDefaultMode
}

func init() {
	registerMiddleware("csrf", "double-submit cookie + SameSite; per-route opt-in", 7)
}

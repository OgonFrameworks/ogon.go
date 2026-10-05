// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Security-headers test (HTTP-026..030, SEC-044). Verifies the default
// security header set is complete:
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY
//   - Referrer-Policy: strict-origin-when-cross-origin
//   - Strict-Transport-Security (HSTS, when serving over TLS)
//   - Content-Security-Policy (per-request nonce option)
//
// Two surfaces are tested:
//   1. auth.SafeHeaders — the in-package convenience middleware.
//   2. http.DefaultSecurityConfig + http.SecurityHeadersMiddleware —
//      the full HTTP-layer middleware that powers `ogon build`'s edge
//      stack.

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

// TestSafeHeadersComplete: auth.SafeHeaders must set the complete
// baseline header set on every response.
func TestSafeHeadersComplete(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	SafeHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	h := rec.Header()
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"Content-Security-Policy": "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; upgrade-insecure-requests",
	}
	for k, v := range want {
		got := h.Get(k)
		if got != v {
			t.Errorf("header %s: want %q, got %q", k, v, got)
		}
	}
	// HSTS is opt-in via auth.SafeHeaders (1-year, includeSubDomains).
	if got := h.Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Errorf("HSTS header: want max-age=31536000; includeSubDomains, got %q", got)
	}
	// Permissions-Policy default disables camera/mic/geo.
	if got := h.Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
		t.Errorf("Permissions-Policy header wrong: got %q", got)
	}
}

// TestSecurityHeadersMiddlewareComplete: the HTTP-layer middleware with
// the default config emits all five required headers, with HSTS off by
// default (HSTS is opt-in because serving HSTS over plain HTTP pins
// users into a broken state).
func TestSecurityHeadersMiddlewareComplete(t *testing.T) {
	mw := ogonhttp.SecurityHeadersMiddleware()
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)
	h := rec.Header()
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("header %s: want %q, got %q", k, v, got)
		}
	}
	// HSTS OFF by default in the default config — serving HSTS over HTTP
	// would brick clients whose first request was plain HTTP.
	if got := h.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must be OFF by default, got %q", got)
	}
	// CSP is opt-in via SecurityHeadersConfig.CSP — confirm the default
	// does not emit a (potentially too-permissive) CSP.
	if got := h.Get("Content-Security-Policy"); got != "" {
		t.Errorf("CSP must be empty by default (operators opt in via SecurityHeadersConfig.CSP), got %q", got)
	}
	// Cross-Origin-* defaults.
	if got := h.Get("Cross-Origin-Embedder-Policy"); got != "require-corp" {
		t.Errorf("COEP default: want require-corp, got %q", got)
	}
	if got := h.Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
		t.Errorf("COOP default: want same-origin, got %q", got)
	}
}

// TestSecurityHeadersHSTSWhenEnabled: when HSTS is on, the middleware
// emits max-age=63072000 (2 years) by default, with includeSubDomains.
func TestSecurityHeadersHSTSWhenEnabled(t *testing.T) {
	cfg := ogonhttp.DefaultSecurityConfig()
	cfg.HSTS = true
	mw := ogonhttp.SecurityHeadersMiddlewareWith(cfg)
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)
	h := rec.Header()
	got := h.Get("Strict-Transport-Security")
	want := "max-age=63072000; includeSubDomains"
	if got != want {
		t.Errorf("HSTS header: want %q, got %q", want, got)
	}
}

// TestSecurityHeadersCSPNonce: with CSPNonce=true and a CSP containing
// %NONCE%, the middleware substitutes the per-request nonce.
func TestSecurityHeadersCSPNonce(t *testing.T) {
	cfg := ogonhttp.DefaultSecurityConfig()
	cfg.CSP = "default-src 'self'; script-src 'nonce-%NONCE%'"
	cfg.CSPNonce = true
	mw := ogonhttp.SecurityHeadersMiddlewareWith(cfg)
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)
	h := rec.Header()
	got := h.Get("Content-Security-Policy")
	if !contains(got, "nonce-") {
		t.Fatalf("expected CSP with nonce substitution, got: %s", got)
	}
	if contains(got, "%NONCE%") {
		t.Fatalf("nonce not substituted: %s", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

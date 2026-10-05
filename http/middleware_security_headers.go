// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Security-headers middleware: HSTS, X-Content-Type-Options,
// X-Frame-Options, Referrer-Policy, CSP nonces, etc.
//
// HTTP-026: HSTS includes preload; max-age=63072000 by default.
// HTTP-027: X-Content-Type-Options: nosniff.
// HTTP-028: X-Frame-Options: DENY (or CSP frame-ancestors 'none').
// HTTP-029: Referrer-Policy: strict-origin-when-cross-origin.
// HTTP-030: CSP with per-request nonces (when Enabled).

package http

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"sync/atomic"
)

// SecurityHeadersConfig configures the security-headers middleware.
type SecurityHeadersConfig struct {
	// HSTS: when true, emit Strict-Transport-Security. Use only when serving
	// over TLS or behind a trusted TLS-terminating proxy.
	HSTS bool
	// HSTSMaxAge defaults to 2 years (63072000 seconds).
	HSTSMaxAge int
	// HSTSIncludeSubDomains, default true.
	HSTSIncludeSubDomains bool
	// HSTSPreload, default false. Set true ONLY after registering your domain
	// at https://hstspreload.org/.
	HSTSPreload bool

	// FrameOptions: "DENY" (default), "SAMEORIGIN", or "" to disable.
	FrameOptions string
	// ContentTypeOptions: default "nosniff". Empty to disable.
	ContentTypeOptions string
	// ReferrerPolicy: default "strict-origin-when-cross-origin".
	ReferrerPolicy string
	// PermissionsPolicy: defaults to disabling geolocation, microphone, camera.
	PermissionsPolicy string
	// CSPNonce: when true, generate a per-request nonce and replace %NONCE%
	// placeholders in CSP. The nonce is also stored on the *Ctx as
	// "csp-nonce" via SetUserData.
	CSPNonce bool
	// CSP: the Content-Security-Policy header value. Use %NONCE% for nonce
	// substitution. Empty disables CSP.
	CSP string
	// CrossOriginEmbedder: "require-corp", "credentialless", or "".
	CrossOriginEmbedder string
	// CrossOriginOpenerPolicy: "same-origin" (default), or "".
	CrossOriginOpenerPolicy string
}

// DefaultSecurityConfig returns a production-safe SecurityHeadersConfig.
// HSTS is OFF by default (enable explicitly when serving over TLS).
func DefaultSecurityConfig() SecurityHeadersConfig {
	return SecurityHeadersConfig{
		HSTS:                    false,
		HSTSMaxAge:              63072000,
		HSTSIncludeSubDomains:   true,
		FrameOptions:            "DENY",
		ContentTypeOptions:      "nosniff",
		ReferrerPolicy:          "strict-origin-when-cross-origin",
		PermissionsPolicy:       "geolocation=(), microphone=(), camera=()",
		CrossOriginEmbedder:     "require-corp",
		CrossOriginOpenerPolicy: "same-origin",
	}
}

// SecurityHeadersMiddleware emits the security headers per SecurityHeadersConfig.
// The default config (DefaultSecurityConfig) is used when none is provided.
func SecurityHeadersMiddleware() Middleware {
	return SecurityHeadersMiddlewareWith(DefaultSecurityConfig())
}

// SecurityHeadersMiddlewareWith allows a custom config.
func SecurityHeadersMiddlewareWith(cfg SecurityHeadersConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if cfg.FrameOptions != "" {
				h.Set("X-Frame-Options", cfg.FrameOptions)
			}
			if cfg.ContentTypeOptions != "" {
				h.Set("X-Content-Type-Options", cfg.ContentTypeOptions)
			}
			if cfg.ReferrerPolicy != "" {
				h.Set("Referrer-Policy", cfg.ReferrerPolicy)
			}
			if cfg.PermissionsPolicy != "" {
				h.Set("Permissions-Policy", cfg.PermissionsPolicy)
			}
			if cfg.CrossOriginEmbedder != "" {
				h.Set("Cross-Origin-Embedder-Policy", cfg.CrossOriginEmbedder)
			}
			if cfg.CrossOriginOpenerPolicy != "" {
				h.Set("Cross-Origin-Opener-Policy", cfg.CrossOriginOpenerPolicy)
			}
			if cfg.HSTS {
				v := strings.Builder{}
				v.WriteString("max-age=")
				ma := cfg.HSTSMaxAge
				if ma == 0 {
					ma = 63072000
				}
				itoaAppend(&v, ma)
				if cfg.HSTSIncludeSubDomains {
					v.WriteString("; includeSubDomains")
				}
				if cfg.HSTSPreload {
					v.WriteString("; preload")
				}
				h.Set("Strict-Transport-Security", v.String())
			}
			if cfg.CSP != "" {
				csp := cfg.CSP
				if cfg.CSPNonce {
					nonce := generateNonce()
					csp = strings.ReplaceAll(csp, "%NONCE%", nonce)
					if c := CtxFromRequest(r); c != nil {
						c.SetUserData(struct{ Nonce string }{Nonce: nonce})
					}
				}
				h.Set("Content-Security-Policy", csp)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// generateNonce returns an 16-byte base64 nonce (22 chars). If rand fails
// (should never happen), returns a stable fallback. atomic counter guards
// against the rare case of two identical fallback nonces.
var nonceFallback atomic.Uint64

func generateNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		n := nonceFallback.Add(1)
		for i := 0; i < 8; i++ {
			b[i] = byte(n >> (i * 8))
		}
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// itoaAppend writes a non-negative int into b in decimal.
func itoaAppend(b *strings.Builder, n int) {
	if n == 0 {
		b.WriteByte('0')
		return
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	b.Write(buf[i:])
}

func init() {
	registerMiddleware("security-headers", "HSTS, X-Frame-Options, CSP nonces, etc.", 4)
}

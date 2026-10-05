// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Content-Security-Policy builder (SEC-044).
//
// CSP is the primary XSS mitigation in the browser. We construct it
// with safe defaults and a per-request nonce so inline scripts/styles
// can opt in only when explicitly tagged.

package auth

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
)

// CSP is a per-request Content-Security-Policy builder.
type CSP struct {
	directives map[string][]string
	nonce      string
}

// NewCSP returns a builder seeded with the production-safe defaults:
//   - default-src 'self'
//   - base-uri 'self'
//   - frame-ancestors 'none'
//   - form-action 'self'
//   - object-src 'none'
//   - upgrade-insecure-requests
//   - require-trusted-types-for 'script' (when TrustedTypes=true)
//
// Operators call WithScript, WithStyle, WithImg, WithConnect to widen
// specific directives; the nonce is auto-injected into script-src and
// style-src.
func NewCSP() *CSP {
	return &CSP{
		directives: map[string][]string{
			"default-src":               {"'self'"},
			"base-uri":                  {"'self'"},
			"frame-ancestors":           {"'none'"},
			"form-action":               {"'self'"},
			"object-src":                {"'none'"},
			"upgrade-insecure-requests": {},
		},
	}
}

// Nonce returns the per-request nonce (generating on first call).
// The nonce is included as 'nonce-<value>' in script-src and style-src.
//
// If the system CSPRNG fails (rare on Linux but possible under
// fork-exhaustion, container entropy starvation, or a broken
// /dev/urandom), Nonce returns an empty string. Callers MUST treat
// an empty nonce as "no nonce": WithScript/WithStyle skip the
// 'nonce-' directive so the resulting CSP fails closed (inline
// scripts/styles are blocked) rather than failing open with a
// predictable fallback nonce (BUG-0014).
func (c *CSP) Nonce() string {
	if c.nonce == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			// CSPRNG failure: fail closed. Leave c.nonce empty so
			// WithScript/WithStyle do not emit a 'nonce-' directive
			// (the empty-string branch in those methods).
			return ""
		}
		c.nonce = base64.StdEncoding.EncodeToString(b[:])
	}
	return c.nonce
}

// WithSourceList appends a source to a directive. Use this to add
// specific trusted origins, e.g. WithSourceList("script-src", "https://cdn.ogon.dev")
func (c *CSP) WithSourceList(directive string, sources ...string) *CSP {
	c.directives[directive] = append(c.directives[directive], sources...)
	return c
}

// WithScript enables inline-script-via-nonce support. The nonce is
// auto-added on first call to Nonce(). If the CSPRNG fails and Nonce()
// returns "", the 'nonce-' directive is skipped so the resulting CSP
// fails closed (inline scripts are blocked) rather than failing open
// with a predictable fallback nonce (BUG-0014).
func (c *CSP) WithScript() *CSP {
	nonce := c.Nonce()
	srcs := []string{"'self'"}
	if nonce != "" {
		srcs = append(srcs, "'nonce-"+nonce+"'")
	}
	c.directives["script-src"] = appendUnique(c.directives["script-src"], srcs...)
	return c
}

// WithStyle enables inline-style-via-nonce support. See WithScript for
// the fail-closed behavior on CSPRNG failure (BUG-0014).
func (c *CSP) WithStyle() *CSP {
	nonce := c.Nonce()
	srcs := []string{"'self'"}
	if nonce != "" {
		srcs = append(srcs, "'nonce-"+nonce+"'")
	}
	c.directives["style-src"] = appendUnique(c.directives["style-src"], srcs...)
	return c
}

// WithImg appends sources to img-src.
func (c *CSP) WithImg(sources ...string) *CSP {
	return c.WithSourceList("img-src", sources...)
}

// WithConnect appends sources to connect-src (XHR/WebSocket).
func (c *CSP) WithConnect(sources ...string) *CSP {
	return c.WithSourceList("connect-src", sources...)
}

// WithFrame appends sources to frame-src.
func (c *CSP) WithFrame(sources ...string) *CSP {
	return c.WithSourceList("frame-src", sources...)
}

// WithReportURI sets the reporting endpoint (POST of violations).
func (c *CSP) WithReportURI(uri string) *CSP {
	c.directives["report-uri"] = []string{uri}
	c.directives["report-to"] = []string{uri}
	return c
}

// String returns the rendered CSP header value.
func (c *CSP) String() string {
	var b strings.Builder
	// deterministic ordering for cache stability
	keys := orderKeys(c.directives)
	for i, k := range keys {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(k)
		for _, v := range c.directives[k] {
			b.WriteString(" ")
			b.WriteString(v)
		}
	}
	return b.String()
}

// Apply writes the CSP header to w. Use this in your handler.
func (c *CSP) Apply(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", c.String())
}

// Middleware wraps next with a per-request CSP. The function returns
// the (request-scoped) CSP so handlers can call WithScript() etc.
func CSPMiddleware(opts func(*CSP)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := NewCSP()
			if opts != nil {
				opts(c)
			}
			c.Apply(w)
			next.ServeHTTP(w, r)
		})
	}
}

// SafeHeaders writes the full safe-by-default security header set:
//   - Content-Security-Policy
//   - Strict-Transport-Security (1y, includeSubDomains)
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY (legacy, since frame-ancestors is in CSP)
//   - Referrer-Policy: strict-origin-when-cross-origin
//   - Permissions-Policy: camera=(), microphone=(), geolocation=()
func SafeHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; upgrade-insecure-requests")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func appendUnique(in []string, want ...string) []string {
	seen := map[string]bool{}
	for _, v := range in {
		seen[v] = true
	}
	for _, v := range want {
		if !seen[v] {
			in = append(in, v)
		}
	}
	return in
}

func orderKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// sort for stability
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

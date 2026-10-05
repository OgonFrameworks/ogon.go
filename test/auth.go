// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Auth helpers (TEST-003). The App exposes Login/As which return Recorders
// pre-wired with the auth header for the given role. The actual token
// materialisation is pluggable so the test suite does not depend on a
// concrete auth backend.

package test

import (
	"net/http"
	"sync"
)

// AuthRegistry maps a role name to an auth header value (typically a bearer
// token, but could be a cookie). Tests register their materialiser once at
// init() — the fixture stays backend-agnostic.
type AuthRegistry struct {
	mu       sync.RWMutex
	roles    map[string]string        // role -> header value
	material func(role string) string // optional token materialiser
}

var (
	authRegMu sync.RWMutex
	authReg   = &AuthRegistry{roles: map[string]string{}}
)

// RegisterAuth installs the auth header materialiser. Tests pass a function
// that returns the auth header value (e.g., "Bearer <jwt>") for a given
// role name. Tests then call App.Login("admin") to get a Recorder.
//
// Safe to call from init() in any test file.
func RegisterAuth(material func(role string) string) {
	authRegMu.Lock()
	defer authRegMu.Unlock()
	authReg.material = material
}

// RegisterRole stores a fixed header value for a role, bypassing the
// materialiser. Useful for static test fixtures (no real JWT issuance).
func RegisterRole(role, headerValue string) {
	authRegMu.Lock()
	defer authRegMu.Unlock()
	authReg.roles[role] = headerValue
}

// AuthHeaderFor resolves a role to its auth header value. Prefers the static
// RegisterRole map; falls back to the materialiser.
func AuthHeaderFor(role string) string {
	authRegMu.RLock()
	defer authRegMu.RUnlock()
	if v, ok := authReg.roles[role]; ok {
		return v
	}
	if authReg.material != nil {
		return authReg.material(role)
	}
	return ""
}

// Login returns a Recorder with the auth header set for the given role.
// The role must have been registered via RegisterRole or produced by the
// materialiser installed via RegisterAuth. If the role is unknown an empty
// header is set; tests should assert a 401/403 in that case.
func (a *App) Login(role string) *Recorder {
	a.t.Helper()
	r := NewRecorder(a.t, a)
	val := AuthHeaderFor(role)
	if val == "" {
		a.t.Logf("ogontest: Login: no auth materialised for role %q", role)
	}
	parts := splitAuthHeader(val)
	r.header.Set(parts[0], parts[1])
	return r
}

// As is an alias for Login that reads slightly better in table tests:
//
//	rec := app.As("viewer").Get("/api/users/1")
func (a *App) As(role string) *Recorder { return a.Login(role) }

// Recorder returns an unauthenticated Recorder.
func (a *App) Recorder() *Recorder {
	a.t.Helper()
	return NewRecorder(a.t, a)
}

// splitAuthHeader splits "Bearer xyz" → ["Authorization", "Bearer xyz"].
// Default header name is "Authorization". A bare token gets the same.
// If the value already contains a recognised header-name prefix (e.g.,
// "Cookie: sid=..."), the caller can override the header name.
func splitAuthHeader(v string) [2]string {
	if v == "" {
		return [2]string{"Authorization", ""}
	}
	// Cookie style
	if hasCookiePrefix(v) {
		return [2]string{"Cookie", v}
	}
	// Already in "Authorization: Bearer ..." form
	if hasBearerPrefix(v) || hasSchemePrefix(v) {
		return [2]string{"Authorization", v}
	}
	// Plain token: wrap as Bearer
	return [2]string{"Authorization", "Bearer " + v}
}

func hasCookiePrefix(v string) bool {
	return startsWithCI(v, "sid=") || startsWithCI(v, "session=") || startsWithCI(v, "ogon_sid=")
}

func hasBearerPrefix(v string) bool { return startsWithCI(v, "Bearer ") }

func hasSchemePrefix(v string) bool {
	for _, s := range []string{"Basic ", "Negotiate ", "Token "} {
		if startsWithCI(v, s) {
			return true
		}
	}
	return false
}

func startsWithCI(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c, p := s[i], prefix[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if 'A' <= p && p <= 'Z' {
			p += 'a' - 'A'
		}
		if c != p {
			return false
		}
	}
	return true
}

// SetAuthHeader is the low-level hook used by Login; exported so tests with
// unusual auth schemes (e.g., mTLS, custom headers) can install directly.
func SetAuthHeader(h http.Header, role string) {
	val := AuthHeaderFor(role)
	parts := splitAuthHeader(val)
	h.Set(parts[0], parts[1])
}

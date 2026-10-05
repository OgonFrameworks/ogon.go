// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// CORS middleware. STRICT mode: refuses `Access-Control-Allow-Origin: *`
// when credentials are involved (HTTP-040). Origin allowlist is mandatory
// — wildcards are rejected at config time.

package http

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CORSConfig configures strict CORS.
type CORSConfig struct {
	// AllowOrigins is the explicit allowlist. "*" is permitted ONLY when
	// AllowCredentials is false. Empty list disables CORS entirely.
	AllowOrigins []string
	// AllowCredentials: cookies/Authorization. When true, ALL of:
	//   - AllowOrigins MUST NOT contain "*"
	//   - AllowHeaders MUST be explicit (no "*")
	//   - AllowMethods MUST be explicit (no "*")
	AllowCredentials bool
	// AllowMethods: defaults to GET/POST/PUT/PATCH/DELETE/OPTIONS/HEAD.
	AllowMethods []string
	// AllowHeaders: defaults to Content-Type, Authorization, X-Request-Id,
	// X-Idempotency-Key, Api-Version.
	AllowHeaders []string
	// ExposeHeaders: response headers exposed to JS clients.
	ExposeHeaders []string
	// MaxAge: preflight cache duration, default 600s.
	MaxAge int
}

// DefaultCORSConfig returns a safe default: credentials allowed, explicit
// origins empty (MUST be supplied by the app).
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins:     nil,
		AllowCredentials: true,
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodHead},
		AllowHeaders:     []string{"Content-Type", "Authorization", RequestIDHeader, IdempotencyHeader, APIVersionHeader},
		ExposeHeaders:    []string{RequestIDHeader},
		MaxAge:           600,
	}
}

// Validate returns an error if the config violates strict-CORS invariants
// (HTTP-040). NewCORS calls this at registration time so the framework
// refuses to boot on misconfiguration.
func (c CORSConfig) Validate() error {
	hasWild := false
	hasExplicit := false
	for _, o := range c.AllowOrigins {
		if o == "*" {
			hasWild = true
			if c.AllowCredentials {
				return errCORSWildcardWithCreds
			}
		} else {
			hasExplicit = true
		}
	}
	// BUG-0017: mixing "*" with explicit origins is almost always a
	// misconfiguration — the "*" wins and silently opens CORS to
	// everyone, defeating the explicit allowlist. Reject at config
	// time so the operator sees the mistake.
	if hasWild && hasExplicit {
		return errCORSWildcardMixed
	}
	for _, h := range c.AllowHeaders {
		if h == "*" && c.AllowCredentials {
			return errCORSWildcardWithCreds
		}
	}
	for _, m := range c.AllowMethods {
		if m == "*" && c.AllowCredentials {
			return errCORSWildcardWithCreds
		}
	}
	return nil
}

var errCORSWildcardWithCreds = NewProblem(http.StatusInternalServerError,
	"CORS misconfiguration",
	"Access-Control-Allow-Origin cannot be '*' when AllowCredentials is true")

var errCORSWildcardMixed = NewProblem(http.StatusInternalServerError,
	"CORS misconfiguration",
	"AllowOrigins cannot mix '*' with explicit origins (the '*' would silently win)")

// CORSMiddleware returns the strict CORS middleware. Calling it with an
// invalid config panics at boot (HTTP-040).
func CORSMiddleware(cfg CORSConfig) Middleware {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	// Pre-render header strings.
	allowMethods := strings.Join(cfg.AllowMethods, ", ")
	allowHeaders := strings.Join(cfg.AllowHeaders, ", ")
	exposeHeaders := strings.Join(cfg.ExposeHeaders, ", ")
	maxAge := strconv.Itoa(cfg.MaxAge)
	allowCreds := "true"
	if !cfg.AllowCredentials {
		allowCreds = ""
	}

	originSet := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		originSet[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := ""
			if origin != "" {
				if _, ok := originSet[origin]; ok {
					allowed = origin
				} else if _, ok := originSet["*"]; ok && !cfg.AllowCredentials {
					allowed = "*"
				}
			}
			if allowed != "" {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", allowed)
				// BUG-0016: whenever the ACAO header is set, the
				// response varies by request Origin. Always emit
				// Vary: Origin so CDNs do not cache one client's
				// ACAO value and return it to another client.
				h.Add("Vary", "Origin")
				if cfg.AllowCredentials {
					h.Set("Access-Control-Allow-Credentials", allowCreds)
				}
				h.Set("Access-Control-Expose-Headers", exposeHeaders)
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				// Preflight.
				if allowed == "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				h := w.Header()
				h.Set("Access-Control-Allow-Methods", allowMethods)
				h.Set("Access-Control-Allow-Headers", allowHeaders)
				h.Set("Access-Control-Max-Age", maxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isAbsoluteURL is a small helper for origin validation.
func isAbsoluteURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.IsAbs()
}

func init() {
	registerMiddleware("cors", "strict CORS; refuses *+creds", 6)
}

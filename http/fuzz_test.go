// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fuzz the router's match path (P14 bug-bounty). The router must
// never panic on adversarial (method, path) pairs — missing leading
// slash, embedded NULs, very long paths, unicode, path traversal
// segments, etc. A valid input may return MatchNone, but never a
// panic. A valid registered route must never produce a 500-class
// outcome from Match alone (the handler body is out of scope here).

package http

import (
	"net/http"
	"strings"
	"testing"
)

// FuzzRouterPaths drives Router.Match with adversarial (method, path) inputs.
// The router is pre-populated with a representative set of routes; the
// fuzz target ensures Match never panics, returns a consistent outcome
// for the same input (deterministic), and the returned methods slice
// (when MatchMethodNotAllowed) is non-nil and sorted-stable.
//
// (Named FuzzRouterPaths to avoid colliding with the pre-existing
// FuzzRouterMatch target in router_test.go which exercises the
// matching-with-registered-routes path differently.)
//
// Run: go test ./http -fuzz=FuzzRouterPaths -fuzztime=3s
func FuzzRouterPaths(f *testing.F) {
	// Seed corpus — at least 5 cases per spec.
	f.Add(http.MethodGet, "/users/42")
	f.Add(http.MethodGet, "/")
	f.Add(http.MethodGet, "")
	f.Add(http.MethodGet, "/users/")
	f.Add("BOGUS-METHOD", "/users/42")
	f.Add(http.MethodGet, "/users/{id}/posts/{slug}")
	f.Add(http.MethodGet, "//multiple//slashes")
	f.Add(http.MethodGet, "/\x00binary\x00path")
	f.Add(http.MethodGet, "/../../../etc/passwd")
	f.Add(http.MethodGet, strings.Repeat("/deep", 32))
	f.Add(http.MethodDelete, "/static/*")
	f.Add(http.MethodOptions, "*")

	f.Fuzz(func(t *testing.T, method, path string) {
		// Recovery guard — any panic is a bug.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Match(%q, %q) panicked: %v", method, path, r)
			}
		}()

		// Build a fresh router per iteration so we don't race with mutations.
		// (Pre-registration of routes is deterministic; this is the "valid
		// input" surface that must never return MatchNone on a registered
		// route and must never return MatchOK for an unregistered one.)
		r := NewRouter()
		r.MapGet("/users", dummyOK)
		r.MapPost("/users", dummyOK)
		r.MapGet("/users/{id}", dummyOK)
		r.MapGet("/users/{id}/posts/{slug}", dummyOK)
		r.MapDelete("/static/*", dummyOK)
		r.MapGet("/", dummyOK)
		r.Map(http.MethodOptions, "*", dummyOK)

		// First call — observe the outcome.
		match1, outcome1, methods1 := r.Match(method, path)

		// Second call — must be deterministic.
		match2, outcome2, methods2 := r.Match(method, path)

		if outcome1 != outcome2 {
			t.Fatalf("Match non-deterministic: outcome1=%v outcome2=%v for (%q,%q)",
				outcome1, outcome2, method, path)
		}

		// methods slice stability: MatchMethodNotAllowed MUST return a
		// non-nil, sorted-stable slice. The spec demands Allow: header
		// consistency.
		if outcome1 == MatchMethodNotAllowed {
			if methods1 == nil {
				t.Fatalf("MatchMethodNotAllowed returned nil methods for (%q,%q)", method, path)
			}
			if len(methods1) != len(methods2) {
				t.Fatalf("methods slice length drift: %d vs %d", len(methods1), len(methods2))
			}
			// Equal element-by-element.
			for i := range methods1 {
				if methods1[i] != methods2[i] {
					t.Fatalf("methods drift at idx %d: %q vs %q",
						i, methods1[i], methods2[i])
				}
			}
		}

		// MatchOK consistency: same Route + same Params.
		if outcome1 == MatchOK {
			if match1.Route == nil || match2.Route == nil {
				t.Fatalf("MatchOK but match.Route is nil for (%q,%q)", method, path)
			}
			if match1.Route.Template != match2.Route.Template {
				t.Fatalf("Route drift: %q vs %q",
					match1.Route.Template, match2.Route.Template)
			}
			if len(match1.Params) != len(match2.Params) {
				t.Fatalf("param-count drift: %d vs %d",
					len(match1.Params), len(match2.Params))
			}
			for k, v := range match1.Params {
				if v2, ok := match2.Params[k]; !ok || v != v2 {
					t.Fatalf("param drift on %q: %q vs %q (or missing)",
						k, v, v2)
				}
			}
		}
	})
}

// dummyOK is the trivial handler used by the fuzz target. It must not
// panic — Match() inspects the Route, not the handler body.
func dummyOK(c *Ctx) error { return nil }

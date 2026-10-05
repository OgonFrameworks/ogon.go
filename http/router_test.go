// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package http

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
)

func TestRouterStaticRoute(t *testing.T) {
	r := NewRouter()
	r.MapGet("/users", func(c *Ctx) error { return c.JSON(map[string]string{"ok": "1"}) })

	match, outcome, _ := r.Match(http.MethodGet, "/users")
	if outcome != MatchOK {
		t.Fatalf("expected MatchOK, got %v", outcome)
	}
	if match.Route == nil || match.Route.Template != "/users" {
		t.Fatalf("bad match: %+v", match.Route)
	}
}

func TestRouterParamRoute(t *testing.T) {
	r := NewRouter()
	r.MapGet("/users/{id}", func(c *Ctx) error { return c.JSON(map[string]string{"id": c.Param("id")}) })

	match, outcome, _ := r.Match(http.MethodGet, "/users/42")
	if outcome != MatchOK {
		t.Fatalf("expected MatchOK, got %v", outcome)
	}
	if got := match.Params["id"]; got != "42" {
		t.Fatalf("id param: want 42, got %q", got)
	}
}

func TestRouterTypedParamInt(t *testing.T) {
	r := NewRouter()
	r.MapGet("/users/{id:int}", func(c *Ctx) error { return nil })

	match, outcome, _ := r.Match(http.MethodGet, "/users/7")
	if outcome != MatchOK {
		t.Fatalf("expected MatchOK, got %v", outcome)
	}
	if got := match.Params["id"]; got != "7" {
		t.Fatalf("id param: want 7, got %q", got)
	}
	// Spec: typed params are not strictly enforced at runtime (the codegen
	// path emits checked adapters). The runtime fallback accepts any value.
}

func TestRouterMethodNotAllowed(t *testing.T) {
	r := NewRouter()
	r.MapGet("/users", func(c *Ctx) error { return nil })

	_, outcome, allow := r.Match(http.MethodPost, "/users")
	if outcome != MatchMethodNotAllowed {
		t.Fatalf("expected MatchMethodNotAllowed, got %v", outcome)
	}
	if len(allow) != 1 || allow[0] != http.MethodGet {
		t.Fatalf("allow list: want [GET], got %v", allow)
	}
}

func TestRouterNotFound(t *testing.T) {
	r := NewRouter()
	r.MapGet("/users", func(c *Ctx) error { return nil })

	_, outcome, _ := r.Match(http.MethodGet, "/missing")
	if outcome != MatchNone {
		t.Fatalf("expected MatchNone, got %v", outcome)
	}
}

func TestRouterNestedParams(t *testing.T) {
	r := NewRouter()
	r.MapGet("/orgs/{org}/users/{user}", func(c *Ctx) error { return nil })

	match, outcome, _ := r.Match(http.MethodGet, "/orgs/acme/users/alice")
	if outcome != MatchOK {
		t.Fatalf("expected MatchOK, got %v", outcome)
	}
	if got := match.Params["org"]; got != "acme" {
		t.Fatalf("org: want acme, got %q", got)
	}
	if got := match.Params["user"]; got != "alice" {
		t.Fatalf("user: want alice, got %q", got)
	}
}

func TestRouterConflictPanics(t *testing.T) {
	r := NewRouter()
	r.MapGet("/users", func(c *Ctx) error { return nil })
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on conflict")
		}
	}()
	r.MapGet("/users", func(c *Ctx) error { return nil })
}

// Golden route-table snapshot (HTTP-069). The exact bytes are the contract.
// ANY change to the snapshot format requires a review — codegen, explain,
// and metrics labels all depend on it.
func TestRouterSnapshot(t *testing.T) {
	r := NewRouter()
	r.MapGet("/api/users", func(c *Ctx) error { return nil })
	r.MapPost("/api/users", func(c *Ctx) error { return nil })
	r.MapGet("/api/users/{id:int}", func(c *Ctx) error { return nil }).RequireAuth()
	r.MapPut("/api/users/{id:int}", func(c *Ctx) error { return nil }).CSRF()
	r.MapDelete("/api/users/{id:int}", func(c *Ctx) error { return nil }).RateLimit("std")

	got := r.Snapshot()
	want := `GET /api/users
POST /api/users
GET /api/users/{id:int} params=id:int meta=auth=true
PUT /api/users/{id:int} params=id:int meta=csrf=true
DELETE /api/users/{id:int} params=id:int meta=ratelimit=std
`
	if got != want {
		t.Fatalf("snapshot mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// Router fuzz (HTTP-080). Random path/method combinations must not panic
// the router; the matched route (when found) must be deterministic for the
// same input.
func FuzzRouterMatch(f *testing.F) {
	seed := []string{"/users", "/users/1", "/orgs/a/users/b", "/x", "/y/z"}
	for _, s := range seed {
		f.Add(s, "GET")
		f.Add(s, "POST")
	}
	r := NewRouter()
	r.MapGet("/users", func(c *Ctx) error { return nil })
	r.MapGet("/users/{id}", func(c *Ctx) error { return nil })
	r.MapGet("/orgs/{org}/users/{user}", func(c *Ctx) error { return nil })

	f.Fuzz(func(t *testing.T, path, method string) {
		if path == "" {
			return
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		match, outcome, _ := r.Match(method, path)
		// Re-run; result must be identical.
		match2, outcome2, _ := r.Match(method, path)
		if outcome != outcome2 {
			t.Fatalf("non-deterministic outcome for %s %s: %v vs %v", method, path, outcome, outcome2)
		}
		if outcome == MatchOK && (match.Route.Template != match2.Route.Template) {
			t.Fatalf("non-deterministic route for %s %s: %s vs %s",
				method, path, match.Route.Template, match2.Route.Template)
		}
	})
}

// BenchmarkRouterMatchSteadyState measures the steady-state hot path.
// Per Part VI.5 the budget is < 1µs for a 10k-route table. This benchmark
// uses a small table; the 10k variant lives in benchmarks/.
func BenchmarkRouterMatchSteadyState(b *testing.B) {
	r := NewRouter()
	for i := 0; i < 1000; i++ {
		path := fmt.Sprintf("/api/v1/resources/%d/{id}", i)
		r.MapGet(path, func(c *Ctx) error { return nil })
	}
	probe := "/api/v1/resources/500/abc"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, outcome, _ := r.Match(http.MethodGet, probe)
		if outcome != MatchOK {
			b.Fatalf("miss at iter %d", i)
		}
	}
}

// fuzzSeed is used by the FuzzRouterMatch seed corpus when more diverse
// inputs are required.
var fuzzSeed = rand.NewChaCha8([32]byte{1, 2, 3})

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Framework overhead benchmark (PERF-003 / Part XV.1 budget table).
//
// Target: ≤ 10 µs per request, framework overhead only (bind → route →
// headers), excluding the user handler body.
// Uses httptest to drive a real Server.Handler() chain end-to-end so the
// measured cost includes the InitCtx pool acquire, RouterMatch lookup,
// middleware chain dispatch, Ctx bookkeeping, and the response write —
// everything the user's handler would observe as "framework tax".

package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// noopHandler is the terminal handler. It writes a 204 with no body so the
// framework overhead is what's measured, not the handler body cost.
func noopHandler(c *Ctx) error { return c.NoContent(http.StatusNoContent) }

// silenceSlog swaps slog.Default() to a discard handler for the duration of
// a benchmark. The production default chain emits a per-request access log
// via slog.Default(); left at its real default the bench measures I/O time,
// not framework overhead. Callers restore the prior default via the returned
// restore func (deferred).
func silenceSlog() func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return func() { slog.SetDefault(prev) }
}

// BenchmarkFrameworkOverhead drives a single-route server end-to-end via
// httptest. The server has the full default middleware chain plus the
// implicit InitCtx + RouterMatch middlewares, so the per-request cost is
// the complete framework tax (pool acquire/release, route match, middleware
// chain, status write). Handler body is a 204 NoContent — its cost is
// dwarfed by the framework path.
//
// Budget: ≤ 10 µs per request. A regression here indicates either an
// allocation in the hot path (pool miss, params map, or middleware
// closure construction per request) or a lock contention on the router
// (RLock held for the duration of the trie walk).
func BenchmarkFrameworkOverhead(b *testing.B) {
	restore := silenceSlog()
	defer restore()

	r := NewRouter()
	_ = r.MapGet("/healthz", noopHandler)
	srv, err := NewServer(ServerOptions{Router: r})
	if err != nil {
		b.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		rq := req.WithContext(ctx)
		h.ServeHTTP(w, rq)
		if w.Code != http.StatusNoContent {
			b.Fatalf("status = %d, want 204", w.Code)
		}
	}
}

// BenchmarkFrameworkOverheadWithParam is the variant that exercises the
// params-extraction path. The hot path here is slightly slower because
// the router allocates a params map; the budget relaxes to ≤ 12 µs for
// the param-bearing variant (still within the framework-overhead budget
// for the common param route).
func BenchmarkFrameworkOverheadWithParam(b *testing.B) {
	restore := silenceSlog()
	defer restore()

	r := NewRouter()
	_ = r.MapGet("/users/{id}", func(c *Ctx) error {
		_ = c.Param("id")
		return c.NoContent(http.StatusNoContent)
	})
	srv, err := NewServer(ServerOptions{Router: r})
	if err != nil {
		b.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		rq := req.WithContext(ctx)
		h.ServeHTTP(w, rq)
		if w.Code != http.StatusNoContent {
			b.Fatalf("status = %d, want 204", w.Code)
		}
	}
}

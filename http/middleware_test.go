// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRecoverMiddleware verifies that a panicking handler is converted to
// a 500 ProblemDetails.
func TestRecoverMiddleware(t *testing.T) {
	mw := RecoverMiddleware()
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ProblemMediaType {
		t.Fatalf("content-type: want %s, got %s", ProblemMediaType, ct)
	}
}

// TestRequestIDMiddleware verifies generation and propagation.
func TestRequestIDMiddleware(t *testing.T) {
	mw := RequestIDMiddleware()
	called := false
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if id := r.Header.Get(RequestIDHeader); id == "" {
			t.Fatal("request id missing from request header")
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)
	if !called {
		t.Fatal("handler not called")
	}
	if got := rec.Header().Get(RequestIDHeader); got == "" {
		t.Fatal("response missing X-Request-Id")
	}
}

// TestRequestIDMiddlewarePreservesClientHeader verifies that a client-supplied
// X-Request-Id is preserved (truncated to 128 bytes max).
func TestRequestIDMiddlewarePreservesClientHeader(t *testing.T) {
	mw := RequestIDMiddleware()
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "client-supplied-123")
	wrapped.ServeHTTP(rec, req)
	if got := rec.Header().Get(RequestIDHeader); got != "client-supplied-123" {
		t.Fatalf("request id not preserved: %q", got)
	}
}

// TestTimeoutMiddlewareFastPath verifies that a fast handler completes
// before the timeout.
func TestTimeoutMiddlewareFastPath(t *testing.T) {
	mw := TimeoutMiddleware(1 * time.Second)
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
}

// TestTimeoutMiddlewareExpiry verifies the context is cancelled on timeout.
func TestTimeoutMiddlewareExpiry(t *testing.T) {
	mw := TimeoutMiddleware(50 * time.Millisecond)
	called := make(chan struct{})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(called)
			return
		case <-time.After(2 * time.Second):
			close(called)
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	go wrapped.ServeHTTP(rec, req)
	select {
	case <-called:
	case <-time.After(1 * time.Second):
		t.Fatal("handler did not observe context cancellation")
	}
}

// TestSecurityHeadersMiddleware verifies HSTS, X-Frame-Options, etc.
func TestSecurityHeadersMiddleware(t *testing.T) {
	cfg := DefaultSecurityConfig()
	cfg.HSTS = true
	mw := SecurityHeadersMiddlewareWith(cfg)
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)

	if v := rec.Header().Get("X-Frame-Options"); v != "DENY" {
		t.Fatalf("X-Frame-Options: want DENY, got %q", v)
	}
	if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Fatalf("X-Content-Type-Options: want nosniff, got %q", v)
	}
	if v := rec.Header().Get("Referrer-Policy"); v != "strict-origin-when-cross-origin" {
		t.Fatalf("Referrer-Policy: %q", v)
	}
	if v := rec.Header().Get("Strict-Transport-Security"); !strings.HasPrefix(v, "max-age=") {
		t.Fatalf("HSTS: %q", v)
	}
}

// TestCSRFMiddlewareOptIn verifies that CSRF only enforces on routes that
// opt in via Route.CSRF().
func TestCSRFMiddlewareOptIn(t *testing.T) {
	r := NewRouter()
	r.MapPost("/state", func(c *Ctx) error { return c.NoContent(http.StatusNoContent) }).CSRF()
	r.MapPost("/public", func(c *Ctx) error { return c.NoContent(http.StatusNoContent) })

	// Build a server with CSRF in the chain.
	srv, err := NewServer(ServerOptions{
		Router: r,
		Addr:   ":0",
		Middlewares: []Middleware{
			CSRFMiddleware(DefaultCSRFConfig()),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// /public: should NOT enforce CSRF (no .CSRF() on the route).
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/public", strings.NewReader(""))
	req1.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("public POST should pass CSRF; got %d", rec1.Code)
	}

	// /state: should be 403 because CSRF token is missing.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/state", strings.NewReader(""))
	req2.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("state POST without CSRF should be 403; got %d", rec2.Code)
	}
}

// TestRateLimitMiddleware verifies 429 after burst exhausted.
func TestRateLimitMiddleware(t *testing.T) {
	rl := NewRateLimiter()
	rl.Register(RateLimitPolicy{
		Name:      "test",
		Burst:     2,
		Steady:    1, // 1/sec refill
		WindowMax: 0,
	})

	mw := RateLimitMiddleware(rl, "test", nil)
	called := 0
	for i := 0; i < 2; i++ {
		c := AcquireCtx(newResponseRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), NewRouter())
		c.route = &Route{Template: "/"}
		err := mw(c)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error %v", i, err)
		}
		ReleaseCtx(c)
	}
	// 3rd call should be rejected.
	c := AcquireCtx(newResponseRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), NewRouter())
	c.route = &Route{Template: "/"}
	err := mw(c)
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	ReleaseCtx(c)
	_ = called
}

// TestCORSMiddlewareStrict verifies that *+creds is refused.
func TestCORSMiddlewareStrict(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected CORS misconfig panic")
		}
	}()
	_ = CORSMiddleware(CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowCredentials: true,
	})
}

// TestCORSMiddlewarePreflight verifies the preflight flow.
func TestCORSMiddlewarePreflight(t *testing.T) {
	mw := CORSMiddleware(CORSConfig{
		AllowOrigins:     []string{"https://example.com"},
		AllowCredentials: true,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: want 204, got %d", rec.Code)
	}
	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "https://example.com" {
		t.Fatalf("ACAO: want https://example.com, got %q", v)
	}
}

// TestETagMiddleware verifies 304 on If-None-Match match.
func TestETagMiddleware(t *testing.T) {
	// Build a server with the route opted into ETag.
	r := NewRouter()
	r.MapGet("/static", func(c *Ctx) error {
		return c.JSON(map[string]string{"hello": "world"})
	}).groupSet("etag", true)
	// We need to set the etag meta key directly.
	for _, rt := range r.Routes() {
		rt.groupSet("etag", true)
	}

	srv, err := NewServer(ServerOptions{
		Router:      r,
		Addr:        ":0",
		Middlewares: []Middleware{ETagMiddleware(false)},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/static", nil)
	srv.Handler().ServeHTTP(rec1, req1)
	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag not set on first response")
	}

	// Second request with If-None-Match should 304.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/static", nil)
	req2.Header.Set("If-None-Match", etag)
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status: want 304, got %d", rec2.Code)
	}
}

// TestCompressMiddleware verifies gzip is applied.
func TestCompressMiddleware(t *testing.T) {
	mw := CompressMiddleware()
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write ≥1KiB to exceed threshold.
		w.Write(make([]byte, 2048))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	wrapped.ServeHTTP(rec, req)

	ce := rec.Header().Get("Content-Encoding")
	if ce != "gzip" {
		t.Fatalf("Content-Encoding: want gzip, got %q (body len=%d)", ce, len(rec.Body.Bytes()))
	}
}

// TestIdempotencyMiddleware verifies cache replay via the dispatcher.
func TestIdempotencyMiddleware(t *testing.T) {
	cache := NewIdempotencyCache(time.Minute, 100)
	calls := 0

	r := NewRouter()
	r.MapPost("/x", func(c *Ctx) error {
		calls++
		c.Status(http.StatusCreated)
		return c.JSON(map[string]int{"n": calls})
	}).Use(IdempotencyMiddleware(cache))

	srv, err := NewServer(ServerOptions{
		Router: r,
		Addr:   ":0",
	})
	if err != nil {
		t.Fatal(err)
	}

	// First request: handler runs, response cached.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set(IdempotencyHeader, "client-123")
	srv.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first call: status want 201, got %d", rec1.Code)
	}

	// Second request with same key: handler should NOT run; replay cached.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set(IdempotencyHeader, "client-123")
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second call: status want 201, got %d", rec2.Code)
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Fatalf("expected replayed body, got first=%q second=%q",
			rec1.Body.String(), rec2.Body.String())
	}
	if calls != 1 {
		t.Fatalf("calls: want 1, got %d", calls)
	}
}

// TestChainOrder ensures the chain runs middlewares in declared order.
func TestChainOrder(t *testing.T) {
	var trace []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				trace = append(trace, "in:"+name)
				next.ServeHTTP(w, r)
				trace = append(trace, "out:"+name)
			})
		}
	}
	chain := []Middleware{mk("a"), mk("b"), mk("c")}
	wrapped := Chain(chain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace = append(trace, "handler")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	wrapped.ServeHTTP(rec, req)

	want := "in:a in:b in:c handler out:c out:b out:a"
	got := strings.Join(trace, " ")
	if got != want {
		t.Fatalf("chain order: want %q, got %q", want, got)
	}
}

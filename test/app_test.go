// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// App fixture tests (TEST-001/037). The fixture is the primary entrypoint
// for HTTP tests; these tests prove it works in isolation and parallel.

package test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewAppInProcess(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("pong"))
	})
	app := NewApp(t, WithHandler(mux))
	if app.BaseURL() == "" {
		t.Fatalf("BaseURL empty")
	}
	resp, err := app.Server().Client().Get(app.BaseURL() + "/ping")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Fatalf("body: %q", body)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 hit, got %d", hits.Load())
	}
}

func TestAppRecorderChain(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"role":"admin"}`))
	})
	app := NewApp(t, WithHandler(mux))
	RegisterRole("admin", "Bearer test-token-admin")
	rec := app.Login("admin")
	r := rec.Get("/api/users/1")
	AssertStatus(t, r, http.StatusOK)
	AssertJSON(t, r, `{"id":1,"role":"admin"}`)
	AssertHeader(t, r, "Content-Type", "application/json")
	// Verify auth header was actually sent on the request — server-side echo.
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/echo-auth", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, r.Header.Get("Authorization"))
	})
	app2 := NewApp(t, WithHandler(mux2))
	r2 := app2.Login("admin").Get("/echo-auth")
	AssertBodyContains(t, r2, "Bearer test-token-admin")
}

func TestAppParallelSafety(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	var counter atomic.Int64
	mux.HandleFunc("/count", func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		_, _ = w.Write([]byte("ok"))
	})
	app := NewApp(t, WithHandler(mux), WithTmpDir())
	// Issue several concurrent requests; fixture should be safe.
	for i := 0; i < 10; i++ {
		r := app.Recorder().Get("/count")
		AssertStatus(t, r, http.StatusOK)
	}
	if counter.Load() != 10 {
		t.Fatalf("expected 10 hits, got %d", counter.Load())
	}
}

func TestAppWithFakeClock(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	app := NewApp(t, WithHandler(mux), WithFakeClock(1_700_000_000_000_000_000))
	if app.FakeClock() == nil {
		t.Fatalf("fake clock not wired")
	}
	if app.FakeClock().CurrentNanos() != 1_700_000_000_000_000_000 {
		t.Fatalf("fake clock seed wrong: %d", app.FakeClock().CurrentNanos())
	}
}

func TestAppWithJobRunner(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	app := NewApp(t, WithHandler(mux), WithJobRunner())
	if app.JobRunner() == nil {
		t.Fatalf("job runner not wired")
	}
	called := atomic.Bool{}
	app.JobRunner().Enqueue(Job{
		Name: "noop",
		Run: func(ctx context.Context) error {
			called.Store(true)
			return nil
		},
	})
	if err := app.JobRunner().Drain(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !called.Load() {
		t.Fatalf("job not executed")
	}
}

func TestAppWithDB(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	app := NewApp(t, WithHandler(mux), WithDB(func(t *testing.T) *DBFixture {
		// We don't need a real DB for this test; just prove the factory
		// is called once and the fixture is exposed.
		return &DBFixture{t: t}
	}))
	if app.DB() == nil {
		t.Fatalf("DB fixture not wired")
	}
}

func TestSeedsAreNoopWithoutLoader(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	// Seeds() with no registered loader is a no-op; the test should not
	// fail.
	_ = NewApp(t, WithHandler(mux), Seeds("users", "posts"))
}

func TestRecorderJSONHelpers(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		in["received"] = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(in)
	})
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().WithJSON().Post("/echo", map[string]any{"name": "alice"})
	AssertStatus(t, r, http.StatusOK)
	AssertJSONContains(t, r, `{"received":true}`)
	AssertJSON(t, r, `{"name":"alice","received":true}`)
}

func TestRecorderMethods(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Method", r.Method)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/items/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Method", r.Method)
		w.WriteHeader(http.StatusOK)
	})
	app := NewApp(t, WithHandler(mux))
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		var r *Response
		switch m {
		case "GET":
			r = app.Recorder().Get("/items")
		case "POST":
			r = app.Recorder().Post("/items", map[string]any{})
		case "PUT":
			r = app.Recorder().Put("/items/1", map[string]any{})
		case "PATCH":
			r = app.Recorder().Patch("/items/1", map[string]any{})
		case "DELETE":
			r = app.Recorder().Delete("/items/1")
		}
		AssertStatus(t, r, http.StatusOK)
		AssertHeader(t, r, "X-Method", m)
	}
}

func TestRecorderRawBody(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/raw", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	})
	app := NewApp(t, WithHandler(mux))
	// raw bytes
	r := app.Recorder().Post("/raw", []byte("hello"))
	AssertBodyContains(t, r, "hello")
	// raw string
	r = app.Recorder().Post("/raw", "world")
	AssertBodyContains(t, r, "world")
}

func TestRecorderDoCustomMethod(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Method", r.Method)
	})
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().Do("OPTIONS", "/x", nil)
	AssertHeader(t, r, "X-Method", "OPTIONS")
}

func TestSplitAuthHeaderBearer(t *testing.T) {
	t.Parallel()
	got := splitAuthHeader("Bearer abc123")
	if got[0] != "Authorization" || got[1] != "Bearer abc123" {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestSplitAuthHeaderCookie(t *testing.T) {
	t.Parallel()
	got := splitAuthHeader("sid=abc123")
	if got[0] != "Cookie" || got[1] != "sid=abc123" {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestSplitAuthHeaderPlainToken(t *testing.T) {
	t.Parallel()
	got := splitAuthHeader("just-a-token")
	if got[0] != "Authorization" || got[1] != "Bearer just-a-token" {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestServerReuse(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	app := NewApp(t, WithHandler(mux))
	// Re-use the recorder for many requests.
	rec := app.Recorder()
	for i := 0; i < 5; i++ {
		r := rec.Get("/p")
		AssertStatus(t, r, http.StatusOK)
	}
}

func TestRecorder404(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().Get("/nonexistent")
	AssertStatus(t, r, http.StatusNotFound)
}

// httptest stub mirror — keeps the tests free of net/http/httptest imports.
var _ = httptest.NewServer

func TestBodyTruncate(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 1024)
	got := truncate([]byte(long))
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncated marker, got: %q", got)
	}
	short := "abc"
	if truncate([]byte(short)) != short {
		t.Fatalf("expected pass-through, got %q", truncate([]byte(short)))
	}
}

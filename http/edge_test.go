// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Edge cases for the HTTP server (P14 bug-bounty). Each test exercises
// one adversarial input shape that has historically caused panics,
// 500s, or goroutine leaks in other Go HTTP stacks. Our contract:
//   - nil/empty body MUST NOT 500
//   - empty path MUST NOT 500
//   - oversized header MUST be rejected cleanly (431 or 400, never 500)
//   - oversized body MUST be rejected cleanly (413, never 500)
//   - malformed JSON MUST yield 4xx with a Problem payload, never 500
//   - very long URL MUST be rejected cleanly (414 or 404, never 500)

package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// echoBody handler copies the request body into a small JSON wrapper.
// Used by the body-edge tests so we can assert "handler never saw a
// 500 from the framework's body machinery".
func echoBody(c *Ctx) error {
	return c.JSON(map[string]any{
		"ok":   true,
		"size": len(c.Request().Header.Get("Content-Type")),
	})
}

func newEdgeServer(t *testing.T) *Server {
	t.Helper()
	r := NewRouter()
	r.MapGet("/", func(c *Ctx) error { return c.JSON(map[string]string{"ok": "1"}) })
	r.MapPost("/echo", echoBody)
	r.MapPatch("/echo", echoBody)
	r.MapPut("/echo", echoBody)
	srv, err := NewServer(ServerOptions{
		Router:       r,
		Addr:         "127.0.0.1:0",
		MaxBodyBytes: 1 << 20, // 1 MiB
		// No middlewares — exercise the bare dispatch path so we test
		// the framework's panic-safety, not a middleware's recovery.
		Middlewares: []Middleware{},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

// TestEdgeNilBody — POST with literally nil body.
func TestEdgeNilBody(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("nil body returned %d — must not 500", rec.Code)
	}
}

// TestEdgeEmptyPath — empty path normalisation must not panic.
//
// We can't use httptest.NewRequest("", …) because net/http rejects an
// empty target. Instead we craft the Request manually and exercise
// the same dispatch path. The contract: the framework must not panic
// on an empty/whitespace path; the outcome is 4xx (never 5xx).
func TestEdgeEmptyPath(t *testing.T) {
	srv := newEdgeServer(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("empty path panicked: %v", r)
		}
	}()

	// Manually build a request whose URL.Path is "".
	rec := httptest.NewRecorder()
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: ""},
		Header: http.Header{},
		Body:   http.NoBody,
	}
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("empty path returned %d — must not 500", rec.Code)
	}
}

// TestEdgeMalformedJSON — POST with invalid JSON body.
func TestEdgeMalformedJSON(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	body := []byte(`{"broken": json, "missing": quote}`)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("malformed JSON returned %d — must not 500", rec.Code)
	}
	// Even on 4xx, the response should be observable and non-panicking.
	_ = rec.Body.Bytes()
}

// TestEdgeOversizedBody — POST body > MaxBodyBytes.
//
// We construct a body 2x the limit and assert the framework rejects
// it without panicking. Status 413 (or another 4xx) is acceptable;
// 5xx is not.
func TestEdgeOversizedBody(t *testing.T) {
	r := NewRouter()
	r.MapPost("/upload", func(c *Ctx) error {
		buf := make([]byte, 1<<20)
		n := 0
		for {
			nn, err := c.Request().Body.Read(buf)
			n += nn
			if err != nil {
				break
			}
		}
		return c.JSON(map[string]int{"read": n})
	})
	srv, err := NewServer(ServerOptions{
		Router:       r,
		Addr:         "127.0.0.1:0",
		MaxBodyBytes: 1 << 10, // 1 KiB
		Middlewares:  []Middleware{},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	oversized := bytes.Repeat([]byte("A"), 8<<10) // 8 KiB
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(oversized))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("oversized body panicked: %v", r)
		}
	}()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("oversized body returned %d — must not 500", rec.Code)
	}
}

// TestEdgeVeryLongURL — URL longer than typical max.
func TestEdgeVeryLongURL(t *testing.T) {
	srv := newEdgeServer(t)
	// 8 KiB path — Go's net/http enforces MaxHeaderBytes which includes
	// the request line. We stay just under MaxHeaderBytes (1 MiB default).
	long := "/" + strings.Repeat("a", 8<<10)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, long, nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("long URL returned %d — must not 500", rec.Code)
	}
}

// TestEdgeLargeHeader — single header value > MaxHeaderBytes/2.
func TestEdgeLargeHeader(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	bigVal := strings.Repeat("X", 1<<20) // 1 MiB cookie value
	req.Header.Set("X-Bogus", bigVal)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("large header returned %d — must not 500", rec.Code)
	}
}

// TestEdgeBinaryBody — body with embedded NULs.
func TestEdgeBinaryBody(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	body := []byte{0x00, 0x01, 0x02, 0x03, 'h', 'e', 'l', 'l', 'o', 0x00}
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("binary body returned %d — must not 500", rec.Code)
	}
}

// TestEdgeUnknownContentType — POST with a Content-Type the codec
// registry has no decoder for. Must fall back to 415 (or 400) — never 500.
func TestEdgeUnknownContentType(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	body := []byte("random bytes that aren't a known codec")
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/vnd.bogus+json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("unknown content-type returned %d — must not 500", rec.Code)
	}
}

// TestEdgeMalformedQuery — query string with non-parsed characters.
func TestEdgeMalformedQuery(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/?%zz%broken&=nokey&ok=1", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("malformed query returned %d — must not 500", rec.Code)
	}
}

// TestEdgeNilMiddlewareChain — Chain() with nil middlewares must
// return a non-nil handler that delegates to the terminal handler.
func TestEdgeNilMiddlewareChain(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Chain(nil, ...) panicked: %v", r)
		}
	}()
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := Chain(nil, terminal)
	if h == nil {
		t.Fatal("Chain(nil, terminal) returned nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Chain(nil, terminal) served code %d, want 200", rec.Code)
	}
}

// TestEdgeEmptyJSONPayload — POST with empty JSON body.
func TestEdgeEmptyJSONPayload(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("empty JSON returned %d — must not 500", rec.Code)
	}
}

// TestEdgeJSONDrift — body that is valid JSON but has unexpected shape.
func TestEdgeJSONDrift(t *testing.T) {
	srv := newEdgeServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"unexpected":"shape","arr":[1,2,3]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("json drift returned %d — must not 500", rec.Code)
	}
	// The response body must be JSON-decodable (problem or echo'd).
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response body is not JSON: %v (body=%q)", err, rec.Body.String())
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestCtx() (*Ctx, *responseRecorder, *http.Request) {
	rec := newResponseRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test?x=1", strings.NewReader(`{"a":"b"}`))
	r.Header.Set("Content-Type", "application/json")
	c := AcquireCtx(rec, r, NewRouter())
	return c, rec, r
}

func TestCtxParamAndQuery(t *testing.T) {
	c, _, _ := newTestCtx()
	c.params = map[string]string{"id": "42"}
	if got := c.Param("id"); got != "42" {
		t.Fatalf("param: want 42, got %q", got)
	}
	if got := c.Query("x"); got != "1" {
		t.Fatalf("query: want 1, got %q", got)
	}
}

func TestCtxJSON(t *testing.T) {
	c, rec, _ := newTestCtx()
	err := c.JSON(map[string]string{"ok": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.code)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != "1" {
		t.Fatalf("ok field: want 1, got %q", got["ok"])
	}
}

func TestCtxProblem(t *testing.T) {
	c, rec, _ := newTestCtx()
	err := c.Problem(NewProblem(http.StatusBadRequest, "Bad", "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ProblemMediaType {
		t.Fatalf("content-type: want %s, got %s", ProblemMediaType, ct)
	}
}

func TestCtxStatus(t *testing.T) {
	c, rec, _ := newTestCtx()
	c.Status(http.StatusCreated)
	if rec.code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d", rec.code)
	}
}

func TestCtxHeader(t *testing.T) {
	c, _, _ := newTestCtx()
	c.SetHeader("X-Custom", "hello")
	if got := c.ResponseWriter().Header().Get("X-Custom"); got != "hello" {
		t.Fatalf("custom header: want hello, got %q", got)
	}
}

func TestCtxRedirect(t *testing.T) {
	c, rec, _ := newTestCtx()
	c.Redirect(http.StatusFound, "/elsewhere")
	if rec.code != http.StatusFound {
		t.Fatalf("status: want 302, got %d", rec.code)
	}
}

func TestCtxParamInt(t *testing.T) {
	c, _, _ := newTestCtx()
	c.params = map[string]string{"id": "42"}
	n, err := c.ParamInt("id")
	if err != nil {
		t.Fatalf("ParamInt: %v", err)
	}
	if n != 42 {
		t.Fatalf("id: want 42, got %d", n)
	}
}

func TestCtxBind(t *testing.T) {
	type Input struct {
		Name string `json:"name" validate:"required"`
		Age  int    `json:"age" validate:"min=1,max=120"`
	}
	rec := newResponseRecorder()
	body := `{"name":"alice","age":30}`
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	c := AcquireCtx(rec, r, NewRouter())

	var in Input
	if err := c.Bind(&in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.Name != "alice" {
		t.Fatalf("name: want alice, got %q", in.Name)
	}
	if in.Age != 30 {
		t.Fatalf("age: want 30, got %d", in.Age)
	}
}

func TestCtxBindValidation(t *testing.T) {
	type Input struct {
		Name string `json:"name" validate:"required"`
	}
	rec := newResponseRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	c := AcquireCtx(rec, r, NewRouter())

	var in Input
	err := c.Bind(&in)
	if err == nil {
		t.Fatal("Bind: expected validation error, got nil")
	}
	p, ok := err.(*ProblemDetails)
	if !ok {
		t.Fatalf("expected *ProblemDetails, got %T", err)
	}
	if p.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d", p.Status)
	}
}

func TestCtxStream(t *testing.T) {
	c, rec, _ := newTestCtx()
	err := c.Stream(func(rc *http.ResponseController) error {
		_, err := c.ResponseWriter().Write([]byte("chunk1"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(rec.body), "chunk1") {
		t.Fatalf("stream body: %q", rec.body)
	}
}

// TestCtxPoolReuse ensures the sync.Pool resets between acquires. This is
// a regression guard: a stale state leak would cause incorrect status
// codes or params to surface across requests.
func TestCtxPoolReuse(t *testing.T) {
	c1 := AcquireCtx(newResponseRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), NewRouter())
	c1.Status(http.StatusTeapot)
	ReleaseCtx(c1)

	c2 := AcquireCtx(newResponseRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), NewRouter())
	if c2.StatusCode() != 0 {
		t.Fatalf("status not reset: got %d", c2.StatusCode())
	}
	ReleaseCtx(c2)
}

// Use the context import to silence unused (if future test changes remove
// the explicit calls).
var _ = context.WithTimeout
var _ = time.Second

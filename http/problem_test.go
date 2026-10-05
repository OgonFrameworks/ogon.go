// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package http

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestProblemBasic(t *testing.T) {
	p := NewProblem(http.StatusBadRequest, "Bad Request", "missing field")
	if p.Status != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", p.Status)
	}
	if p.Error() == "" {
		t.Fatal("Error() returned empty string")
	}
}

func TestProblemFieldError(t *testing.T) {
	p := NewProblem(http.StatusUnprocessableEntity, "Validation", "bad input").
		WithFieldError("email", "email", "must be valid", "x")
	if len(p.Errors) != 1 {
		t.Fatalf("errors: want 1, got %d", len(p.Errors))
	}
	if p.Errors[0].Field != "email" {
		t.Fatalf("field: want email, got %q", p.Errors[0].Field)
	}
}

func TestProblemExtension(t *testing.T) {
	p := NewProblem(http.StatusBadRequest, "Bad", "")
	p.SetExtension("trace-id", "abc123")

	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["trace-id"].(string); !ok || v != "abc123" {
		t.Fatalf("trace-id extension missing or wrong: %v", got["trace-id"])
	}
}

func TestProblemReservedExtensionPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on reserved extension key")
		}
	}()
	p := NewProblem(http.StatusBadRequest, "x", "")
	p.SetExtension("status", 200)
}

func TestProblemWrite(t *testing.T) {
	rec := newResponseRecorder()
	p := NewProblem(http.StatusBadRequest, "Bad Request", "missing field")
	if err := p.Write(rec, nil); err != nil {
		t.Fatal(err)
	}
	if rec.code != http.StatusBadRequest {
		t.Fatalf("status code: want 400, got %d", rec.code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ProblemMediaType {
		t.Fatalf("Content-Type: want %s, got %s", ProblemMediaType, ct)
	}
	var got ProblemDetails
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "Bad Request" {
		t.Fatalf("title: want 'Bad Request', got %q", got.Title)
	}
}

func TestValidateProblem(t *testing.T) {
	p := ValidateProblem("failed", FieldError{Field: "x", Code: "required", Message: "missing"})
	if p.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d", p.Status)
	}
}

// newResponseRecorder is a tiny httptest stand-in for hermetic tests.
type responseRecorder struct {
	header http.Header
	code   int
	body   []byte
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header)}
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	r.body = append(r.body, p...)
	return len(p), nil
}
func (r *responseRecorder) WriteHeader(code int) { r.code = code }

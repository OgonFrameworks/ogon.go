// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Recorder tests (TEST-002/012). These tests exercise the recorder client
// against an in-process handler so the fixture's JSON helpers stay
// regression-free.

package test

import (
	"net/http"
	"testing"
)

func TestRecorderGetReturnsResponse(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().Get("/ok")
	if r == nil {
		t.Fatalf("nil response")
	}
	AssertStatus(t, r, http.StatusOK)
}

func TestRecorderAssertJSONFailure(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"a":1}`))
	})
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().Get("/json")

	// Mismatch should fail a stub t.
	stub := &failStubT{}
	defer func() { _ = recover() }()
	AssertJSON(stub, r, `{"a":2}`)
	if !stub.failed {
		t.Fatalf("expected AssertJSON to fail on mismatch")
	}
}

func TestRecorderAssertJSONContainsSubset(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"a":1,"b":2,"c":3}`))
	})
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().Get("/json")
	AssertJSONContains(t, r, `{"a":1}`)
	AssertJSONContains(t, r, `{"b":2,"c":3}`)
}

func TestRecorderAssertHeaderFailure(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/h", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-A", "alpha")
	})
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().Get("/h")
	AssertHeader(t, r, "X-A", "alpha")

	stub := &failStubT{}
	AssertHeader(stub, r, "X-A", "beta")
	if !stub.failed {
		t.Fatalf("expected AssertHeader to fail on mismatch")
	}
}

func TestRecorderAssertBodyContains(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("the quick brown fox"))
	})
	app := NewApp(t, WithHandler(mux))
	r := app.Recorder().Get("/b")
	AssertBodyContains(t, r, "brown")
	AssertBodyContains(t, r, "")
}

func TestRecorderQueryValues(t *testing.T) {
	t.Parallel()
	q := QueryValues("a", "1", "b", "2")
	if q == "" {
		t.Fatalf("expected non-empty query")
	}
	// Malformed pairs return empty string.
	if bad := QueryValues("a"); bad != "" {
		t.Fatalf("expected empty for odd pairs, got %q", bad)
	}
}

func TestRecorderNilGuards(t *testing.T) {
	t.Parallel()
	// All assertions should fail gracefully on nil.
	stub := &failStubT{}
	AssertStatus(stub, nil, 200)
	AssertJSON(stub, nil, `{}`)
	AssertJSONContains(stub, nil, `{}`)
	AssertHeader(stub, nil, "X", "Y")
	AssertBodyContains(stub, nil, "x")
	if !stub.failed {
		t.Fatalf("expected assertions to fail on nil response")
	}
}

func TestRecorderCloneDoesNotMutateOriginal(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/x", func(w http.ResponseWriter, r *http.Request) {})
	app := NewApp(t, WithHandler(mux))
	rec := app.Recorder()
	recWithHeader := rec.WithHeader("X-Test", "1")
	if rec.header.Get("X-Test") != "" {
		t.Fatalf("original recorder mutated")
	}
	if recWithHeader.header.Get("X-Test") != "1" {
		t.Fatalf("clone missing header")
	}
}

func TestRecorderBearerClone(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth", r.Header.Get("Authorization"))
	})
	app := NewApp(t, WithHandler(mux))
	rec := app.Recorder().WithBearer("abc")
	r := rec.Get("/x")
	AssertHeader(t, r, "X-Auth", "Bearer abc")
}

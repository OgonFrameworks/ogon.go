// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Minimal assert library (TEST-026). The bar is set deliberately low: every
// function is one line of behaviour + a t.Helper + a descriptive message.
// No external dependency, no fluent builder, no in-band assertions. The law
// from Part XIV says: test infrastructure must never exceed the behaviour
// under test. This file is the proof that the rule was respected.

package test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// testFailT is shared between recorder.go and assert.go so both the recorder
// assertions and the standalone assert lib can be driven by *testing.T or
// by the failStubT used in failure-path tests. Defined here so it lives
// with the assert lib.

// testFailT is declared in recorder.go (one definition per package).
// Keep this alias for readability within assert.go callers.
type assertT = testFailT

// AssertEqual fails the test if want != got using reflect.DeepEqual.
func AssertEqual(t testFailT, want, got any, msg ...string) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		failEq(t, want, got, msg...)
	}
}

// AssertNotEqual fails the test if want == got.
func AssertNotEqual(t testFailT, want, got any, msg ...string) {
	t.Helper()
	if reflect.DeepEqual(want, got) {
		base := fmt.Sprintf("ogontest: expected values to differ, both = %#v", want)
		t.Fatalf("%s%s", prefix(msg), base)
	}
}

// AssertTrue fails the test if !cond.
func AssertTrue(t testFailT, cond bool, msg ...string) {
	t.Helper()
	if !cond {
		t.Fatalf("%sogontest: expected true, got false", prefix(msg))
	}
}

// AssertFalse fails the test if cond.
func AssertFalse(t testFailT, cond bool, msg ...string) {
	t.Helper()
	if cond {
		t.Fatalf("%sogontest: expected false, got true", prefix(msg))
	}
}

// AssertNil fails the test if v != nil (using reflect deep nil check on
// interface values so that typed-nil pointers are caught too).
func AssertNil(t testFailT, v any, msg ...string) {
	t.Helper()
	if !isNil(v) {
		t.Fatalf("%sogontest: expected nil, got %#v", prefix(msg), v)
	}
}

// AssertNotNil fails the test if v == nil.
func AssertNotNil(t testFailT, v any, msg ...string) {
	t.Helper()
	if isNil(v) {
		t.Fatalf("%sogontest: expected non-nil, got nil", prefix(msg))
	}
}

// AssertError fails the test if err == nil.
func AssertError(t testFailT, err error, msg ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%sogontest: expected an error, got nil", prefix(msg))
	}
}

// AssertNoError fails the test if err != nil.
func AssertNoError(t testFailT, err error, msg ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%sogontest: unexpected error: %v", prefix(msg), err)
	}
}

// AssertErrorIs fails the test if !errors.Is(err, target).
func AssertErrorIs(t testFailT, err, target error, msg ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%sogontest: expected error matching %v, got nil", prefix(msg), target)
		return
	}
	if !errIs(err, target) {
		t.Fatalf("%sogontest: expected error matching %v, got %v", prefix(msg), target, err)
	}
}

// AssertContains fails the test if s does not contain substr.
func AssertContains(t testFailT, s, substr string, msg ...string) {
	t.Helper()
	if !contains(s, substr) {
		t.Fatalf("%sogontest: %q does not contain %q", prefix(msg), s, substr)
	}
}

// AssertJSONEqual re-marshals want and got to canonical JSON and compares.
// Use this when field order is not significant (TEST-012 snapshot style).
func AssertJSONEqual(t testFailT, want, got any, msg ...string) {
	t.Helper()
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("%sogontest: marshal want: %v", prefix(msg), err)
		return
	}
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%sogontest: marshal got: %v", prefix(msg), err)
		return
	}
	if !bytes.Equal(w, g) {
		t.Fatalf("%sogontest: JSON mismatch\nwant: %s\ngot:  %s", prefix(msg), w, g)
	}
}

// AssertLen fails the test if len(got) != want.
func AssertLen(t testFailT, want int, got any, msg ...string) {
	t.Helper()
	n := reflect.ValueOf(got).Len()
	if n != want {
		t.Fatalf("%sogontest: expected len %d, got %d", prefix(msg), want, n)
	}
}

// ---- internal helpers ----

func prefix(msg []string) string {
	if len(msg) > 0 {
		return msg[0] + ": "
	}
	return ""
}

func failEq(t testFailT, want, got any, msg ...string) {
	t.Helper()
	t.Fatalf("%sogontest: want %#v, got %#v", prefix(msg), want, got)
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	}
	return false
}

// errIs returns true if err wraps target. Local errors.Is so the assert
// lib stays dependency-free.
func errIs(err, target error) bool {
	return errors.Is(err, target)
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// equalAny is a thin reflect.DeepEqual alias used by helpers.go MustEqual.
func equalAny(a, b any) bool { return reflect.DeepEqual(a, b) }

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Naming conventions (TEST-024) and the table-test helper (TEST-025). A
// one-screen summary that a contributor reads once and then writes tests in
// the house style without re-checking. No code in this file is clever —
// that is the point.

package test

import (
	"fmt"
	"reflect"
	"testing"
)

// NamingConventions is a doc-shaped value: callers embed it into a generated
// TESTING.md or read it from `ogon doc test conventions`. The intent is that
// the conventions are codified in exactly one place.
const NamingConventions = `# OgonGo test conventions (TEST-024)

File layout
- *_test.go sits next to its unit; package match (white-box) by default.
- Integration-only files carry a '//go:build integration' tag.

Naming
- TestFoo: unit.
- TestFoo_Subname: subtests using t.Run for table tests.
- TestFoo_Fuzz: fuzz wrappers; never call rand inside.
- BenchmarkFoo: std benchmark.
- ExampleFoo: example function with // Output: comment.

Fixtures
- Use ogontest.NewApp for in-process HTTP; never bind a real port.
- Use ogontest.TmpDir for any on-disk state.
- Use ogontest.WithEnv for env-var overrides.
- Use t.Cleanup for teardown; never bare defer in helpers.

Parallel
- Call t.Parallel() at the top of every test unless the test mutates
  shared state through WithEnv or a shared global.
- Fixtures here are parallel-safe by design (TEST-036).

Coverage
- Target 85% statement coverage on packages in ./http, ./record, ./auth.
- Race detector is mandatory in CI (TEST-018).
- Fuzz seeds live in ogontest.FuzzSeeds (TEST-019).

The law (Part XIV): writing test infrastructure must never exceed writing
the behaviour under test. If a helper is more complex than the code under
test, delete the helper.
`

// TableCase is a single row of a table test. Name is used as t.Run subtest
// name; the remaining fields are user-typed via generics so callers get
// compile-checked inputs (TEST-025).
type TableCase[I any] struct {
	Name  string
	Input I
	// Want may be any comparable or struct; Assert decides what to do with it.
	Want any
}

// RunTable runs each case through fn. fn receives the test, the case input,
// and the case's Want. The Want is supplied as any; callers do their own
// type assertion in fn (the typed value is one short line).
//
// Typical usage:
//
//	type in struct{ X int }
//	ogontest.RunTable(t, []ogontest.TableCase[in]{{
//	    Name: "positive", Input: in{X: 1}, Want: 1,
//	}}, func(t *testing.T, c in, want any) {
//	    ogontest.AssertEqual(t, want, c.X)
//	})
func RunTable[I any](t *testing.T, cases []TableCase[I], fn func(t *testing.T, in I, want any)) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			fn(t, c.Input, c.Want)
		})
	}
}

// RunTableErr is the error-shaped variant: each case declares an expected
// sentinel (or nil) and fn returns an error; the helper asserts.
func RunTableErr[I any](t *testing.T, cases []TableCase[I], fn func(t *testing.T, in I) error) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			err := fn(t, c.Input)
			if c.Want == nil {
				AssertNoError(t, err)
				return
			}
			target, ok := c.Want.(error)
			if !ok {
				t.Fatalf("ogontest: Want for case %q must be error or nil, got %T", c.Name, c.Want)
			}
			AssertErrorIs(t, err, target)
		})
	}
}

// MustType asserts v has the same concrete type as the zero value of T and
// returns v cast to T. Cuts boilerplate in table tests where the input is
// of a fixed type. Panics (via Fatal) on mismatch.
func MustType[T any](t *testing.T, v any) T {
	t.Helper()
	var zero T
	if reflect.TypeOf(v) != reflect.TypeOf(zero) {
		t.Fatalf("ogontest: type mismatch: want %T, got %T", zero, v)
	}
	return v.(T)
}

// Describe returns a short label suitable for t.Run names. Strips spaces.
// Useful when building subtest names from inputs.
func Describe(v any) string {
	return fmt.Sprintf("%v", v)
}

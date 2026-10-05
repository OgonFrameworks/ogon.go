// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Minimal-assert lib tests (TEST-026). These exercise every public
// function in assert.go against both passing and failing inputs so the
// lib stays regression-free.

package test

import (
	"errors"
	"testing"
)

func TestAssertEqualPasses(t *testing.T) {
	t.Parallel()
	AssertEqual(t, 1, 1)
	AssertEqual(t, "x", "x")
	AssertEqual(t, []int{1, 2, 3}, []int{1, 2, 3})
}

func TestAssertEqualFails(t *testing.T) {
	t.Parallel()
	stub := &failStubT{}
	AssertEqual(stub, 1, 2, "equality")
	if !stub.failed {
		t.Fatalf("expected AssertEqual to fail on 1!=2")
	}
}

func TestAssertNotEqual(t *testing.T) {
	t.Parallel()
	AssertNotEqual(t, 1, 2)
	stub := &failStubT{}
	AssertNotEqual(stub, 1, 1)
	if !stub.failed {
		t.Fatalf("expected AssertNotEqual to fail on 1==1")
	}
}

func TestAssertTrueFalse(t *testing.T) {
	t.Parallel()
	AssertTrue(t, true)
	AssertFalse(t, false)
	stub := &failStubT{}
	AssertTrue(stub, false)
	AssertFalse(stub, true)
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

func TestAssertNilNotNil(t *testing.T) {
	t.Parallel()
	var p *int
	AssertNil(t, p)
	AssertNil(t, nil)
	AssertNotNil(t, "x")
	stub := &failStubT{}
	AssertNil(stub, "x")
	AssertNotNil(stub, nil)
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

func TestAssertErrorAndNoError(t *testing.T) {
	t.Parallel()
	AssertNoError(t, nil)
	AssertError(t, errors.New("boom"))
	stub := &failStubT{}
	AssertNoError(stub, errors.New("boom"))
	AssertError(stub, nil)
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

func TestAssertErrorIs(t *testing.T) {
	t.Parallel()
	base := errors.New("sentinel")
	wrapped := errors.Join(base)
	AssertErrorIs(t, wrapped, base)
	stub := &failStubT{}
	AssertErrorIs(stub, errors.New("other"), base)
	AssertErrorIs(stub, nil, base)
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

func TestAssertContains(t *testing.T) {
	t.Parallel()
	AssertContains(t, "hello world", "world")
	AssertContains(t, "hello world", "")
	stub := &failStubT{}
	AssertContains(stub, "hello", "world")
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

func TestAssertJSONEqual(t *testing.T) {
	t.Parallel()
	AssertJSONEqual(t, map[string]int{"a": 1}, map[string]int{"a": 1})
	stub := &failStubT{}
	AssertJSONEqual(stub, map[string]int{"a": 1}, map[string]int{"a": 2})
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

func TestAssertLen(t *testing.T) {
	t.Parallel()
	AssertLen(t, 3, []int{1, 2, 3})
	AssertLen(t, 2, "hi")
	stub := &failStubT{}
	AssertLen(stub, 5, []int{1})
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

func TestMustEqual(t *testing.T) {
	t.Parallel()
	MustEqual(t, 1, 1)
	stub := &failStubT{}
	MustEqual(stub, 1, 2, "msg")
	if !stub.failed {
		t.Fatalf("expected failure")
	}
}

// Note: failStubT.Fatalf does not abort execution; helpers above exercise
// the failure path by inspecting the failed flag.

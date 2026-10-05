// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the codes package.

package codes

import (
	"strings"
	"testing"
)

func TestStarterSet_Registered(t *testing.T) {
	t.Parallel()
	want := []string{
		E3001ConfigNotFound,
		E1001RouteConflict,
		E2001ValidationFailed,
		E4001MigrationConflict,
		E5001DependencyMissing,
		E6001SecurityViolation,
		E7001RuntimeSupervisorClosed,
		E8001GenerationConflict,
	}
	for _, code := range want {
		info, ok := Lookup(code)
		if !ok {
			t.Fatalf("starter code %q not registered", code)
		}
		if info.Code != code {
			t.Fatalf("Lookup returned wrong code: %+v", info)
		}
		if info.ShortTitle == "" {
			t.Fatalf("code %q has empty ShortTitle", code)
		}
		if info.DocURL == "" {
			t.Fatalf("code %q has empty DocURL", code)
		}
	}
}

func TestLookup_UnregisteredCode(t *testing.T) {
	t.Parallel()
	if _, ok := Lookup("OGON-E0000"); ok {
		t.Fatal("OGON-E0000 should not be registered")
	}
}

func TestAll_SortedAndContainsStarter(t *testing.T) {
	t.Parallel()
	all := All()
	if len(all) < 8 {
		t.Fatalf("All must include starter set: got %d", len(all))
	}
	prev := ""
	for _, info := range all {
		if prev != "" && info.Code < prev {
			t.Fatalf("All not sorted: %q then %q", prev, info.Code)
		}
		prev = info.Code
	}
}

func TestRegister_IdempotentSameMetadata(t *testing.T) {
	t.Parallel()
	// E9001 is reserved for tests; class G (8xxx).
	const testCode = "OGON-E9001"
	t.Cleanup(func() {
		// We cannot deregister, but re-running the test binary is fine
		// because the metadata is identical each time.
	})
	Register(testCode, "Test code", "https://ogongo.dev/errors/OGON-E9001")
	Register(testCode, "Test code", "https://ogongo.dev/errors/OGON-E9001")
	info, ok := Lookup(testCode)
	if !ok || info.ShortTitle != "Test code" {
		t.Fatalf("idempotent register failed: %+v", info)
	}
}

func TestRegister_RejectsReuse_DifferentMetadata(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register with different metadata must panic")
		}
	}()
	Register("OGON-E9002", "First", "https://ogongo.dev/errors/OGON-E9002")
	Register("OGON-E9002", "Different", "https://ogongo.dev/errors/OGON-E9002")
}

func TestRegister_RejectsInvalidCode(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("invalid code must panic")
		}
		if !strings.Contains(toAnyString(r), "invalid code format") {
			t.Fatalf("panic message unexpected: %v", r)
		}
	}()
	Register("NOT-A-CODE", "title", "url")
}

func TestRegister_RejectsNonDigits(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("non-digit code must panic")
		}
	}()
	Register("OGON-EABCD", "title", "url")
}

func TestRegister_RejectsWrongLength(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("wrong-length code must panic")
		}
	}()
	Register("OGON-E123", "title", "url")
}

func TestClassOf_StarterClasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code string
		want Class
	}{
		{E1001RouteConflict, ClassRoute},
		{E2001ValidationFailed, ClassValidation},
		{E3001ConfigNotFound, ClassConfig},
		{E4001MigrationConflict, ClassMigration},
		{E5001DependencyMissing, ClassDependency},
		{E6001SecurityViolation, ClassSecurity},
		{E7001RuntimeSupervisorClosed, ClassRuntime},
		{E8001GenerationConflict, ClassGeneration},
	}
	for _, c := range cases {
		got := classOf(c.code)
		if got != c.want {
			t.Fatalf("classOf(%q): got %q want %q", c.code, got, c.want)
		}
	}
}

func TestClassPrefixDigit_Contract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cls   Class
		digit byte
	}{
		{ClassCodegen, '0'},
		{ClassRoute, '1'},
		{ClassValidation, '2'},
		{ClassConfig, '3'},
		{ClassMigration, '4'},
		{ClassDependency, '5'},
		{ClassSecurity, '6'},
		{ClassRuntime, '7'},
		{ClassGeneration, '8'},
	}
	for _, c := range cases {
		got, ok := ClassPrefixDigit[c.cls]
		if !ok || got != c.digit {
			t.Fatalf("prefix digit for %q: got %q want %q", c.cls, string(got), string(c.digit))
		}
	}
}

// toAnyString avoids importing fmt in the test for the panic message check.
func toAnyString(r any) string {
	switch v := r.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return ""
	}
}

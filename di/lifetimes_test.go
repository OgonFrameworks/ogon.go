// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for lifetime tracking (DI-005) and scope-violation detection
// (DI-023): a singleton depending on a scoped provider is a generation
// error.

package di

import (
	"errors"
	"strings"
	"testing"
)

func TestScopeViolationDirect(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("ScopedThing", "*ScopedThing"), // default singleton
	}
	providers[0].Lifetime = LifetimeScoped
	providers = append(providers, makeProvider("SingletonUser", "*SingletonUser", "*ScopedThing"))
	// SingletonUser is default singleton; depends on ScopedThing → violation.
	_, err := NewGraph(providers, nil)
	if err == nil {
		t.Fatalf("expected scope violation error")
	}
	var se ScopeError
	if !errors.As(err, &se) {
		t.Fatalf("got %T, want ScopeError", err)
	}
	if len(se) != 1 {
		t.Fatalf("violations = %d, want 1", len(se))
	}
	if !strings.Contains(se.Error(), "SingletonUser") || !strings.Contains(se.Error(), "ScopedThing") {
		t.Errorf("error %q missing names", se.Error())
	}
}

func TestScopeViolationIndirectThroughTransient(t *testing.T) {
	t.Parallel()
	scoped := makeProvider("ScopedThing", "*ScopedThing")
	scoped.Lifetime = LifetimeScoped
	transient := makeProvider("TransientThing", "*TransientThing", "*ScopedThing")
	transient.Lifetime = LifetimeTransient
	singleton := makeProvider("SingletonUser", "*SingletonUser", "*TransientThing")
	// Singleton → Transient → Scoped is also a violation.
	_, err := NewGraph([]Provider{scoped, transient, singleton}, nil)
	if err == nil {
		t.Fatalf("expected scope violation error")
	}
	var se ScopeError
	if !errors.As(err, &se) {
		t.Fatalf("got %T, want ScopeError", err)
	}
}

func TestScopedDependingOnSingletonAllowed(t *testing.T) {
	t.Parallel()
	singleton := makeProvider("DB", "*DB")
	scoped := makeProvider("Cart", "*Cart", "*DB")
	scoped.Lifetime = LifetimeScoped
	// Scoped → Singleton is fine; singleton is shared across requests.
	_, err := NewGraph([]Provider{singleton, scoped}, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestTransientDependingOnScopedAllowed(t *testing.T) {
	t.Parallel()
	scoped := makeProvider("Cart", "*Cart")
	scoped.Lifetime = LifetimeScoped
	transient := makeProvider("CartView", "*CartView", "*Cart")
	transient.Lifetime = LifetimeTransient
	// Transient → Scoped is fine: each call creates a fresh transient that
	// captures the current scope's Cart.
	_, err := NewGraph([]Provider{scoped, transient}, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSingletonDependingOnSingletonAllowed(t *testing.T) {
	t.Parallel()
	a := makeProvider("A", "*A")
	b := makeProvider("B", "*B", "*A")
	_, err := NewGraph([]Provider{a, b}, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestScopeErrorString(t *testing.T) {
	t.Parallel()
	se := ScopeError{
		ScopeViolation{
			Singleton: Provider{Name: "X"},
			Scoped:    Provider{Name: "Y"},
		},
	}
	if !strings.Contains(se.Error(), "X") || !strings.Contains(se.Error(), "Y") {
		t.Errorf("error %q missing names", se.Error())
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package compiler

import (
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/diag"
)

func TestDiagnostics_EffectSelfWrite(t *testing.T) {
	g := &DependencyGraph{Component: "Chat"}
	g.Primitives = []Primitive{
		{Kind: PrimState, Name: "count"},
		{Kind: PrimEffect, Name: "audit", Reads: []string{"count"}, Writes: []string{"count"}},
	}
	diags := DiagnoseReactivity(g)
	found := false
	for _, d := range diags {
		if d.Severity == diag.SeverityError && strings.Contains(d.Title, "self-write") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected effect-self-write diagnostic; got %+v", diags)
	}
}

func TestDiagnostics_DerivedCycle(t *testing.T) {
	g := &DependencyGraph{Component: "Chat"}
	g.Primitives = []Primitive{
		{Kind: PrimDerived, Name: "a", Reads: []string{"b"}},
		{Kind: PrimDerived, Name: "b", Reads: []string{"a"}},
	}
	diags := DiagnoseReactivity(g)
	found := false
	for _, d := range diags {
		if d.Severity == diag.SeverityError && strings.Contains(d.Title, "cycle") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected derived-cycle diagnostic; got %+v", diags)
	}
}

func TestDiagnostics_IllegalMutation_Warn(t *testing.T) {
	g := &DependencyGraph{Component: "Chat"}
	g.Primitives = []Primitive{
		{Kind: PrimState, Name: "count"},
	}
	// No effect, no handler → warning.
	diags := DiagnoseReactivity(g)
	found := false
	for _, d := range diags {
		if d.Severity == diag.SeverityWarning && strings.Contains(d.Title, "never mutated") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'never mutated' warning; got %+v", diags)
	}
}

func TestDiagnostics_NoFalsePositive(t *testing.T) {
	g := &DependencyGraph{Component: "Chat"}
	g.Primitives = []Primitive{
		{Kind: PrimState, Name: "count"},
		{Kind: PrimEffect, Name: "audit", Reads: []string{"count"}, Writes: []string{"audit_log"}},
	}
	g.Regions = []Region{{Name: "button", Handler: "Send", Reads: []string{"count"}}}
	diags := DiagnoseReactivity(g)
	for _, d := range diags {
		if d.Severity == diag.SeverityError {
			t.Errorf("unexpected error diag: %+v", d)
		}
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the DI graph builder (DI-002), cycle detection (DI-003),
// missing-dep diagnostics (DI-016), and ambiguous-provider diagnostics
// (DI-017).

package di

import (
	"errors"
	"strings"
	"testing"
)

var _ = errors.Is // retained for clarity; tests use errors.As

// makeProvider builds a Provider with the given name and provided/required
// type strings (parsed as same-package idents; supply "PkgPath:Name" for
// cross-package refs).
func makeProvider(name string, provides string, requires ...string) Provider {
	p := Provider{
		Name:     name,
		FuncName: "New" + name,
		PkgPath:  "example.com/myapp/services",
		PkgName:  "services",
		Provides: parseTypeRef(provides),
		Lifetime: LifetimeSingleton,
	}
	for _, r := range requires {
		p.Requires = append(p.Requires, parseTypeRef(r))
	}
	return p
}

// parseTypeRef parses a type string into a TypeRef. Supports:
//   - "Foo"            → same-package type, no PkgPath
//   - "pkg.Foo"        → cross-package selector, PkgPath="pkg"
//   - "*Foo" / "*pkg.Foo"
//   - "context.Context" → builtin special case, PkgPath="context"
func parseTypeRef(s string) TypeRef {
	t := TypeRef{Expr: s}
	rest := s
	star := false
	if strings.HasPrefix(rest, "*") {
		star = true
		rest = rest[1:]
	}
	if i := strings.Index(rest, "."); i >= 0 {
		t.PkgName = rest[:i]
		t.Name = rest[i+1:]
		if t.PkgName == "context" {
			t.PkgPath = "context"
		} else {
			t.PkgPath = "example.com/myapp/" + t.PkgName
		}
	} else {
		t.Name = rest
	}
	if star {
		t.Name = "*" + t.Name
	}
	return t
}

func TestGraphBuildsAndResolves(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("OrderService", "*OrderService", "*UserService"),
		makeProvider("UserService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	if got := len(g.providers); got != 2 {
		t.Errorf("providers = %d, want 2", got)
	}
}

func TestGraphDetectsMissingDep(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("OrderService", "*OrderService", "*UserService", "*record.Pool"),
		makeProvider("UserService", "*UserService"),
	}
	// Missing deps are NOT a hard error — they're surfaced as external deps.
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	missing := g.Missing()
	if len(missing) != 1 {
		t.Fatalf("missing entries = %d, want 1", len(missing))
	}
	if missing[0].Type.Name != "*Pool" {
		t.Errorf("missing type = %v", missing[0].Type)
	}
}

func TestGraphDetectsAmbiguousProvider(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
		makeProvider("AltUserService", "*UserService"),
	}
	_, err := NewGraph(providers, nil)
	if err == nil {
		t.Fatalf("expected ambiguous error")
	}
	var amb AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("got %T, want AmbiguousError", err)
	}
	if len(amb) != 1 {
		t.Fatalf("ambiguous entries = %d, want 1", len(amb))
	}
}

func TestGraphDetectsCycle(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("A", "*A", "*B"),
		makeProvider("B", "*B", "*C"),
		makeProvider("C", "*C", "*A"),
	}
	_, err := NewGraph(providers, nil)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	var cycle CycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("got %T, want CycleError", err)
	}
	// Cycle path must contain all three providers and close back.
	msg := cycle.Error()
	for _, want := range []string{"A", "B", "C"} {
		if !strings.Contains(msg, want) {
			t.Errorf("cycle path %q missing %q", msg, want)
		}
	}
}

func TestGraphOverrideReplacesGenerated(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
	}
	override := makeProvider("MockUserService", "*UserService")
	override.Named = true
	override.FuncName = "NewMockUserService"
	g, err := NewGraph(providers, []Provider{override})
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	p, ok := g.ProviderFor(parseTypeRef("*UserService").Key())
	if !ok {
		t.Fatal("no provider for *UserService")
	}
	if p.FuncName != "NewMockUserService" {
		t.Errorf("got %q, want NewMockUserService", p.FuncName)
	}
}

func TestGraphTopoSort(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("OrderService", "*OrderService", "*UserService", "*PaymentService"),
		makeProvider("UserService", "*UserService"),
		makeProvider("PaymentService", "*PaymentService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	order, err := g.TopoSort()
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("len = %d, want 3", len(order))
	}
	// UserService must come before OrderService and PaymentService.
	idx := map[string]int{}
	for i, p := range order {
		idx[p.Name] = i
	}
	if idx["UserService"] > idx["OrderService"] {
		t.Errorf("UserService must precede OrderService")
	}
	if idx["UserService"] > idx["PaymentService"] {
		t.Errorf("UserService must precede PaymentService")
	}
	if idx["PaymentService"] > idx["OrderService"] {
		t.Errorf("PaymentService must precede OrderService")
	}
}

func TestGraphDependents(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("A", "*A", "*C"),
		makeProvider("B", "*B", "*C"),
		makeProvider("C", "*C"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	deps := g.Dependents(parseTypeRef("*C").Key())
	if len(deps) != 2 {
		t.Fatalf("dependents = %d, want 2", len(deps))
	}
}

func TestGraphSortedProviderKeysStable(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("Z", "*Z"),
		makeProvider("A", "*A"),
		makeProvider("M", "*M"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	keys := g.SortedProviderKeys()
	if len(keys) != 3 {
		t.Fatalf("len = %d, want 3", len(keys))
	}
	if keys[0].Name != "*A" || keys[1].Name != "*M" || keys[2].Name != "*Z" {
		t.Errorf("order = %v", keys)
	}
}

func TestCycleErrorRendersPath(t *testing.T) {
	t.Parallel()
	c := CycleError{Ref{Name: "*A"}, Ref{Name: "*B"}, Ref{Name: "*A"}}
	want := "di: cycle detected: *A → *B → *A"
	if c.Error() != want {
		t.Errorf("got %q, want %q", c.Error(), want)
	}
}

func TestMissingErrorRendersCandidates(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserRepository", "*UserRepository"),
		makeProvider("OrderService", "*OrderService", "*UserRepo"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	missing := g.Missing()
	if len(missing) != 1 {
		t.Fatalf("missing entries = %d, want 1", len(missing))
	}
	me := MissingError(missing)
	msg := me.Error()
	if !strings.Contains(msg, "UserRepo") {
		t.Errorf("error %q missing the missing type name", msg)
	}
	if !strings.Contains(msg, "UserRepository") {
		t.Errorf("error %q missing did-you-mean candidate", msg)
	}
}

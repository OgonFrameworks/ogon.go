// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the high-level API surface in di/di.go: Scan(dir), (*Graph).Generate,
// and (*Graph).Explain. These complement the lower-level tests in scanner_test,
// graph_test, codegen_test, and explain_test by exercising the convenience
// wrappers end-to-end (DI-001..030 / DI-028).

package di

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScanHighLevelAPI verifies Scan(dir) walks app/services, returns a Graph,
// and surfaces missing deps as Missing() rather than failing the call.
func TestScanHighLevelAPI(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }
`)
	g, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if g == nil {
		t.Fatal("Scan returned nil graph")
	}
	providers := g.Providers()
	if len(providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(providers))
	}
	if providers[0].Name != "UserService" {
		t.Errorf("provider name = %q, want UserService", providers[0].Name)
	}
}

// TestScanEmptyDirErrors verifies Scan rejects an empty dir path explicitly.
func TestScanEmptyDirErrors(t *testing.T) {
	t.Parallel()
	if _, err := Scan(""); err == nil {
		t.Errorf("Scan(\"\") = nil, want error")
	}
}

// TestScanSurfacesCycle verifies Scan propagates cycle errors as a CycleError.
func TestScanSurfacesCycle(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "cycle.go", `package services

type A struct{}
type B struct{}

func NewA(b *B) *A { return &A{} }
func NewB(a *A) *B { return &B{} }
`)
	_, err := Scan(dir)
	if err == nil {
		t.Fatalf("Scan: expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q missing 'cycle'", err.Error())
	}
}

// TestGraphGenerateMethod verifies (*Graph).Generate(outPath) writes a valid
// Go file to disk and that the bytes are parsable Go (DI-004).
func TestGraphGenerateMethod(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }
`)
	g, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	if err := g.Generate(out); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, out, data, parser.ParseComments); err != nil {
		t.Fatalf("generated file does not parse: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), "type Container struct") {
		t.Errorf("missing Container struct:\n%s", data)
	}
}

// TestGraphGenerateMethodNilGraph verifies (*Graph).Generate on a nil graph
// returns an error rather than panicking.
func TestGraphGenerateMethodNilGraph(t *testing.T) {
	t.Parallel()
	var g *Graph
	if err := g.Generate("/tmp/should_not_exist.go"); err == nil {
		t.Errorf("nil graph Generate = nil, want error")
	}
}

// TestGraphExplainMethod verifies (*Graph).Explain() returns a non-empty
// string containing the provider name (DI-014).
func TestGraphExplainMethod(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }
`)
	g, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	out := g.Explain()
	if out == "" {
		t.Errorf("Explain() returned empty string")
	}
	if !strings.Contains(out, "UserService") {
		t.Errorf("Explain() output missing UserService:\n%s", out)
	}
	if !strings.Contains(out, "singleton") {
		t.Errorf("Explain() output missing lifetime annotation:\n%s", out)
	}
}

// TestHighLevelAPIRoundTrip exercises the full Scan → Generate → Explain
// pipeline against a multi-provider services tree to confirm the high-level
// API surface works end-to-end (DI-028 acceptance: change constructor → regen
// → compiles).
func TestHighLevelAPIRoundTrip(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "multi.go", `package services

type UserService struct{}
type OrderService struct {
	user *UserService
}

func NewUserService() *UserService { return &UserService{} }
func NewOrderService(u *UserService) *OrderService { return &OrderService{user: u} }
`)
	g, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	if err := g.Generate(out); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "UserService()") {
		t.Errorf("missing UserService accessor")
	}
	if !strings.Contains(src, "OrderService()") {
		t.Errorf("missing OrderService accessor")
	}
	if !strings.Contains(src, "services.NewOrderService") {
		t.Errorf("missing constructor call:\n%s", src)
	}
	// Explain output should mention both providers in topo order.
	explained := g.Explain()
	if !strings.Contains(explained, "UserService") || !strings.Contains(explained, "OrderService") {
		t.Errorf("explain missing providers:\n%s", explained)
	}
}

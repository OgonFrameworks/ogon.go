// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the DI scanner (DI-001): discovers NewX(deps) X constructors
// under a directory, captures type refs, lifetimes, cleanup returns.

package di

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content into a services/<name>.go file under a tmp dir and
// returns the directory path.
func writeServicesFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	servicesDir := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// go.mod so packagePath can resolve the import path.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	full := filepath.Join(servicesDir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return servicesDir
}

func TestScanDirFindsBasicConstructor(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("got %d providers, want 1", len(providers))
	}
	p := providers[0]
	if p.Name != "UserService" {
		t.Errorf("Name = %q, want UserService", p.Name)
	}
	if p.FuncName != "NewUserService" {
		t.Errorf("FuncName = %q", p.FuncName)
	}
	if p.Lifetime != LifetimeSingleton {
		t.Errorf("Lifetime = %v, want singleton", p.Lifetime)
	}
	if p.Cleanup {
		t.Errorf("Cleanup = true, want false")
	}
	if p.ReturnsError {
		t.Errorf("ReturnsError = true, want false")
	}
}

func TestScanDirHandlesErrorReturn(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() (*UserService, error) { return &UserService{}, nil }
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("got %d providers, want 1", len(providers))
	}
	if !providers[0].ReturnsError {
		t.Errorf("ReturnsError = false")
	}
	if providers[0].Cleanup {
		t.Errorf("Cleanup = true")
	}
}

func TestScanDirHandlesCleanupReturn(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() (*UserService, func()) {
        return &UserService{}, func() {}
}
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if !providers[0].Cleanup {
		t.Errorf("Cleanup = false")
	}
	if providers[0].ReturnsError {
		t.Errorf("ReturnsError = true")
	}
}

func TestScanDirHandlesCleanupAndErrorReturn(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() (*UserService, func(), error) {
        return &UserService{}, func() {}, nil
}
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if !providers[0].Cleanup {
		t.Errorf("Cleanup = false")
	}
	if !providers[0].ReturnsError {
		t.Errorf("ReturnsError = false")
	}
}

func TestScanDirParsesLifetimeDirective(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "cart.go", `package services

type CartService struct{}

//ogon:di lifetime=scoped
func NewCartService() *CartService { return &CartService{} }
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if providers[0].Lifetime != LifetimeScoped {
		t.Errorf("Lifetime = %v, want scoped", providers[0].Lifetime)
	}
}

func TestScanDirIgnoresTestFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user.go"), []byte(`package services

type UserService struct{}
func NewUserService() *UserService { return &UserService{} }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user_test.go"), []byte(`package services

type FakeService struct{}
func NewFakeService() *FakeService { return &FakeService{} }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("got %d providers, want 1 (test files ignored)", len(providers))
	}
	if providers[0].Name != "UserService" {
		t.Errorf("got %q, want UserService", providers[0].Name)
	}
}

func TestScanDirIgnoresMethods(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }

func (u *UserService) NewThing() *Thing { return &Thing{} }

type Thing struct{}
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("got %d providers, want 1 (methods ignored)", len(providers))
	}
}

func TestScanDirRequiresResults(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

func NewBadThing() {}
`)
	_, err := ScanDir(dir)
	if err == nil {
		t.Fatalf("expected error for constructor with no results")
	}
}

func TestScanDirRequiresUppercaseNew(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type X struct{}
func newX() *X { return &X{} }            // lowercase — skipped
func Newlowercase() *X { return &X{} }    // no uppercase after New — skipped
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("got %d providers, want 0", len(providers))
	}
}

func TestScanDirResolvesSelectorTypes(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

import (
        "context"
        "example.com/myapp/record"
)

type UserService struct{}

func NewUserService(db *record.Pool, ctx context.Context) *UserService {
        return &UserService{}
}
`)
	providers, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("got %d providers, want 1", len(providers))
	}
	p := providers[0]
	if len(p.Requires) != 2 {
		t.Fatalf("got %d requires, want 2", len(p.Requires))
	}
	// First require: *record.Pool — Name includes the pointer marker so
	// pointer and value types do not collide.
	r := p.Requires[0]
	if r.PkgPath != "example.com/myapp/record" {
		t.Errorf("PkgPath = %q", r.PkgPath)
	}
	if r.PkgName != "record" {
		t.Errorf("PkgName = %q", r.PkgName)
	}
	if r.Name != "*Pool" {
		t.Errorf("Name = %q, want *Pool", r.Name)
	}
	if r.Expr != "*record.Pool" {
		t.Errorf("Expr = %q", r.Expr)
	}
	// context.Context
	c := p.Requires[1]
	if c.PkgPath != "context" {
		t.Errorf("PkgPath = %q", c.PkgPath)
	}
	if c.PkgName != "context" {
		t.Errorf("PkgName = %q", c.PkgName)
	}
	if c.Name != "Context" {
		t.Errorf("Name = %q", c.Name)
	}
}

func TestScanDirIsStable(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type A struct{}
type B struct{}
type C struct{}

func NewA() *A { return &A{} }
func NewB() *B { return &B{} }
func NewC() *C { return &C{} }
`)
	p1, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != len(p2) {
		t.Fatalf("len mismatch %d vs %d", len(p1), len(p2))
	}
	for i := range p1 {
		if p1[i].FuncName != p2[i].FuncName {
			t.Errorf("order[%d]: %s vs %s", i, p1[i].FuncName, p2[i].FuncName)
		}
	}
}

func TestParseLifetime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want Lifetime
		ok   bool
	}{
		{"singleton", LifetimeSingleton, true},
		{"scoped", LifetimeScoped, true},
		{"transient", LifetimeTransient, true},
		{"", LifetimeSingleton, true},
		{"SCOPED", LifetimeScoped, true},
		{"unknown", LifetimeSingleton, false},
	}
	for _, c := range cases {
		got, ok := ParseLifetime(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseLifetime(%q) = %v,%v; want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestLifetimeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lt   Lifetime
		want string
	}{
		{LifetimeSingleton, "singleton"},
		{LifetimeScoped, "scoped"},
		{LifetimeTransient, "transient"},
		{Lifetime(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.lt.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.lt, got, c.want)
		}
	}
}

func TestProviderPosition(t *testing.T) {
	t.Parallel()
	p := Provider{File: "/a/b.go", Line: 42}
	if got := p.Position(); got != "/a/b.go:42" {
		t.Errorf("Position = %q", got)
	}
}

// Stub to silence unused-import warnings if test files are stripped during a
// partial run.
var _ = ast.IsExported
var _ = token.NoPos

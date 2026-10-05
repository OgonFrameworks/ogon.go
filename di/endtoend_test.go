// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// End-to-end DI codegen test: generates a di_gen.go into a real Go project
// and runs `go build` on it to verify the output is not just syntactically
// valid but actually type-checks and compiles. (DI-028: change constructor →
// regen → compiles.)

package di

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndGeneratedCodeCompiles is the strongest test we can run: build a
// real Go project, run the codegen, then invoke `go build` on the generated
// package to verify it type-checks and compiles.
func TestEndToEndGeneratedCodeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end build test skipped in -short mode")
	}
	goBin := lookPathGo(t)
	dir := t.TempDir()
	// Project layout:
	//   go.mod                          module example.com/myapp
	//   app/services/user.go            package services; NewUserService
	//   app/services/order.go           package services; NewOrderService(*UserService)
	//   generated/di/di_gen.go          (generated)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	servicesDir := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servicesDir, "user.go"), []byte(`package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servicesDir, "order.go"), []byte(`package services

type OrderService struct {
	user *UserService
}

func NewOrderService(user *UserService) *OrderService {
	return &OrderService{user: user}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Run the codegen pipeline.
	out := filepath.Join(dir, "generated", "di", "di_gen.go")
	if _, err := Build(BuildOptions{
		ServicesDir: servicesDir,
		OutputPath:  out,
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Verify the generated package compiles.
	build := exec.Command(goBin, "build", "./generated/di/...")
	build.Dir = dir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("generated di_gen.go does not compile: %v", err)
	}
}

// TestEndToEndScopedProviderCompiles verifies that scoped providers emit a
// compileable Scope struct + accessor.
func TestEndToEndScopedProviderCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end build test skipped in -short mode")
	}
	goBin := lookPathGo(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	servicesDir := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servicesDir, "cart.go"), []byte(`package services

type CartService struct{}

//ogon:di lifetime=scoped
func NewCartService() *CartService { return &CartService{} }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "generated", "di", "di_gen.go")
	if _, err := Build(BuildOptions{
		ServicesDir: servicesDir,
		OutputPath:  out,
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	build := exec.Command(goBin, "build", "./generated/di/...")
	build.Dir = dir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("generated di_gen.go with scoped provider does not compile: %v", err)
	}
}

// TestEndToEndExternalDepCompiles verifies that external deps emit a Deps
// struct and the package compiles.
func TestEndToEndExternalDepCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end build test skipped in -short mode")
	}
	goBin := lookPathGo(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a `record` sub-package with a Pool type to serve as the
	// external dependency.
	recordDir := filepath.Join(dir, "app", "record")
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recordDir, "pool.go"), []byte(`package record

type Pool struct{}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	servicesDir := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servicesDir, "user.go"), []byte(`package services

import "example.com/myapp/app/record"

type UserService struct {
	db *record.Pool
}

func NewUserService(db *record.Pool) *UserService {
	return &UserService{db: db}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "generated", "di", "di_gen.go")
	if _, err := Build(BuildOptions{
		ServicesDir: servicesDir,
		OutputPath:  out,
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Inspect the generated file — it must reference the Deps struct.
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "type Deps struct") {
		t.Errorf("missing Deps struct:\n%s", data)
	}
	if !strings.Contains(string(data), "Pool") {
		t.Errorf("missing Pool external dep:\n%s", data)
	}
	// And it must compile.
	build := exec.Command(goBin, "build", "./generated/di/...")
	build.Dir = dir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("generated di_gen.go with external dep does not compile: %v", err)
	}
}

// TestEndToEndCleanupReturnCompiles verifies that a constructor returning
// (T, func()) emits compileable cleanup registration.
func TestEndToEndCleanupReturnCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end build test skipped in -short mode")
	}
	goBin := lookPathGo(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	servicesDir := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servicesDir, "resource.go"), []byte(`package services

type Resource struct{}

func NewResource() (*Resource, func()) {
	return &Resource{}, func() {}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "generated", "di", "di_gen.go")
	if _, err := Build(BuildOptions{
		ServicesDir: servicesDir,
		OutputPath:  out,
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	build := exec.Command(goBin, "build", "./generated/di/...")
	build.Dir = dir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("generated di_gen.go with cleanup return does not compile: %v", err)
	}
}

// TestEndToEndErrorReturnCompiles verifies that a constructor returning
// (T, error) emits compileable error handling.
func TestEndToEndErrorReturnCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end build test skipped in -short mode")
	}
	goBin := lookPathGo(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	servicesDir := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(servicesDir, "thing.go"), []byte(`package services

type Thing struct{}

func NewThing() (*Thing, error) {
	return &Thing{}, nil
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "generated", "di", "di_gen.go")
	if _, err := Build(BuildOptions{
		ServicesDir: servicesDir,
		OutputPath:  out,
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	build := exec.Command(goBin, "build", "./generated/di/...")
	build.Dir = dir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("generated di_gen.go with error return does not compile: %v", err)
	}
}

// lookPathGo returns the path to the go binary, or skips the test.
func lookPathGo(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go binary not in PATH: %v", err)
	}
	return p
}

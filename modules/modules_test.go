// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for modules/modules.go (public API entry point): Module type,
// Environment.Register/List/Get, Boot/Close orchestration, LoadEnvironment,
// Install/Uninstall, ValidateEnvironment, FindOrphans, Search.

package modules

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// helper: build a Module with a manifest and no hooks.
func mod(name, ver, ogon string, requires ...string) *Module {
	return &Module{Manifest: m(name, ver, ogon, requires...)}
}

func TestEnvironmentRegisterAndGet(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	if err := env.Register(mod("a", "1.0.0", ">=1.0")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := env.Get("a")
	if !ok {
		t.Fatal("Get: not found")
	}
	if got.Name() != "a" {
		t.Errorf("Name = %q", got.Name())
	}
	if _, ok := env.Get("nonexistent"); ok {
		t.Errorf("Get(nonexistent) = true, want false")
	}
}

func TestEnvironmentListSorted(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	_ = env.Register(mod("z", "1.0.0", ">=1.0"))
	_ = env.Register(mod("a", "1.0.0", ">=1.0"))
	_ = env.Register(mod("m", "1.0.0", ">=1.0"))
	list := env.List()
	if len(list) != 3 {
		t.Fatalf("len = %d", len(list))
	}
	want := []string{"a", "m", "z"}
	for i, w := range want {
		if list[i].Name() != w {
			t.Errorf("list[%d] = %q, want %q", i, list[i].Name(), w)
		}
	}
}

func TestEnvironmentRegisterNil(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	if err := env.Register(nil); err == nil {
		t.Errorf("Register(nil) = nil, want error")
	}
	if err := env.Register(&Module{}); err == nil {
		t.Errorf("Register(&Module{}) with nil Manifest = nil, want error")
	}
}

func TestEnvironmentRegisterDuplicate(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	_ = env.Register(mod("a", "1.0.0", ">=1.0"))
	if err := env.Register(mod("a", "1.0.0", ">=1.0")); err == nil {
		t.Errorf("Register(duplicate) = nil, want error")
	}
}

func TestEnvironmentRegisterRollsBackOnHookDup(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	// Pre-register hooks for "a" directly on the Lifecycle, bypassing
	// Environment.Register. This puts the Lifecycle in a state where
	// Registry.Register will accept a new "a" manifest (Registry has no
	// entry yet) but Lifecycle.Register will reject the duplicate name.
	// Environment.Register must roll back the Registry insertion so the
	// Environment stays consistent.
	_ = env.Lifecycle.Register("a", Hooks{
		Init: func(ctx context.Context) error { return nil },
	})
	err := env.Register(&Module{
		Manifest: m("a", "1.0.0", ">=1.0"),
		Hooks:    Hooks{},
	})
	if err == nil {
		t.Fatal("Register = nil, want error (Lifecycle duplicate)")
	}
	if len(env.List()) != 0 {
		t.Errorf("len = %d, want 0 (Registry entry was not rolled back)", len(env.List()))
	}
}

func TestEnvironmentBootAndCloseOrdering(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	_ = env.Register(mod("a", "1.0.0", ">=1.0"))
	_ = env.Register(mod("b", "1.0.0", ">=1.0", "a"))

	var trace []string
	env.Lifecycle = NewLifecycle() // reset to inject instrumented hooks
	_ = env.Lifecycle.Register("a", Hooks{
		Init:     func(ctx context.Context) error { trace = append(trace, "init-a"); return nil },
		Start:    func(ctx context.Context) error { trace = append(trace, "start-a"); return nil },
		Stop:     func(ctx context.Context) error { trace = append(trace, "stop-a"); return nil },
		Shutdown: func(ctx context.Context) error { trace = append(trace, "shutdown-a"); return nil },
	})
	_ = env.Lifecycle.Register("b", Hooks{
		Init:     func(ctx context.Context) error { trace = append(trace, "init-b"); return nil },
		Start:    func(ctx context.Context) error { trace = append(trace, "start-b"); return nil },
		Stop:     func(ctx context.Context) error { trace = append(trace, "stop-b"); return nil },
		Shutdown: func(ctx context.Context) error { trace = append(trace, "shutdown-b"); return nil },
	})

	ctx := context.Background()
	if err := env.Boot(ctx); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if err := env.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := []string{
		"init-a", "init-b",
		"start-a", "start-b",
		"stop-b", "stop-a", // reverse init
		"shutdown-b", "shutdown-a",
	}
	if len(trace) != len(want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}
	for i, w := range want {
		if trace[i] != w {
			t.Errorf("trace[%d] = %q, want %q (full: %v)", i, trace[i], w, trace)
		}
	}
}

func TestEnvironmentBootPropagatesInitError(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	_ = env.Register(mod("a", "1.0.0", ">=1.0"))
	env.Lifecycle = NewLifecycle()
	boom := errors.New("boom")
	_ = env.Lifecycle.Register("a", Hooks{
		Init: func(ctx context.Context) error { return boom },
	})
	if err := env.Boot(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Boot err = %v, want boom", err)
	}
}

func TestEnvironmentCloseNilSafe(t *testing.T) {
	t.Parallel()
	var env *Environment
	if err := env.Boot(context.Background()); err == nil {
		t.Errorf("Boot on nil Environment = nil, want error")
	}
	if err := env.Close(context.Background()); err == nil {
		t.Errorf("Close on nil Environment = nil, want error")
	}
}

func TestModuleNameNilSafe(t *testing.T) {
	t.Parallel()
	var nilMod *Module
	if got := nilMod.Name(); got != "" {
		t.Errorf("nilMod.Name() = %q, want empty", got)
	}
	emptyMod := &Module{}
	if got := emptyMod.Name(); got != "" {
		t.Errorf("emptyMod.Name() = %q, want empty", got)
	}
}

func TestLoadEnvironmentEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env, err := LoadEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadEnvironment on empty dir: %v", err)
	}
	if len(env.List()) != 0 {
		t.Errorf("expected 0 modules, got %d", len(env.List()))
	}
}

func TestLoadEnvironmentPicksUpManifests(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Drop two manifests under <dir>/modules/<name>/ogon.module.yaml.
	writeManifest(t, dir, "modules/stripe/ogon.module.yaml", `name: ogon-stripe
version: 1.0.0
ogon: ">=1.0"
provides:
  - routes
`)
	writeManifest(t, dir, "modules/sentry/ogon.module.yaml", `name: ogon-sentry
version: 1.0.0
ogon: ">=1.0"
provides:
  - obs
`)
	env, err := LoadEnvironment(dir)
	if err != nil {
		t.Fatalf("LoadEnvironment: %v", err)
	}
	list := env.List()
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].Name() != "ogon-sentry" {
		t.Errorf("list[0] = %q", list[0].Name())
	}
	if list[1].Name() != "ogon-stripe" {
		t.Errorf("list[1] = %q", list[1].Name())
	}
}

func TestInstallWritesLockfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := Install(dir, "ogon-stripe", "1.0.0", "github.com/ogonframeworks/ogon-stripe"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	loaded, err := LoadLock(LockfilePath(dir))
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	e, ok := loaded.Get("ogon-stripe")
	if !ok {
		t.Fatal("ogon-stripe not in lockfile")
	}
	if e.Version != "1.0.0" {
		t.Errorf("version = %q", e.Version)
	}
	if e.Source != "github.com/ogonframeworks/ogon-stripe" {
		t.Errorf("source = %q", e.Source)
	}
}

func TestInstallReplacesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = Install(dir, "a", "1.0.0", "src1")
	if err := Install(dir, "a", "2.0.0", "src2"); err != nil {
		t.Fatalf("Install v2: %v", err)
	}
	loaded, _ := LoadLock(LockfilePath(dir))
	if len(loaded.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(loaded.Entries))
	}
	if loaded.Entries[0].Version != "2.0.0" {
		t.Errorf("version = %q", loaded.Entries[0].Version)
	}
}

func TestInstallInvalidName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := Install(dir, "BAD NAME", "1.0.0", ""); err == nil {
		t.Errorf("Install with invalid name = nil, want error")
	}
}

func TestUninstallRemovesEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = Install(dir, "a", "1.0.0", "src")
	_ = Install(dir, "b", "1.0.0", "src")
	removed, err := Uninstall(dir, "a")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !removed {
		t.Errorf("removed = false, want true")
	}
	loaded, _ := LoadLock(LockfilePath(dir))
	if len(loaded.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(loaded.Entries))
	}
	if loaded.Entries[0].Name != "b" {
		t.Errorf("entry = %q", loaded.Entries[0].Name)
	}
}

func TestUninstallMissingReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	removed, err := Uninstall(dir, "nonexistent")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if removed {
		t.Errorf("removed = true for missing module")
	}
}

func TestUninstallInvalidName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := Uninstall(dir, "BAD"); err == nil {
		t.Errorf("Uninstall with invalid name = nil, want error")
	}
}

func TestValidateEnvironmentAllSatisfy(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.0"), "1.5.0")
	_ = r.Register(m("b", "1.0.0", ">=1.0,<2"), "1.5.0")
	if err := ValidateEnvironment(r, "1.5.0"); err != nil {
		t.Errorf("ValidateEnvironment: %v", err)
	}
}

func TestValidateEnvironmentReportsFirstFailure(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Register with a constraint the framework-version passed to Register
	// satisfies, then call ValidateEnvironment with a different (older)
	// version that fails the constraint.
	_ = r.Register(m("a", "1.0.0", ">=1.0"), "1.5.0")
	if err := ValidateEnvironment(r, "0.5.0"); err == nil {
		t.Errorf("ValidateEnvironment = nil, want error")
	}
}

func TestValidateEnvironmentNilRegistry(t *testing.T) {
	t.Parallel()
	if err := ValidateEnvironment(nil, "1.0.0"); err == nil {
		t.Errorf("ValidateEnvironment(nil, _) = nil, want error")
	}
}

func TestFindOrphans(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.0"), "1.5.0")
	_ = r.Register(m("b", "1.0.0", ">=1.0"), "1.5.0")
	_ = r.Register(m("c", "1.0.0", ">=1.0"), "1.5.0")
	lock := &Lockfile{}
	lock.Add(LockEntry{Name: "a", Version: "1.0.0"})
	// b is missing from lockfile → orphan. c is also missing.
	orphans := FindOrphans(r, lock)
	if len(orphans) != 2 {
		t.Fatalf("orphans = %v, want [b c]", orphans)
	}
	if orphans[0] != "b" || orphans[1] != "c" {
		t.Errorf("orphans = %v, want [b c]", orphans)
	}
}

func TestFindOrphansEmpty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.0"), "1.5.0")
	lock := &Lockfile{}
	lock.Add(LockEntry{Name: "a", Version: "1.0.0"})
	if got := FindOrphans(r, lock); len(got) != 0 {
		t.Errorf("orphans = %v, want empty", got)
	}
}

func TestFindOrphansNilSafe(t *testing.T) {
	t.Parallel()
	if got := FindOrphans(nil, &Lockfile{}); got != nil {
		t.Errorf("FindOrphans(nil, _) = %v, want nil", got)
	}
	if got := FindOrphans(NewRegistry(), nil); got != nil {
		t.Errorf("FindOrphans(_, nil) = %v, want nil", got)
	}
}

func TestSearchDelegatesToDefaultIndex(t *testing.T) {
	t.Parallel()
	results := Search("stripe")
	if len(results) == 0 {
		t.Fatal("Search(stripe) returned no results")
	}
	if results[0].Name != "ogon-stripe" {
		t.Errorf("first result = %q, want ogon-stripe", results[0].Name)
	}
}

func TestSearchEmptyQueryReturnsAll(t *testing.T) {
	t.Parallel()
	results := Search("")
	if len(results) == 0 {
		t.Fatal("Search() returned no results")
	}
}

// FrameworkVersion override: confirm tests can manipulate the package-level
// variable without affecting other tests (via t.Parallel + restore).
func TestFrameworkVersionOverridable(t *testing.T) {
	// Not parallel — mutates a package-level var.
	orig := FrameworkVersion
	defer func() { FrameworkVersion = orig }()
	FrameworkVersion = "0.5.0"

	env := NewEnvironment()
	if err := env.Register(mod("a", "1.0.0", ">=1.0")); err == nil {
		t.Errorf("Register with constraint >=1.0 against framework 0.5.0 = nil, want error")
	}
}

// TestEnvironmentRegisterFailsOnOgonConstraint verifies that Register refuses
// a module whose ogon constraint isn't met by the current FrameworkVersion.
func TestEnvironmentRegisterFailsOnOgonConstraint(t *testing.T) {
	t.Parallel()
	env := NewEnvironment()
	if err := env.Register(mod("a", "1.0.0", ">=99.0")); err == nil {
		t.Errorf("Register with unsatisfiable constraint = nil, want error")
	}
}

// TestInstallCreatesParentDir covers the Save→MkdirAll path for a nested
// lockfile location.
func TestInstallCreatesParentDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nestedRoot := filepath.Join(dir, "subdir")
	if err := Install(nestedRoot, "a", "1.0.0", "src"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(LockfilePath(nestedRoot)); err != nil {
		t.Errorf("lockfile not created: %v", err)
	}
}

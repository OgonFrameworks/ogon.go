// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// modules/modules.go: public API entry point for the modules package. This
// file composes the lower-level primitives (Manifest, Registry, Lifecycle,
// Lockfile, RegistryIndex) into a single Module type and Environment that
// host code can drive with one boot/shutdown pair.
//
// Spec coverage: MOD-001..035 (Part XVI). The Module type pairs a manifest
// with its lifecycle hooks (MOD-012); Environment drives Init → Start →
// (serve) → Stop → Shutdown in topological order (MOD-013); Install /
// Uninstall write to ogon.lock (MOD-021); FindOrphans powers the
// orphan-config warning on `ogon remove` (MOD-035).

package modules

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FrameworkVersion is the framework version the modules package evaluates
// ogon-constraints against. It defaults to "1.0.0" (the framework's release
// version per ogon.Version). Tests may override it to exercise constraint
// evaluation without ldflags. (MOD-003.)
var FrameworkVersion = "1.0.0"

// Module is the public-API pairing of a parsed manifest with its lifecycle
// hooks. Host code that wants to wire a module into an Environment constructs
// one of these and passes it to Environment.Register.
//
// The Hooks field is optional: a module may declare contributions in its
// manifest without registering runtime hooks (e.g. a migrations-only module
// whose contributions are picked up at codegen time).
type Module struct {
	Manifest *Manifest
	Hooks    Hooks
}

// Name returns the module's name. Convenience for Module.Manifest.Name.
// Returns "" if Manifest is nil (defensive — Register rejects nil manifests).
func (m *Module) Name() string {
	if m == nil || m.Manifest == nil {
		return ""
	}
	return m.Manifest.Name
}

// Environment bundles a Registry with its Lifecycle so a host process can
// boot a set of modules in a single Boot call. It is the high-level public
// entry point for the modules package.
type Environment struct {
	Registry  *Registry
	Lifecycle *Lifecycle
}

// NewEnvironment returns an empty Environment with freshly-allocated Registry
// and Lifecycle fields.
func NewEnvironment() *Environment {
	return &Environment{
		Registry:  NewRegistry(),
		Lifecycle: NewLifecycle(),
	}
}

// Register adds a module to the environment: its manifest is registered with
// the Registry (which validates the ogon constraint against FrameworkVersion)
// and its hooks are registered with the Lifecycle. Returns an error on
// duplicate name, validation failure, or unsatisfied ogon constraint. On
// hook-registration failure the manifest registration is rolled back so the
// Environment stays internally consistent.
func (e *Environment) Register(m *Module) error {
	if e == nil {
		return errors.New("modules: nil environment")
	}
	if m == nil {
		return errors.New("modules: nil module")
	}
	if m.Manifest == nil {
		return errors.New("modules: nil manifest")
	}
	if err := e.Registry.Register(m.Manifest, FrameworkVersion); err != nil {
		return err
	}
	if err := e.Lifecycle.Register(m.Manifest.Name, m.Hooks); err != nil {
		// Roll back the manifest registration to keep env consistent.
		e.Registry.Unregister(m.Manifest.Name)
		return err
	}
	return nil
}

// List returns the registered modules sorted by name. Each entry pairs the
// stored manifest with the hooks that were registered for it (Hooks is the
// zero value if no hooks were supplied). The returned slice is fresh —
// callers may mutate freely.
func (e *Environment) List() []*Module {
	if e == nil {
		return nil
	}
	manifests := e.Registry.All()
	out := make([]*Module, 0, len(manifests))
	for _, mf := range manifests {
		out = append(out, &Module{Manifest: mf, Hooks: e.lookupHooks(mf.Name)})
	}
	return out
}

// Get returns the module with the given name, or (nil, false) if absent.
func (e *Environment) Get(name string) (*Module, bool) {
	if e == nil {
		return nil, false
	}
	mf, ok := e.Registry.Get(name)
	if !ok {
		return nil, false
	}
	return &Module{Manifest: mf, Hooks: e.lookupHooks(name)}, true
}

// lookupHooks retrieves the hooks previously registered for name. Returns the
// zero Hooks value if the module has no hooks (read-only traversal of the
// Lifecycle's registrations slice — no locking required because callers of
// lookupHooks have already mutated the Lifecycle and established a
// happens-before relationship).
func (e *Environment) lookupHooks(name string) Hooks {
	for _, r := range e.Lifecycle.registrations {
		if r.Module == name {
			return r.Hooks
		}
	}
	return Hooks{}
}

// Boot runs Init then Start in sequence. On failure, already-started modules
// are rolled back (Stop + Shutdown) by the underlying Lifecycle. The context
// is propagated to every hook. (MOD-012/013.)
func (e *Environment) Boot(ctx context.Context) error {
	if e == nil {
		return errors.New("modules: nil environment")
	}
	if err := e.Lifecycle.Init(ctx, e.Registry); err != nil {
		return err
	}
	return e.Lifecycle.Start(ctx, e.Registry)
}

// Close runs Stop then Shutdown in sequence. Errors are collected; the first
// error is returned (matches Lifecycle.Stop/Shutdown semantics). A nil error
// means every hook completed cleanly. (MOD-012/013, reverse-init ordering.)
func (e *Environment) Close(ctx context.Context) error {
	if e == nil {
		return errors.New("modules: nil environment")
	}
	err := e.Lifecycle.Stop(ctx, e.Registry)
	if err2 := e.Lifecycle.Shutdown(ctx, e.Registry); err == nil {
		err = err2
	}
	return err
}

// LoadEnvironment scans `<root>/modules/*/ogon.module.yaml` and constructs an
// Environment from the manifests found. (MOD-001/002/017 + MOD-006..013.)
// Modules without hooks can still be loaded — only their manifest is
// registered. Returns an empty Environment if the modules directory is
// missing or contains no manifests.
//
// This is the primary entry point used by `ogon dev` and `ogon run` to wire
// declared modules into the host process before serving traffic.
func LoadEnvironment(root string) (*Environment, error) {
	env := NewEnvironment()
	modulesDir := filepath.Join(root, "modules")
	manifests, err := LoadDir(modulesDir)
	if err != nil {
		// A missing modules dir is not an error — it just means no modules
		// are declared. Surface any other read/parse error.
		if !os.IsNotExist(err) && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("modules: load %s: %w", modulesDir, err)
		}
		return env, nil
	}
	for _, mf := range manifests {
		if err := env.Registry.Register(mf, FrameworkVersion); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// Install writes a LockEntry to <root>/ogon.lock (MOD-021). If an entry with
// the same name exists, it is replaced. The lockfile is created if missing.
// The caller is responsible for actually fetching the module's source (Go
// module proxy, git clone, etc.) — this function only records the install in
// the lockfile so builds are reproducible.
func Install(root, name, version, source string) error {
	if !isValidName(name) {
		return fmt.Errorf("modules: invalid module name %q", name)
	}
	lockPath := LockfilePath(root)
	lock, err := LoadLock(lockPath)
	if err != nil {
		return err
	}
	lock.Add(LockEntry{Name: name, Version: version, Source: source})
	return lock.Save(lockPath)
}

// Uninstall removes a module from <root>/ogon.lock. Returns (true, nil) on
// success, (false, nil) if the module was not present. The caller should
// consult FindOrphans after a successful uninstall to surface orphaned
// config / contribution entries (MOD-035).
func Uninstall(root, name string) (bool, error) {
	if !isValidName(name) {
		return false, fmt.Errorf("modules: invalid module name %q", name)
	}
	lockPath := LockfilePath(root)
	lock, err := LoadLock(lockPath)
	if err != nil {
		return false, err
	}
	if _, ok := lock.Get(name); !ok {
		return false, nil
	}
	lock.Remove(name)
	if err := lock.Save(lockPath); err != nil {
		return false, err
	}
	return true, nil
}

// ValidateEnvironment walks a registry's manifests and confirms each one's
// ogon constraint is satisfied by frameworkVersion. Returns the first
// failure (with the offending module's name wrapped in). (MOD-003.)
func ValidateEnvironment(r *Registry, frameworkVersion string) error {
	if r == nil {
		return errors.New("modules: nil registry")
	}
	for _, m := range r.All() {
		if err := CheckOgonConstraint(m.Ogon, frameworkVersion); err != nil {
			return fmt.Errorf("modules: %s: %w", m.Name, err)
		}
	}
	return nil
}

// FindOrphans returns the names of modules registered in r that are no longer
// present in the lockfile. Used by `ogon remove` (MOD-035) to warn the user
// about orphaned config / contribution entries left behind by the removed
// module's dependents. The returned slice is sorted by name.
func FindOrphans(r *Registry, lock *Lockfile) []string {
	if r == nil || lock == nil {
		return nil
	}
	lockedNames := make(map[string]struct{}, len(lock.Entries))
	for _, e := range lock.Entries {
		lockedNames[e.Name] = struct{}{}
	}
	var orphans []string
	for _, m := range r.All() {
		if _, ok := lockedNames[m.Name]; !ok {
			orphans = append(orphans, m.Name)
		}
	}
	// r.All() is already sorted by name; no extra sort needed.
	return orphans
}

// Search wraps DefaultFirstPartyIndex().Search for the common CLI case
// (`ogon modules search <query>`). It returns the matching IndexEntries
// sorted by name. (MOD-034.)
func Search(query string) []IndexEntry {
	return DefaultFirstPartyIndex().Search(query)
}

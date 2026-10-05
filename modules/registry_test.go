// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for modules/registry.go (MOD-013): topological init order with
// cycle detection and missing-require errors.

package modules

import (
	"errors"
	"strings"
	"testing"
)

func m(name, ver, ogon string, requires ...string) *Manifest {
	return &Manifest{
		Name:     name,
		Version:  ver,
		Ogon:     ogon,
		Requires: requires,
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("a")
	if !ok {
		t.Fatal("not found")
	}
	if got.Version != "1.0.0" {
		t.Errorf("Version = %q", got.Version)
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	if err := r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0"); err == nil {
		t.Fatalf("expected duplicate error")
	}
}

func TestRegistryRegisterUnsatisfiedOgon(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(m("a", "1.0.0", ">=2.0"), "1.8.0"); err == nil {
		t.Fatalf("expected constraint error")
	}
}

func TestRegistryUnregister(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	r.Unregister("a")
	if _, ok := r.Get("a"); ok {
		t.Errorf("module still present after Unregister")
	}
	// No-op for unknown name.
	r.Unregister("nonexistent")
}

func TestRegistryInitOrder(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("c", "1.0.0", ">=1.8", "a", "b"), "1.8.0")
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("b", "1.0.0", ">=1.8", "a"), "1.8.0")
	order, err := r.InitOrder()
	if err != nil {
		t.Fatalf("InitOrder: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("len = %d, want 3", len(order))
	}
	names := []string{order[0].Name, order[1].Name, order[2].Name}
	if names[0] != "a" {
		t.Errorf("first = %q, want a", names[0])
	}
	if names[2] != "c" {
		t.Errorf("last = %q, want c", names[2])
	}
	// b must come after a and before c.
	if names[1] != "b" {
		t.Errorf("middle = %q, want b", names[1])
	}
}

func TestRegistryInitOrderMissingRequire(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8", "nonexistent"), "1.8.0")
	_, err := r.InitOrder()
	if err == nil {
		t.Fatalf("expected missing-require error")
	}
}

func TestRegistryInitOrderCycle(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8", "b"), "1.8.0")
	_ = r.Register(m("b", "1.0.0", ">=1.8", "a"), "1.8.0")
	_, err := r.InitOrder()
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q missing cycle", err.Error())
	}
}

func TestRegistryShutdownOrderReversed(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("b", "1.0.0", ">=1.8", "a"), "1.8.0")
	order, err := r.ShutdownOrder()
	if err != nil {
		t.Fatalf("ShutdownOrder: %v", err)
	}
	if order[0].Name != "b" || order[1].Name != "a" {
		t.Errorf("shutdown order = %v,%v; want b,a", order[0].Name, order[1].Name)
	}
}

func TestRegistryAllSorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("z", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("len = %d", len(all))
	}
	if all[0].Name != "a" || all[1].Name != "z" {
		t.Errorf("order = %v,%v", all[0].Name, all[1].Name)
	}
}

func TestRegistryNamesSorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("z", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	names := r.Names()
	if names[0] != "a" || names[1] != "z" {
		t.Errorf("names = %v", names)
	}
}

func TestRegistryProvides(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	m1 := m("a", "1.0.0", ">=1.8")
	m1.Provides = []string{ContributionRoutes}
	m2 := m("b", "1.0.0", ">=1.8")
	m2.Provides = []string{ContributionRoutes, ContributionConfig}
	_ = r.Register(m1, "1.8.0")
	_ = r.Register(m2, "1.8.0")
	got := r.Provides(ContributionRoutes)
	if len(got) != 2 {
		t.Errorf("Provides(routes) = %d, want 2", len(got))
	}
	got = r.Provides(ContributionConfig)
	if len(got) != 1 {
		t.Errorf("Provides(config) = %d, want 1", len(got))
	}
}

func TestNewRegistryFromManifests(t *testing.T) {
	t.Parallel()
	manifests := []*Manifest{
		m("a", "1.0.0", ">=1.8"),
		m("b", "1.0.0", ">=1.8", "a"),
	}
	r, err := NewRegistryFromManifests(manifests, "1.8.0")
	if err != nil {
		t.Fatalf("NewRegistryFromManifests: %v", err)
	}
	if len(r.All()) != 2 {
		t.Errorf("len = %d", len(r.All()))
	}
}

func TestNewRegistryFromManifestsDuplicate(t *testing.T) {
	t.Parallel()
	manifests := []*Manifest{
		m("a", "1.0.0", ">=1.8"),
		m("a", "1.0.0", ">=1.8"),
	}
	_, err := NewRegistryFromManifests(manifests, "1.8.0")
	if err == nil {
		t.Fatalf("expected duplicate error")
	}
	var _ = errors.Is
}

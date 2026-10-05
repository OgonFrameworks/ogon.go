// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// modules/registry.go: module registry with topological init-order
// resolution (MOD-013). The registry deduplicates modules by name, validates
// framework-version constraints, and exposes a deterministic init order.

package modules

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Registry is the in-memory module registry. Construction is via NewRegistry
// (empty) then Register, or NewRegistryFromManifests (bulk).
type Registry struct {
	modules map[string]*Manifest
	// order is the registration order — used as a stable tiebreaker when
	// topological order has ties.
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{modules: make(map[string]*Manifest)}
}

// NewRegistryFromManifests constructs a registry from a slice of manifests and
// validates framework-version constraints. Returns an error if duplicates or
// unsatisfied ogon constraints are found.
func NewRegistryFromManifests(manifests []*Manifest, frameworkVersion string) (*Registry, error) {
	r := NewRegistry()
	for _, m := range manifests {
		if err := r.Register(m, frameworkVersion); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds a module to the registry. Returns an error on duplicate name
// or unsatisfied ogon constraint.
func (r *Registry) Register(m *Manifest, frameworkVersion string) error {
	if m == nil {
		return errors.New("modules: nil manifest")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if _, exists := r.modules[m.Name]; exists {
		return fmt.Errorf("modules: duplicate module %q", m.Name)
	}
	if frameworkVersion != "" {
		if err := CheckOgonConstraint(m.Ogon, frameworkVersion); err != nil {
			return err
		}
	}
	r.modules[m.Name] = m
	r.order = append(r.order, m.Name)
	return nil
}

// Unregister removes a module by name. No-op if not present.
func (r *Registry) Unregister(name string) {
	if _, ok := r.modules[name]; !ok {
		return
	}
	delete(r.modules, name)
	out := r.order[:0]
	for _, n := range r.order {
		if n != name {
			out = append(out, n)
		}
	}
	r.order = out
}

// Get returns the manifest for a named module.
func (r *Registry) Get(name string) (*Manifest, bool) {
	m, ok := r.modules[name]
	return m, ok
}

// All returns the registered manifests sorted by Name.
func (r *Registry) All() []*Manifest {
	out := make([]*Manifest, 0, len(r.modules))
	for _, m := range r.modules {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns the registered module names sorted alphabetically.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.modules))
	for n := range r.modules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// InitOrder returns the manifests in topological order (MOD-013): a module's
// dependencies appear before it. Returns an error if a require is missing or
// a cycle exists.
func (r *Registry) InitOrder() ([]*Manifest, error) {
	// Validate requires are present.
	for _, m := range r.modules {
		for _, req := range m.Requires {
			if _, ok := r.modules[req]; !ok {
				return nil, fmt.Errorf("modules: %q requires unknown module %q", m.Name, req)
			}
		}
	}
	// DFS-based topological sort with cycle detection.
	color := map[string]int{} // 0=white 1=gray 2=black
	var order []string
	var stack []string
	var dfs func(name string) error
	dfs = func(name string) error {
		if color[name] == 1 {
			// cycle: report path.
			cycle := []string{}
			for i := len(stack) - 1; i >= 0; i-- {
				cycle = append([]string{stack[i]}, cycle...)
				if stack[i] == name {
					break
				}
			}
			cycle = append(cycle, name)
			return fmt.Errorf("modules: cycle detected: %s", strings.Join(cycle, " → "))
		}
		if color[name] == 2 {
			return nil
		}
		color[name] = 1
		stack = append(stack, name)
		m := r.modules[name]
		if m != nil {
			// Visit requires in sorted order for determinism.
			reqs := append([]string(nil), m.Requires...)
			sort.Strings(reqs)
			for _, req := range reqs {
				if err := dfs(req); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = 2
		order = append(order, name)
		return nil
	}
	// Visit modules in sorted name order for determinism.
	names := r.Names()
	for _, n := range names {
		if color[n] == 0 {
			if err := dfs(n); err != nil {
				return nil, err
			}
		}
	}
	out := make([]*Manifest, 0, len(order))
	for _, n := range order {
		out = append(out, r.modules[n])
	}
	return out, nil
}

// ShutdownOrder returns the manifests in reverse init order (DI-013-style).
func (r *Registry) ShutdownOrder() ([]*Manifest, error) {
	order, err := r.InitOrder()
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}

// Provides returns the names of modules that declare the given contribution
// (MOD-006..013). Sorted by name.
func (r *Registry) Provides(contribution string) []*Manifest {
	var out []*Manifest
	for _, m := range r.modules {
		if m.HasContribution(contribution) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// di/lifetimes.go: lifetime (singleton/scoped/transient) tracking and scope
// validation (DI-005/023). A singleton depending on a scoped provider is a
// generation error — the singleton would capture the first request's scoped
// instance and leak it across requests.

package di

import (
	"fmt"
	"strings"
)

// ScopeViolation describes a singleton→scoped dependency capture (DI-023).
type ScopeViolation struct {
	// Singleton is the provider that incorrectly captures a scoped dep.
	Singleton Provider
	// Scoped is the scoped provider being captured.
	Scoped Provider
}

// Error returns a human-readable scope violation message.
func (v ScopeViolation) Error() string {
	return fmt.Sprintf("di: scope violation: singleton %s depends on scoped %s (capture would leak across requests)",
		v.Singleton.Name, v.Scoped.Name)
}

// ScopeError aggregates one or more scope violations.
type ScopeError []ScopeViolation

func (s ScopeError) Error() string {
	if len(s) == 0 {
		return "di: scope violation"
	}
	parts := make([]string, 0, len(s))
	for _, v := range s {
		parts = append(parts, v.Error())
	}
	return strings.Join(parts, "; ")
}

// validateScopes walks every provider and reports any singleton that
// transitively depends on a scoped provider. Transients are allowed to
// depend on anything (they're per-call); scoped may depend on anything
// except transient→singleton capture is fine (singleton is just reused).
//
// The only forbidden edge is: Singleton → (Scoped | Transient that depends
// on Scoped). The direct check (Singleton → Scoped) and the transitive
// check (Singleton → Transient+ → Scoped) are reported once each; direct
// hits take precedence so a singleton with both a direct scoped dep and a
// transient path to the same scoped dep is reported only once.
func (g *Graph) validateScopes() error {
	var violations []ScopeViolation
	direct := map[Ref]bool{} // (singleton, scoped) pairs already reported
	// Direct: singleton → scoped.
	for _, p := range g.providers {
		if p.Lifetime != LifetimeSingleton {
			continue
		}
		for _, r := range g.requires[p.Provides.Key()] {
			rp, ok := g.providers[r]
			if !ok {
				continue
			}
			if rp.Lifetime == LifetimeScoped {
				violations = append(violations, ScopeViolation{Singleton: p, Scoped: rp})
				direct[scopeKey(p, rp)] = true
			}
		}
	}
	// Indirect: singleton → (transient)+ → scoped. Only descend through
	// transients; direct scoped deps were already reported above.
	for _, p := range g.providers {
		if p.Lifetime != LifetimeSingleton {
			continue
		}
		visited := map[Ref]bool{p.Provides.Key(): true}
		if scoped := g.firstScopedThroughTransientOnly(p.Provides.Key(), visited); scoped != nil {
			if direct[scopeKey(p, *scoped)] {
				continue
			}
			violations = append(violations, ScopeViolation{Singleton: p, Scoped: *scoped})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return ScopeError(violations)
}

// scopeKey returns a stable key for (singleton, scoped) pair dedup.
func scopeKey(singleton, scoped Provider) Ref {
	return Ref{PkgPath: singleton.PkgPath + "::" + singleton.Name, Name: scoped.Name}
}

// firstScopedThroughTransientOnly walks from start through transient
// providers only, returning the first scoped provider reached (or nil).
// Direct deps of start are NOT visited unless they are transient; the direct
// check already handles the non-transient scoped case.
func (g *Graph) firstScopedThroughTransientOnly(start Ref, visited map[Ref]bool) *Provider {
	for _, r := range g.requires[start] {
		if isBuiltinRef(r) {
			continue
		}
		rp, ok := g.providers[r]
		if !ok {
			continue
		}
		if visited[rp.Provides.Key()] {
			continue
		}
		visited[rp.Provides.Key()] = true
		// Only descend through transients.
		if rp.Lifetime != LifetimeTransient {
			continue
		}
		// Check the transient's direct deps for a scoped provider.
		for _, r2 := range g.requires[rp.Provides.Key()] {
			if isBuiltinRef(r2) {
				continue
			}
			rp2, ok := g.providers[r2]
			if !ok {
				continue
			}
			if visited[rp2.Provides.Key()] {
				continue
			}
			if rp2.Lifetime == LifetimeScoped {
				scoped := rp2
				return &scoped
			}
		}
		// Recurse through nested transients.
		if found := g.firstScopedThroughTransientOnly(rp.Provides.Key(), visited); found != nil {
			return found
		}
	}
	return nil
}

// LifetimeOf returns the lifetime for a provider key, or LifetimeSingleton if
// unknown. Used by explain output.
func (g *Graph) LifetimeOf(r Ref) Lifetime {
	if p, ok := g.providers[r]; ok {
		return p.Lifetime
	}
	return LifetimeSingleton
}

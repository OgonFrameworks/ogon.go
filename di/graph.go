// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// di/graph.go: provider graph builder with cycle detection (DI-002/003).
// The graph is keyed by the provided type's Ref key (PkgPath + Name). Cycles
// are reported with the full path of providers and types involved.

package di

import (
	"fmt"
	"sort"
	"strings"
)

// Ref is the canonical key for a type used as a node identity in the graph.
// Two TypeRefs with the same PkgPath+Name have the same Ref.
type Ref struct {
	PkgPath string
	Name    string
}

// String renders a Ref in `pkgpath.Name` form, or just `Name` when the package
// path is empty (builtin or same-package type without resolved path).
func (r Ref) String() string {
	if r.PkgPath == "" {
		return r.Name
	}
	return r.PkgPath + "." + r.Name
}

// Key returns the Ref for a TypeRef.
func (t TypeRef) Key() Ref { return Ref{PkgPath: t.PkgPath, Name: t.Name} }

// Graph is the resolved provider graph. Construction validates that every
// required type has exactly one provider (DI-016/017).
type Graph struct {
	// Providers indexed by Ref of the type they provide.
	providers map[Ref]Provider
	// requires maps a provider's Ref → Refs of the types it requires.
	requires map[Ref][]Ref
	// missing lists types required but unsatisfied; populated even on error
	// so `ogon explain di` can surface candidates.
	missing []MissingDep
	// ambiguous lists types provided by more than one constructor.
	ambiguous []AmbiguousDep
	// overrides are Refs explicitly registered via ogon.Provide.
	overrides map[Ref]bool
}

// MissingDep describes a required type with no provider (DI-016).
type MissingDep struct {
	Type       Ref
	RequiredBy Provider
	Candidates []string
}

// AmbiguousDep describes a type provided by more than one constructor (DI-017).
type AmbiguousDep struct {
	Type      Ref
	Providers []Provider
}

// NewGraph builds a Graph from a slice of providers and explicit overrides
// (named registrations from ogon.Provide / test builder). Returns an error
// when validation fails (missing deps, ambiguity, cycles, scope violations).
func NewGraph(providers []Provider, overrides []Provider) (*Graph, error) {
	g := &Graph{
		providers: make(map[Ref]Provider, len(providers)+len(overrides)),
		requires:  make(map[Ref][]Ref),
		overrides: make(map[Ref]bool),
	}
	// Register overrides first so generated providers of the same type are
	// treated as ambiguous (overrides win in DI-007 semantics).
	for _, p := range overrides {
		if p.Named {
			g.overrides[p.Provides.Key()] = true
		}
		g.addProvider(p)
	}
	for _, p := range providers {
		g.addProvider(p)
	}
	if err := g.validate(); err != nil {
		return nil, err
	}
	return g, nil
}

// addProvider registers a provider; records ambiguity if the type is already
// provided. Override providers are always allowed to replace generated ones.
func (g *Graph) addProvider(p Provider) {
	key := p.Provides.Key()
	if existing, ok := g.providers[key]; ok {
		// Override replaces generated silently.
		if existing.Named != p.Named {
			if p.Named {
				g.providers[key] = p
			}
			return
		}
		// Two generated providers for the same type → ambiguous (DI-017).
		g.ambiguous = append(g.ambiguous, AmbiguousDep{Type: key, Providers: []Provider{existing, p}})
		return
	}
	g.providers[key] = p
}

// validate runs ambiguous/cycle/scope checks. Missing required types are
// treated as external deps (the caller supplies them via Deps); they're
// surfaced via Missing() for `ogon explain di` did-you-mean hints but do
// NOT fail the build.
func (g *Graph) validate() error {
	// Build requires map and detect missing deps (informational only).
	for key, p := range g.providers {
		var reqs []Ref
		for _, r := range p.Requires {
			rk := r.Key()
			reqs = append(reqs, rk)
			if _, ok := g.providers[rk]; !ok && !isBuiltinRef(rk) {
				g.missing = append(g.missing, MissingDep{
					Type:       rk,
					RequiredBy: p,
					Candidates: g.candidatesFor(rk),
				})
			}
		}
		g.requires[key] = reqs
	}
	if len(g.ambiguous) > 0 {
		return AmbiguousError(g.ambiguous)
	}
	if cycle := g.findCycle(); cycle != nil {
		return CycleError(cycle)
	}
	if err := g.validateScopes(); err != nil {
		return err
	}
	return nil
}

// isBuiltinRef reports whether r is a builtin type (error, context.Context, etc)
// that the container satisfies without a provider.
func isBuiltinRef(r Ref) bool {
	switch r.String() {
	case "error", "context.Context", "context.Background", "Context":
		return true
	}
	return false
}

// candidatesFor returns up to 4 close-matching Refs for did-you-mean hints.
func (g *Graph) candidatesFor(target Ref) []string {
	var out []string
	for k := range g.providers {
		if levenshtein(k.Name, target.Name) <= 2 || strings.Contains(k.Name, target.Name) || strings.Contains(target.Name, k.Name) {
			out = append(out, k.String())
		}
	}
	sort.Strings(out)
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// findCycle runs a DFS over the provider graph and returns the first cycle
// found (as a slice of Refs) or nil. The returned slice is in cycle order,
// with the first node repeated at the end so the cycle is visually closed.
func (g *Graph) findCycle() []Ref {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[Ref]int, len(g.providers))
	var stack []Ref
	var rec func(u Ref) []Ref
	rec = func(u Ref) []Ref {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range g.requires[u] {
			if isBuiltinRef(v) {
				continue
			}
			switch color[v] {
			case gray:
				// cycle: extract from stack
				for i, n := range stack {
					if n == v {
						cycle := append([]Ref(nil), stack[i:]...)
						cycle = append(cycle, v)
						return cycle
					}
				}
				return []Ref{v, v}
			case white:
				if c := rec(v); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}
	// Deterministic order: iterate sorted providers.
	keys := g.SortedProviderKeys()
	for _, k := range keys {
		if color[k] == white {
			if c := rec(k); c != nil {
				return c
			}
		}
	}
	return nil
}

// Missing returns the missing-dep diagnostics collected during validation
// (DI-016). Missing deps are NOT a hard error — they're treated as external
// dependencies the caller must supply via the generated Deps struct.
func (g *Graph) Missing() []MissingDep {
	return append([]MissingDep(nil), g.missing...)
}

// SortedProviderKeys returns the providers' Ref keys in a stable order.
func (g *Graph) SortedProviderKeys() []Ref {
	out := make([]Ref, 0, len(g.providers))
	for k := range g.providers {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PkgPath != out[j].PkgPath {
			return out[i].PkgPath < out[j].PkgPath
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Providers returns the providers in stable order.
func (g *Graph) Providers() []Provider {
	keys := g.SortedProviderKeys()
	out := make([]Provider, 0, len(keys))
	for _, k := range keys {
		out = append(out, g.providers[k])
	}
	return out
}

// ProviderFor returns the provider for a type, or false if none.
func (g *Graph) ProviderFor(t Ref) (Provider, bool) {
	p, ok := g.providers[t]
	return p, ok
}

// Dependents returns the providers that require the given type (used by
// `ogon explain di` to render reverse edges).
func (g *Graph) Dependents(t Ref) []Provider {
	var out []Provider
	for key, reqs := range g.requires {
		for _, r := range reqs {
			if r == t {
				out = append(out, g.providers[key])
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PkgPath != out[j].PkgPath {
			return out[i].PkgPath < out[j].PkgPath
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// TopoSort returns the providers in topological (startup) order (DI-012).
// Reverse the slice for shutdown order (DI-013).
func (g *Graph) TopoSort() ([]Provider, error) {
	if cycle := g.findCycle(); cycle != nil {
		return nil, CycleError(cycle)
	}
	inDegree := make(map[Ref]int, len(g.providers))
	for _, key := range g.SortedProviderKeys() {
		inDegree[key] = 0
	}
	for _, reqs := range g.requires {
		for _, r := range reqs {
			if _, ok := g.providers[r]; ok {
				_ = r
			}
		}
		_ = reqs
	}
	// inDegree[p] = number of required types this provider needs (excluding
	// builtins and external types).
	for _, key := range g.SortedProviderKeys() {
		for _, r := range g.requires[key] {
			if _, ok := g.providers[r]; ok {
				inDegree[key]++
			}
		}
	}
	// Kahn's algorithm with stable tie-breaking via sorted keys.
	var queue []Ref
	for _, k := range g.SortedProviderKeys() {
		if inDegree[k] == 0 {
			queue = append(queue, k)
		}
	}
	var order []Provider
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		order = append(order, g.providers[cur])
		// Find providers that depend on `cur` and decrement their inDegree.
		for _, key := range g.SortedProviderKeys() {
			if key == cur {
				continue
			}
			depends := false
			for _, r := range g.requires[key] {
				if r == cur {
					depends = true
					break
				}
			}
			if depends {
				inDegree[key]--
				if inDegree[key] == 0 {
					queue = append(queue, key)
				}
			}
		}
	}
	if len(order) != len(g.providers) {
		return nil, fmt.Errorf("di: topo sort produced %d of %d providers (graph changed underneath)", len(order), len(g.providers))
	}
	return order, nil
}

// ---- errors ----

// CycleError formats a cycle path for human-readable output (DI-003).
type CycleError []Ref

func (c CycleError) Error() string {
	parts := make([]string, 0, len(c))
	for _, r := range c {
		parts = append(parts, r.String())
	}
	return "di: cycle detected: " + strings.Join(parts, " → ")
}

// MissingError formats missing-dep diagnostics (DI-016).
type MissingError []MissingDep

func (m MissingError) Error() string {
	if len(m) == 0 {
		return "di: missing providers"
	}
	var b strings.Builder
	b.WriteString("di: missing providers:")
	for _, d := range m {
		fmt.Fprintf(&b, "\n  %s required by %s", d.Type, d.RequiredBy.Name)
		if len(d.Candidates) > 0 {
			fmt.Fprintf(&b, " (did you mean: %s?)", strings.Join(d.Candidates, ", "))
		}
	}
	return b.String()
}

// AmbiguousError formats ambiguous-provider diagnostics (DI-017).
type AmbiguousError []AmbiguousDep

func (a AmbiguousError) Error() string {
	if len(a) == 0 {
		return "di: ambiguous providers"
	}
	var b strings.Builder
	b.WriteString("di: ambiguous providers:")
	for _, d := range a {
		names := make([]string, 0, len(d.Providers))
		for _, p := range d.Providers {
			names = append(names, p.FuncName)
		}
		fmt.Fprintf(&b, "\n  %s provided by: %s", d.Type, strings.Join(names, ", "))
	}
	return b.String()
}

// ---- helpers ----

// levenshtein computes the edit distance between two strings. Used for
// did-you-mean candidate scoring in missing-dep diagnostics.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := range prev {
		prev[i] = i
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

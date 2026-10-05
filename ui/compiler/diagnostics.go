// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Compile-time reactivity diagnostics (UI-095). Three classes:
//   - Illegal mutation: State/Shared written outside of Mount,
//     Handle<Event>, or Effect (the only mutation contexts).
//   - Derived cycle: a Derived's body reads itself transitively.
//   - Effect self-write: an Effect's body writes a primitive it
//     also reads — these can deadlock or spin.
//
// The diagnostics are returned as diag.Diag values (Part II.7)
// with stable Codes so tests and `ogon explain` can match them.

package compiler

import (
	"github.com/OgonFrameworks/ogon.go/diag"
)

// Diag is re-exported so callers can refer to compiler.Diag without
// importing the diag package directly. This keeps the surface area
// of the compiler package stable if the diag package is renamed.
type Diag = diag.Diag

// DiagnoseReactivity runs the UI-095 diagnostics against a graph.
func DiagnoseReactivity(g *DependencyGraph) []Diag {
	var out []Diag
	out = append(out, checkDerivedCycles(g)...)
	out = append(out, checkEffectSelfWrite(g)...)
	out = append(out, checkIllegalMutation(g)...)
	return out
}

// checkDerivedCycles flags a Derived that reads itself (directly or
// transitively). Self-reads are reported once per primitive.
func checkDerivedCycles(g *DependencyGraph) []Diag {
	var out []Diag
	for _, p := range g.Primitives {
		if p.Kind != PrimDerived {
			continue
		}
		seen := map[string]bool{}
		if cycle := findCycle(p.Name, p.Name, g, seen); cycle != "" {
			out = append(out, diag.Diag{
				Code:     "OGON-UI-095",
				Severity: diag.SeverityError,
				Title:    "Derived cycle detected",
				What:     "Derived " + p.Name + " forms a cycle: " + cycle,
				Why:      "Derived primitives must be acyclic; a cycle re-renders infinitely.",
				Fix:      []string{"Break the cycle by extracting a separate Derived or by removing the read."},
			})
		}
	}
	return out
}

// findCycle performs a depth-first search across Derived primitives'
// reads list, returning a textual trace when a cycle is detected.
// `origin` is the starting primitive; `start` is the current node
// being explored. We track visited nodes in `seen` to prevent
// infinite recursion on indirect cycles.
func findCycle(origin, start string, g *DependencyGraph, seen map[string]bool) string {
	seen[start] = true
	for _, p := range g.Primitives {
		if p.Kind != PrimDerived || p.Name != start {
			continue
		}
		for _, dep := range p.Reads {
			if dep == origin {
				return start + " → " + dep
			}
			if seen[dep] {
				continue
			}
			if sub := findCycle(origin, dep, g, seen); sub != "" {
				return start + " → " + sub
			}
		}
	}
	return ""
}

// checkEffectSelfWrite flags an Effect that reads and writes the
// same primitive — the runtime would loop the effect.
func checkEffectSelfWrite(g *DependencyGraph) []Diag {
	var out []Diag
	for _, p := range g.Primitives {
		if p.Kind != PrimEffect {
			continue
		}
		for _, r := range p.Reads {
			for _, w := range p.Writes {
				if r == w {
					out = append(out, diag.Diag{
						Code:     "OGON-UI-095",
						Severity: diag.SeverityError,
						Title:    "Effect self-write",
						What:     "Effect " + p.Name + " reads and writes " + r,
						Why:      "Effects that write their own dependencies cause infinite loops.",
						Fix:      []string{"Move the write into the event handler that triggers the effect."},
					})
				}
			}
		}
	}
	return out
}

// checkIllegalMutation flags any State/Shared mutation that is not
// declared inside Mount/Unmount/Handle<Event>/Effect.
//
// The graph doesn't carry per-line call sites (the line-based scan
// is approximate); we approximate by checking whether the component
// declares any handler methods. If the component has State but no
// mutation context, every State primitive gets a diagnostic.
func checkIllegalMutation(g *DependencyGraph) []Diag {
	var out []Diag
	hasHandler := false
	for _, p := range g.Primitives {
		if p.Kind == PrimEffect {
			hasHandler = true
			break
		}
	}
	for _, r := range g.Regions {
		if r.Handler != "" {
			hasHandler = true
			break
		}
	}
	if !hasHandler {
		for _, p := range g.Primitives {
			if p.Kind != PrimState && p.Kind != PrimShared {
				continue
			}
			out = append(out, diag.Diag{
				Code:     "OGON-UI-095",
				Severity: diag.SeverityWarning,
				Title:    "State never mutated",
				What:     stringOfKind(p.Kind) + " " + p.Name + " has no mutation context",
				Why:      "State and Shared are server-owned and may only be mutated in Mount/Unmount/Handle<Event>/Effect.",
				Fix:      []string{"Add a handler that mutates this primitive, or change it to Derived."},
			})
		}
	}
	return out
}

func stringOfKind(k PrimitiveKind) string { return k.String() }

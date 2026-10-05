// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// di/explain.go: `ogon explain di` graph render + Graphviz export (DI-014/015).
// The render is human-readable text; the Graphviz output is a stable DOT
// digraph keyed by provider Ref strings.

package di

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// ExplainOptions controls explain output shape.
type ExplainOptions struct {
	// Lifetime toggles whether to annotate each node with its lifetime.
	Lifetime bool
	// Source toggles whether to show file:line for each provider.
	Source bool
	// Dependents toggles whether to render reverse edges (dependents).
	Dependents bool
}

// DefaultExplainOptions returns the recommended explain options.
func DefaultExplainOptions() ExplainOptions {
	return ExplainOptions{Lifetime: true, Source: true, Dependents: false}
}

// Explain renders the graph as human-readable text. Output is stable for the
// same graph (DI-020 byte-stability). The format is:
//
//	providers (topological order):
//	  UserService          [singleton]    services.NewUserService @ app/services/user.go:12
//	    requires: *record.Pool, Config
//	    provides: *services.UserService
//	    dependents: OrderService
//	  ...
//	cycles: (none)
//	external deps: *record.Pool, Config
func Explain(g *Graph, w io.Writer, opts ExplainOptions) error {
	if g == nil {
		return fmt.Errorf("di: nil graph")
	}
	order, err := g.TopoSort()
	if err != nil {
		// Even on cycle/missing errors we render what we have so the user can
		// see the partial picture.
		if cycleErr, ok := err.(CycleError); ok {
			fmt.Fprintf(w, "cycle detected: %s\n\n", cycleErr.Error())
		} else {
			fmt.Fprintf(w, "graph error: %s\n\n", err.Error())
		}
		order = g.Providers()
	}
	if len(order) == 0 {
		fmt.Fprintln(w, "no providers registered")
		return nil
	}
	fmt.Fprintln(w, "providers (topological order):")
	for _, p := range order {
		writeProviderLine(w, p, g, opts)
	}
	// External deps summary.
	externalSeen := map[Ref]bool{}
	for _, p := range order {
		for _, r := range p.Requires {
			key := r.Key()
			if isBuiltinRef(key) {
				continue
			}
			if _, ok := g.providers[key]; ok {
				continue
			}
			externalSeen[key] = true
		}
	}
	if len(externalSeen) > 0 {
		refs := make([]Ref, 0, len(externalSeen))
		for r := range externalSeen {
			refs = append(refs, r)
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		parts := make([]string, 0, len(refs))
		for _, r := range refs {
			parts = append(parts, r.String())
		}
		fmt.Fprintf(w, "\nexternal deps: %s\n", strings.Join(parts, ", "))
	}
	return nil
}

// writeProviderLine emits the multi-line block for a single provider.
func writeProviderLine(w io.Writer, p Provider, g *Graph, opts ExplainOptions) {
	header := fmt.Sprintf("  %-20s", p.Name)
	if opts.Lifetime {
		header += fmt.Sprintf(" [%s]", p.Lifetime.String())
	}
	if opts.Source {
		header += fmt.Sprintf(" @ %s", p.Position())
	}
	fmt.Fprintln(w, header)
	if len(p.Requires) > 0 {
		reqs := make([]string, 0, len(p.Requires))
		for _, r := range p.Requires {
			reqs = append(reqs, r.Expr)
		}
		fmt.Fprintf(w, "    requires: %s\n", strings.Join(reqs, ", "))
	}
	fmt.Fprintf(w, "    provides: %s\n", p.Provides.Expr)
	if opts.Dependents {
		deps := g.Dependents(p.Provides.Key())
		if len(deps) > 0 {
			names := make([]string, 0, len(deps))
			for _, d := range deps {
				names = append(names, d.Name)
			}
			fmt.Fprintf(w, "    dependents: %s\n", strings.Join(names, ", "))
		}
	}
}

// Graphviz renders the graph as a DOT digraph (DI-015). Stable for the same
// graph (DI-020). Node IDs are the provider's Ref string, sanitized.
func Graphviz(g *Graph, w io.Writer) error {
	if g == nil {
		return fmt.Errorf("di: nil graph")
	}
	fmt.Fprintln(w, "digraph di {")
	fmt.Fprintln(w, "  rankdir=LR;")
	fmt.Fprintln(w, "  node [shape=record, fontname=\"Helvetica\"];")
	// Nodes (sorted by Ref for stability).
	for _, key := range g.SortedProviderKeys() {
		p := g.providers[key]
		label := fmt.Sprintf("{%s|%s|lifetime=%s}", escapeDot(p.Name), escapeDot(p.Provides.Expr), p.Lifetime.String())
		fmt.Fprintf(w, "  %q [label=%q];\n", dotNodeID(p), label)
	}
	// Edges: provider → provider for each required type that's also a provider.
	for _, key := range g.SortedProviderKeys() {
		p := g.providers[key]
		for _, r := range g.requires[key] {
			rp, ok := g.providers[r]
			if !ok {
				continue
			}
			fmt.Fprintf(w, "  %q -> %q;\n", dotNodeID(rp), dotNodeID(p))
		}
	}
	fmt.Fprintln(w, "}")
	return nil
}

// dotNodeID returns a stable, unique node id for a provider.
func dotNodeID(p Provider) string {
	return p.Provides.Key().String()
}

// escapeDot sanitises a string for inclusion in a DOT label. Backslashes,
// quotes, pipes and braces are escaped.
func escapeDot(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		`"`, `\"`,
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		"<", "\\<",
		">", "\\>",
	)
	return r.Replace(s)
}

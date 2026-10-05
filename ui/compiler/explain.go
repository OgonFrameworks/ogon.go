// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon explain component <name>` — renders the per-component
// dependency graph (UI-096). Output is a tree showing:
//   primitive → derived → region → event
// in the order the compiler analysed them.

package compiler

import (
	"fmt"
	"strings"
)

// ExplainComponent returns a human-readable description of the
// analysed dependency graph. The format is stable across builds
// so tests can assert against it.
func ExplainComponent(g *DependencyGraph) string {
	if g == nil {
		return "(no graph)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "component %s\n", g.Component)
	fmt.Fprintf(&b, "  primitives (%d):\n", len(g.Primitives))
	for _, p := range g.Primitives {
		fmt.Fprintf(&b, "    - %s %s", p.Kind, p.Name)
		if p.TypeName != "" {
			fmt.Fprintf(&b, " (%s)", p.TypeName)
		}
		if len(p.Reads) > 0 {
			fmt.Fprintf(&b, " reads=%v", p.Reads)
		}
		if len(p.Writes) > 0 {
			fmt.Fprintf(&b, " writes=%v", p.Writes)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "  regions (%d):\n", len(g.Regions))
	for _, r := range g.Regions {
		fmt.Fprintf(&b, "    - %s", r.Name)
		if r.Handler != "" {
			fmt.Fprintf(&b, " → event %s", r.Handler)
		}
		if len(r.Reads) > 0 {
			fmt.Fprintf(&b, " reads=%v", r.Reads)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ExplainJSON renders the same graph as a single-line JSON object
// so that LSP consumers (UI-086) and CI dashboards can parse it.
func ExplainJSON(g *DependencyGraph) string {
	if g == nil {
		return "{}"
	}
	var b strings.Builder
	b.WriteString(`{"component":"`)
	b.WriteString(g.Component)
	b.WriteString(`","primitives":[`)
	for i, p := range g.Primitives {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"kind":"%s","name":"%s"}`, p.Kind, p.Name)
	}
	b.WriteString(`],"regions":[`)
	for i, r := range g.Regions {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"name":"%s","handler":"%s"}`, r.Name, r.Handler)
	}
	b.WriteString(`]}`)
	return b.String()
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Reactivity analysis pass (UI-091/092/093/094). The compiler walks
// the `<go>` block to collect State/Derived/Effect/Shared/Local
// primitive declarations, then walks the `<template>` to mark every
// reactive region with the primitives it reads. The result is a
// per-component dependency graph used by:
//   - codegen_template.go (region → primitive mapping)
//   - diagnostics.go (illegal mutation, Derived cycle, effect self-write)
//   - explain.go (`ogon explain component <name>`)
//
// State, Derived, Effect, Shared, and Local are compiler-recognised
// primitives — the compiler rewrites their reads/writes into tracked
// operations and emits a graph. Nothing is tracked by runtime
// reflection.

package compiler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// PrimitiveKind enumerates the recognised reactivity primitives.
type PrimitiveKind int

const (
	PrimState PrimitiveKind = iota
	PrimDerived
	PrimEffect
	PrimShared
	PrimLocal
)

// String renders a stable primitive name for `ogon explain` output.
func (k PrimitiveKind) String() string {
	switch k {
	case PrimState:
		return "State"
	case PrimDerived:
		return "Derived"
	case PrimEffect:
		return "Effect"
	case PrimShared:
		return "Shared"
	case PrimLocal:
		return "Local"
	}
	return "Unknown"
}

// Primitive is a single State/Derived/Effect/Shared/Local declaration
// observed in the `<go>` block.
type Primitive struct {
	Kind     PrimitiveKind
	Name     string
	TypeName string   // optional, e.g. Shared[int]
	Reads    []string // primitives this Derived/Effect reads (deps)
	Writes   []string // primitives this Effect writes
	Line     int
	Col      int
}

// Region is a template subtree that reads one or more primitives.
type Region struct {
	Name       string
	ElementTag string
	Reads      []string // primitive names
	Handler    string   // for event regions (ogon:click/submit)
}

// DependencyGraph is the per-component analysis output (UI-091/096).
type DependencyGraph struct {
	Component  string
	Primitives []Primitive
	Regions    []Region
}

// AnalyzeReactivity runs the reactivity pass on a parsed `.ogon` file.
// Returns a graph and the set of compile-time diagnostics (UI-095).
func AnalyzeReactivity(f *File) (*DependencyGraph, []Diag) {
	g := &DependencyGraph{Component: f.Name}
	if f.Go == nil {
		return g, nil
	}
	// Parse the <go> body with go/parser. If the body is not valid
	// Go (which is fine — it's still being authored), fall back to
	// a line-based scan.
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, f.Name+".go", f.Go.Raw, parser.AllErrors)
	if err == nil {
		g.Primitives = collectPrimitivesAST(astFile)
	} else {
		g.Primitives = collectPrimitivesLineScan(f.Go.Raw)
	}
	// Walk the template to collect regions.
	visitElements(f.Root, func(el *Element) {
		r := Region{Name: el.Tag, ElementTag: el.Tag}
		for _, d := range el.Directives {
			if d.Binding == nil {
				continue
			}
			if d.Binding.Handler != "" {
				r.Handler = d.Binding.Handler
			}
			if d.Binding.State != "" {
				r.Reads = append(r.Reads, d.Binding.State)
			}
			if d.Binding.Expr != "" {
				r.Reads = append(r.Reads, exprReads(d.Binding.Expr)...)
			}
		}
		// Text interpolations count as regions too.
		for _, child := range el.Children {
			if ip, ok := child.(*Interp); ok {
				r.Reads = append(r.Reads, exprReads(ip.Expr)...)
			}
		}
		g.Regions = append(g.Regions, r)
	})
	// Run diagnostics (UI-095).
	diags := DiagnoseReactivity(g)
	return g, diags
}

// collectPrimitivesAST uses go/parser to find ogon.State/...
// call sites. We look for `ogon.X(...)` and `ogon.X[T](...)` forms.
func collectPrimitivesAST(astFile *ast.File) []Primitive {
	out := []Primitive{}
	ast.Inspect(astFile, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "ogon" {
			return true
		}
		p := Primitive{Name: sel.Sel.Name}
		switch sel.Sel.Name {
		case "State":
			p.Kind = PrimState
		case "Derived":
			p.Kind = PrimDerived
		case "Effect":
			p.Kind = PrimEffect
		case "Shared":
			p.Kind = PrimShared
		case "Local":
			p.Kind = PrimLocal
		default:
			return true
		}
		// Try to lift the name from the LHS of an assignment.
		if len(call.Args) > 0 {
			p.TypeName = fmtAny(call.Args[0])
		}
		out = append(out, p)
		return true
	})
	return out
}

// collectPrimitivesLineScan is a fallback when the body is partial.
func collectPrimitivesLineScan(raw string) []Primitive {
	out := []Primitive{}
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		for _, p := range []struct {
			kw   string
			kind PrimitiveKind
		}{
			{"ogon.State", PrimState},
			{"ogon.Derived", PrimDerived},
			{"ogon.Effect", PrimEffect},
			{"ogon.Shared", PrimShared},
			{"ogon.Local", PrimLocal},
		} {
			if !strings.Contains(line, p.kw) {
				continue
			}
			prim := Primitive{Kind: p.kind, Line: i + 1}
			// Try to lift the var name from the LHS.
			if eq := strings.Index(line, "="); eq > 0 {
				prim.Name = strings.TrimSpace(strings.Fields(line[:eq])[0])
			}
			out = append(out, prim)
		}
	}
	return out
}

// exprReads returns the list of State/Derived names referenced by an
// expression. We heuristically split on `.` and collect any token
// that starts with an uppercase letter or matches a known primitive
// pattern (e.g. State.X → "State.X").
func exprReads(expr string) []string {
	if expr == "" {
		return nil
	}
	out := []string{}
	// Split on whitespace and most punctuation except `.` and `_`.
	tok := strings.Builder{}
	flush := func() {
		if s := strings.TrimSpace(tok.String()); s != "" {
			out = append(out, s)
		}
		tok.Reset()
	}
	for _, r := range expr {
		switch {
		case r == '.' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			tok.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// fmtAny renders a Go AST argument as text for primitive metadata.
// We don't need a precise rendering — the field is only used in
// `ogon explain` output for developer convenience.
func fmtAny(v any) string { return toString(v) }

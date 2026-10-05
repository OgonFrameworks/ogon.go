// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Unknown-directive handling (UI-082). Any `ogon:<name>` attribute
// not in the closed set defined in directives.go is a compile error.
// This file exposes a stable error constructor used by both the
// parser and the diagnostics surface so that error messages stay
// consistent across pipeline phases.

package compiler

import "strings"

// UnknownDirectiveError is the canonical UI-082 diagnostic. It
// includes the directive name and the source position so the LSP
// (UI-086) and the CLI explain surface can surface it.
type UnknownDirectiveError struct {
	Name string
	Line int
	Col  int
}

// Error renders the diagnostic in the `OGON-UI-082` form expected
// by the test harness.
func (e *UnknownDirectiveError) Error() string {
	return "OGON-UI-082: unknown directive ogon:" + e.Name +
		" at " + itoa(e.Line) + ":" + itoa(e.Col)
}

// ClassifyDirective returns the directive kind for a name or the
// canonical unknown-directive error. Used by the reactivity pass
// to decide whether a binding should be tracked server-side.
func ClassifyDirective(name string) (kind string, ok bool) {
	spec, found := knownDirectives[name]
	if !found {
		return "", false
	}
	switch {
	case spec.HasHandler:
		return "handler", true
	case spec.HasState:
		return "state", true
	case spec.HasExpr:
		return "expr", true
	}
	return "expr", true
}

// DirectivePrefix returns the directive namespace without the `ogon:`
// prefix and a flag indicating whether the attribute was namespaced.
// Non-namespaced attributes are plain HTML attributes.
func DirectivePrefix(attr string) (name string, namespaced bool) {
	if !strings.HasPrefix(attr, "ogon:") {
		return attr, false
	}
	return strings.TrimPrefix(attr, "ogon:"), true
}

// NewUnknownDirectiveError is the canonical UI-082 constructor.
func NewUnknownDirectiveError(name string, line, col int) error {
	return &UnknownDirectiveError{Name: name, Line: line, Col: col}
}

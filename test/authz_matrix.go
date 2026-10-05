// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Authz matrix test gen (TEST-015/067). Produces a deterministic role×route
// matrix (allow/deny/own-data) for every authenticated route. Tests load
// the matrix from the authz policy, then assert per-cell behaviour with a
// parameterised test. Failures print the exact (role, route, method) cell.

package test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// AuthzDecision is the per-cell verdict.
type AuthzDecision int

const (
	// DecisionAllow: role may call (route, method) without restriction.
	DecisionAllow AuthzDecision = iota
	// DecisionDeny: role may NOT call (route, method).
	DecisionDeny
	// DecisionOwnData: role may call (route, method) only for resources
	// owned by the calling subject (enforced by the handler).
	DecisionOwnData
	// DecisionNotApplicable: route is not authenticated for this role
	// (e.g., public route, no role needed).
	DecisionNotApplicable
)

// String returns the canonical matrix cell label.
func (d AuthzDecision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	case DecisionOwnData:
		return "own"
	case DecisionNotApplicable:
		return "n/a"
	}
	return "?"
}

// AuthzCell is a single (role, route, method, decision) tuple.
type AuthzCell struct {
	Role     string
	Route    string
	Method   string
	Decision AuthzDecision
}

// AuthzMatrix is the table of cells plus the roles and routes axis.
type AuthzMatrix struct {
	Roles  []string
	Routes []RouteEntry
	Cells  []AuthzCell
}

// NewAuthzMatrix constructs an empty matrix.
func NewAuthzMatrix() *AuthzMatrix { return &AuthzMatrix{} }

// AddRole adds a role to the matrix's role axis.
func (m *AuthzMatrix) AddRole(role string) *AuthzMatrix {
	for _, r := range m.Roles {
		if r == role {
			return m
		}
	}
	m.Roles = append(m.Roles, role)
	sort.Strings(m.Roles)
	return m
}

// AddRoutes appends routes to the route axis.
func (m *AuthzMatrix) AddRoutes(rs []RouteEntry) *AuthzMatrix {
	m.Routes = append(m.Routes, rs...)
	m.sortRoutes()
	return m
}

// Set sets the decision for a (role, route, method) cell.
func (m *AuthzMatrix) Set(role string, route RouteEntry, decision AuthzDecision) *AuthzMatrix {
	m.AddRole(role)
	m.Cells = append(m.Cells, AuthzCell{
		Role:     role,
		Route:    route.Path,
		Method:   strings.ToUpper(route.Method),
		Decision: decision,
	})
	return m
}

func (m *AuthzMatrix) sortRoutes() {
	sort.SliceStable(m.Routes, func(i, j int) bool {
		if m.Routes[i].Path != m.Routes[j].Path {
			return m.Routes[i].Path < m.Routes[j].Path
		}
		return methodOrder(m.Routes[i].Method) < methodOrder(m.Routes[j].Method)
	})
}

// String renders the matrix as a TSV (tab-separated values) so diffs are
// reviewable in PRs. Format: ROLE \t METHOD \t PATH \t DECISION.
func (m *AuthzMatrix) String() string {
	var b strings.Builder
	// Header
	fmt.Fprintf(&b, "role\tmethod\troute\tdecision\n")
	cells := make([]AuthzCell, len(m.Cells))
	copy(cells, m.Cells)
	sort.SliceStable(cells, func(i, j int) bool {
		if cells[i].Role != cells[j].Role {
			return cells[i].Role < cells[j].Role
		}
		if cells[i].Route != cells[j].Route {
			return cells[i].Route < cells[j].Route
		}
		return methodOrder(cells[i].Method) < methodOrder(cells[j].Method)
	})
	for _, c := range cells {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", c.Role, c.Method, c.Route, c.Decision.String())
	}
	return b.String()
}

// AssertMatrix runs a parameterised test over every cell, calling fn to
// verify the recorded decision matches the actual runtime behaviour.
func AssertMatrix(t *testing.T, m *AuthzMatrix, fn func(t *testing.T, c AuthzCell)) {
	t.Helper()
	for _, c := range m.Cells {
		c := c
		t.Run(c.Role+"_"+c.Method+"_"+routeLabel(c.Route), func(t *testing.T) {
			t.Parallel()
			fn(t, c)
		})
	}
}

func routeLabel(p string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, p)
	return out
}

// DecisionFor returns the matrix's decision for a (role, route, method)
// triple, or DecisionNotApplicable if no cell matches.
func (m *AuthzMatrix) DecisionFor(role, route, method string) AuthzDecision {
	method = strings.ToUpper(method)
	for _, c := range m.Cells {
		if c.Role == role && c.Route == route && c.Method == method {
			return c.Decision
		}
	}
	return DecisionNotApplicable
}

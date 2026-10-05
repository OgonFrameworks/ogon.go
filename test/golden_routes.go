// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Golden route tests gen (TEST-013/030). Produces a deterministic, ordered
// dump of the router's route table so any new route, renamed path, or
// changed method shows up in the diff. The golden file lives in
// testdata/golden/routes.txt and is asserted on every CI run.

package test

import (
	"fmt"
	"sort"
	"strings"
)

// RouteEntry is a flattened route description used by both GoldenRoutes and
// OpenAPIDiff so the same fixture serves both TEST-013 and TEST-014/031.
type RouteEntry struct {
	Method     string
	Path       string
	Name       string
	Handler    string // opaque descriptor; tests fill in
	Middleware []string
}

// RouteTable is a set of RouteEntry.
type RouteTable struct {
	entries []RouteEntry
}

// NewRouteTable constructs an empty table.
func NewRouteTable() *RouteTable { return &RouteTable{} }

// Add appends an entry.
func (t *RouteTable) Add(e RouteEntry) *RouteTable {
	t.entries = append(t.entries, e)
	return t
}

// AddRange appends many entries.
func (t *RouteTable) AddRange(es []RouteEntry) *RouteTable {
	t.entries = append(t.entries, es...)
	return t
}

// Sort orders entries by (path, method) so dumps are deterministic.
func (t *RouteTable) Sort() *RouteTable {
	sort.SliceStable(t.entries, func(i, j int) bool {
		if t.entries[i].Path != t.entries[j].Path {
			return t.entries[i].Path < t.entries[j].Path
		}
		return methodOrder(t.entries[i].Method) < methodOrder(t.entries[j].Method)
	})
	return t
}

// String returns the canonical dump format:
//
//	METHOD path  [name]  (mw1,mw2)  handler
func (t *RouteTable) String() string {
	t.Sort()
	var b strings.Builder
	for _, e := range t.entries {
		mw := strings.Join(e.Middleware, ",")
		fmt.Fprintf(&b, "%-6s %-40s [%s] (%s) %s\n",
			e.Method, e.Path, e.Name, mw, e.Handler)
	}
	return b.String()
}

// GoldenDump returns the bytes to write to the golden file.
func (t *RouteTable) GoldenDump() []byte { return []byte(t.String()) }

// Diff returns the lines that differ between this table and another (used
// by tests to print a focused diff rather than the entire dump).
func (t *RouteTable) Diff(other *RouteTable) (added, removed []string) {
	a := t.lineSet()
	b := other.lineSet()
	for line := range a {
		if !b[line] {
			removed = append(removed, line)
		}
	}
	for line := range b {
		if !a[line] {
			added = append(added, line)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}

func (t *RouteTable) lineSet() map[string]bool {
	t.Sort()
	out := map[string]bool{}
	for _, e := range t.entries {
		out[fmt.Sprintf("%s %s", e.Method, e.Path)] = true
	}
	return out
}

// methodOrder is a stable ordering for common HTTP methods so dumps look
// natural (GET before POST before PATCH before DELETE).
func methodOrder(m string) int {
	switch strings.ToUpper(m) {
	case "GET":
		return 1
	case "POST":
		return 2
	case "PUT":
		return 3
	case "PATCH":
		return 4
	case "DELETE":
		return 5
	case "OPTIONS":
		return 6
	case "HEAD":
		return 7
	}
	return 99
}

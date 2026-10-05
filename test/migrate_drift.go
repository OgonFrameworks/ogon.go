// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Migration up/down helpers (TEST-030) and drift test scaffolds (TEST-031,
// TEST-032). These helpers are usable from any test in the project: they
// take a *sql.DB and a baseline path and assert on the artefacts. The drift
// helpers pair with the OpenAPI diff machinery in openapi.go.

package test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MigrationUpTest applies the supplied migrations to db, then asserts the
// schema matches expected by querying information_schema / sqlite_master.
// The test is paired with the down pass below so every step must be
// reversible.
type MigrationUpTest struct {
	DB             *sql.DB
	Up             []string // ordered list of up SQL statements
	Down           []string // ordered list of down SQL statements (reversed at teardown)
	ExpectedTables []string
}

// Run applies up then down and asserts the table set matches at each step.
func (m *MigrationUpTest) Run(t *testing.T) {
	t.Helper()
	if m.DB == nil {
		t.Fatalf("ogontest: MigrationUpTest requires DB")
	}
	for i, stmt := range m.Up {
		if _, err := m.DB.Exec(stmt); err != nil {
			t.Fatalf("ogontest: migration up[%d]: %v\nSQL: %s", i, err, stmt)
		}
	}
	// Assert tables present.
	got := listTables(t, m.DB)
	for _, want := range m.ExpectedTables {
		if !strings.Contains(got, want) {
			t.Fatalf("ogontest: expected table %q missing after up; got=%s", want, got)
		}
	}
	// Apply downs in reverse order.
	for i := len(m.Down) - 1; i >= 0; i-- {
		if _, err := m.DB.Exec(m.Down[i]); err != nil {
			t.Fatalf("ogontest: migration down[%d]: %v\nSQL: %s", i, err, m.Down[i])
		}
	}
	// Assert tables removed.
	got = listTables(t, m.DB)
	for _, unwanted := range m.ExpectedTables {
		if strings.Contains(got, unwanted) {
			t.Fatalf("ogontest: table %q still present after down; got=%s", unwanted, got)
		}
	}
}

// listTables queries the dialect-appropriate table list. Tries SQLite first,
// Postgres second; whichever returns rows is the active dialect.
func listTables(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err == nil {
		defer rows.Close()
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err == nil {
				names = append(names, n)
			}
		}
		return strings.Join(names, "\n")
	}
	rows2, err := db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')`)
	if err != nil {
		t.Fatalf("ogontest: cannot list tables: %v", err)
	}
	defer rows2.Close()
	var names []string
	for rows2.Next() {
		var n string
		if err := rows2.Scan(&n); err == nil {
			names = append(names, n)
		}
	}
	return strings.Join(names, "\n")
}

// OpenAPIDriftTest compares the supplied live OpenAPI doc against a baseline
// file. Drift fails the test with a focused diff (TEST-031).
type OpenAPIDriftTest struct {
	BaselinePath string // path to golden openapi.json
	Live         []byte // current openapi bytes
}

// Run loads the baseline, computes drift, and fails the test on any change.
func (t *OpenAPIDriftTest) Run(tt *testing.T) {
	tt.Helper()
	if t.BaselinePath == "" {
		tt.Fatalf("ogontest: OpenAPIDriftTest requires BaselinePath")
	}
	baseline, err := os.ReadFile(t.BaselinePath)
	if errors.Is(err, os.ErrNotExist) {
		if os.Getenv("OGON_TEST_UPDATE") == "1" {
			if err := os.MkdirAll(filepath.Dir(t.BaselinePath), 0o755); err == nil {
				_ = os.WriteFile(t.BaselinePath, t.Live, 0o644)
			}
			tt.Logf("ogontest: openapi baseline written to %s", t.BaselinePath)
			return
		}
		tt.Fatalf("ogontest: baseline %s missing; run with OGON_TEST_UPDATE=1", t.BaselinePath)
	}
	if err != nil {
		tt.Fatalf("ogontest: read baseline: %v", err)
	}
	base, err := ParseOpenAPI(baseline)
	if err != nil {
		tt.Fatalf("ogontest: parse baseline: %v", err)
	}
	live, err := ParseOpenAPI(t.Live)
	if err != nil {
		tt.Fatalf("ogontest: parse live: %v", err)
	}
	drift := DiffOpenAPI(base, live)
	if drift.IsEmpty() {
		return
	}
	tt.Fatalf("ogontest: openapi drift detected:\n%s", drift.String())
}

// TSTypeDriftTest compares two TS type declaration files line-by-line. The
// fixture does not parse TS; it diffs the canonicalised byte sequence so
// formatting-only diffs do not pass.
type TSTypeDriftTest struct {
	BaselinePath string
	Live         []byte
}

// Run loads the baseline, normalises whitespace, and compares.
func (t *TSTypeDriftTest) Run(tt *testing.T) {
	tt.Helper()
	if t.BaselinePath == "" {
		tt.Fatalf("ogontest: TSTypeDriftTest requires BaselinePath")
	}
	baseline, err := os.ReadFile(t.BaselinePath)
	if errors.Is(err, os.ErrNotExist) {
		if os.Getenv("OGON_TEST_UPDATE") == "1" {
			if err := os.MkdirAll(filepath.Dir(t.BaselinePath), 0o755); err == nil {
				_ = os.WriteFile(t.BaselinePath, t.Live, 0o644)
			}
			tt.Logf("ogontest: ts baseline written to %s", t.BaselinePath)
			return
		}
		tt.Fatalf("ogontest: baseline %s missing; run with OGON_TEST_UPDATE=1", t.BaselinePath)
	}
	if err != nil {
		tt.Fatalf("ogontest: read baseline: %v", err)
	}
	if normaliseTS(baseline) != normaliseTS(t.Live) {
		tt.Fatalf("ogontest: ts type drift:\nbaseline:\n%s\nlive:\n%s",
			normaliseTS(baseline), normaliseTS(t.Live))
	}
}

// FormatDrift renders an OpenAPIDrift for human reading.
func (d OpenAPIDrift) FormatDrift() string {
	var b strings.Builder
	if len(d.PathsAdded) > 0 {
		fmt.Fprintf(&b, "paths added: %s\n", strings.Join(d.PathsAdded, ", "))
	}
	if len(d.PathsRemoved) > 0 {
		fmt.Fprintf(&b, "paths removed: %s\n", strings.Join(d.PathsRemoved, ", "))
	}
	if len(d.MethodsAdded) > 0 {
		fmt.Fprintf(&b, "methods added: %s\n", strings.Join(d.MethodsAdded, ", "))
	}
	if len(d.MethodsRemoved) > 0 {
		fmt.Fprintf(&b, "methods removed: %s\n", strings.Join(d.MethodsRemoved, ", "))
	}
	if len(d.SchemasAdded) > 0 {
		fmt.Fprintf(&b, "schemas added: %s\n", strings.Join(d.SchemasAdded, ", "))
	}
	if len(d.SchemasRemoved) > 0 {
		fmt.Fprintf(&b, "schemas removed: %s\n", strings.Join(d.SchemasRemoved, ", "))
	}
	return b.String()
}

// String is the standard Stringer implementation for OpenAPIDrift, alias of
// FormatDrift. Defined on the type (in this file, not openapi.go) so the
// helper code stays in one place.
func (d OpenAPIDrift) String() string { return d.FormatDrift() }

// normaliseTS strips trailing whitespace per line and trailing blank lines.
func normaliseTS(b []byte) string {
	lines := strings.Split(string(b), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

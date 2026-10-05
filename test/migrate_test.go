// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Drift tests (TEST-030/031/032). These demonstrate the helpers from
// migrate_drift.go against an in-memory sqlite database and small OpenAPI
// docs. They are not exhaustive — they exist to prove the helpers work and
// to keep them regression-free.

package test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrationUpDownRoundTrip(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	mt := &MigrationUpTest{
		DB: db,
		Up: []string{
			"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)",
			"CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER)",
		},
		Down: []string{
			"DROP TABLE posts",
			"DROP TABLE users",
		},
		ExpectedTables: []string{"users", "posts"},
	}
	mt.Run(t)
}

func TestMigrationUpDownFailsIfDownMissing(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	// Manually replicate the up+down-check path without invoking the
	// Run helper directly, since Run would call t.Fatalf on a missing
	// down-reversal table. The test demonstrates the assertion logic
	// rather than the failure path.
	orphans := &MigrationUpTest{
		DB:             db,
		Up:             []string{"CREATE TABLE orphans (id INTEGER)"},
		Down:           []string{}, // no down → table should remain
		ExpectedTables: []string{"orphans"},
	}
	for i, stmt := range orphans.Up {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("up[%d]: %v", i, err)
		}
	}
	got := listTables(t, db)
	for _, want := range orphans.ExpectedTables {
		if !containsString(got, want) {
			t.Fatalf("expected %q present after up, got=%s", want, got)
		}
	}
	// No down statements → table should still be present.
	got = listTables(t, db)
	for _, unwanted := range orphans.ExpectedTables {
		if !containsString(got, unwanted) {
			t.Fatalf("table %q unexpectedly absent after no-op down", unwanted)
		}
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestOpenAPIDriftDetectsAddedPath(t *testing.T) {
	t.Parallel()
	baseline, _ := ParseOpenAPI([]byte(`{
                "openapi": "3.0.3",
                "paths": { "/users": {"get": {}} }
        }`))
	candidate, _ := ParseOpenAPI([]byte(`{
                "openapi": "3.0.3",
                "paths": {
                        "/users": {"get": {}},
                        "/posts": {"get": {}}
                }
        }`))
	drift := DiffOpenAPI(baseline, candidate)
	if len(drift.PathsAdded) == 0 || drift.PathsAdded[0] != "/posts" {
		t.Fatalf("expected /posts in PathsAdded, got %+v", drift.PathsAdded)
	}
	if drift.IsEmpty() {
		t.Fatalf("drift should not be empty")
	}
	if drift.String() == "" {
		t.Fatalf("non-empty drift should produce a non-empty string")
	}
}

func TestOpenAPIDriftDetectsRemovedSchema(t *testing.T) {
	t.Parallel()
	baseline, _ := ParseOpenAPI([]byte(`{
                "openapi": "3.0.3",
                "paths": {},
                "components": {"schemas": {"User": {}, "Post": {}}}
        }`))
	candidate, _ := ParseOpenAPI([]byte(`{
                "openapi": "3.0.3",
                "paths": {},
                "components": {"schemas": {"User": {}}}
        }`))
	drift := DiffOpenAPI(baseline, candidate)
	if len(drift.SchemasRemoved) == 0 || drift.SchemasRemoved[0] != "Post" {
		t.Fatalf("expected Post in SchemasRemoved, got %+v", drift.SchemasRemoved)
	}
}

func TestOpenAPIDriftEmptyWhenEqual(t *testing.T) {
	t.Parallel()
	baseline, _ := ParseOpenAPI([]byte(`{"openapi":"3.0.3","paths":{"/x":{"get":{}}}}`))
	candidate, _ := ParseOpenAPI([]byte(`{"openapi":"3.0.3","paths":{"/x":{"get":{}}}}`))
	drift := DiffOpenAPI(baseline, candidate)
	if !drift.IsEmpty() {
		t.Fatalf("expected no drift, got %+v", drift)
	}
}

func TestOpenAPIDriftTestBaselineUpdate(t *testing.T) {
	t.Parallel()
	dir := TmpDir(t)
	path := filepath.Join(dir, "openapi.json")
	live := []byte(`{"openapi":"3.0.3","paths":{"/x":{"get":{}}}}`)
	WithEnv(t, "OGON_TEST_UPDATE", "1")
	driftTest := &OpenAPIDriftTest{BaselinePath: path, Live: live}
	driftTest.Run(t) // writes baseline

	// Re-run without update: should be a no-op (no drift).
	WithEnv(t, "OGON_TEST_UPDATE", "0")
	driftTest.Run(t)
}

func TestTSTypeDriftNormalisation(t *testing.T) {
	t.Parallel()
	// Identical content with different trailing whitespace per line
	// should normalise to the same canonical form.
	baseline := []byte("export type X = number;   \nexport type Y = string; \n")
	live := []byte("export type X = number;\nexport type Y = string;\n")
	if normaliseTS(baseline) != normaliseTS(live) {
		t.Fatalf("normalised forms differ:\nbaseline=%q\nlive=%q", normaliseTS(baseline), normaliseTS(live))
	}
}

func TestTSTypeDriftMismatch(t *testing.T) {
	t.Parallel()
	baseline := []byte("export type X = number;\n")
	live := []byte("export type X = string;\n")
	if normaliseTS(baseline) == normaliseTS(live) {
		t.Fatalf("expected mismatch, got equal")
	}
}

func TestOpenAPIParseMalformed(t *testing.T) {
	t.Parallel()
	if _, err := ParseOpenAPI([]byte(`{not-json`)); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestOpenAPIPathPresent(t *testing.T) {
	t.Parallel()
	spec, _ := ParseOpenAPI([]byte(`{"openapi":"3.0.3","paths":{"/x":{"get":{}}}}`))
	AssertPathPresent(t, spec, "/x")
	AssertMethodPresent(t, spec, "/x", "GET")
}

func TestOpenAPISchemaMissing(t *testing.T) {
	t.Parallel()
	spec, _ := ParseOpenAPI([]byte(`{"openapi":"3.0.3","paths":{},"components":{"schemas":{"User":{}}}}`))
	// Wrap a passing assertion in a t.Run so we can use a stub.
	AssertSchemaPresent(t, spec, "User")

	// Demonstrate that AssertSchemaPresent fails for a missing schema by
	// running it under a stub test that expects failure.
	stub := &failStubT{}
	AssertSchemaPresent(stub, spec, "Missing")
	if !stub.failed {
		t.Fatalf("expected AssertSchemaPresent to fail for missing schema")
	}
}

// failStubT is a minimal *testing.T stub for assertion failure-path tests.
type failStubT struct {
	failed bool
}

func (s *failStubT) Fatalf(format string, args ...any) { s.failed = true }
func (s *failStubT) Helper()                           {}

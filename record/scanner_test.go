// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package record

import (
	"reflect"
	"testing"

	"github.com/OgonFrameworks/ogon.go/record/types"
)

// scannerUser mirrors a developer's app/models/user.go — used by the
// scanner and query tests. Reusing the model here avoids a separate
// testdata package.
type scannerUser struct {
	BaseModel
	Email    string        `ogon:"column:email,type:text,unique,email"`
	Metadata types.JSONB   `ogon:"column:metadata,jsonb"`
	Balance  types.Decimal `ogon:"column:balance"`
	TenantID string        `ogon:"column:tenant_id,index,rls"`
}

// newTestDriver opens a fresh private in-memory SQLite DB for the test.
// Uses plain ":memory:" (no cache=shared) so each *sql.DB / connection
// owns a private in-memory database — there is no cross-test data bleed.
// SetMaxOpenConns(1) (applied in NewSQLiteDriver) keeps one stable
// connection alive for the test's lifetime.
func newTestDriver(t *testing.T) Driver {
	t.Helper()
	d, err := NewSQLiteDriver(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteDriver: %v", err)
	}
	return d
}

// scannerUserTableName resolves the registered model's table name. Tests
// create and populate this table directly since the migration engine is
// tested separately.
func scannerUserTableName(t *testing.T) string {
	t.Helper()
	mm := Lookup("scannerUser")
	if mm == nil {
		t.Fatal("scannerUser not registered")
	}
	return mm.Table
}

func ensureUserTable(t *testing.T, d Driver) {
	t.Helper()
	table := scannerUserTableName(t)
	_, err := d.Exec(t.Context(), `CREATE TABLE IF NOT EXISTS "`+table+`" (
  id TEXT PRIMARY KEY,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  deleted_at DATETIME,
  email TEXT NOT NULL,
  metadata TEXT,
  balance TEXT,
  tenant_id TEXT
);`)
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
}

func TestScanReflect_Named(t *testing.T) {
	d := newTestDriver(t)
	ensureUserTable(t, d)
	uid := types.NewUUIDv4()
	_, err := d.Exec(t.Context(), `INSERT INTO scanner_user(id,created_at,updated_at,email,metadata,tenant_id,balance) VALUES (?,?,?,?,?,?,?);`,
		uid.String(), "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "a@b.c", `{"k":"v"}`, "t1", "12.34")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	rows, err := d.Query(t.Context(), "SELECT id, created_at, updated_at, deleted_at, email, metadata, tenant_id, balance FROM scanner_user LIMIT 1;")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no rows")
	}
	var u scannerUser
	if err := scanReflect(rows, &u); err != nil {
		t.Fatalf("scanReflect: %v", err)
	}
	if u.Email != "a@b.c" {
		t.Errorf("Email = %q", u.Email)
	}
	if u.TenantID != "t1" {
		t.Errorf("TenantID = %q", u.TenantID)
	}
}

func TestScanReflect_PositionalFallback(t *testing.T) {
	// Unregistered struct: positional scan by field index works.
	d := newTestDriver(t)
	if _, err := d.Exec(t.Context(), `CREATE TABLE pos (a TEXT, b TEXT);`); err != nil {
		t.Fatalf("create pos: %v", err)
	}
	if _, err := d.Exec(t.Context(), `INSERT INTO pos VALUES ('x','y');`); err != nil {
		t.Fatalf("insert pos: %v", err)
	}
	rows, err := d.Query(t.Context(), "SELECT a, b FROM pos;")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no rows")
	}
	type Pos struct {
		A string
		B string
	}
	var p Pos
	if err := scanReflect(rows, &p); err != nil {
		t.Fatalf("scanReflect: %v", err)
	}
	if p.A != "x" || p.B != "y" {
		t.Errorf("got %+v", p)
	}
}

func TestScanReflect_NonStructDest(t *testing.T) {
	d := newTestDriver(t)
	if _, err := d.Exec(t.Context(), `CREATE TABLE ns (v INTEGER);`); err != nil {
		t.Fatalf("create ns: %v", err)
	}
	if _, err := d.Exec(t.Context(), `INSERT INTO ns VALUES (42);`); err != nil {
		t.Fatalf("insert ns: %v", err)
	}
	rows, err := d.Query(t.Context(), "SELECT v FROM ns;")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no rows")
	}
	var v int
	if err := scanReflect(rows, &v); err != nil {
		t.Fatalf("scanReflect int: %v", err)
	}
	if v != 42 {
		t.Errorf("v = %d", v)
	}
}

func TestScanReflect_NilDest(t *testing.T) {
	if err := scanReflect(nil, nil); err == nil {
		t.Error("expected error on nil dest")
	}
}

func TestLookupByReflect_Registered(t *testing.T) {
	mm, err := Register(scannerUser{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	zero := scannerUser{}
	got, err := lookupByReflect(zero)
	if err != nil {
		t.Fatalf("lookupByReflect: %v", err)
	}
	if got == nil || got != mm {
		t.Error("lookup did not return registered meta")
	}
	if got.Table != "scanner_user" {
		t.Errorf("Table = %q, want scanner_user", got.Table)
	}
	if got.PK == nil || got.PK.Column != "id" {
		t.Errorf("PK = %+v", got.PK)
	}
}

func TestRegistry_RejectsBadInput(t *testing.T) {
	if _, err := Register(nil); err == nil {
		t.Error("expected nil-model error")
	}
	if _, err := Register("not a struct"); err == nil {
		t.Error("expected non-struct error")
	}
}

func TestRegistry_NonPointerModel(t *testing.T) {
	if _, err := Register(42); err == nil {
		t.Error("expected error on int")
	}
}

// sanity: reflect.TypeOf works on the scannerUser zero value
func TestReflectTypeof(t *testing.T) {
	v := scannerUser{}
	rt := reflect.TypeOf(v)
	if rt.Name() != "scannerUser" {
		t.Errorf("Name = %s", rt.Name())
	}
}

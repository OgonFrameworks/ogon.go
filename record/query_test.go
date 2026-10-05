// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package record

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/record/types"
)

// ensureModelRegistered registers scannerUser and installs a fresh
// test pool/driver named "querytest". Each invocation gets a clean DB.
func ensureModelRegistered(t *testing.T) Driver {
	t.Helper()
	if Lookup("scannerUser") == nil {
		if _, err := Register(scannerUser{}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	d := newTestDriver(t)
	if err := RegisterPool("querytest", d); err != nil {
		t.Fatalf("RegisterPool: %v", err)
	}
	ensureUserTable(t, d)
	return d
}

func insertScannerUser(t *testing.T, d Driver, email, tenant string) string {
	t.Helper()
	uid := types.NewUUIDv4()
	_, err := d.Exec(context.Background(), `INSERT INTO scanner_user(id,created_at,updated_at,email,metadata,tenant_id,balance) VALUES (?,?,?,?,?,?,?);`,
		uid.String(), "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", email, `{"k":"v"}`, tenant, "12.34")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	return uid.String()
}

func TestQuery_AllFiltersByTenant(t *testing.T) {
	d := ensureModelRegistered(t)
	insertScannerUser(t, d, "a@x.com", "t1")
	insertScannerUser(t, d, "b@x.com", "t2")

	users, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "t1")).
		All(context.Background())
	if err != nil {
		t.Fatalf("Query.All: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("len(users) = %d, want 1", len(users))
	}
	if users[0].Email != "a@x.com" {
		t.Errorf("Email = %q", users[0].Email)
	}
}

func TestQuery_OrderByAndLimit(t *testing.T) {
	d := ensureModelRegistered(t)
	for _, e := range []string{"c@x", "a@x", "b@x"} {
		insertScannerUser(t, d, e, "tx")
	}
	users, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "tx")).
		OrderBy("email").
		Limit(2).
		All(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("len = %d, want 2", len(users))
	}
	if users[0].Email != "a@x" {
		t.Errorf("first email = %q, want a@x", users[0].Email)
	}
	if users[1].Email != "b@x" {
		t.Errorf("second email = %q, want b@x", users[1].Email)
	}
}

func TestQuery_Count(t *testing.T) {
	d := ensureModelRegistered(t)
	insertScannerUser(t, d, "c1@x", "tc")
	insertScannerUser(t, d, "c2@x", "tc")
	n, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "tc")).
		Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n < 2 {
		t.Errorf("Count = %d, want >=2", n)
	}
}

func TestQuery_OneNoRows(t *testing.T) {
	ensureModelRegistered(t)
	_, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("email", "nope@nope.nope")).
		One(context.Background())
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("err = %v, want ErrNoRows", err)
	}
}

func TestQuery_Iter(t *testing.T) {
	d := ensureModelRegistered(t)
	for i := 0; i < 3; i++ {
		insertScannerUser(t, d, "iter"+string(rune('a'+i))+"@x", "ti")
	}
	count := 0
	for _, err := range NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "ti")).
		Iter(context.Background()) {
		if err != nil {
			t.Fatalf("Iter: %v", err)
		}
		count++
	}
	if count != 3 {
		t.Errorf("Iter count = %d, want 3", count)
	}
}

func TestQuery_UnscopedIncludesDeleted(t *testing.T) {
	d := ensureModelRegistered(t)
	uid := insertScannerUser(t, d, "del@x", "td")
	_, err := d.Exec(context.Background(), `UPDATE scanner_user SET deleted_at = '2026-01-02T00:00:00Z' WHERE id = ?;`, uid)
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	live, _ := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "td")).
		All(context.Background())
	all, _ := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "td")).
		Unscoped().
		All(context.Background())
	if len(live) != 0 {
		t.Errorf("live count = %d, want 0 (soft-deleted)", len(live))
	}
	if len(all) != 1 {
		t.Errorf("unscoped count = %d, want 1", len(all))
	}
}

func TestQuery_InEmptyIsAlwaysFalse(t *testing.T) {
	ensureModelRegistered(t)
	// Empty IN short-circuits to a 1=0 guard via rawCondition.
	q := NewQuery[scannerUser]().Pool("querytest").Where(In("tenant_id"))
	users, err := q.All(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("empty IN returned %d rows", len(users))
	}
}

func TestQuery_BuildSQLInjectionGuard(t *testing.T) {
	ensureModelRegistered(t)
	sqlStr, args, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("email", "'; DROP TABLE users; --")).
		Limit(1).
		buildSQL(DialectSQLite)
	if err != nil {
		t.Fatalf("buildSQL: %v", err)
	}
	// SQL must NOT contain the literal payload — only a ? placeholder.
	if strings.Contains(sqlStr, "DROP TABLE") {
		t.Fatalf("SQL contains injected DROP TABLE: %s", sqlStr)
	}
	if len(args) != 2 {
		t.Errorf("args count = %d, want 2", len(args))
	}
}

func TestQuery_NoPoolReturnsDiag(t *testing.T) {
	ensureModelRegistered(t)
	_, err := NewQuery[scannerUser]().
		Pool("no-such-pool").
		All(context.Background())
	if err == nil {
		t.Fatal("expected error on missing pool")
	}
}

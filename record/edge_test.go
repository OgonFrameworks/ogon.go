// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Edge cases for the record package (P14 bug-bounty / DATA-008).
// Each test exercises one adversarial input shape: nil pool, empty
// result, oversized IN clause, very long string value, etc.

package record

import (
	"context"
	"strings"
	"testing"
)

// TestEdgeQueryNilPool — Query against a pool name that was never
// registered. Must return a non-nil error (diag-typed), never panic.
func TestEdgeQueryNilPool(t *testing.T) {
	if Lookup("scannerUser") == nil {
		if _, err := Register(scannerUser{}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("query on nil pool panicked: %v", r)
		}
	}()
	_, err := NewQuery[scannerUser]().
		Pool("nonexistent-pool-name-edge").
		All(context.Background())
	if err == nil {
		t.Fatal("expected error on nil pool, got nil")
	}
}

// TestEdgeQueryEmptyIN — IN() with no args must short-circuit to
// 1=0 (always-false). Must not panic, not produce invalid SQL.
func TestEdgeQueryEmptyIN(t *testing.T) {
	d := ensureModelRegistered(t)

	// Seed at least one row so the test would actually see it without
	// the empty-IN guard.
	insertScannerUser(t, d, "edge@x.com", "t1")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("empty IN panicked: %v", r)
		}
	}()

	users, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(In("tenant_id")).
		All(context.Background())
	if err != nil {
		t.Fatalf("empty IN returned err: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("empty IN returned %d rows; want 0 (always-false)", len(users))
	}
}

// TestEdgeQueryHugeINClause — IN with 5000 values must execute
// without panicking. The driver bound-check is the actual security
// surface; the builder just needs to not choke.
func TestEdgeQueryHugeINClause(t *testing.T) {
	d := ensureModelRegistered(t)
	insertScannerUser(t, d, "huge@x.com", "huge-tenant")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("huge IN panicked: %v", r)
		}
	}()

	// 5000 unique tenant values — only one is real, the rest are noise.
	args := make([]any, 0, 5000)
	for i := 0; i < 5000; i++ {
		args = append(args, "tenant-"+strings.Repeat("t", i%4+1)+string(rune('A'+(i%26))))
	}
	args = append(args, "huge-tenant") // the real one

	users, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(In("tenant_id", args...)).
		All(context.Background())
	if err != nil {
		t.Fatalf("huge IN returned err: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("huge IN returned %d rows; want 1", len(users))
	}
}

// TestEdgeQueryVeryLongStringValue — A column value that is very long
// must round-trip without truncation or panic.
func TestEdgeQueryVeryLongStringValue(t *testing.T) {
	d := ensureModelRegistered(t)
	longEmail := strings.Repeat("a", 4096) + "@x.com"
	insertScannerUser(t, d, longEmail, "long-tenant")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("long value query panicked: %v", r)
		}
	}()

	users, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("email", longEmail)).
		All(context.Background())
	if err != nil {
		t.Fatalf("long value query returned err: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("long value returned %d rows; want 1", len(users))
	}
	if users[0].Email != longEmail {
		t.Fatalf("email mismatch: got len=%d, want %d", len(users[0].Email), len(longEmail))
	}
}

// TestEdgeQueryEmptyResult — query against a populated table that
// legitimately returns zero rows. Must return (empty slice, nil),
// never (nil, nil) or panic.
func TestEdgeQueryEmptyResult(t *testing.T) {
	ensureModelRegistered(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("empty result panicked: %v", r)
		}
	}()

	users, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "nonexistent-tenant-edge")).
		All(context.Background())
	if err != nil {
		t.Fatalf("empty result err: %v", err)
	}
	if users == nil {
		t.Fatal("All returned nil slice; want empty non-nil")
	}
	if len(users) != 0 {
		t.Fatalf("got %d rows; want 0", len(users))
	}
}

// TestEdgeQueryOneNoRowsSpecial — One() on a table with no rows
// must return a "no rows" error that callers can errors.Is against
// (or at minimum, a non-nil error). Must not panic.
func TestEdgeQueryOneNoRowsSpecial(t *testing.T) {
	ensureModelRegistered(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("One no rows panicked: %v", r)
		}
	}()

	_, err := NewQuery[scannerUser]().
		Pool("querytest").
		Where(Eq("tenant_id", "definitely-missing-edge")).
		One(context.Background())
	if err == nil {
		t.Fatal("One on missing row returned nil error")
	}
}

// TestEdgeQueryNegativeLimit — A negative limit must be a no-op
// (the builder's `if q.limit > 0` guard), not a panic.
func TestEdgeQueryNegativeLimit(t *testing.T) {
	ensureModelRegistered(t)
	d := newTestDriver(t)
	if err := RegisterPool("querytest-edge-neg", d); err != nil {
		t.Fatalf("RegisterPool: %v", err)
	}
	ensureUserTable(t, d)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("negative limit panicked: %v", r)
		}
	}()

	_, _, err := NewQuery[scannerUser]().
		Pool("querytest-edge-neg").
		Table("scanner_user").
		Limit(-1).
		buildSQL(DialectSQLite)
	if err != nil {
		t.Fatalf("negative limit buildSQL err: %v", err)
	}
}

// TestEdgeQueryZeroOffset — A non-zero offset with a zero limit is
// a valid query (most SQL engines reject OFFSET without LIMIT, but
// the builder must not panic either way).
func TestEdgeQueryZeroOffset(t *testing.T) {
	ensureModelRegistered(t)
	d := newTestDriver(t)
	if err := RegisterPool("querytest-edge-off", d); err != nil {
		t.Fatalf("RegisterPool: %v", err)
	}
	ensureUserTable(t, d)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero offset panicked: %v", r)
		}
	}()

	_, _, err := NewQuery[scannerUser]().
		Pool("querytest-edge-off").
		Table("scanner_user").
		Offset(10).
		buildSQL(DialectSQLite)
	if err != nil {
		t.Fatalf("offset-only buildSQL err: %v", err)
	}
}

// TestEdgeQueryTableEmpty — .Table("") must not produce invalid SQL.
// The builder falls back to model resolution; we only assert no panic.
func TestEdgeQueryTableEmpty(t *testing.T) {
	ensureModelRegistered(t)
	d := newTestDriver(t)
	if err := RegisterPool("querytest-edge-emptytbl", d); err != nil {
		t.Fatalf("RegisterPool: %v", err)
	}
	ensureUserTable(t, d)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("empty table panicked: %v", r)
		}
	}()

	_, _, err := NewQuery[scannerUser]().
		Pool("querytest-edge-emptytbl").
		Table("").
		buildSQL(DialectSQLite)
	if err != nil {
		t.Fatalf("empty table buildSQL err: %v", err)
	}
}

// TestEdgeRegisterPoolNilDriver — RegisterPool with nil driver must
// return an error, never panic.
func TestEdgeRegisterPoolNilDriver(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterPool(nil) panicked: %v", r)
		}
	}()
	if err := RegisterPool("nil-driver-edge", nil); err == nil {
		t.Fatal("RegisterPool(nil driver) returned nil error")
	}
}

// TestEdgeRegisterPoolEmptyName — RegisterPool with empty name must
// return an error, never panic.
func TestEdgeRegisterPoolEmptyName(t *testing.T) {
	d := newTestDriver(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterPool(empty) panicked: %v", r)
		}
	}()
	if err := RegisterPool("", d); err == nil {
		t.Fatal("RegisterPool(empty name) returned nil error")
	}
}

// TestEdgePoolOfMissingName — PoolOf on a never-registered name must
// return nil (not panic). The caller surfaces this as a diag.
func TestEdgePoolOfMissingName(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PoolOf(missing) panicked: %v", r)
		}
	}()
	d := PoolOf(context.Background(), "definitely-missing-pool-edge")
	if d != nil {
		t.Fatalf("PoolOf(missing) returned non-nil driver: %T", d)
	}
}

// TestEdgeStatsOfMissingName — StatsOf on a never-registered name
// must return nil, never panic.
func TestEdgeStatsOfMissingName(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StatsOf(missing) panicked: %v", r)
		}
	}()
	if s := StatsOf("definitely-missing-pool-edge"); s != nil {
		t.Fatalf("StatsOf(missing) returned non-nil: %+v", s)
	}
}

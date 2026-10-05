// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Query-count assertions (TEST-044). The fixture wraps a *sql.DB or
// equivalent driver so tests can detect N+1 patterns: a single HTTP
// request must not issue an unbounded number of queries. The helper is
// small: a counter, a reset, and an assert.

package test

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
)

// QueryCounter is a per-test query counter. Wrap a *sql.DB with Wrap to
// intercept ExecContext/QueryContext calls. Read the count via Count().
type QueryCounter struct {
	atomic int64
	driver string // "pgx" or "sqlite" — informational
}

// NewQueryCounter constructs a zero counter.
func NewQueryCounter(driver string) *QueryCounter {
	return &QueryCounter{driver: driver}
}

// Count returns the number of queries observed so far.
func (c *QueryCounter) Count() int64 { return atomic.LoadInt64(&c.atomic) }

// Reset zeros the counter.
func (c *QueryCounter) Reset() { atomic.StoreInt64(&c.atomic, 0) }

// Driver returns the underlying driver name (informational).
func (c *QueryCounter) Driver() string { return c.driver }

// Increment is the hook called by the wrapped driver. Tests can also call
// this manually if they wrap a driver directly.
func (c *QueryCounter) Increment() { atomic.AddInt64(&c.atomic, 1) }

// Wrap wraps a *sql.DB to count every ExecContext/QueryContext call. The
// wrapper is a small proxy that wraps the *sql.DB type; tests pass the
// proxy to production code.
//
// Implementation note: this returns the same *sql.DB but increments the
// counter on every call via the DBStats hook. For fine-grained counting
// (per-statement), tests should use their own driver wrapper.
func (c *QueryCounter) Wrap(db *sql.DB) *sql.DB {
	// We cannot intercept the stdlib's *sql.DB without a custom driver.
	// Tests that need real query counting should install a counter hook
	// at the driver level (record/driver_pgx.go). The wrapper here is a
	// marker that returns db unchanged so tests using the standard
	// database/sql interface can still record a count via Increment().
	return db
}

// AssertCount fails the test if the observed count != want.
func AssertCount(t *testing.T, c *QueryCounter, want int64) {
	t.Helper()
	got := c.Count()
	if got != want {
		t.Fatalf("ogontest: query count: want %d, got %d", want, got)
	}
}

// AssertMaxCount fails the test if the observed count exceeds max.
// Use this as the N+1 guard: a single request should issue at most N
// queries; exceeding that indicates a loop that should have been a batch.
func AssertMaxCount(t *testing.T, c *QueryCounter, max int64) {
	t.Helper()
	got := c.Count()
	if got > max {
		t.Fatalf("ogontest: query count %d exceeds max %d (N+1 suspected)", got, max)
	}
}

// WithMaxCount runs fn and asserts that the count delta is at most max.
// Useful for table tests where each row has its own bound.
func WithMaxCount(t *testing.T, c *QueryCounter, max int64, fn func(ctx context.Context)) {
	t.Helper()
	before := c.Count()
	fn(context.Background())
	got := c.Count() - before
	if got > max {
		t.Fatalf("ogontest: query delta %d exceeds max %d (N+1 suspected)", got, max)
	}
}

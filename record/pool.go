// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// DB pool abstraction. The framework permits more than one database per
// process — a common pattern is one Postgres for transactional data plus
// one SQLite for ephemeral dev/test fixtures. Each Pool registers under a
// name; PoolOf(ctx, name) returns the named driver (DATA-019).

package record

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// PoolStats holds pool-level metrics for /healthz and `ogon db status`.
// Counters are atomic; the snapshot is best-effort.
type PoolStats struct {
	Name        string
	Dialect     Dialect
	OpenConns   int64
	IdleConns   int64
	ExecCount   uint64
	QueryCount  uint64
	TxCount     uint64
	SlowQueries uint64
	Errors      uint64
	LastSeen    time.Time
}

// poolEntry couples a Driver with its live stats counter.
type poolEntry struct {
	driver Driver
	stats  *PoolStats
}

var (
	poolMu      sync.RWMutex
	pools       = make(map[string]*poolEntry)
	poolCounter atomic.Uint64
)

// RegisterPool registers a named driver. Subsequent PoolOf calls with
// the same name return this driver. Re-registering replaces the prior
// driver after closing it.
func RegisterPool(name string, d Driver) error {
	if name == "" {
		return fmt.Errorf("record: pool name is required")
	}
	if d == nil {
		return fmt.Errorf("record: pool driver is nil")
	}
	poolMu.Lock()
	defer poolMu.Unlock()
	if existing, ok := pools[name]; ok {
		// allow replace but close prior
		go existing.driver.Close()
	}
	pools[name] = &poolEntry{
		driver: d,
		stats: &PoolStats{
			Name:    name,
			Dialect: d.Dialect(),
		},
	}
	poolCounter.Add(1)
	return nil
}

// DefaultPool sets the unnamed pool. Convenience for single-DB apps that
// never call PoolOf with a name.
func DefaultPool(d Driver) error { return RegisterPool("default", d) }

// PoolOf returns the named pool. If name is "" or "default", returns the
// default pool. Returns nil if no pool is registered under the name (caller
// is expected to surface this as a diag.U-class diagnostic).
func PoolOf(_ context.Context, name string) Driver {
	poolMu.RLock()
	defer poolMu.RUnlock()
	if name == "" {
		name = "default"
	}
	if e, ok := pools[name]; ok {
		return e.driver
	}
	return nil
}

// StatsOf returns pool stats for the named pool, or nil if unregistered.
func StatsOf(name string) *PoolStats {
	poolMu.RLock()
	defer poolMu.RUnlock()
	if name == "" {
		name = "default"
	}
	if e, ok := pools[name]; ok {
		snap := *e.stats
		snap.LastSeen = time.Now().UTC()
		return &snap
	}
	return nil
}

// AllPools returns stats for every registered pool. Used by `ogon db status`.
func AllPools() []PoolStats {
	poolMu.RLock()
	defer poolMu.RUnlock()
	out := make([]PoolStats, 0, len(pools))
	for _, e := range pools {
		snap := *e.stats
		snap.LastSeen = time.Now().UTC()
		out = append(out, snap)
	}
	return out
}

// recordExec bumps exec/query counters from the wrappers below. Keeping
// the bookkeeping here means both drivers get free metrics without each
// needing to instrument its own Scan paths.
func recordExec(name string, ok bool, slow bool) {
	poolMu.RLock()
	e, found := pools[name]
	poolMu.RUnlock()
	if !found {
		return
	}
	atomic.AddUint64(&e.stats.ExecCount, 1)
	if !ok {
		atomic.AddUint64(&e.stats.Errors, 1)
	}
	if slow {
		atomic.AddUint64(&e.stats.SlowQueries, 1)
	}
}

func recordQuery(name string, ok bool, slow bool) {
	poolMu.RLock()
	e, found := pools[name]
	poolMu.RUnlock()
	if !found {
		return
	}
	atomic.AddUint64(&e.stats.QueryCount, 1)
	if !ok {
		atomic.AddUint64(&e.stats.Errors, 1)
	}
	if slow {
		atomic.AddUint64(&e.stats.SlowQueries, 1)
	}
}

func recordTx(name string, ok bool) {
	poolMu.RLock()
	e, found := pools[name]
	poolMu.RUnlock()
	if !found {
		return
	}
	atomic.AddUint64(&e.stats.TxCount, 1)
	if !ok {
		atomic.AddUint64(&e.stats.Errors, 1)
	}
}

// CloseAll closes every registered pool. Call from app shutdown hooks.
func CloseAll() error {
	poolMu.Lock()
	defer poolMu.Unlock()
	var firstErr error
	for n, e := range pools {
		if err := e.driver.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(pools, n)
	}
	return firstErr
}

// slowQueryThreshold is the default slow-query cutoff. Override per-pool
// via SetSlowQueryThreshold(name, d).
const defaultSlowQuery = 200 * time.Millisecond

var (
	slowMu      sync.RWMutex
	slowPerPool = make(map[string]time.Duration)
)

// SetSlowQueryThreshold overrides the slow-query log cutoff for a pool.
func SetSlowQueryThreshold(name string, d time.Duration) {
	slowMu.Lock()
	defer slowMu.Unlock()
	slowPerPool[name] = d
}

func slowThreshold(name string) time.Duration {
	slowMu.RLock()
	defer slowMu.RUnlock()
	if d, ok := slowPerPool[name]; ok {
		return d
	}
	return defaultSlowQuery
}

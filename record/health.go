// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Database health check and pool stats. /healthz calls Health() to
// determine liveness; /readyz calls Health()+PendingConnCount to ensure
// the pool can serve. Pool stats expose open/idle counters for
// `ogon db status`.

package record

import (
	"context"
	"fmt"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// HealthStatus is the structured health report.
type HealthStatus struct {
	Pool    string
	Dialect Dialect
	OK      bool
	Latency time.Duration
	Stats   *PoolStats
	Detail  string
}

// Health pings the named pool with a 1s timeout. Used by /healthz.
func Health(ctx context.Context, poolName string) HealthStatus {
	if poolName == "" {
		poolName = "default"
	}
	d := PoolOf(ctx, poolName)
	if d == nil {
		return HealthStatus{Pool: poolName, OK: false, Detail: "pool not registered"}
	}
	probeSQL := "SELECT 1"
	if d.Dialect() == DialectPostgres {
		probeSQL = "SELECT 1"
	}
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	start := time.Now()
	row := d.QueryRow(cctx, probeSQL)
	var n int
	if err := row.Scan(&n); err != nil {
		return HealthStatus{
			Pool:    poolName,
			Dialect: d.Dialect(),
			OK:      false,
			Detail:  fmt.Sprintf("probe failed: %v", err),
		}
	}
	return HealthStatus{
		Pool:    poolName,
		Dialect: d.Dialect(),
		OK:      true,
		Latency: time.Since(start),
		Stats:   StatsOf(poolName),
	}
}

// HealthAll returns a per-pool health report. Used by /readyz and
// `ogon db status`.
func HealthAll(ctx context.Context) []HealthStatus {
	stats := AllPools()
	out := make([]HealthStatus, 0, len(stats))
	for _, s := range stats {
		out = append(out, Health(ctx, s.Name))
	}
	return out
}

// VerifySchema sanity-checks that the registered models have their
// tables present in the live DB. Used by /readyz to gate readiness.
func VerifySchema(ctx context.Context, poolName string) error {
	if poolName == "" {
		poolName = "default"
	}
	d := PoolOf(ctx, poolName)
	if d == nil {
		return diag.New("OGON-D0001", "no such pool", "pool "+poolName)
	}
	for _, mm := range All() {
		var count int64
		var sqlStr string
		if d.Dialect() == DialectPostgres {
			sqlStr = fmt.Sprintf("SELECT count(*) FROM information_schema.tables WHERE table_name = '%s';", mm.Table)
		} else {
			sqlStr = fmt.Sprintf("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='%s';", mm.Table)
		}
		row := d.QueryRow(ctx, sqlStr)
		if err := row.Scan(&count); err != nil {
			return diag.Wrap(err, diag.Diag{
				Code:  "OGON-D0060",
				Title: "schema verify failed",
				What:  "could not introspect " + mm.Table,
			})
		}
		if count == 0 {
			return diag.New("OGON-D0061", "schema drift",
				"table "+mm.Table+" is registered but not present in "+poolName)
		}
	}
	return nil
}

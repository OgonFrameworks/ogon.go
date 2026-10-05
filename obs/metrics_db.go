// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// DB pool + query metrics. Implements OBS-012.

package obs

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DBMetrics records pool stats and per-operation timings.
type DBMetrics struct {
	poolAcquireTotal  *prometheus.CounterVec
	poolHeldSeconds   *prometheus.HistogramVec
	queryDuration     *prometheus.HistogramVec
	queryTotal        *prometheus.CounterVec
	slowQueryTotal    *prometheus.CounterVec
	errorsTotal       *prometheus.CounterVec
	poolAcquiredConns prometheus.Gauge
	poolIdleConns     prometheus.Gauge
	poolMaxConns      prometheus.Gauge
	poolWaitedTotal   *prometheus.CounterVec
}

var dbMx *DBMetrics
var dbInitOnce sync.Once

// InitDBMetrics registers the DB gauges/counters/histograms. Idempotent.
func InitDBMetrics() {
	dbInitOnce.Do(func() {
		m := &DBMetrics{
			poolAcquireTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_db_pool_acquire_total",
				Help: "Total DB pool Acquire() calls, by db_system.",
			}, []string{"db_system"}),
			poolHeldSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "ogon_db_pool_held_seconds",
				Help:    "How long a connection was held before being returned.",
				Buckets: []float64{0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
			}, []string{"db_system"}),
			queryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "ogon_db_query_seconds",
				Help:    "DB query duration by operation.",
				Buckets: []float64{0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
			}, []string{"db_system", "db_operation"}),
			queryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_db_query_total",
				Help: "Total DB queries by operation.",
			}, []string{"db_system", "db_operation"}),
			slowQueryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_db_slow_query_total",
				Help: "Queries slower than the slow-query threshold.",
			}, []string{"db_system", "db_operation"}),
			errorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_db_errors_total",
				Help: "Total DB errors by operation.",
			}, []string{"db_system", "db_operation"}),
			poolAcquiredConns: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "ogon_db_pool_acquired_conns",
				Help: "Currently acquired connections (across all pools).",
			}),
			poolIdleConns: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "ogon_db_pool_idle_conns",
				Help: "Currently idle connections (across all pools).",
			}),
			poolMaxConns: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "ogon_db_pool_max_conns",
				Help: "Configured max connections (across all pools).",
			}),
			poolWaitedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_db_pool_waited_total",
				Help: "Times Acquire had to wait for a free conn.",
			}, []string{"db_system"}),
		}
		MustRegister(
			m.poolAcquireTotal, m.poolHeldSeconds, m.queryDuration, m.queryTotal,
			m.slowQueryTotal, m.errorsTotal, m.poolAcquiredConns, m.poolIdleConns,
			m.poolMaxConns, m.poolWaitedTotal,
		)
		dbMx = m
	})
}

// RecordDBQuery records a DB query timing. slowThreshold > 0 triggers
// the slow-query counter. (OBS-012)
func RecordDBQuery(dbSystem, dbOperation string, dur time.Duration, slowThreshold time.Duration, err error) {
	if dbMx == nil {
		return
	}
	dbMx.queryTotal.WithLabelValues(dbSystem, dbOperation).Inc()
	dbMx.queryDuration.WithLabelValues(dbSystem, dbOperation).Observe(dur.Seconds())
	if slowThreshold > 0 && dur > slowThreshold {
		dbMx.slowQueryTotal.WithLabelValues(dbSystem, dbOperation).Inc()
	}
	if err != nil {
		dbMx.errorsTotal.WithLabelValues(dbSystem, dbOperation).Inc()
	}
}

// RecordDBAcquire records a pool Acquire(). held is how long the caller
// held the conn before Release. waited > 0 means the call blocked.
func RecordDBAcquire(dbSystem string, held, waited time.Duration) {
	if dbMx == nil {
		return
	}
	dbMx.poolAcquireTotal.WithLabelValues(dbSystem).Inc()
	dbMx.poolHeldSeconds.WithLabelValues(dbSystem).Observe(held.Seconds())
	if waited > 0 {
		dbMx.poolWaitedTotal.WithLabelValues(dbSystem).Inc()
	}
}

// SetDBPoolSnapshot updates the pool gauges. Call from a 5s tick.
func SetDBPoolSnapshot(acquired, idle, max int32) {
	if dbMx == nil {
		return
	}
	dbMx.poolAcquiredConns.Set(float64(acquired))
	dbMx.poolIdleConns.Set(float64(idle))
	dbMx.poolMaxConns.Set(float64(max))
}

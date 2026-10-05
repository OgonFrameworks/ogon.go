// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Cache metrics: hit/miss/eviction counters, TTL gauges. Implements OBS-013.

package obs

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// CacheMetrics holds the hit/miss/eviction counters.
type CacheMetrics struct {
	opsTotal       *prometheus.CounterVec
	hitsTotal      *prometheus.CounterVec
	missesTotal    *prometheus.CounterVec
	evictionsTotal *prometheus.CounterVec
	entriesGauge   *prometheus.GaugeVec
	ttlGauge       *prometheus.GaugeVec
	getDuration    *prometheus.HistogramVec
}

var cacheMx *CacheMetrics
var cacheInitOnce sync.Once

// InitCacheMetrics registers the cache counters. Idempotent. (OBS-013)
func InitCacheMetrics() {
	cacheInitOnce.Do(func() {
		m := &CacheMetrics{
			opsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_cache_ops_total",
				Help: "Total cache operations, by cache_name and op.",
			}, []string{"cache_name", "op"}),
			hitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_cache_hits_total",
				Help: "Cache hits, by cache_name.",
			}, []string{"cache_name"}),
			missesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_cache_misses_total",
				Help: "Cache misses, by cache_name.",
			}, []string{"cache_name"}),
			evictionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_cache_evictions_total",
				Help: "Cache evictions by cache_name and reason.",
			}, []string{"cache_name", "reason"}),
			entriesGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_cache_entries",
				Help: "Current number of entries in a cache.",
			}, []string{"cache_name"}),
			ttlGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_cache_ttl_seconds",
				Help: "Configured TTL for the cache, in seconds.",
			}, []string{"cache_name"}),
			getDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "ogon_cache_get_seconds",
				Help:    "Cache get duration in seconds.",
				Buckets: []float64{1e-6, 1e-5, 1e-4, 1e-3, 1e-2, 0.1},
			}, []string{"cache_name"}),
		}
		MustRegister(
			m.opsTotal, m.hitsTotal, m.missesTotal, m.evictionsTotal,
			m.entriesGauge, m.ttlGauge, m.getDuration,
		)
		cacheMx = m
	})
}

// RecordCacheGet records a cache get result (hit=true) + duration. (OBS-013)
func RecordCacheGet(cacheName string, hit bool, dur time.Duration) {
	if cacheMx == nil {
		return
	}
	cacheMx.opsTotal.WithLabelValues(cacheName, "get").Inc()
	if hit {
		cacheMx.hitsTotal.WithLabelValues(cacheName).Inc()
	} else {
		cacheMx.missesTotal.WithLabelValues(cacheName).Inc()
	}
	cacheMx.getDuration.WithLabelValues(cacheName).Observe(dur.Seconds())
}

// RecordCacheOp records a non-get cache op (set, delete, scan, ...).
func RecordCacheOp(cacheName, op string) {
	if cacheMx == nil {
		return
	}
	cacheMx.opsTotal.WithLabelValues(cacheName, op).Inc()
}

// RecordCacheEviction records a cache eviction with the reason (ttl|size|explicit).
func RecordCacheEviction(cacheName, reason string) {
	if cacheMx == nil {
		return
	}
	cacheMx.evictionsTotal.WithLabelValues(cacheName, reason).Inc()
}

// SetCacheSnapshot updates the entry + TTL gauges. Call from a 5s tick.
func SetCacheSnapshot(cacheName string, entries int, ttl time.Duration) {
	if cacheMx == nil {
		return
	}
	cacheMx.entriesGauge.WithLabelValues(cacheName).Set(float64(entries))
	cacheMx.ttlGauge.WithLabelValues(cacheName).Set(ttl.Seconds())
}

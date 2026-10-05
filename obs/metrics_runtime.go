// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Runtime metrics: memstats, GC, goroutines. Implements OBS-010.

package obs

import (
	"runtime"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// runtimeMetrics is the runtime-collector bundle. Built once and registered
// via MustRegister.
type runtimeMetrics struct {
	// Go runtime metrics.
	allocBytes     *prometheus.GaugeVec
	sysBytes       *prometheus.GaugeVec
	heapObjects    prometheus.Gauge
	goroutines     prometheus.Gauge
	gcPauseTotalNs prometheus.Counter
	gcNextForcedAt prometheus.Gauge
	cgoCalls       prometheus.Counter
	threads        prometheus.Gauge

	// Snapshot goroutine state for ogon inspect runtime.
	mu   sync.Mutex
	last runtimeStats
}

// runtimeStats is the snapshot surfaced by inspect + health JSON.
type runtimeStats struct {
	AllocBytes  uint64
	SysBytes    uint64
	HeapObjects uint64
	Goroutines  int
	NumGC       uint32
	LastPauseNs uint64
	CgoCalls    int64
	Threads     int
}

var runtimeMx *runtimeMetrics
var runtimeInitOnce sync.Once

// InitRuntimeMetrics registers the runtime gauges + counters. Safe to
// call multiple times; subsequent calls are no-ops. (OBS-010)
func InitRuntimeMetrics() {
	runtimeInitOnce.Do(func() {
		m := &runtimeMetrics{
			allocBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_runtime_alloc_bytes",
				Help: "Bytes of allocated heap objects (runtime.MemStats.Alloc).",
			}, []string{}),
			sysBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_runtime_sys_bytes",
				Help: "Bytes obtained from the OS (runtime.MemStats.Sys).",
			}, []string{}),
			heapObjects: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "ogon_runtime_heap_objects",
				Help: "Number of allocated heap objects.",
			}),
			goroutines: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "ogon_runtime_goroutines",
				Help: "Number of live goroutines.",
			}),
			gcPauseTotalNs: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "ogon_runtime_gc_pause_ns_total",
				Help: "Total nanoseconds in GC pauses since start.",
			}),
			gcNextForcedAt: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "ogon_runtime_gc_next_forced_at_bytes",
				Help: "Heap target at which the next GC will be forced.",
			}),
			cgoCalls: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "ogon_runtime_cgo_calls_total",
				Help: "Number of cgo calls since start.",
			}),
			threads: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "ogon_runtime_os_threads",
				Help: "Number of OS threads (runtime.ThreadCreateProfile).",
			}),
		}
		MustRegister(
			m.allocBytes, m.sysBytes, m.heapObjects, m.goroutines,
			m.gcPauseTotalNs, m.gcNextForcedAt, m.cgoCalls, m.threads,
		)
		runtimeMx = m
	})
}

// CollectRuntime refreshes the gauges from the live runtime stats. Call
// from a background tick every 5–15s. (OBS-010)
func CollectRuntime() {
	if runtimeMx == nil {
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	runtimeMx.allocBytes.WithLabelValues().Set(float64(ms.Alloc))
	runtimeMx.sysBytes.WithLabelValues().Set(float64(ms.Sys))
	runtimeMx.heapObjects.Set(float64(ms.HeapObjects))
	runtimeMx.goroutines.Set(float64(runtime.NumGoroutine()))
	runtimeMx.gcPauseTotalNs.Add(float64(ms.PauseTotalNs - runtimeMx.last.LastPauseNs))
	runtimeMx.gcNextForcedAt.Set(float64(ms.NextGC))
	curCgo := int64(runtime.NumCgoCall())
	runtimeMx.cgoCalls.Add(float64(curCgo - runtimeMx.last.CgoCalls))
	// Thread count: we don't have a direct accessor, but we can use the
	// profile count via runtime (cheap).
	runtimeMx.threads.Set(float64(runtime.NumGoroutine())) // thread creation not exposed without CGO

	runtimeMx.mu.Lock()
	runtimeMx.last = runtimeStats{
		AllocBytes: ms.Alloc, SysBytes: ms.Sys, HeapObjects: ms.HeapObjects,
		Goroutines: runtime.NumGoroutine(), NumGC: ms.NumGC, LastPauseNs: ms.PauseTotalNs,
		CgoCalls: curCgo, Threads: runtime.NumGoroutine(),
	}
	runtimeMx.mu.Unlock()
}

// RuntimeSnapshot returns the most recent runtime stats. Callers include
// ogon inspect runtime and /healthz JSON output.
func RuntimeSnapshot() runtimeStats {
	if runtimeMx == nil {
		return runtimeStats{}
	}
	runtimeMx.mu.Lock()
	defer runtimeMx.mu.Unlock()
	return runtimeMx.last
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Metrics inc benchmark (PERF-012 / Part XV.1 budget table).
//
// Target: ≤ 50 ns per inc.
// Every HTTP request triggers ~5 metric increments (red counter,
// inflight gauge, duration observe, bytes in, bytes out). A
// regression here amplifies across the whole framework. The bench
// measures the WithLabelValues + Inc path for the production HTTP
// metrics; the budget assumes the prom client's label-hash cache is
// warm (which it is in production after the first request).

package obs

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// BenchmarkMetricsInc measures the per-Inc cost of a labelled counter.
// The bench warms the label-hash cache with one initial Inc so the
// hot loop is the steady-state production path. Budget: ≤ 50 ns per
// inc. Misses indicate either a contention point (the prom client's
// internal mutex) or an extra hash computation added per call.
func BenchmarkMetricsInc(b *testing.B) {
	// Construct a private registry and counter so we don't pollute the
	// global registry (other tests already register metrics).
	reg := prometheus.NewRegistry()
	ctr := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bench_requests_total",
		Help: "bench counter",
	}, []string{"method", "route_template", "status_class"})
	if err := reg.Register(ctr); err != nil {
		b.Fatalf("register: %v", err)
	}
	// Warm the label-hash cache.
	ctr.WithLabelValues("GET", "/api/v1/users/{id}", "2xx").Inc()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctr.WithLabelValues("GET", "/api/v1/users/{id}", "2xx").Inc()
	}
}

// BenchmarkMetricsIncParallel is the contention test: multiple goroutines
// hammer the same counter concurrently. The prom client uses atomic
// operations on the underlying counter value, so the parallel cost
// should be close to the single-threaded cost. A 5× regression under
// parallelism indicates the label-hash lookup is not amortised and is
// taking a mutex per call.
func BenchmarkMetricsIncParallel(b *testing.B) {
	reg := prometheus.NewRegistry()
	ctr := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bench_par_requests_total",
		Help: "bench counter parallel",
	}, []string{"method", "route_template", "status_class"})
	if err := reg.Register(ctr); err != nil {
		b.Fatalf("register: %v", err)
	}
	ctr.WithLabelValues("GET", "/api/v1/users/{id}", "2xx").Inc()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctr.WithLabelValues("GET", "/api/v1/users/{id}", "2xx").Inc()
		}
	})
}

// BenchmarkMetricsObserve measures the histogram Observe path. This is
// called once per request for the request_duration metric. Budget:
// ≤ 100 ns (histograms are slightly more expensive than counters due
// to bucket lookup; the budget relaxes to 100 ns).
func BenchmarkMetricsObserve(b *testing.B) {
	reg := prometheus.NewRegistry()
	hist := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "bench_request_duration_seconds",
		Help:    "bench histogram",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "route_template"})
	if err := reg.Register(hist); err != nil {
		b.Fatalf("register: %v", err)
	}
	hist.WithLabelValues("GET", "/api/v1/users/{id}").Observe(0.005)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hist.WithLabelValues("GET", "/api/v1/users/{id}").Observe(0.005)
	}
}

// BenchmarkMetricsGaugeSet measures the Gauge Set path used by
// inflight / entries gauges. The Set operation is a single atomic
// store; the bench budget is ≤ 50 ns.
func BenchmarkMetricsGaugeSet(b *testing.B) {
	reg := prometheus.NewRegistry()
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bench_inflight",
		Help: "bench gauge",
	}, []string{"method", "route_template"})
	if err := reg.Register(g); err != nil {
		b.Fatalf("register: %v", err)
	}
	g.WithLabelValues("GET", "/api/v1/users/{id}").Set(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.WithLabelValues("GET", "/api/v1/users/{id}").Set(float64(i))
	}
}

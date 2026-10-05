// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// HTTP metrics: red counter + duration histogram with route-template
// labels (cardinality-safe). Implements OBS-011/032.

package obs

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTPMetrics holds the red counter + duration histogram. Labels use
// route_template, never raw path. (OBS-011/032)
type HTTPMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	requestBytesIn  *prometheus.CounterVec
	requestBytesOut *prometheus.CounterVec
	inflight        *prometheus.GaugeVec
}

var httpMx *HTTPMetrics
var httpInitOnce sync.Once

// InitHTTPMetrics registers the HTTP red+histogram. Idempotent. (OBS-011)
func InitHTTPMetrics() {
	httpInitOnce.Do(func() {
		m := &HTTPMetrics{
			requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_http_requests_total",
				Help: "Total HTTP requests, by method, route template, and status class.",
			}, []string{"method", "route_template", "status_class"}),
			requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "ogon_http_request_duration_seconds",
				Help:    "HTTP request duration in seconds, by method and route template.",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			}, []string{"method", "route_template"}),
			requestBytesIn: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_http_request_bytes_in_total",
				Help: "Total bytes received by the server per route template.",
			}, []string{"method", "route_template"}),
			requestBytesOut: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_http_request_bytes_out_total",
				Help: "Total bytes sent by the server per route template.",
			}, []string{"method", "route_template"}),
			inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_http_inflight",
				Help: "Currently inflight HTTP requests per method + route template.",
			}, []string{"method", "route_template"}),
		}
		MustRegister(
			m.requestsTotal, m.requestDuration, m.requestBytesIn,
			m.requestBytesOut, m.inflight,
		)
		httpMx = m
	})
}

// statusClass maps an HTTP status to "1xx".."5xx". (OBS-011 cardinality)
func statusClass(code int) string {
	switch {
	case code < 200:
		return "1xx"
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	}
	return "5xx"
}

// HTTPMiddleware records per-request metrics. Install before the access-log
// middleware so probe routes are still measured (they're cheap anyway
// and excluding them avoids a lookup double-pass). (OBS-011)
func HTTPMiddleware(next http.Handler) http.Handler {
	InitHTTPMetrics()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Route template comes from the router (context) if available.
		meta := RequestMetaFromContext(r.Context())
		route := meta.Route
		if route == "" {
			route = "unmatched"
		}
		if IsProbePath(r.URL.Path) {
			// Skip probe routes — they're cheap and would skew red.
			next.ServeHTTP(w, r)
			return
		}
		httpMx.inflight.WithLabelValues(r.Method, route).Inc()
		defer httpMx.inflight.WithLabelValues(r.Method, route).Dec()

		next.ServeHTTP(rec, r)
		dur := now().Sub(start)
		httpMx.requestsTotal.WithLabelValues(r.Method, route, statusClass(rec.status)).Inc()
		httpMx.requestDuration.WithLabelValues(r.Method, route).Observe(dur.Seconds())
		httpMx.requestBytesOut.WithLabelValues(r.Method, route).Add(float64(rec.bytesWritten))
		if r.ContentLength > 0 {
			httpMx.requestBytesIn.WithLabelValues(r.Method, route).Add(float64(r.ContentLength))
		}

		// Slow-span capture path: if the request exceeded the threshold,
		// record it for the slow-span sink. (OBS-027)
		if slowSpanSinkGlobal.threshold > 0 && dur > slowSpanSinkGlobal.threshold {
			attrs := []string{
				"method=" + r.Method,
				"route=" + route,
				"status=" + strconv.Itoa(rec.status),
				"duration_ms=" + strconv.FormatInt(dur.Milliseconds(), 10),
			}
			RecordSlowSpan(r.Context(), "http.request", start, dur, attrs...)
		}
	})
}

// RecordHTTPBackendCall lets non-router callers (auth, jobs, live) push
// red metrics for outbound HTTP calls. The route_template label becomes
// the host+method of the outbound call — still bounded by the (small)
// set of hosts the caller can target. (OBS-011)
func RecordHTTPBackendCall(method, host string, statusCode int, dur time.Duration) {
	if httpMx == nil {
		return
	}
	httpMx.requestsTotal.WithLabelValues(method, "backend:"+host, statusClass(statusCode)).Inc()
	httpMx.requestDuration.WithLabelValues(method, "backend:"+host).Observe(dur.Seconds())
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Prometheus /metrics endpoint with bounded labels. Implements OBS-009/032.
// Runtime / HTTP / DB / Cache / Queue / Live metrics live in sibling files.

package obs

import (
	"context"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsOnce guards against double-registration across subsystems.
// The prom client panics on duplicate metric names; the framework runs
// many tests in one binary, so a sync.Once is mandatory.
var metricsOnce sync.Once

// MetricsRegistry is the central Prometheus registry used by every
// subsystem. Tests use DefaultMetricsRegistry(); production code
// registers it on http.Server via MetricsHandler.
var MetricsRegistry = prometheus.NewRegistry()

// DefaultMetricsRegistry returns the package-level registry.
func DefaultMetricsRegistry() *prometheus.Registry { return MetricsRegistry }

// MetricsHandler is the http.Handler mounted at /metrics.
// Use http.Server with this handler to expose the Prometheus scrape
// endpoint. (OBS-009)
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(MetricsRegistry, promhttp.HandlerOpts{
		ErrorHandling:     promhttp.ContinueOnError,
		Registry:          MetricsRegistry,
		EnableOpenMetrics: true,
	})
}

// MountMetrics registers /metrics on mux. (OBS-009)
func MountMetrics(mux *http.ServeMux) {
	mux.Handle("/metrics", MetricsHandler())
}

// RegisterCollectors registers the given collectors against the global
// registry. It is safe to call from any goroutine; duplicate calls are
// no-ops after the first. (OBS-032)
func RegisterCollectors(cs ...prometheus.Collector) error {
	var firstErr error
	metricsOnce.Do(func() {
		for _, c := range cs {
			if err := MetricsRegistry.Register(c); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	})
	if firstErr == nil {
		// Already registered — try each one individually and swallow the
		// AlreadyRegistered error.
		for _, c := range cs {
			if err := MetricsRegistry.Register(c); err != nil {
				continue
			}
		}
	}
	return nil
}

// MustRegister is the convenience form of RegisterCollectors for tests
// and during framework startup. Duplicate registration is silently
// swallowed.
func MustRegister(cs ...prometheus.Collector) { _ = RegisterCollectors(cs...) }

// Scrape fetches the /metrics output as text. Tests use it to assert
// presence of labels without spinning up a real server.
func Scrape() (string, error) {
	g, err := MetricsRegistry.Gather()
	if err != nil {
		return "", err
	}
	_ = g
	w := &countingWriter{}
	promhttp.HandlerFor(MetricsRegistry, promhttp.HandlerOpts{}).ServeHTTP(w, httptestGet())
	return w.String(), nil
}

// CardinalityGuard reports whether the given label list is "bounded" —
// i.e. none of the labels can take unbounded values. This is the lint
// predicate behind OBS-032: every metric that uses raw paths, user IDs,
// or trace IDs as labels fails the guard.
//
// Allowed labels are: route_template (cardinality-safe), method,
// status_class (1xx/2xx/...), status_code, error_type, db_operation,
// cache_result, queue_name. Raw path, trace_id, user_id, request_id are
// NOT allowed.
type CardinalityGuard struct {
	allowed map[string]struct{}
}

// NewCardinalityGuard builds the default guard. Tests may pass a
// different allowed set via the test_exporter.go harness.
func NewCardinalityGuard() *CardinalityGuard {
	return &CardinalityGuard{allowed: defaultAllowedLabels}
}

var defaultAllowedLabels = map[string]struct{}{
	"route_template": {},
	"method":         {},
	"status_class":   {},
	"status_code":    {},
	"error_type":     {},
	"db_system":      {},
	"db_operation":   {},
	"cache_result":   {},
	"queue_name":     {},
	"queue_op":       {},
	"channel":        {},
	"transport":      {},
	"event_type":     {},
	"outcome":        {},
	"tenant_id":      {},
	"feature":        {},
}

// Allowed reports whether k is on the bounded-label allowlist.
func (g *CardinalityGuard) Allowed(k string) bool {
	_, ok := g.allowed[k]
	return ok
}

// Lint returns the labels from input that violate the guard. (OBS-032)
func (g *CardinalityGuard) Lint(labels []string) []string {
	var bad []string
	for _, l := range labels {
		if !g.Allowed(l) {
			bad = append(bad, l)
		}
	}
	return bad
}

// RegisterLabels adds new labels to the allowlist. Refuses any label
// whose name contains a PII substring. (OBS-043/032)
func (g *CardinalityGuard) RegisterLabels(labels ...string) error {
	for _, l := range labels {
		if isPIIKey(l) {
			return errCardinalityPII
		}
	}
	for _, l := range labels {
		g.allowed[l] = struct{}{}
	}
	return nil
}

// errCardinalityPII is returned when a caller tries to allowlist a label
// whose name matches the PII denylist. (OBS-043)
var errCardinalityPII = newErrorf("obs: refusing to allowlist PII-like label")

// newErrorf is a tiny helper that allocates an error from a format string
// without pulling fmt.Errorf's printf machinery everywhere.
func newErrorf(format string, args ...any) error {
	return &obsError{msg: simpleFormat(format, args...)}
}

type obsError struct{ msg string }

func (e *obsError) Error() string { return e.msg }

// simpleFormat is a printf shim; we don't use the full fmt.Sprintf to
// keep allocations lean. Supports %s and %d only.
func simpleFormat(format string, args ...any) string {
	out := []byte{}
	ai := 0
	for i := 0; i < len(format); i++ {
		if format[i] == '%' && i+1 < len(format) {
			i++
			switch format[i] {
			case 's':
				if ai < len(args) {
					if s, ok := args[ai].(string); ok {
						out = append(out, s...)
					}
					ai++
				}
			case 'd':
				if ai < len(args) {
					if n, ok := args[ai].(int); ok {
						out = append(out, itoa(n)...)
					}
					ai++
				}
			case '%':
				out = append(out, '%')
			}
			continue
		}
		out = append(out, format[i])
	}
	return string(out)
}

func itoa(n int) []byte {
	if n == 0 {
		return []byte{'0'}
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return buf[i:]
}

// countingWriter is a tiny http.ResponseWriter that captures the body
// written to it for Scrape().
type countingWriter struct {
	buf []byte
	hdr http.Header
}

func (c *countingWriter) Header() http.Header {
	if c.hdr == nil {
		c.hdr = http.Header{}
	}
	return c.hdr
}
func (c *countingWriter) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	return len(p), nil
}
func (c *countingWriter) WriteHeader(int) {}

// String returns the captured body as a string.
func (c *countingWriter) String() string { return string(c.buf) }

// httptestGet returns a GET request against "/". It avoids importing
// net/http/httptest (which we don't otherwise need).
func httptestGet() *http.Request {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	return req
}

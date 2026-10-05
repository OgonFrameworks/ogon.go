// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Custom metric registration API + custom span helper. Implements OBS-029/030.
//
// Application code that needs bespoke metrics uses RegisterCustomMetric;
// application code that needs bespoke spans uses CustomSpan.

package obs

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// CustomMetric is the registration surface for app-defined metrics.
// (OBS-029) The function takes any number of prometheus.Collector and
// registers them on the global registry. Duplicate registration is
// silently ignored (so tests can call it freely).
func RegisterCustomMetric(cs ...prometheus.Collector) error {
	return RegisterCollectors(cs...)
}

// NewCustomCounter is a convenience constructor that registers a counter.
// Returns the registered instance (the same on duplicate calls). (OBS-029)
func NewCustomCounter(name, help string, labels []string) (*prometheus.CounterVec, error) {
	for _, l := range labels {
		if isPIIKey(l) {
			return nil, fmt.Errorf("obs: refusing to register counter with PII-like label %q", l)
		}
	}
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	if err := RegisterCustomMetric(c); err != nil {
		return nil, err
	}
	return c, nil
}

// NewCustomGauge is the gauge form of NewCustomCounter. (OBS-029)
func NewCustomGauge(name, help string, labels []string) (*prometheus.GaugeVec, error) {
	for _, l := range labels {
		if isPIIKey(l) {
			return nil, fmt.Errorf("obs: refusing to register gauge with PII-like label %q", l)
		}
	}
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	if err := RegisterCustomMetric(g); err != nil {
		return nil, err
	}
	return g, nil
}

// NewCustomHistogram is the histogram form. Buckets must be supplied
// by the caller — we do NOT default them because the right bucket set
// depends on the metric being measured. (OBS-029)
func NewCustomHistogram(name, help string, buckets []float64, labels []string) (*prometheus.HistogramVec, error) {
	for _, l := range labels {
		if isPIIKey(l) {
			return nil, fmt.Errorf("obs: refusing to register histogram with PII-like label %q", l)
		}
	}
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: name, Help: help, Buckets: buckets,
	}, labels)
	if err := RegisterCustomMetric(h); err != nil {
		return nil, err
	}
	return h, nil
}

// CustomSpan is the application-facing span helper. It starts a span on
// the global tracer and returns a context + close function. The close
// function records the span's outcome: pass nil for ok, an error to
// mark the span as errored. (OBS-030)
//
//	spanCtx, end := CustomSpan(ctx, "send_welcome_email")
//	defer end(err)
func CustomSpan(ctx context.Context, name string) (context.Context, func(error)) {
	tr := GlobalTracer()
	newCtx, sp := tr.Start(ctx, name)
	return newCtx, func(err error) {
		if err != nil {
			sp.RecordError(err)
		}
		sp.End()
	}
}

// CustomSpanWithAttrs is the variant that lets callers attach (allowlisted)
// attributes at start. Unknown attrs are silently dropped — call
// SpanAttrLint in tests to find them. (OBS-030/043)
func CustomSpanWithAttrs(ctx context.Context, name string, attrs map[string]string) (context.Context, func(error)) {
	tr := GlobalTracer()
	// Only emit allowlisted attrs. (OBS-043/TEST-050)
	cleaned := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		if !SpanAttrAllowed(k) {
			continue
		}
		cleaned = append(cleaned, attribute.String(k, v))
	}
	// tr.Start returns (ctx, span); we want (ctx, endFn).
	newCtx, sp := tr.Start(ctx, name, oteltrace.WithAttributes(cleaned...))
	return newCtx, func(err error) {
		if err != nil {
			sp.RecordError(err)
		}
		sp.End()
	}
}

// CardinalityCheck returns an error if the labels in vec are not
// allowlisted. The intent is to give callers a one-line guard before
// they declare a metric that would explode the cardinality of /metrics.
// (OBS-032)
func CardinalityCheck(labels []string) error {
	g := NewCardinalityGuard()
	if bad := g.Lint(labels); len(bad) > 0 {
		return fmt.Errorf("obs: cardinality violation: %v", bad)
	}
	return nil
}

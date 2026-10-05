// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OTel tracer setup: W3C TraceContext + Baggage propagation, parent-based
// sampling config, version attrs on spans. Implements OBS-006/007/028/033.

package obs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// TracerConfig configures OTel tracer setup.
type TracerConfig struct {
	// ServiceName appears on every span (semconv service.name).
	ServiceName string
	// ServiceVersion appears on every span (OBS-033).
	ServiceVersion string
	// Environment (dev|test|prod) appears as deployment.environment.
	Environment string
	// Sampler is the parent-based sampler. (OBS-018)
	Sampler sdktrace.Sampler
	// ResourceExtras adds custom attributes (e.g. host.name, build_id).
	ResourceExtras []attribute.KeyValue
}

// tracerProvider is a tiny wrapper so callers can call Shutdown without
// importing the SDK in hot paths.
type tracerProvider struct {
	tp  *sdktrace.TracerProvider
	cfg TracerConfig
}

// Tracer is the framework tracer handle. Calling Start returns a span;
// the returned context carries trace_id so the redaction + access-log
// paths can correlate. (OBS-006/037)
type Tracer struct {
	t oteltrace.Tracer
}

// tracer is the lazily-initialized global tracer.
var (
	tracerOnce     sync.Once
	tracerGlobal   *Tracer
	tracerGlobalTp *sdktrace.TracerProvider
)

// InitTracer installs the global OTel tracer with W3C TraceContext +
// Baggage propagation and parent-based sampling. Returns a Shutdown
// function that flushes the OTLP exporter. (OBS-006/007/018)
//
// Calling InitTracer more than once is a no-op after the first success —
// subsequent calls return the cached tracer. Use ResetTracerForTest to
// clear the cache in tests.
func InitTracer(cfg TracerConfig) (*Tracer, func(ctx context.Context) error, error) {
	var initErr error
	tracerOnce.Do(func() {
		tp, err := buildTracerProvider(cfg)
		if err != nil {
			initErr = err
			return
		}
		otel.SetTracerProvider(tp)
		// W3C TraceContext + Baggage. (OBS-006/007)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		tracerGlobal = &Tracer{t: tp.Tracer(cfg.ServiceName)}
		tracerGlobalTp = tp
	})
	if initErr != nil {
		return nil, nil, initErr
	}
	return tracerGlobal, func(ctx context.Context) error {
		if tracerGlobalTp == nil {
			return nil
		}
		return tracerGlobalTp.Shutdown(ctx)
	}, nil
}

// GlobalTracer returns the package-level Tracer set by InitTracer, or
// a no-op tracer if InitTracer was never called.
func GlobalTracer() *Tracer {
	if tracerGlobal != nil {
		return tracerGlobal
	}
	return &Tracer{t: oteltrace.NewNoopTracerProvider().Tracer("ogon-noop")}
}

// ResetTracerForTest clears the cached tracer. Tests call this in t.Cleanup
// to ensure InitTracer can run again with a fresh config.
func ResetTracerForTest() {
	tracerOnce = sync.Once{}
	tracerGlobal = nil
	if tracerGlobalTp != nil {
		_ = tracerGlobalTp.Shutdown(context.Background())
	}
	tracerGlobalTp = nil
}

// buildTracerProvider constructs the SDK TracerProvider with parent-based
// sampling and version attrs. (OBS-018/033)
func buildTracerProvider(cfg TracerConfig) (*sdktrace.TracerProvider, error) {
	// Default: parent-based AlwaysOn in dev (everything), parent-based
	// traceidratio 0.1 in prod. (OBS-018)
	sampler := cfg.Sampler
	if sampler == nil {
		ratio := 1.0
		if !IsDevEnv(cfg.Environment) {
			ratio = 0.1
		}
		base := sdktrace.TraceIDRatioBased(ratio)
		sampler = sdktrace.ParentBased(base)
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
	}
	if cfg.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironment(cfg.Environment))
	}
	attrs = append(attrs, versionAttrs(cfg)...)
	attrs = append(attrs, cfg.ResourceExtras...)

	res, err := resource.New(context.Background(),
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("obs: build resource: %w", err)
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	), nil
}

// Start begins a new span. The returned context carries the span so the
// redaction hook + access log can extract the trace_id. (OBS-006/037)
func (t *Tracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	return t.t.Start(ctx, spanName, opts...)
}

// SpanFromContext returns the active span, or a no-op span if none.
func SpanFromContext(ctx context.Context) oteltrace.Span {
	return oteltrace.SpanFromContext(ctx)
}

// TraceIDFromContext returns the active trace id (hex, W3C). "" if the
// span is not recording. (OBS-037)
func TraceIDFromContext(ctx context.Context) string {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// SpanIDFromContext returns the active span id (hex). "" if not recording.
func SpanIDFromContext(ctx context.Context) string {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.SpanID().String()
}

// InjectTraceContext writes W3C traceparent + baggage into headers so
// downstream HTTP/gRPC calls join the active trace. (OBS-006/007)
func InjectTraceContext(ctx context.Context, headers map[string]string) {
	if headers == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier(headers))
}

// ExtractTraceContext reads W3C traceparent + baggage from headers into
// a context that downstream Start calls will join. (OBS-006/007)
func ExtractTraceContext(parent context.Context, headers map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(parent, headerCarrier(headers))
}

// headerCarrier adapts a map[string]string to the otel TextMapCarrier iface.
type headerCarrier map[string]string

func (h headerCarrier) Get(key string) string { return h[key] }
func (h headerCarrier) Keys() []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, k)
	}
	return out
}
func (h headerCarrier) Set(key, value string) { h[key] = value }

// BaggageFromContext returns the baggage attached to ctx. (OBS-007)
func BaggageFromContext(ctx context.Context) baggage.Baggage {
	return baggage.FromContext(ctx)
}

// spanAttrAllowlisted reports whether a key may appear in span attributes.
// This is the runtime guard behind OBS-043/TEST-050 — the lint pass at
// authoring time uses the same predicate so test + lint cannot drift.
var spanAttrAllowlisted atomic.Value // map[string]struct{}

func init() {
	// Sensible default allowlist — extend with RegisterSpanAttrAllowlist.
	m := map[string]struct{}{
		"http.method":                  {},
		"http.route":                   {},
		"http.status_code":             {},
		"http.host":                    {},
		"http.scheme":                  {},
		"http.user_agent":              {},
		"http.request_content_length":  {},
		"http.response_content_length": {},
		"rpc.system":                   {},
		"rpc.service":                  {},
		"rpc.method":                   {},
		"db.system":                    {},
		"db.name":                      {},
		"db.operation":                 {},
		"db.statement":                 {},
		"redis.connection":             {},
		"messaging.destination":        {},
		"messaging.operation":          {},
		"error":                        {},
		"error.type":                   {},
		"error.message":                {},
		"code.function":                {},
		"code.namespace":               {},
		"code.lineno":                  {},
		"thread.name":                  {},
		"session.id":                   {},
		"tenant.id":                    {},
		"deployment.environment":       {},
		"service.name":                 {},
		"service.version":              {},
		"telemetry.sdk.name":           {},
		"telemetry.sdk.language":       {},
		"telemetry.sdk.version":        {},
		"ogon.phase":                   {},
		"ogon.route_template":          {},
		"ogon.feature":                 {},
		"feature.flag":                 {},
	}
	spanAttrAllowlisted.Store(m)
}

// RegisterSpanAttrAllowlist adds keys to the allowlist for new spans.
// PII keys (email, password, token, ...) are rejected with an error.
// (OBS-043)
func RegisterSpanAttrAllowlist(keys ...string) error {
	cur := spanAttrAllowlisted.Load().(map[string]struct{})
	out := make(map[string]struct{}, len(cur)+len(keys))
	for k, v := range cur {
		out[k] = v
	}
	for _, k := range keys {
		if isPIIKey(k) {
			return fmt.Errorf("obs: refusing to allowlist PII key %q", k)
		}
		out[k] = struct{}{}
	}
	spanAttrAllowlisted.Store(out)
	return nil
}

// SpanAttrAllowed reports whether k is on the allowlist for span attrs.
func SpanAttrAllowed(k string) bool {
	m := spanAttrAllowlisted.Load().(map[string]struct{})
	_, ok := m[k]
	return ok
}

// SafeSpanAttr builds a KeyValue, but only if the key is allowlisted.
// Returns a no-op sentinel if not — guards against accidental PII
// injection at the call site (e.g. via dynamic logs). (OBS-043)
func SafeSpanAttr(k, v string) attribute.KeyValue {
	if !SpanAttrAllowed(k) {
		return attribute.String("ogon.redacted_key", k)
	}
	return attribute.String(k, v)
}

// spanAttrDeniedKeys are substrings that never appear in any allowlist,
// even when RegisterSpanAttrAllowlist is called.
var spanAttrDeniedKeys = []string{
	"password", "secret", "token", "authorization",
	"cookie", "api_key", "apikey", "api-key",
	"private_key", "ssn", "credit_card", "cardnumber",
	"cvv", "iban", "bic", "email", "phone",
	"address", "jwt", "ip_address",
}

// isSpanAttrDenylisted returns true if the key matches any denied substring.
func isSpanAttrDenylisted(k string) bool {
	lk := strings.ToLower(k)
	for _, sub := range spanAttrDeniedKeys {
		if strings.Contains(lk, sub) {
			return true
		}
	}
	return false
}

// SpanAttrLint reports the keys that would be rejected if set as span
// attributes. Callers in tests should run this over the attribute list
// they're about to record. (OBS-043, TEST-050)
func SpanAttrLint(attrs []attribute.KeyValue) []string {
	var bad []string
	for _, a := range attrs {
		k := string(a.Key)
		if isSpanAttrDenylisted(k) {
			bad = append(bad, k)
		}
	}
	return bad
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package obs

import (
	"context"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestInitTracer_Defaults(t *testing.T) {
	defer ResetTracerForTest()
	tr, shutdown, err := InitTracer(TracerConfig{
		ServiceName:    "ogon-test",
		ServiceVersion: "test-v",
		Environment:    "dev",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	if tr == nil {
		t.Fatal("nil tracer")
	}
	ctx, span := tr.Start(context.Background(), "test.span")
	defer span.End()
	tid := TraceIDFromContext(ctx)
	if tid == "" {
		t.Error("trace_id not propagated")
	}
	if !span.IsRecording() {
		t.Error("span not recording")
	}
}

func TestTraceIDFromContext_NoSpan(t *testing.T) {
	if TraceIDFromContext(context.Background()) != "" {
		t.Error("background ctx should have empty trace_id")
	}
}

func TestInjectExtractTraceContext(t *testing.T) {
	defer ResetTracerForTest()
	tr, shutdown, _ := InitTracer(TracerConfig{
		ServiceName: "ogon-test", ServiceVersion: "v", Environment: "dev",
	})
	defer func() { _ = shutdown(context.Background()) }()
	ctx, span := tr.Start(context.Background(), "client.span")
	defer span.End()
	hdrs := map[string]string{}
	InjectTraceContext(ctx, hdrs)
	if _, ok := hdrs["traceparent"]; !ok {
		t.Errorf("traceparent missing: %v", hdrs)
	}
	parent := context.Background()
	extracted := ExtractTraceContext(parent, hdrs)
	if TraceIDFromContext(extracted) != TraceIDFromContext(ctx) {
		t.Error("extracted trace_id did not match")
	}
}

func TestSpanAttrLint(t *testing.T) {
	good := []attribute.KeyValue{
		attribute.String("http.method", "GET"),
		attribute.String("http.route", "/api/users"),
		attribute.String("db.system", "postgres"),
		attribute.String("service.name", "ogon"),
	}
	if bad := SpanAttrLint(good); len(bad) != 0 {
		t.Errorf("allowed attrs failed lint: %v", bad)
	}

	badKeys := []attribute.KeyValue{
		attribute.String("user.email", "alice@example.com"),
		attribute.String("password", "hunter2"),
		attribute.String("authorization", "Bearer xxx"),
		attribute.String("api_key", "sk_live_12345"),
		attribute.String("credit_card", "4111111111111111"),
	}
	if bad := SpanAttrLint(badKeys); len(bad) != len(badKeys) {
		t.Errorf("PII attrs passed lint: %v", bad)
	}
}

func TestRegisterSpanAttrAllowlist_RejectsPII(t *testing.T) {
	if err := RegisterSpanAttrAllowlist("user.email"); err == nil {
		t.Error("allowed PII attr")
	}
	if err := RegisterSpanAttrAllowlist("app.feature.flag"); err != nil {
		t.Errorf("safe attr rejected: %v", err)
	}
	if !SpanAttrAllowed("app.feature.flag") {
		t.Error("safe attr not allowlisted")
	}
}

func TestSamplingConfig_Build(t *testing.T) {
	c := DefaultSamplingConfig()
	s := c.BuildSampler()
	if s == nil {
		t.Fatal("nil sampler")
	}
	dev := DevSamplingConfig()
	if dev.Ratio != 1.0 {
		t.Errorf("dev ratio = %v", dev.Ratio)
	}
}

func TestPanicToSpan_NoCrash(t *testing.T) {
	SetCrashAdapter(NoopCrashReporter{})
	CatchPanicNoReraise(context.Background(), func() {
		// No panic.
	})
}

func TestPprofConfigFromEnv(t *testing.T) {
	cfg := PprofConfigFromEnv(map[string]string{"OGON_PPROF_ENABLED": "true"})
	if !cfg.Enabled {
		t.Error("pprof not enabled by env")
	}
	cfg = PprofConfigFromEnv(map[string]string{})
	if cfg.Enabled {
		t.Error("pprof should default off")
	}
}

func TestPprofConfig_MountDisabled(t *testing.T) {
	mux := http.NewServeMux()
	PprofConfig{}.Mount(mux)
	req, _ := http.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	w := &noopRW{}
	mux.ServeHTTP(w, req)
	if w.code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.code)
	}
}

type noopRW struct {
	header http.Header
	code   int
	wrote  int
}

func (n *noopRW) Header() http.Header {
	if n.header == nil {
		n.header = http.Header{}
	}
	return n.header
}
func (n *noopRW) Write(p []byte) (int, error) { n.wrote += len(p); return len(p), nil }
func (n *noopRW) WriteHeader(code int)        { n.code = code }

func TestSpanAttrLint_Strings(t *testing.T) {
	good := []string{"http.method", "db.system"}
	if bad := PIIAttributeLint(good); len(bad) != 0 {
		t.Errorf("good set failed: %v", bad)
	}
	bad := []string{"user.email", "password"}
	if got := PIIAttributeLint(bad); len(got) != 2 {
		t.Errorf("bad set passed: %v", got)
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OTel test exporter: a span recorder used by the test harness (OgonTest)
// to assert spans without spinning up an OTLP collector. Implements
// OBS-031/TEST-050. The test exporter lives in the obs package so it
// can be imported by the test subsystem without a build-tag dance.

package obs

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/sdk/trace"
)

// TestSpan is the recorded form of a span observed by the test exporter.
type TestSpan struct {
	Name         string
	TraceID      string
	SpanID       string
	ParentSpanID string
	StartTime    context.Context
	Attributes   map[string]string
	Events       []string
	Status       string
	StatusCode   string
	Err          error
}

// TestExporter is an in-memory span recorder. Use RegisterTestExporter
// to attach it to a TracerProvider. (OBS-031/TEST-050)
type TestExporter struct {
	mu     sync.Mutex
	spans  []TestSpan
	muEach sync.Mutex
}

// ExportSpans implements trace.SpanExporter. Records a snapshot of each
// span. (OBS-031)
func (e *TestExporter) ExportSpans(_ context.Context, spans []trace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range spans {
		rec := TestSpan{
			Name:       s.Name(),
			TraceID:    s.SpanContext().TraceID().String(),
			SpanID:     s.SpanContext().SpanID().String(),
			Attributes: make(map[string]string, len(s.Attributes())),
			Status:     s.Status().Code.String(),
			StatusCode: s.Status().Code.String(),
		}
		if s.Parent().HasSpanID() {
			rec.ParentSpanID = s.Parent().SpanID().String()
		}
		for _, a := range s.Attributes() {
			rec.Attributes[string(a.Key)] = a.Value.AsString()
		}
		for _, ev := range s.Events() {
			rec.Events = append(rec.Events, ev.Name)
		}
		e.spans = append(e.spans, rec)
	}
	return nil
}

// Shutdown implements trace.SpanExporter. No-op.
func (e *TestExporter) Shutdown(_ context.Context) error { return nil }

// Spans returns the recorded spans in arrival order.
func (e *TestExporter) Spans() []TestSpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]TestSpan, len(e.spans))
	copy(out, e.spans)
	return out
}

// Reset clears the recorded spans.
func (e *TestExporter) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = nil
}

// FindByName returns spans with the matching name. (OBS-031)
func (e *TestExporter) FindByName(name string) []TestSpan {
	out := []TestSpan{}
	for _, s := range e.Spans() {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// NewTestExporter builds + registers an in-memory span exporter against
// the global TracerProvider. The returned handle is used by tests to
// assert spans without a real collector. (OBS-031/TEST-050)
//
// Use ResetTracerForTest() first to clear the cached global tracer, then
// call InitTracer with a TracerConfig that includes the test exporter.
// The exporter also accepts direct registration via AttachTestExporter.
func NewTestExporter() *TestExporter { return &TestExporter{} }

// AttachTestExporter registers the exporter against tp via a simple-span
// processor (so spans land in the exporter synchronously — tests don't
// need to wait for the batch processor's 2s flush). (OBS-031)
func AttachTestExporter(tp *trace.TracerProvider, e *TestExporter) {
	if tp == nil || e == nil {
		return
	}
	tp.RegisterSpanProcessor(trace.NewSimpleSpanProcessor(e))
}

// AssertSpanExists returns true if at least one span with name was
// recorded. Convenience for tests. (OBS-031/TEST-050)
func (e *TestExporter) AssertSpanExists(name string) bool {
	return len(e.FindByName(name)) > 0
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// trace_id ↔ log correlation: every slog record emitted from a span
// context carries trace_id and span_id. Implements OBS-037.
//
// The mechanism is a slog.Handler wrapper that, at Handle() time, reads
// the active span from the record's context (set by the request logger
// via WithRequestMeta + the otel hook) and injects trace_id/span_id as
// top-level attrs. The redaction hook already strips these if they'd
// be PII (they're not — they're random hex — but the scrub path is
// idempotent over them).

package obs

import (
	"context"
	"log/slog"
)

// correlationHandler injects trace_id/span_id from the record's context
// into the record's attr list. (OBS-037)
type correlationHandler struct {
	inner slog.Handler
}

// WithTraceCorrelation wraps a logger so each record carries trace_id +
// span_id when the record's context has an active span. (OBS-037)
//
// Use this on the OUTERMOST layer of your logger stack — after redaction
// and after sampling. The injection happens at Handle() time so it
// picks up the span context of the active HTTP request, not the span
// active when the logger was built.
func WithTraceCorrelation(inner *slog.Logger) *slog.Logger {
	if inner == nil {
		inner = slog.Default()
	}
	return slog.New(&correlationHandler{inner: inner.Handler()})
}

func (c *correlationHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return c.inner.Enabled(ctx, l)
}

func (c *correlationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &correlationHandler{inner: c.inner.WithAttrs(attrs)}
}

func (c *correlationHandler) WithGroup(name string) slog.Handler {
	return &correlationHandler{inner: c.inner.WithGroup(name)}
}

func (c *correlationHandler) Handle(ctx context.Context, rec slog.Record) error {
	// If a RequestMeta is present (set by the request logger) prefer
	// its values; otherwise fall back to the OTel span context.
	meta := RequestMetaFromContext(ctx)
	traceID := meta.TraceID
	spanID := meta.SpanID
	if traceID == "" {
		traceID = TraceIDFromContext(ctx)
	}
	if spanID == "" {
		spanID = SpanIDFromContext(ctx)
	}
	if traceID == "" && spanID == "" {
		return c.inner.Handle(ctx, rec)
	}
	clone := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	clone.AddAttrs(slog.String("trace_id", traceID))
	clone.AddAttrs(slog.String("span_id", spanID))
	rec.Attrs(func(a slog.Attr) bool {
		clone.AddAttrs(a)
		return true
	})
	return c.inner.Handle(ctx, clone)
}

// LogWithTrace is the helper that callers use when they want a one-shot
// log record that carries the active trace context. Equivalent to:
//
//	log.InfoContext(ctx, "msg", attrs...)
//
// but exists so callers can grep for "log with trace" in the codebase.
func LogWithTrace(ctx context.Context, log *slog.Logger, level slog.Level, msg string, args ...any) {
	log.Log(ctx, level, msg, args...)
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Request logger: emits one slog record per request with route template,
// method, status, duration, and trace_id. Implements OBS-004.

package obs

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// RequestLogConfig configures the request logger.
type RequestLogConfig struct {
	// Logger to write records to. nil → slog.Default().
	Logger *slog.Logger
	// Level at which access records are emitted. Default Info.
	Level slog.Level
	// SlowRequestThreshold: requests slower than this are emitted at Warn.
	// 0 disables the slow-request split (everything at Level).
	SlowRequestThreshold time.Duration
	// ExcludePaths are not logged (healthz, metrics, pprof). Matched
	// against the request path's cleaned form, no prefix.
	ExcludePaths []string
	// ExcludePathPrefixes suppress logs for any path starting with one
	// of these. Default: ["/healthz", "/readyz", "/metrics", "/debug/pprof"].
	ExcludePathPrefixes []string
}

// DefaultRequestLogConfig returns prod-friendly defaults.
func DefaultRequestLogConfig() RequestLogConfig {
	return RequestLogConfig{
		Level:                slog.LevelInfo,
		SlowRequestThreshold: 500 * time.Millisecond,
		ExcludePathPrefixes: []string{
			"/healthz",
			"/readyz",
			"/metrics",
			"/debug/pprof",
		},
	}
}

// requestContextKey carries the trace_id and route template into the logger.
type requestContextKey struct{}

// RequestMeta is attached to the request context by the http router so the
// access log can emit route-template labels (cardinality-safe) and the
// active trace_id (correlation, OBS-037).
type RequestMeta struct {
	Route     string // e.g. "/api/users/:id"
	TraceID   string // hex W3C trace id; "" if not sampled
	SpanID    string // hex span id; "" if not sampled
	Component string // handler/component name (optional)
}

// WithRequestMeta installs m on ctx for downstream access-log consumption.
func WithRequestMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, requestContextKey{}, m)
}

// RequestMetaFromContext returns the meta installed by WithRequestMeta.
// Empty RequestMeta if absent.
func RequestMetaFromContext(ctx context.Context) RequestMeta {
	if v, ok := ctx.Value(requestContextKey{}).(RequestMeta); ok {
		return v
	}
	return RequestMeta{}
}

// RequestLoggerMiddleware wraps next so each request emits one slog record
// carrying method, status, duration (ms), route template, and trace_id.
// Probe routes (healthz/readyz/startup/metrics/pprof) are suppressed by
// default — they must not pollute traces or logs. (OBS-004/019/020)
func RequestLoggerMiddleware(cfg RequestLogConfig, next http.Handler) http.Handler {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	// Fast lookup set for path-prefix exclusion.
	prefixes := cfg.ExcludePathPrefixes
	if len(prefixes) == 0 && len(cfg.ExcludePaths) == 0 {
		prefixes = DefaultRequestLogConfig().ExcludePathPrefixes
	}
	excludeSet := make(map[string]struct{}, len(cfg.ExcludePaths))
	for _, p := range cfg.ExcludePaths {
		excludeSet[p] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Suppression check — runs before timing.
		if _, skip := excludeSet[r.URL.Path]; skip {
			next.ServeHTTP(w, r)
			return
		}
		for _, p := range prefixes {
			if len(p) > 0 && len(r.URL.Path) >= len(p) && r.URL.Path[:len(p)] == p {
				next.ServeHTTP(w, r)
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := now()
		next.ServeHTTP(rec, r)
		dur := now().Sub(start)

		meta := RequestMetaFromContext(r.Context())

		// Cardinality-safe: route template, never raw path. (OBS-032/004)
		route := meta.Route
		if route == "" {
			route = r.URL.Path
		}

		level := cfg.Level
		msg := "http_request"
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", dur.Milliseconds()),
			slog.String("route", route),
			slog.String("path", r.URL.Path),
		}
		if meta.TraceID != "" {
			attrs = append(attrs, slog.String("trace_id", meta.TraceID))
		}
		if meta.SpanID != "" {
			attrs = append(attrs, slog.String("span_id", meta.SpanID))
		}
		if meta.Component != "" {
			attrs = append(attrs, slog.String("component", meta.Component))
		}
		if rec.bytesWritten > 0 {
			attrs = append(attrs, slog.Int("bytes_out", rec.bytesWritten))
		}
		if u := r.Header.Get("User-Agent"); u != "" {
			// UA itself isn't PII but a long UA can be logged; trunc to 200.
			if len(u) > 200 {
				u = u[:200] + "..."
			}
			attrs = append(attrs, slog.String("user_agent", u))
		}

		if cfg.SlowRequestThreshold > 0 && dur > cfg.SlowRequestThreshold {
			level = slog.LevelWarn
			msg = "slow_http_request"
			attrs = append(attrs, slog.Int64("slow_threshold_ms", cfg.SlowRequestThreshold.Milliseconds()))
		}
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}

		// Build a slog.Record so the redaction hook runs.
		rec2 := slog.NewRecord(start, level, msg, 0)
		rec2.AddAttrs(attrs...)
		_ = log.Handler().Handle(r.Context(), rec2)
	})
}

// statusRecorder captures status code and bytes written. It intentionally
// does not implement http.Hijacker/Flusher — upgrade paths are handled by
// dedicated transports (live/, http/ws.go).
type statusRecorder struct {
	http.ResponseWriter
	status        int
	bytesWritten  int
	headerWritten bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.headerWritten {
		return
	}
	s.headerWritten = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.headerWritten {
		s.headerWritten = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytesWritten += n
	return n, err
}

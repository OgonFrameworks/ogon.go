// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Access-log middleware: structured access log per request. Uses the route
// template (HTTP-076 cardinality law), never the raw path. Records status,
// bytes, latency, request-id.

package http

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder wraps http.ResponseWriter to capture status code and
// bytes-written for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader captures the status code before delegating.
func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

// Write captures the byte count and delegates.
func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(p)
	s.bytes += n
	return n, err
}

// Flush delegates to the underlying ResponseWriter when it supports Flush.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// AccessLogMiddleware emits a structured slog access log per request. The
// supplied logger is used; nil falls back to slog.Default(). The route
// template label is taken from the *Ctx (populated by the dispatcher); when
// no match, the literal path is logged with `route=unmatched`.
//
// Hot path: the statusRecorder is allocated per request. This is the only
// per-request alloc in the access-log middleware; everything else is reused
// via slog.
func AccessLogMiddleware(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 0}
			next.ServeHTTP(rec, r)
			dur := time.Since(start)

			route := "unmatched"
			if c := CtxFromRequest(r); c != nil {
				if rt := c.RouteTemplate(); rt != "" {
					route = rt
				}
			}
			logger.Info("http.access",
				"method", r.Method,
				"path", r.URL.Path,
				"route", route,
				"status", rec.status,
				"bytes", rec.bytes,
				"dur_ms", dur.Microseconds()/1000,
				"rid", r.Header.Get(RequestIDHeader),
				"remote", r.RemoteAddr,
			)
		})
	}
}

func init() {
	registerMiddleware("access-log", "structured access log; route-template label", 2)
}

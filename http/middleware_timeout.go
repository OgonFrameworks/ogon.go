// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Timeout middleware: enforces a per-request deadline. Cancels the request
// context on expiry; the dispatcher's handler MUST observe the context to
// avoid leaked work (CORE-013 no-leak law).
//
// HTTP-019: handlers MUST observe r.Context().Done(). The middleware relies
// on this contract — Go has no goroutine kill. The deadline is propagated
// via context only; the recover middleware (which wraps the dispatcher)
// catches any panic in the same goroutine.

package http

import (
	"context"
	"net/http"
	"time"
)

// TimeoutMiddleware returns a middleware that wraps the handler context with
// a deadline. The handler MUST observe r.Context().Done(); on context
// cancellation the handler short-circuits. We do NOT spawn a sidecar
// goroutine (no way to kill it without leaks). The http.Server's
// WriteTimeout is the hard cap on wire-level delays.
func TimeoutMiddleware(timeout time.Duration) Middleware {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func init() {
	registerMiddleware("timeout", "per-request context deadline", 3)
}

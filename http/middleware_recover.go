// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Recover middleware: catches panics in the handler chain and converts them
// to 500 ProblemDetails responses. Logs the panic with a stack hint.

package http

import (
	"fmt"
	"net/http"
	"runtime/debug"
)

// RecoverMiddleware returns the recover middleware. Panics are caught,
// logged via slog.Default, and rendered as 500 ProblemDetails. The middleware
// MUST be the first user-configurable element in the chain (after
// InitCtxMiddleware which is implicit).
//
// HTTP-013: panics never reach the client; the request is closed with a
// clean 500 ProblemDetails body.
func RecoverMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					// NOTE: do NOT call w.Flush() here. Several ResponseWriter
					// wrappers (incl. httptest.ResponseRecorder) implicitly
					// write a 200 status on Flush when none has been written
					// yet, which would clobber our 500. We write the 500
					// explicitly below.
					p := NewProblem(http.StatusInternalServerError,
						"Internal Server Error",
						"the request panicked; see server logs")
					fmt.Printf("ogon/http: panic recovered: %v\n%s\n", rv, debug.Stack())
					_ = p.Write(w, nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func init() {
	registerMiddleware("recover", "catches handler panics; emits 500 Problem", 0)
}

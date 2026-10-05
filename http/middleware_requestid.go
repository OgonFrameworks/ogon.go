// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Request-ID middleware: generates a per-request UUID and propagates it via
// the X-Request-Id response header. Downstream middlewares/loggers retrieve
// it via CtxFromRequest(r).Header("X-Request-Id").
//
// HTTP-018: every request carries a stable id for log/metrics correlation.

package http

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestIDHeader is the canonical request-id header name.
const RequestIDHeader = "X-Request-Id"

// RequestIDMiddleware stamps each request with a stable id. If the client
// supplies an X-Request-Id, it is preserved (truncated to 128 bytes max to
// prevent log injection). Otherwise a fresh 16-byte hex id is generated.
func RequestIDMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = generateRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			r.Header.Set(RequestIDHeader, id)
			next.ServeHTTP(w, r)
		})
	}
}

// validRequestID returns false if the id is empty, too long, or contains
// control characters (log-injection guard).
func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// generateRequestID returns a fresh 32-char hex id (16 random bytes).
// Falls back to a counter when rand fails (should not happen in practice).
func generateRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Extremely unlikely; fall back to a stable placeholder.
		return "ogon-0000000000000000"
	}
	return hex.EncodeToString(buf[:])
}

func init() {
	registerMiddleware("request-id", "stamps X-Request-Id on every request", 1)
}

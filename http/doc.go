// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// http subsystem: OGON-HTTP. Wraps net/http with a typed router, a fixed-order
// middleware chain, RFC 9457 ProblemDetails, SSE/WS realtime primitives, and
// sync.Pool-backed hot-path allocators. See PROMPT.md Part VI.

package http

import (
	"net/http"
	"strings"
	"sync"
)

// Version is the http subsystem version stamp. Bumped per release.
const Version = "1.0.0"

// headerPool reuses small header maps between requests. Hot path only;
// oversized maps are dropped back to GC to avoid pinning memory.
var headerPool = sync.Pool{
	New: func() any {
		h := make(map[string]string, 8)
		return &h
	},
}

func acquireHeaders() *map[string]string { return headerPool.Get().(*map[string]string) }
func releaseHeaders(h *map[string]string) {
	if h == nil {
		return
	}
	if len(*h) > 64 { // do not retain oversized maps
		*h = nil
		return
	}
	for k := range *h {
		delete(*h, k)
	}
	headerPool.Put(h)
}

// canonicalHeader returns the canonical MIME header form of name. Cheap
// wrapper around http.CanonicalHeaderKey for benchmarks/tests.
func canonicalHeader(name string) string {
	return http.CanonicalHeaderKey(name)
}

// splitCommaHeader splits a comma-separated header value, trimming
// whitespace per RFC 7230 §7. Empty entries are skipped.
func splitCommaHeader(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

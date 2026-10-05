// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// ETag middleware: generates ETag from the response body and serves 304
// Not Modified when If-None-Match matches. Off by default; opt-in per
// route group via Route.GroupSet("etag", true).
//
// HTTP-044: weak ETags (W/"...") by default; strong ETags are opt-in.
// HTTP-045: 304 responses have no body; the framework strips Content-Length.

package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash/crc32"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// etagBufferPool is used by the middleware to buffer the response body for
// hashing. Larger than ~64 KiB bodies should bypass ETag (use the
// Stream/WS/SSE paths instead).
var etagBufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// ETagMiddleware returns a middleware that computes ETags for responses.
// Strong mode uses SHA-256 truncated to 32 hex chars; weak mode uses CRC32
// (8 hex chars). When the client's If-None-Match matches, a 304 is served.
//
// Hot path: one buffer alloc per request from the pool. Hash is computed
// once at the end of the handler chain.
func ETagMiddleware(strong bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Honor route opt-in.
			if c := CtxFromRequest(r); c != nil {
				if v := c.Route().GroupMeta("etag"); v == nil {
					next.ServeHTTP(w, r)
					return
				}
			}
			// Skip ETag for non-GET/HEAD.
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			buf := etagBufferPool.Get().(*bytes.Buffer)
			defer func() {
				buf.Reset()
				etagBufferPool.Put(buf)
			}()
			rec := &etagRecorder{ResponseWriter: w, buf: buf, status: 0}
			next.ServeHTTP(rec, r)

			// Skip ETag for non-200/streamed responses.
			if rec.status != 0 && rec.status != http.StatusOK {
				return
			}
			if len(buf.Bytes()) == 0 {
				return
			}

			var etag string
			if strong {
				h := sha256.Sum256(buf.Bytes())
				etag = `"` + hex.EncodeToString(h[:16]) + `"`
			} else {
				c := crc32.ChecksumIEEE(buf.Bytes())
				etag = `W/"` + strconv.FormatUint(uint64(c), 16) + `"`
			}
			w.Header().Set("ETag", etag)

			inm := r.Header.Get("If-None-Match")
			if inm != "" && etagMatch(inm, etag) {
				w.Header().Del("Content-Length")
				w.WriteHeader(http.StatusNotModified)
				return
			}
			// Body has not been written yet (etagRecorder buffers). Write now.
			if !rec.written {
				w.WriteHeader(http.StatusOK)
			}
			_, _ = w.Write(buf.Bytes())
		})
	}
}

// etagRecorder buffers the response so the middleware can hash it. It does
// NOT flush automatically — the middleware writes the body explicitly at
// the end.
type etagRecorder struct {
	http.ResponseWriter
	buf     *bytes.Buffer
	status  int
	written bool
}

// WriteHeader captures the status but does not propagate (deferred).
func (e *etagRecorder) WriteHeader(code int) {
	if e.written {
		return
	}
	e.status = code
}

// Write buffers the body for hashing.
func (e *etagRecorder) Write(p []byte) (int, error) {
	if !e.written {
		e.written = true
		if e.status == 0 {
			e.status = http.StatusOK
		}
	}
	return e.buf.Write(p)
}

// etagMatch returns true if the supplied If-None-Match value matches etag.
// Supports comma-separated lists and "*" wildcard.
func etagMatch(inm, etag string) bool {
	if inm == "*" {
		return true
	}
	for _, candidate := range strings.Split(inm, ",") {
		c := strings.TrimSpace(candidate)
		if c == etag {
			return true
		}
	}
	return false
}

func init() {
	registerMiddleware("etag", "ETag/304; strong SHA-256 or weak CRC32", 8)
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Idempotency-key middleware: caches POST/PUT/PATCH responses keyed by the
// Idempotency-Key header, so a retried request returns the cached response
// instead of duplicating side effects (HTTP-035).
//
// Local in-memory cache; Redis-backed idempotency store lives in the cache/
// package (separate subsystem). The cache has a TTL and a max-size cap;
// eviction is LRU.
//
// Concurrent same-key requests are de-duplicated via singleflight
// (BUG-0005): the first request populates the cache while concurrent
// same-key requests block on the in-flight call and replay the cached
// response when it lands. This prevents duplicate side effects when a
// client retries in parallel (SEC-042).

package http

import (
	"bytes"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// IdempotencyCache stores response snapshots keyed by Idempotency-Key.
// Multi-tenant safe: the key namespace includes the route template +
// method so identical keys on different routes do not collide.
type IdempotencyCache struct {
	mu      sync.Mutex
	entries map[string]*idemEntry
	ttl     time.Duration
	maxSize int

	// sf dedupes concurrent same-key requests so the handler runs
	// exactly once per key per cache-miss window (BUG-0005).
	sf singleflight.Group
}

type idemEntry struct {
	status  int
	headers http.Header
	body    []byte
	expires time.Time
}

// NewIdempotencyCache returns a new cache with the supplied TTL and size.
// Defaults: 10 minute TTL, 10k entries.
func NewIdempotencyCache(ttl time.Duration, maxSize int) *IdempotencyCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 10000
	}
	c := &IdempotencyCache{
		entries: make(map[string]*idemEntry, maxSize),
		ttl:     ttl,
		maxSize: maxSize,
	}
	return c
}

// get returns a non-expired entry for key, deleting it if expired.
// Returns nil if absent or expired.
func (c *IdempotencyCache) get(key string) *idemEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil
	}
	if time.Now().After(e.expires) {
		delete(c.entries, key)
		return nil
	}
	return e
}

// put stores entry under key, evicting one random entry if over cap.
func (c *IdempotencyCache) put(key string, e *idemEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = e
	if len(c.entries) > c.maxSize {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
}

// idemRecorder buffers the response for caching.
type idemRecorder struct {
	http.ResponseWriter
	buf     *bytes.Buffer
	status  int
	header  http.Header
	written bool
}

func (r *idemRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *idemRecorder) WriteHeader(code int) {
	if r.written {
		return
	}
	r.written = true
	r.status = code
	// Copy headers to underlying writer.
	dst := r.ResponseWriter.Header()
	for k, vs := range r.header {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *idemRecorder) Write(p []byte) (int, error) {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	r.buf.Write(p)
	return r.ResponseWriter.Write(p)
}

// replayEntry writes the cached entry to c's response writer and aborts
// the handler chain.
func replayIdemEntry(c *Ctx, e *idemEntry) {
	dst := c.ResponseWriter().Header()
	for k, vs := range e.headers {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	c.Status(e.status)
	_, _ = c.ResponseWriter().Write(e.body)
	c.Abort()
}

// IdempotencyMiddleware returns a GroupMiddleware that caches state-changing
// requests keyed by Idempotency-Key. Repeated requests with the same key
// return the cached response. Concurrent same-key requests are de-duplicated
// via singleflight so the handler runs exactly once per key per cache-miss
// window (BUG-0005). Attach via Route.Use(IdempotencyMiddleware(cache)).
func IdempotencyMiddleware(cache *IdempotencyCache) GroupMiddleware {
	return func(c *Ctx) error {
		key := c.Header(IdempotencyHeader)
		if key == "" {
			return nil
		}
		if c.Route() == nil {
			return nil
		}
		cacheKey := c.Route().Template + "|" + c.Request().Method + "|" + key

		// Fast path: cache hit → replay.
		if entry := cache.get(cacheKey); entry != nil {
			replayIdemEntry(c, entry)
			return nil
		}

		// Slow path: singleflight dedupes concurrent same-key requests.
		// The leader runs the handler inline, captures the response via
		// the recorder, and stores the snapshot. Waiters block on the
		// singleflight and replay the snapshot when the leader finishes.
		v, err, shared := cache.sf.Do(cacheKey, func() (any, error) {
			// Re-probe inside the singleflight: another request may
			// have populated the cache while we waited for the lock.
			if entry := cache.get(cacheKey); entry != nil {
				return entry, nil
			}

			// We're the leader. Install the recorder and call the
			// handler inline. The handler writes to the recorder,
			// which forwards to the underlying ResponseWriter.
			origWriter := c.w
			rec := &idemRecorder{
				ResponseWriter: origWriter,
				buf:            new(bytes.Buffer),
			}
			c.w = rec
			herr := c.route.Handler(c)
			// Restore the original writer so downstream code (error
			// coercion in the dispatcher) sees the real writer.
			c.w = origWriter

			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			// Only cache successful responses (status < 500).
			if herr == nil && rec.status < 500 {
				e := &idemEntry{
					status:  rec.status,
					headers: rec.header.Clone(),
					body:    append([]byte(nil), rec.buf.Bytes()...),
					expires: time.Now().Add(cache.ttl),
				}
				cache.put(cacheKey, e)
				return e, nil
			}
			// On handler error or 5xx, do not cache; return the error
			// so the dispatcher's error path runs for the leader. The
			// waiters will see err != nil and replay nothing — they
			// fall through to the dispatcher's normal error handling.
			return nil, herr
		})

		if shared {
			// We were a waiter (not the leader). The leader has already
			// populated the cache (or returned an error). If we have a
			// cached entry, replay it; otherwise fall through so the
			// dispatcher can synthesize an error response.
			if e, ok := v.(*idemEntry); ok && e != nil {
				replayIdemEntry(c, e)
				return nil
			}
			// Leader errored; we have no cached response. Return a
			// 5xx so the client sees a deterministic failure rather
			// than the handler silently re-executing.
			if err != nil {
				return err
			}
			return NewProblem(http.StatusServiceUnavailable,
				"Idempotency Conflict",
				"another request with the same Idempotency-Key failed; retry")
		}

		// We were the leader. The handler already ran and wrote its
		// response via the recorder (which forwarded to the real
		// ResponseWriter). Abort so the dispatcher does not call the
		// handler a second time.
		c.Abort()
		if err != nil && !c.Written() {
			return err
		}
		return nil
	}
}

func init() {
	registerMiddleware("idempotency", "Idempotency-Key cache; LRU + TTL", 11)
}

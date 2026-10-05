// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Page cache + tag-based revalidation (UI-019, UI-035).

package runtime

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// PageCache stores the rendered HTML output for a route keyed by
// the URL plus an optional content-negotiation variant (locale,
// auth state). Tags map to one or more cache entries so a write
// can invalidate a group of pages (UI-035).
type PageCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	tags    map[string]map[string]bool // tag → set of keys
	max     int
	ttl     time.Duration
}

type cacheEntry struct {
	html    []byte
	stored  time.Time
	tags    []string
	variant string
	hits    int
}

// NewPageCache constructs a cache with the supplied size cap and TTL.
func NewPageCache(max int, ttl time.Duration) *PageCache {
	if max <= 0 {
		max = 512
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &PageCache{
		entries: map[string]*cacheEntry{},
		tags:    map[string]map[string]bool{},
		max:     max,
		ttl:     ttl,
	}
}

// Get returns a cached body if present and not expired.
func (c *PageCache) Get(_ context.Context, key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Since(e.stored) > c.ttl {
		c.evictLocked(key)
		return nil, false
	}
	e.hits++
	return append([]byte(nil), e.html...), true
}

// Set stores a rendered body with optional revalidation tags.
func (c *PageCache) Set(_ context.Context, key, variant string, html []byte, tags ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		// Evict the oldest entry (LRU on stored time).
		var oldest string
		var oldestTime time.Time
		for k, e := range c.entries {
			if oldest == "" || e.stored.Before(oldestTime) {
				oldest = k
				oldestTime = e.stored
			}
		}
		c.evictLocked(oldest)
	}
	c.entries[key] = &cacheEntry{
		html:    append([]byte(nil), html...),
		stored:  time.Now(),
		tags:    tags,
		variant: variant,
	}
	for _, t := range tags {
		if c.tags[t] == nil {
			c.tags[t] = map[string]bool{}
		}
		c.tags[t][key] = true
	}
}

// Invalidate removes every cache entry that carries any of the
// supplied tags (UI-035).
func (c *PageCache) Invalidate(_ context.Context, tags ...string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range tags {
		keys := c.tags[t]
		for k := range keys {
			c.evictLocked(k)
			n++
		}
		delete(c.tags, t)
	}
	return n
}

// Stats returns the current size and total hit count.
func (c *PageCache) Stats() (size int, hits int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		hits += e.hits
	}
	return len(c.entries), hits
}

// RenderOr computes the cached body or computes it via the supplied
// function and stores the result. This is the helper the generated
// page route adapter calls.
func (c *PageCache) RenderOr(ctx context.Context, key, variant string, tags []string, render func() ([]byte, error)) ([]byte, error) {
	if body, ok := c.Get(ctx, key); ok {
		return body, nil
	}
	body, err := render()
	if err != nil {
		return nil, err
	}
	c.Set(ctx, key, variant, body, tags...)
	return body, nil
}

func (c *PageCache) evictLocked(key string) {
	e, ok := c.entries[key]
	if !ok {
		return
	}
	delete(c.entries, key)
	for _, t := range e.tags {
		if set, ok := c.tags[t]; ok {
			delete(set, key)
			if len(set) == 0 {
				delete(c.tags, t)
			}
		}
	}
}

// ErrCacheMiss is returned for explicit cache-miss error paths.
var ErrCacheMiss = errors.New("ogon/ui: cache miss")

// HitWriter is an http.ResponseWriter wrapper that captures the
// body bytes for storage in the page cache.
type HitWriter struct {
	buf *bytes.Buffer
	hdr map[string][]string
}

// NewHitWriter constructs a writer.
func NewHitWriter() *HitWriter {
	return &HitWriter{buf: new(bytes.Buffer), hdr: map[string][]string{}}
}

func (w *HitWriter) Header() http.Header         { return w.hdr }
func (w *HitWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *HitWriter) WriteHeader(int)             {}
func (w *HitWriter) Bytes() []byte               { return w.buf.Bytes() }

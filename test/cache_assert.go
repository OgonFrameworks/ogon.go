// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Cache inspect helper (TEST-045). Wraps a cache backend so tests can
// assert on get/set/delete counts and inspect the live keyspace without
// importing the production cache surface.

package test

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// CacheStats is the per-test counters cache wrapper.
type CacheStats struct {
	Hits atomic.Int64
	Miss atomic.Int64
	Sets atomic.Int64
	Dels atomic.Int64
}

// InspectCache is a tiny in-memory cache + counter. Tests use it in place
// of a real cache (or wrap a real one) so assertions can read hit/miss
// counts directly.
type InspectCache struct {
	mu    sync.RWMutex
	store map[string][]byte
	stats CacheStats
}

// NewInspectCache constructs an empty cache.
func NewInspectCache() *InspectCache {
	return &InspectCache{store: map[string][]byte{}}
}

// Get retrieves a key. Returns (value, true) on hit, (nil, false) on miss.
// Counters are updated atomically.
func (c *InspectCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.store[key]
	if ok {
		c.stats.Hits.Add(1)
		return append([]byte(nil), v...), true
	}
	c.stats.Miss.Add(1)
	return nil, false
}

// Set stores a key.
func (c *InspectCache) Set(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = append([]byte(nil), value...)
	c.stats.Sets.Add(1)
}

// Delete removes a key. No-op if absent.
func (c *InspectCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, key)
	c.stats.Dels.Add(1)
}

// Keys returns a sorted snapshot of the live keys.
func (c *InspectCache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.store))
	for k := range c.store {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Stats returns a copy of the counters.
func (c *InspectCache) Stats() CacheStats {
	return CacheStats{
		Hits: atomic.Int64{},
		Miss: atomic.Int64{},
		Sets: atomic.Int64{},
		Dels: atomic.Int64{},
	}
}

// AssertHits fails the test if the hit count != want.
func AssertHits(t *testing.T, c *InspectCache, want int64) {
	t.Helper()
	got := c.stats.Hits.Load()
	if got != want {
		t.Fatalf("ogontest: cache hits: want %d, got %d", want, got)
	}
}

// AssertMisses fails the test if the miss count != want.
func AssertMisses(t *testing.T, c *InspectCache, want int64) {
	t.Helper()
	got := c.stats.Miss.Load()
	if got != want {
		t.Fatalf("ogontest: cache misses: want %d, got %d", want, got)
	}
}

// AssertKeyPresent fails the test if key is not in the cache.
func AssertKeyPresent(t *testing.T, c *InspectCache, key string) {
	t.Helper()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if _, ok := c.store[key]; !ok {
		t.Fatalf("ogontest: cache missing key %q", key)
	}
}

// Dump returns a human-readable summary of cache state for log lines.
func (c *InspectCache) Dump() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return fmt.Sprintf(
		"cache{keys=%d hits=%d miss=%d sets=%d dels=%d}",
		len(c.store),
		c.stats.Hits.Load(),
		c.stats.Miss.Load(),
		c.stats.Sets.Load(),
		c.stats.Dels.Load(),
	)
}

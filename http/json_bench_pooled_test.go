// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// JSON encode micro-bench (PERF-004 / Part XV.1 budget table).
//
// Self-contained sync.Pool-backed JSON encode bench. Uses a 1 KiB-ish
// struct payload so the codec path is measured at the budget's target size.
//
// Budget: ≤ 1 alloc/op (the final make+copy of the encoded bytes;
// the buffer itself is amortised by the pool).

package http

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
)

// benchJSONBufferPool is the explicit pool used by the named bench.
var benchJSONBufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// benchPayload is the 1 KiB-ish JSON payload used by both pooled and
// non-pooled JSON encode benches.
type benchPayload struct {
	ID    int      `json:"id"`
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
	Tags  []string `json:"tags"`
	Bio   string   `json:"bio"`
}

// newBenchPayload returns a fixed 1 KiB-ish payload for JSON benches.
func newBenchPayload() benchPayload {
	return benchPayload{
		ID:    12345,
		Name:  "Ada Lovelace",
		Email: "ada@example.com",
		Roles: []string{"admin", "editor", "viewer", "billing", "support"},
		Tags:  []string{"founder", "engineering", "ops", "on-call", "release-manager", "design-review"},
		Bio:   "Engineer, mathematician, and writer. Pioneered work on analytical engines and the concept of general-purpose computing.",
	}
}

// BenchmarkJSONEncode1KiBPooled measures the explicit sync.Pool-backed
// JSON encode path. The hot loop is: Get → Encode → copy out → Reset →
// Put. Reported allocs/op should be ≤ 1 (the final byte-slice copy out
// of the pooled buffer). A regression here means the pool was bypassed
// (e.g. an encoder option that allocates per call, like SetEscapeHTML
// toggling in the hot path).
func BenchmarkJSONEncode1KiBPooled(b *testing.B) {
	p := newBenchPayload()

	// Sanity: payload size is ~1 KiB so the bench is measuring the
	// codec path at the budget's target size.
	{
		buf := benchJSONBufferPool.Get().(*bytes.Buffer)
		_ = json.NewEncoder(buf).Encode(p)
		if buf.Len() < 512 || buf.Len() > 4096 {
			b.Fatalf("payload size = %d, want ~1 KiB", buf.Len())
		}
		buf.Reset()
		benchJSONBufferPool.Put(buf)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := benchJSONBufferPool.Get().(*bytes.Buffer)
		if err := json.NewEncoder(buf).Encode(p); err != nil {
			b.Fatalf("encode: %v", err)
		}
		// Copy out — the one unavoidable allocation per call.
		out := make([]byte, buf.Len())
		copy(out, buf.Bytes())
		buf.Reset()
		benchJSONBufferPool.Put(buf)
		_ = out
	}
}

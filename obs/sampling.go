// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Parent-based sampling config + slow-span export. Implements OBS-018/027.

package obs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// SamplingConfig configures parent-based sampling. (OBS-018)
type SamplingConfig struct {
	// Ratio is the trace-id ratio applied when there is no parent.
	// 1.0 = AlwaysOn (dev default), 0.1 = 10% of root traces (prod default).
	Ratio float64
	// RootOnly: when true, child spans inherit the parent's sampling
	// decision (parent-based). Default true.
	RootOnly bool
	// ProbeRoutesExcluded: probe paths (healthz/readyz/startup/metrics)
	// are never sampled. (OBS-019/020/021)
	ProbeRoutesExcluded bool
	// SlowSpanThreshold: spans longer than this are force-exported even
	// if their root was not sampled. (OBS-027)
	SlowSpanThreshold time.Duration
}

// DefaultSamplingConfig returns the prod default.
func DefaultSamplingConfig() SamplingConfig {
	return SamplingConfig{
		Ratio: 0.1, RootOnly: true, ProbeRoutesExcluded: true,
		SlowSpanThreshold: 500 * time.Millisecond,
	}
}

// DevSamplingConfig returns the dev default: AlwaysOn, no probes excluded
// (they're tiny anyway), 250ms slow-span threshold.
func DevSamplingConfig() SamplingConfig {
	return SamplingConfig{
		Ratio: 1.0, RootOnly: true, ProbeRoutesExcluded: false,
		SlowSpanThreshold: 250 * time.Millisecond,
	}
}

// BuildSampler returns an OTel SDK sampler honouring cfg. (OBS-018)
func (cfg SamplingConfig) BuildSampler() sdktrace.Sampler {
	base := sdktrace.TraceIDRatioBased(cfg.Ratio)
	if cfg.Ratio >= 1.0 {
		base = sdktrace.AlwaysSample()
	}
	if cfg.Ratio <= 0 {
		base = sdktrace.NeverSample()
	}
	if cfg.RootOnly {
		return sdktrace.ParentBased(base)
	}
	return base
}

// probePathSet is the set of paths whose spans never make it to the
// exporter. This is enforced by the slowSpanSink as well so even a
// sampled slow probe span is dropped. (OBS-019/020/021)
var probePathSet = map[string]struct{}{
	"/healthz":         {},
	"/healthz/startup": {},
	"/readyz":          {},
	"/readyz/startup":  {},
	"/metrics":         {},
	"/debug/pprof":     {},
	"/debug/pprof/":    {},
}

// IsProbePath returns true if path is a probe route that must be excluded
// from sampling. (OBS-019/020/021)
func IsProbePath(path string) bool {
	if _, ok := probePathSet[path]; ok {
		return true
	}
	for p := range probePathSet {
		if len(p) > 0 && len(path) >= len(p) && path[:len(p)] == p {
			return true
		}
	}
	return false
}

// slowSpanSink collects spans that exceed SlowSpanThreshold so they can
// be force-exported by the slow-span exporter. (OBS-027)
type slowSpanSink struct {
	mu        sync.Mutex
	queue     []slowSpanRecord
	maxSize   int
	threshold time.Duration
	dropped   atomic.Int64
}

// slowSpanRecord captures the minimum information needed to export a
// slow span out-of-band. (OBS-027)
type slowSpanRecord struct {
	Name      string
	Route     string
	StartTime time.Time
	Duration  time.Duration
	TraceID   string
	SpanID    string
	Attrs     []string // pre-rendered key=value strings (kept lean for budget)
}

func newSlowSpanSink(threshold time.Duration, maxSize int) *slowSpanSink {
	if maxSize <= 0 {
		maxSize = 1024
	}
	if threshold <= 0 {
		threshold = 500 * time.Millisecond
	}
	return &slowSpanSink{threshold: threshold, maxSize: maxSize}
}

// Record appends r to the queue unless the queue is full (then it's
// dropped + counted). (OBS-027/044 budget)
func (s *slowSpanSink) Record(r slowSpanRecord) {
	if r.Duration < s.threshold {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) >= s.maxSize {
		s.dropped.Add(1)
		// Drop oldest to make room — newest are usually more interesting.
		s.queue = s.queue[1:]
	}
	s.queue = append(s.queue, r)
}

// Drain returns the slow-span records accumulated since the last call.
// Tests use this to assert slow paths were captured. (OBS-027)
func (s *slowSpanSink) Drain() []slowSpanRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.queue
	s.queue = nil
	return out
}

// DroppedCount returns the number of slow spans that overflowed the ring.
func (s *slowSpanSink) DroppedCount() int64 { return s.dropped.Load() }

// slowSpanSinkGlobal is the package-level sink the request logger and
// otelhttp hook write to.
var slowSpanSinkGlobal = newSlowSpanSink(500*time.Millisecond, 1024)

// SetSlowSpanThreshold changes the global slow-span threshold. (OBS-027)
func SetSlowSpanThreshold(d time.Duration) {
	slowSpanSinkGlobal.mu.Lock()
	slowSpanSinkGlobal.threshold = d
	slowSpanSinkGlobal.mu.Unlock()
}

// DrainSlowSpans returns the slow-span records accumulated globally.
func DrainSlowSpans() []slowSpanRecord { return slowSpanSinkGlobal.Drain() }

// SlowSpanDropped returns the count of slow spans that overflowed the ring.
func SlowSpanDropped() int64 { return slowSpanSinkGlobal.DroppedCount() }

// RecordSlowSpan captures a span that exceeded the threshold. (OBS-027)
func RecordSlowSpan(ctx context.Context, name string, start time.Time, dur time.Duration, attrs ...string) {
	sc := oteltrace.SpanContextFromContext(ctx)
	slowSpanSinkGlobal.Record(slowSpanRecord{
		Name: name, StartTime: start, Duration: dur,
		TraceID: sc.TraceID().String(),
		SpanID:  sc.SpanID().String(),
		Attrs:   attrs,
	})
}

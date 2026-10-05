// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Debug log sampling: high-frequency debug records are sampled so they
// never dominate the log stream. Implements OBS-026.
//
// Strategy: every Nth debug record at a given key is emitted; the rest
// are dropped. The sampling rate is configurable per key (default 1/100
// for keys starting with `db.` or `http.`, 1/1 for everything else).
//
// Implementation: a slog.Handler wrapper that wraps the redaction
// handler. Records at Info+ pass through unsampled.

package obs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// SamplingRule applies a sampling rate to a single key (or prefix).
type SamplingRule struct {
	// Key matches the record's first attr value of "key" or the
	// "event" attribute. May be a prefix when HasPrefix is true.
	Key       string
	HasPrefix bool
	// Rate: 1 in N records is emitted. 1 disables sampling.
	Rate int
}

// samplingHandler samples Debug records per SamplingRule. (OBS-026)
type samplingHandler struct {
	inner  slog.Handler
	rules  []SamplingRule
	counts sync.Map // map[string]*atomic.Int64
}

// SamplingDebugConfig configures the default debug log sampling rules.
// (OBS-026) Renamed to avoid collision with SamplingConfig in sampling.go.
type SamplingDebugConfig struct {
	// Rules applied to Debug-level records only.
	Rules []SamplingRule
	// DefaultRate applies when no rule matches. Default 1 (no sampling).
	DefaultRate int
}

// DefaultSamplingRules returns the framework defaults. (OBS-026)
func DefaultSamplingRules() []SamplingRule {
	return []SamplingRule{
		{Key: "db.", HasPrefix: true, Rate: 100},
		{Key: "http.", HasPrefix: true, Rate: 50},
		{Key: "cache.", HasPrefix: true, Rate: 25},
		{Key: "queue.", HasPrefix: true, Rate: 25},
		{Key: "live.", HasPrefix: true, Rate: 25},
	}
}

// WithSampling wraps the logger so high-frequency debug records are
// sampled per the supplied rules. Records at Info+ pass through.
// (OBS-026)
func WithSampling(inner *slog.Logger, rules []SamplingRule) *slog.Logger {
	if inner == nil {
		inner = slog.Default()
	}
	h := &samplingHandler{
		inner: inner.Handler(),
		rules: rules,
	}
	return slog.New(h)
}

func (s *samplingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return s.inner.Enabled(ctx, l)
}

func (s *samplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &samplingHandler{inner: s.inner.WithAttrs(attrs), rules: s.rules}
}

func (s *samplingHandler) WithGroup(name string) slog.Handler {
	return &samplingHandler{inner: s.inner.WithGroup(name), rules: s.rules}
}

func (s *samplingHandler) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level < slog.LevelInfo {
		// Look for an attr called "key" or "event" to apply rules to.
		key := ""
		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == "key" || a.Key == "event" {
				key = a.Value.String()
				return false
			}
			return true
		})
		if !s.shouldSample(key) {
			return s.inner.Handle(ctx, rec)
		}
		return nil
	}
	return s.inner.Handle(ctx, rec)
}

// shouldSample returns true if the record should be DROPPED. (OBS-026)
func (s *samplingHandler) shouldSample(key string) bool {
	if key == "" {
		return false
	}
	rate := 0
	for _, r := range s.rules {
		if r.HasPrefix {
			if startsWith(key, r.Key) {
				rate = r.Rate
				break
			}
		} else if r.Key == key {
			rate = r.Rate
			break
		}
	}
	if rate <= 1 {
		return false
	}
	counter, _ := s.counts.LoadOrStore(key, new(atomic.Int64))
	n := counter.(*atomic.Int64).Add(1)
	// Drop every record except the Nth.
	return n%int64(rate) != 0
}

// startsWith is a tiny strings.HasPrefix shim — inline because hot path.
func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// SamplingDebugLogger returns a logger that samples debug records.
// Convenience wrapper around WithSampling using the default rules. (OBS-026)
func SamplingDebugLogger(inner *slog.Logger) *slog.Logger {
	return WithSampling(inner, DefaultSamplingRules())
}

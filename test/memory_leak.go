// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Memory-leak soak template (TEST-052) and chaos helpers (TEST-053). The
// soak template runs a workload for a bounded number of iterations and
// asserts that heap usage does not grow unbounded. The chaos helpers
// simulate connection failures so tests can prove graceful-degrade paths.

package test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// SoakConfig captures the inputs to a memory-leak soak test (TEST-052).
type SoakConfig struct {
	Iterations       int           // total iterations; default 1000
	WarmupIters      int           // iterations before measurement; default 50
	MaxHeapGrowthPct float64       // allowed heap growth during measurement; default 25
	Period           time.Duration // how often to sample heap; default 10ms
	Work             func(ctx context.Context, iter int) error
}

// SoakResult is the measurement outcome.
type SoakResult struct {
	WarmupHeap uint64 // bytes after warmup
	FinalHeap  uint64 // bytes at end
	MaxHeap    uint64 // peak heap observed
	GrowthPct  float64
	Iterations int
	Errors     int
}

// RunSoak runs the soak template against cfg. The heap is sampled via
// runtime.ReadMemStats — the fixture does not import the obs package so
// it works without an otel pipeline wired up.
func RunSoak(t *testing.T, cfg SoakConfig) SoakResult {
	t.Helper()
	if cfg.Iterations == 0 {
		cfg.Iterations = 1000
	}
	if cfg.WarmupIters == 0 {
		cfg.WarmupIters = 50
	}
	if cfg.MaxHeapGrowthPct == 0 {
		cfg.MaxHeapGrowthPct = 25
	}
	if cfg.Period == 0 {
		cfg.Period = 10 * time.Millisecond
	}
	if cfg.Work == nil {
		t.Fatalf("ogontest: SoakConfig.Work required")
	}

	// Warmup phase: run cfg.WarmupIters iterations without measuring.
	for i := 0; i < cfg.WarmupIters; i++ {
		if err := cfg.Work(context.Background(), i); err != nil {
			t.Fatalf("ogontest: soak warmup[%d]: %v", i, err)
		}
	}

	// Measurement phase: sample heap periodically while running work.
	var (
		mu         sync.Mutex
		warmupHeap uint64
		maxHeap    uint64
		finalHeap  uint64
		errCount   int32
	)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(cfg.Period)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				stats := readMemStats()
				mu.Lock()
				if warmupHeap == 0 {
					warmupHeap = stats.HeapAlloc
				}
				if stats.HeapAlloc > maxHeap {
					maxHeap = stats.HeapAlloc
				}
				mu.Unlock()
			}
		}
	}()

	for i := 0; i < cfg.Iterations; i++ {
		if err := cfg.Work(context.Background(), i); err != nil {
			atomic.AddInt32(&errCount, 1)
		}
	}
	close(done)

	stats := readMemStats()
	mu.Lock()
	finalHeap = stats.HeapAlloc
	growthPct := 0.0
	if warmupHeap > 0 {
		growthPct = float64(finalHeap) / float64(warmupHeap) * 100
	}
	mu.Unlock()

	result := SoakResult{
		WarmupHeap: warmupHeap,
		FinalHeap:  finalHeap,
		MaxHeap:    maxHeap,
		GrowthPct:  growthPct,
		Iterations: cfg.Iterations,
		Errors:     int(atomic.LoadInt32(&errCount)),
	}

	// Assert no leak.
	if growthPct > 100+cfg.MaxHeapGrowthPct {
		t.Fatalf("ogontest: soak leak: heap grew from %d to %d bytes (%.1f%%, max %d) — exceeds %.0f%% threshold",
			warmupHeap, finalHeap, growthPct-100, maxHeap, cfg.MaxHeapGrowthPct)
	}
	if result.Errors > 0 {
		t.Fatalf("ogontest: soak reported %d errors", result.Errors)
	}
	return result
}

// ---- chaos helpers (TEST-053) ----

// ChaosKilledConn is the chaos primitive: it returns a net.Conn that
// immediately fails every read/write with net.ErrClosed. Tests use this
// to verify the production layer degrades gracefully under connection loss.
type ChaosKilledConn struct {
	mu sync.Mutex
}

// Read always returns an error.
func (*ChaosKilledConn) Read(b []byte) (n int, err error) {
	return 0, net.ErrClosed
}

// Write always returns an error.
func (*ChaosKilledConn) Write(b []byte) (n int, err error) {
	return 0, net.ErrClosed
}

// Close is a no-op.
func (*ChaosKilledConn) Close() error { return nil }

// LocalAddr returns a dummy address.
func (*ChaosKilledConn) LocalAddr() net.Addr { return dummyAddr{} }

// RemoteAddr returns a dummy address.
func (*ChaosKilledConn) RemoteAddr() net.Addr { return dummyAddr{} }

// SetDeadline is a no-op.
func (*ChaosKilledConn) SetDeadline(t time.Time) error { return nil }

// SetReadDeadline is a no-op.
func (*ChaosKilledConn) SetReadDeadline(t time.Time) error { return nil }

// SetWriteDeadline is a no-op.
func (*ChaosKilledConn) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "chaos" }
func (dummyAddr) String() string  { return "chaos:0" }

// assert net.Conn implementation at compile time
var _ net.Conn = (*ChaosKilledConn)(nil)

// ChaosFlaky returns a function that fails every Nth call. Tests use this
// to simulate intermittent network failures.
func ChaosFlaky(every int) func() error {
	var counter atomic.Int64
	return func() error {
		if every <= 0 {
			return nil
		}
		n := counter.Add(1)
		if int(n)%every == 0 {
			return errors.New("ogontest: chaos flaky failure")
		}
		return nil
	}
}

// ChaosJitter returns a function that sleeps for a random duration up to
// max and then optionally fails. Use to simulate latency spikes.
func ChaosJitter(max time.Duration, failPct float64) func() error {
	if max < 0 {
		max = 0
	}
	if failPct < 0 {
		failPct = 0
	}
	if failPct > 1 {
		failPct = 1
	}
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(time.Now().UnixNano())))
	return func() error {
		d := time.Duration(rng.Int64N(int64(max)))
		time.Sleep(d)
		if rng.Float64() < failPct {
			return fmt.Errorf("ogontest: chaos jitter failure (slept %v)", d)
		}
		return nil
	}
}

// readMemStats is the single point of indirection so the soak template
// doesn't import runtime directly (lets test stubs override in unit tests).
var readMemStats = defaultReadMemStats

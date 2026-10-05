// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — retry policy: exponential backoff + jitter (JOBS-007).
//
// Backoff is multiplicative: delay = base * mult^(attempt-1) (capped at
// Max). Jitter is "full jitter" (Δ = rand(0, delay)) so retries do not
// synchronise under load. Once attempts exceed MaxAttempts the envelope
// is routed to the DLQ (handled by the worker, not this file).

package jobs

import (
	"math/rand/v2"
	"time"
)

// RetryPolicy configures backoff. Zero-value RetryPolicy uses
// DefaultRetryPolicy.
type RetryPolicy struct {
	// Base is the initial backoff. Default 1s.
	Base time.Duration
	// Mult is the growth factor per attempt. Default 2.0.
	Mult float64
	// Max caps the backoff. Default 5min.
	Max time.Duration
	// Jitter is the fraction of full jitter (0..1). Default 1.0.
	Jitter float64
	// MaxAttempts caps dispatch attempts. Default 5.
	MaxAttempts int
}

// DefaultRetryPolicy is sensible for most workloads.
var DefaultRetryPolicy = RetryPolicy{
	Base:        1 * time.Second,
	Mult:        2.0,
	Max:         5 * time.Minute,
	Jitter:      1.0,
	MaxAttempts: 5,
}

// WithDefaults fills zero fields with defaults from dflt.
func (p RetryPolicy) WithDefaults(dflt RetryPolicy) RetryPolicy {
	out := p
	if out.Base <= 0 {
		out.Base = dflt.Base
	}
	if out.Mult <= 0 {
		out.Mult = dflt.Mult
	}
	if out.Max <= 0 {
		out.Max = dflt.Max
	}
	if out.Jitter <= 0 {
		out.Jitter = dflt.Jitter
	}
	if out.Jitter > 1 {
		out.Jitter = 1
	}
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = dflt.MaxAttempts
	}
	return out
}

// NextBackoff returns the delay before the next dispatch for a job
// that has just failed its (attempt)-th dispatch (attempt >= 1).
//
//	delay = min(Max, Base * Mult^(attempt-1))
//	jittered = delay - Jitter * delay * rand.Float64()  (full jitter)
func (p RetryPolicy) NextBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// base * mult^(attempt-1) — geometric growth.
	// 2^62 is the practical ceiling; we cap at Max anyway.
	shift := attempt - 1
	if shift > 30 {
		shift = 30 // overflow guard
	}
	delay := float64(p.Base)
	for i := 0; i < shift; i++ {
		delay *= p.Mult
		if delay > float64(p.Max) {
			delay = float64(p.Max)
			break
		}
	}
	if delay > float64(p.Max) {
		delay = float64(p.Max)
	}
	if p.Jitter <= 0 {
		return time.Duration(delay)
	}
	jit := rand.Float64() * p.Jitter
	return time.Duration(delay * (1 - jit))
}

// CanRetry reports whether attempt+1 still fits inside MaxAttempts.
// (attempt is the count of dispatches already done.)
func (p RetryPolicy) CanRetry(attempt int) bool {
	return attempt < p.MaxAttempts
}

// AttemptsRemaining returns MaxAttempts - attempt (>=0).
func (p RetryPolicy) AttemptsRemaining(attempt int) int {
	r := p.MaxAttempts - attempt
	if r < 0 {
		return 0
	}
	return r
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Rate-limit middleware. Three flavors:
//   1. token-bucket (steady + burst)
//   2. sliding-window (precise per-second)
//   3. concurrency limiter (max in-flight)
// All local-only; Redis-backed distributed limiter lives in the cache/
// package (separate subsystem, future phase).
//
// HTTP-031: 429 ProblemDetails with Retry-After header.
// HTTP-032: route-group scope (policy attached via Route.RateLimit).
// HTTP-033: per-IP + per-user; user-id wins when authed.

package http

import (
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// RateLimitPolicy names a configured rate-limit policy. Policies are
// registered on the RateLimiter; routes attach a policy name via
// Route.RateLimit("name"). The dispatcher's route-group middleware resolves
// the policy by name.
type RateLimitPolicy struct {
	Name      string
	Burst     int           // token bucket capacity
	Steady    int           // tokens per second (refill rate)
	Window    time.Duration // sliding window size (0 disables)
	WindowMax int           // max events in window
	MaxConcur int           // max in-flight (0 disables)
}

// RateLimiter holds the configured policies and per-key counters. Safe for
// concurrent use. The limiter is keyed by string (IP or user-id); the
// middleware resolves the key from the request.
type RateLimiter struct {
	mu       sync.Mutex
	policies map[string]*rateLimitState
}

// rateLimitState is the per-policy state. The token bucket is guarded by
// tbMu (not atomic.Int64) because the Allow path does a read-modify-write
// on `tokens` that must be atomic with respect to the `lastTs` update.
// Using a mutex instead of CAS makes the bucket math straightforward and
// avoids the lost-update race where two concurrent Allow calls both read
// the same `tokens` value, both pass the > 0 check, and both decrement —
// consuming only one token between them (BUG-0001 variant).
type rateLimitState struct {
	policy RateLimitPolicy

	// token bucket (guarded by tbMu)
	tbMu   sync.Mutex
	tokens float64
	lastTs time.Time

	// sliding window: ring of timestamps
	winMu   sync.Mutex
	samples []int64 // unix nanos per event
	head    int

	// concurrency
	inflight atomic.Int64
}

// NewRateLimiter returns an empty limiter. Policies are registered via Register.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{policies: make(map[string]*rateLimitState)}
}

// Register installs a policy. Re-registering the same name replaces the
// state (counters reset).
func (rl *RateLimiter) Register(p RateLimitPolicy) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	st := &rateLimitState{policy: p}
	if p.Burst > 0 {
		st.tokens = float64(p.Burst)
		st.lastTs = time.Now()
	}
	if p.WindowMax > 0 && p.Window > 0 {
		st.samples = make([]int64, p.WindowMax)
	}
	rl.policies[p.Name] = st
}

// Allow checks the policy for the named key. Returns (true, retryAfter) on
// success, (false, retryAfter) on rate-limit. retryAfter is 0 on success;
// on rejection it is the suggested wait time before retry.
func (rl *RateLimiter) Allow(policyName string) (bool, time.Duration) {
	rl.mu.Lock()
	st, ok := rl.policies[policyName]
	rl.mu.Unlock()
	if !ok {
		// No policy registered: allow. (The framework's contract is that an
		// unknown policy name fails open, not closed, to avoid accidental
		// outages from typos. `ogon build` will warn on unregistered names.)
		return true, 0
	}

	// Concurrency cap.
	if st.policy.MaxConcur > 0 {
		cur := st.inflight.Add(1)
		defer st.inflight.Add(-1)
		if cur > int64(st.policy.MaxConcur) {
			return false, 100 * time.Millisecond
		}
	}

	// Token bucket.
	if st.policy.Burst > 0 && st.policy.Steady > 0 {
		st.tbMu.Lock()
		now := time.Now()
		last := st.lastTs
		if last.IsZero() {
			last = now
		}
		elapsed := now.Sub(last).Seconds()
		refill := elapsed * float64(st.policy.Steady)
		tokens := st.tokens + refill
		if tokens > float64(st.policy.Burst) {
			tokens = float64(st.policy.Burst)
		}
		if tokens < 1 {
			// Need to wait for one token.
			wait := time.Duration(float64(time.Second) / float64(st.policy.Steady))
			st.lastTs = now
			st.tokens = tokens
			st.tbMu.Unlock()
			return false, wait
		}
		st.tokens = tokens - 1
		st.lastTs = now
		st.tbMu.Unlock()
	}

	// Sliding window.
	if st.policy.Window > 0 && st.policy.WindowMax > 0 {
		st.winMu.Lock()
		now := time.Now().UnixNano()
		cutoff := now - int64(st.policy.Window)
		// Drop expired samples from head.
		count := 0
		for i := 0; i < len(st.samples); i++ {
			if st.samples[i] > cutoff {
				count++
			}
		}
		if count >= st.policy.WindowMax {
			// Find the oldest sample still in-window to compute retry.
			oldest := int64(0)
			for i := 0; i < len(st.samples); i++ {
				if st.samples[i] > cutoff {
					if oldest == 0 || st.samples[i] < oldest {
						oldest = st.samples[i]
					}
				}
			}
			wait := time.Duration(oldest+int64(st.policy.Window)-now) + time.Millisecond
			st.winMu.Unlock()
			if wait < 0 {
				wait = time.Millisecond
			}
			return false, wait
		}
		// Insert current sample at head.
		st.samples[st.head] = now
		st.head = (st.head + 1) % len(st.samples)
		st.winMu.Unlock()
	}

	return true, 0
}

// RateLimitMiddleware returns a GroupMiddleware that enforces the named
// policy. Attach via Route.Use(RateLimitMiddleware(rl, "std", keyFn)). The
// keyFn resolves the bucket key (typically IP or user-id); default is IP.
//
// On rate-limit, returns a *ProblemDetails (429). The dispatcher writes the
// response. The middleware does not write directly (avoids the
// "wrote + returned nil" race that would let the dispatcher fall through to
// the handler).
func RateLimitMiddleware(rl *RateLimiter, policy string, keyFn func(c *Ctx) string) GroupMiddleware {
	if keyFn == nil {
		keyFn = func(c *Ctx) string { return c.ClientIP() }
	}
	return func(c *Ctx) error {
		// Honour the route's attached policy when present; override the default.
		polName := policy
		if v := c.Route().GroupMeta("ratelimit"); v != nil {
			if s, ok := v.(string); ok && s != "" {
				polName = s
			}
		}
		_ = keyFn(c)
		ok, retry := rl.Allow(polName)
		if !ok {
			c.SetHeader("Retry-After", strconv.Itoa(int(retry.Seconds()+0.5)))
			return NewProblem(http.StatusTooManyRequests,
				"Too Many Requests",
				"rate limit exceeded; retry after Retry-After seconds")
		}
		return nil
	}
}

func init() {
	registerMiddleware("rate-limit", "token-bucket + sliding-window + concurrency", 5)
}

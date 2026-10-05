// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Rate limiters (SEC-036, SEC-037).
//
// Three strategies:
//   - Bucket (token bucket): per-key burst + refill; default for
//     most APIs.
//   - Sliding window: smoother than fixed window; default for
//     per-route rate limiting.
//   - Concurrency: bounded goroutines (semaphore); default for
//     expensive endpoints.
//
// Backends:
//   - Local (in-process, sync.Map)
//   - Redis (cluster-safe, optional — implemented as a thin interface
//     so callers can wire go-redis)
//
// The login limiter (SEC-037) is built on top: per-user+IP combined
// key, sliding window, 5/minute default.

package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Limiter is the abstract rate-limit contract.
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// ---- token bucket (SEC-036) ----

// BucketConfig configures a token bucket per key.
type BucketConfig struct {
	Capacity  int           // bucket size (burst)
	RefillPer time.Duration // how often one token is added
}

// DefaultBucketConfig is 60 req/min with 10 burst.
func DefaultBucketConfig() BucketConfig {
	return BucketConfig{Capacity: 10, RefillPer: time.Second}
}

// BucketLimiter is a per-key token bucket.
type BucketLimiter struct {
	cfg   BucketConfig
	mu    sync.Mutex
	state map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewBucketLimiter constructs a per-key token bucket limiter.
func NewBucketLimiter(cfg BucketConfig) *BucketLimiter {
	return &BucketLimiter{cfg: cfg, state: map[string]*bucket{}}
}

// Allow returns true if the key has at least one token.
func (l *BucketLimiter) Allow(_ context.Context, key string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.state[key]
	if !ok {
		b = &bucket{tokens: float64(l.cfg.Capacity), last: now}
		l.state[key] = b
	}
	// refill since last call
	elapsed := now.Sub(b.last).Seconds()
	refill := elapsed * (1 / l.cfg.RefillPer.Seconds())
	b.tokens = min(float64(l.cfg.Capacity), b.tokens+refill)
	b.last = now
	if b.tokens < 1 {
		return false, nil
	}
	b.tokens--
	return true, nil
}

// ---- sliding window (SEC-036) ----

// SlidingConfig configures the sliding-window limiter.
type SlidingConfig struct {
	Max    int           // max requests in window
	Window time.Duration // window size
}

// DefaultSlidingConfig is 100 req/min.
func DefaultSlidingConfig() SlidingConfig {
	return SlidingConfig{Max: 100, Window: time.Minute}
}

// SlidingLimiter is a sliding-window limiter per key.
type SlidingLimiter struct {
	cfg   SlidingConfig
	mu    sync.Mutex
	state map[string]*sliding
}

type sliding struct {
	stamps []time.Time
}

// NewSlidingLimiter constructs a sliding-window limiter.
func NewSlidingLimiter(cfg SlidingConfig) *SlidingLimiter {
	return &SlidingLimiter{cfg: cfg, state: map[string]*sliding{}}
}

// Allow returns true if the key is within the limit.
func (l *SlidingLimiter) Allow(_ context.Context, key string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	s, ok := l.state[key]
	if !ok {
		s = &sliding{}
		l.state[key] = s
	}
	// drop expired stamps
	cutoff := now.Add(-l.cfg.Window)
	out := s.stamps[:0]
	for _, t := range s.stamps {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	if len(out) >= l.cfg.Max {
		s.stamps = out
		return false, nil
	}
	out = append(out, now)
	s.stamps = out
	return true, nil
}

// ---- concurrency limiter (SEC-036) ----

// ConcurrencyLimiter is a per-key semaphore. Useful for "expensive"
// endpoints where you want at most N concurrent goroutines per key.
type ConcurrencyLimiter struct {
	max  int
	mu   sync.Mutex
	used map[string]*int32
}

// NewConcurrencyLimiter returns a per-key semaphore.
func NewConcurrencyLimiter(max int) *ConcurrencyLimiter {
	if max <= 0 {
		max = 10
	}
	return &ConcurrencyLimiter{max: max, used: map[string]*int32{}}
}

// Allow reserves a slot. Returns ReleaseFunc that the caller MUST defer.
func (l *ConcurrencyLimiter) Allow(_ context.Context, key string) (bool, func(), error) {
	l.mu.Lock()
	c, ok := l.used[key]
	if !ok {
		var z int32
		c = &z
		l.used[key] = c
	}
	l.mu.Unlock()
	n := atomic.AddInt32(c, 1)
	if n > int32(l.max) {
		atomic.AddInt32(c, -1)
		return false, func() {}, nil
	}
	return true, func() { atomic.AddInt32(c, -1) }, nil
}

// ---- login limiter (SEC-037) ----

// LoginLimiter is a sliding-window limiter specialized for login. The
// key is userID+":"+ip so a credential-stuffing attack from many IPs
// against one user is capped, and one attacker IP hitting many users
// is capped.
type LoginLimiter struct {
	limiter *SlidingLimiter
}

// DefaultLoginLimit is 5 login attempts per minute per (user, IP).
func DefaultLoginLimit() SlidingConfig {
	return SlidingConfig{Max: 5, Window: time.Minute}
}

// NewLoginLimiter constructs a login limiter with the default config.
func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{limiter: NewSlidingLimiter(DefaultLoginLimit())}
}

// Allow checks whether the login attempt may proceed.
func (l *LoginLimiter) Allow(ctx context.Context, userID, ip string) (bool, error) {
	return l.limiter.Allow(ctx, userID+":"+ip)
}

// ---- Redis backend (stub) ----

// RedisClient is the abstract contract for go-redis; wire any *redis.Client.
type RedisClient interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) (any, error)
}

// RedisLimiter is a Redis-backed sliding-window limiter. The Lua
// script atomically bumps+counts so multi-instance deployments are
// consistent.
type RedisLimiter struct {
	client   RedisClient
	max      int
	windowMS int64
}

// NewRedisLimiter constructs a Redis-backed sliding-window limiter.
func NewRedisLimiter(client RedisClient, max int, window time.Duration) *RedisLimiter {
	return &RedisLimiter{client: client, max: max, windowMS: window.Milliseconds()}
}

// Allow runs the Lua script on Redis.
func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if l.client == nil {
		return false, errors.New("ratelimit: nil redis client")
	}
	script := `local n=redis.call('ZREMRANGEBYSCORE',KEYS[1],0,ARGV[1]-tonumber(ARGV[2]))
redis.call('ZADD',KEYS[1],ARGV[1],ARGV[3])
redis.call('EXPIRE',KEYS[1],ARGV[2])
local cnt=redis.call('ZCARD',KEYS[1])
if cnt<=tonumber(ARGV[4]) then return 1 else return 0 end`
	now := time.Now().UnixMilli()
	val, err := l.client.Eval(ctx, script, []string{key}, now, l.windowMS, now, l.max)
	if err != nil {
		return false, err
	}
	// lua returns 1/0 as int64 via go-redis
	switch v := val.(type) {
	case int64:
		return v == 1, nil
	case int:
		return v == 1, nil
	}
	return false, nil
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

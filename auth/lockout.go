// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Brute-force / lockout protection (SEC-021, SEC-037).
// Breached-password check via k-anonymity (SEC-022).
//
// All counters are kept in Redis (or in-memory fallback) and locked
// atomically. Lockout applies per-user AND per-IP to defeat
// distributed credential stuffing.

package auth

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OgonFrameworks/ogon.go/auth/password"
	"github.com/OgonFrameworks/ogon.go/diag"
)

// LockoutConfig governs lockout behaviour.
type LockoutConfig struct {
	MaxFailures  int           // failures before lockout; default 5
	Window       time.Duration // counter window; default 15m
	LockDuration time.Duration // how long the lock holds; default 15m
	// Per-IP cap defeats one user being attacked from many IPs.
	MaxFailuresPerIP int           // default 50 (broader; many users share IP)
	WindowPerIP      time.Duration // default 1h
}

// DefaultLockoutConfig returns production-safe defaults.
func DefaultLockoutConfig() LockoutConfig {
	return LockoutConfig{
		MaxFailures: 5, Window: 15 * time.Minute, LockDuration: 15 * time.Minute,
		MaxFailuresPerIP: 50, WindowPerIP: 1 * time.Hour,
	}
}

// LockoutStore is the backend for counters+lock state. The Redis
// implementation uses INCR+EXPIRE; the in-memory implementation is for
// tests and single-instance deployments.
type LockoutStore interface {
	IncrFailures(ctx context.Context, userKey, ipKey string) (userFail, ipFail int, err error)
	IsLocked(ctx context.Context, userKey, ipKey string) (userLocked, ipLocked bool, err error)
	Lock(ctx context.Context, userKey, ipKey string, d time.Duration) error
	Reset(ctx context.Context, userKey, ipKey string) error
}

// Lockout coordinates failed-attempt tracking and lockout enforcement.
type Lockout struct {
	cfg   LockoutConfig
	store LockoutStore
}

// NewLockout constructs a Lockout with the in-memory backend.
func NewLockout(cfg LockoutConfig, store LockoutStore) *Lockout {
	if store == nil {
		store = NewMemoryLockoutStore(cfg)
	}
	return &Lockout{cfg: cfg, store: store}
}

// ObserveFailure records a failed login. If the configured threshold is
// reached, the lock is engaged for LockDuration. Returns the active
// lock state for the caller's 401 response.
func (l *Lockout) ObserveFailure(ctx context.Context, userID, ip string) (userLocked, ipLocked bool, err error) {
	userKey, ipKey := l.keys(userID, ip)
	uf, ipf, err := l.store.IncrFailures(ctx, userKey, ipKey)
	if err != nil {
		return false, false, err
	}
	// any prior lock still active?
	ul, il, err := l.store.IsLocked(ctx, userKey, ipKey)
	if err != nil {
		return false, false, err
	}
	if ul || il {
		return ul, il, nil
	}
	if uf >= l.cfg.MaxFailures {
		if err := l.store.Lock(ctx, userKey, ipKey, l.cfg.LockDuration); err != nil {
			return false, false, err
		}
		ul = true
	}
	if ipf >= l.cfg.MaxFailuresPerIP {
		if err := l.store.Lock(ctx, userKey, ipKey, l.cfg.LockDuration); err != nil {
			return ul, false, err
		}
		il = true
	}
	return ul, il, nil
}

// OnSuccess resets the per-user counter on a successful login. The
// per-IP counter is NOT reset — IPs serve many users.
func (l *Lockout) OnSuccess(ctx context.Context, userID, ip string) error {
	userKey, ipKey := l.keys(userID, ip)
	return l.store.Reset(ctx, userKey, ipKey)
}

// CheckLock returns whether the user or IP is currently locked.
func (l *Lockout) CheckLock(ctx context.Context, userID, ip string) (bool, error) {
	userKey, ipKey := l.keys(userID, ip)
	ul, il, err := l.store.IsLocked(ctx, userKey, ipKey)
	if err != nil {
		return false, err
	}
	return ul || il, nil
}

// keys turns (user, ip) into stable hashes — both to bound memory in
// the in-memory store and to avoid leaking PII into Redis key logs.
func (l *Lockout) keys(userID, ip string) (userKey, ipKey string) {
	uh := sha1.Sum([]byte(userID))
	ih := sha1.Sum([]byte(ip))
	return "u:" + hex.EncodeToString(uh[:8]), "ip:" + hex.EncodeToString(ih[:8])
}

// ---- in-memory backend ----

// MemoryLockoutStore is the in-process LockoutStore implementation.
// Production deployments MUST wire a Redis-backed store.
type MemoryLockoutStore struct {
	cfg  LockoutConfig
	mu   sync.Mutex
	fail map[string]*counter
	lock map[string]time.Time // key → expiry
}

type counter struct {
	n     atomic.Int32
	first atomic.Int64 // unix ms of first hit in window
}

// NewMemoryLockoutStore returns a ready store.
func NewMemoryLockoutStore(cfg LockoutConfig) *MemoryLockoutStore {
	return &MemoryLockoutStore{
		cfg:  cfg,
		fail: map[string]*counter{},
		lock: map[string]time.Time{},
	}
}

// IncrFailures bumps both counters.
func (s *MemoryLockoutStore) IncrFailures(_ context.Context, userKey, ipKey string) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	uf := s.bump(userKey, now, s.cfg.Window)
	ipf := s.bump(ipKey, now, s.cfg.WindowPerIP)
	return uf, ipf, nil
}

func (s *MemoryLockoutStore) bump(key string, nowMs int64, window time.Duration) int {
	c, ok := s.fail[key]
	if !ok {
		c = &counter{}
		s.fail[key] = c
	}
	first := c.first.Load()
	if first == 0 || nowMs-first > window.Milliseconds() {
		c.first.Store(nowMs)
		c.n.Store(0)
	}
	c.n.Add(1)
	return int(c.n.Load())
}

// IsLocked returns the per-key lock state.
func (s *MemoryLockoutStore) IsLocked(_ context.Context, userKey, ipKey string) (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	ul := s.locked(userKey, now)
	il := s.locked(ipKey, now)
	return ul, il, nil
}

func (s *MemoryLockoutStore) locked(key string, now time.Time) bool {
	t, ok := s.lock[key]
	if !ok {
		return false
	}
	if now.After(t) {
		delete(s.lock, key)
		return false
	}
	return true
}

// Lock engages a lock for both keys (we lock whichever the caller
// indicates; same duration either way).
func (s *MemoryLockoutStore) Lock(_ context.Context, userKey, ipKey string, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp := time.Now().Add(d)
	s.lock[userKey] = exp
	s.lock[ipKey] = exp
	return nil
}

// Reset clears the per-user counter; keeps IP counter (multi-user IPs).
func (s *MemoryLockoutStore) Reset(_ context.Context, userKey, ipKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fail, userKey)
	delete(s.lock, userKey)
	// do NOT delete ipKey; many users share one IP
	return nil
}

// ---- breached-password check (k-anonymity, SEC-022) ----

// BreachChecker queries an HIBP-style API for SHA-1 suffix ranges.
type BreachChecker struct {
	client interface {
		Do(req *http.Request) (*http.Response, error)
	}
	baseURL string
}

// NewBreachedPasswordChecker returns the production checker. baseURL
// defaults to the HIBP range API.
func NewBreachedPasswordChecker(baseURL string) *BreachChecker {
	if baseURL == "" {
		baseURL = "https://api.pwnedpasswords.com/range"
	}
	return &BreachChecker{client: http.DefaultClient, baseURL: baseURL}
}

// IsBreached implements password.BreachChecker via k-anonymity.
// Sends only the first 5 chars of SHA-1; matches suffix locally.
func (b *BreachChecker) IsBreached(ctx context.Context, pw string) (bool, error) {
	prefix, suffix := password.SHA1Prefix(pw)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/"+prefix, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Add-Data", "false")
	resp, err := b.client.Do(req)
	if err != nil {
		return false, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-022", Title: "breach: request"})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("breach: status %s", resp.Status)
	}
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	// HIBP returns one "SUFFIX:COUNT" per line, uppercase hex suffix
	for _, line := range strings.Split(string(buf), "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		suf := strings.TrimSpace(line[:colon])
		if strings.EqualFold(suf, suffix) {
			return true, nil
		}
	}
	return false, nil
}

// ErrLockedOut is returned by login flows when lockout is active.
var ErrLockedOut = errors.New("auth: locked out (SEC-021)")

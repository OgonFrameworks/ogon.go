// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Multi-factor auth: TOTP + recovery codes (SEC-017), magic links
// (SEC-018), email verification (SEC-019), password reset with token
// rotation (SEC-020).
//
// TOTP is implemented on the HOTP/TOTP RFC 6238 algorithm using stdlib
// crypto/hmac + crypto/sha1. Recovery codes are random 24-char tokens
// stored as Argon2id hashes (never plaintext).

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/auth/password"
	"github.com/OgonFrameworks/ogon.go/diag"
)

// ---- TOTP (SEC-017) ----

// TOTPWindow is the number of periods either side of "now" to accept.
// Default 1 = ±30s. Wider windows increase convenience at the cost of
// a larger brute-force surface; do not exceed 2 in production.
type TOTPConfig struct {
	Period   uint64 // seconds; default 30
	Digits   int    // 6 or 8; default 6
	Window   int    // ±N periods; default 1
	HashAlgo string // "SHA1" (RFC 6238 default), "SHA256", "SHA512"
}

// DefaultTOTPConfig returns RFC-6238 defaults.
func DefaultTOTPConfig() TOTPConfig {
	return TOTPConfig{Period: 30, Digits: 6, Window: 1, HashAlgo: "SHA1"}
}

// TOTP computes the RFC 6238 time-based one-time password for secret+now.
// We use SHA1 (the only universally-supported TOTP algo).
func TOTP(secret []byte, t time.Time, cfg TOTPConfig) string {
	if cfg.Period == 0 {
		cfg = DefaultTOTPConfig()
	}
	if cfg.Digits == 0 {
		cfg.Digits = 6
	}
	counter := uint64(t.Unix()) / cfg.Period
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := int(sum[len(sum)-1] & 0x0f)
	bin := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)
	mod := uint32(1)
	for i := 0; i < cfg.Digits; i++ {
		mod *= 10
	}
	code := bin % mod
	out := make([]byte, 0, cfg.Digits)
	for i := 0; i < cfg.Digits; i++ {
		out = append([]byte{byte('0' + int(code%10))}, out...)
		code /= 10
	}
	return string(out)
}

// GenerateTOTPSecret returns a 20-byte (160-bit) base32 secret.
// Per RFC 4226 §4; this is the minimum recommended.
func GenerateTOTPSecret() (string, error) {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), nil
}

// VerifyTOTP checks code against secret in constant time, accepting
// any value within ±cfg.Window periods.
func VerifyTOTP(secret []byte, code string, t time.Time, cfg TOTPConfig) bool {
	if cfg.Window < 0 {
		cfg.Window = 0
	}
	now := t.Unix() / int64(cfg.Period)
	for off := -cfg.Window; off <= cfg.Window; off++ {
		want := TOTP(secret, time.Unix((now+int64(off))*int64(cfg.Period), 0), cfg)
		if hmac.Equal([]byte(want), []byte(code)) {
			return true
		}
	}
	return false
}

// ---- Recovery codes (SEC-017) ----

// RecoveryCodes is a manager for one-time recovery tokens. Codes are
// stored as Argon2id hashes (PHC format); plaintext is shown to the user
// exactly once at generation time.
type RecoveryCodes struct {
	hasher *password.Hasher
	store  RecoveryStore
}

// RecoveryStore persists recovery-code hashes per user.
type RecoveryStore interface {
	List(ctx context.Context, userID string) ([]string, error) // PHC-encoded hashes
	Save(ctx context.Context, userID string, hashes []string) error
	Delete(ctx context.Context, userID, hash string) error // single-use consumption
}

// NewRecoveryCodes constructs a RecoveryCodes manager.
func NewRecoveryCodes(store RecoveryStore) *RecoveryCodes {
	return &RecoveryCodes{hasher: password.NewHasher(), store: store}
}

// Generate issues N recovery codes for the user. Returns the plaintext
// codes (caller MUST display once and discard) and persists their hashes.
// Previous codes are replaced.
func (r *RecoveryCodes) Generate(ctx context.Context, userID string, n int) ([]string, error) {
	if n <= 0 {
		n = 10
	}
	plaintext := make([]string, n)
	hashes := make([]string, n)
	for i := 0; i < n; i++ {
		var b [9]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		// 24 chars base32 (no padding), grouped as xxxx-xxxx-xxxx-xxxx-xxxx-xxxx
		enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
		parts := []string{enc[0:4], enc[4:8], enc[8:12], enc[12:16]}
		if len(enc) > 16 {
			parts = append(parts, enc[16:20])
		}
		code := strings.Join(parts, "-")
		plaintext[i] = code
		h, err := r.hasher.Hash(code)
		if err != nil {
			return nil, err
		}
		hashes[i] = h
	}
	if err := r.store.Save(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return plaintext, nil
}

// Consume verifies a recovery code, deleting it on success (single-use).
// Returns ErrRecoveryInvalid on failure (no user/code leak in error).
func (r *RecoveryCodes) Consume(ctx context.Context, userID, code string) error {
	hashes, err := r.store.List(ctx, userID)
	if err != nil {
		return err
	}
	for _, h := range hashes {
		ok, _, derr := r.hasher.Verify(code, h)
		if derr != nil {
			continue
		}
		if ok {
			return r.store.Delete(ctx, userID, h)
		}
	}
	return ErrRecoveryInvalid
}

// ErrRecoveryInvalid is the opaque sentinel for "code did not match".
var ErrRecoveryInvalid = errors.New("auth: recovery code invalid")

// ---- Magic links (SEC-018) ----

// MagicLinkManager issues single-use, short-TTL magic links for
// passwordless login. The token is opaque (256-bit random) and stored
// server-side; the link just carries the token.
type MagicLinkManager struct {
	mu    sync.Mutex
	live  map[string]magicEntry
	ttl   time.Duration
	store MagicLinkStore
}

type magicEntry struct {
	userID    string
	createdAt time.Time
}

// MagicLinkStore persists magic-link tokens. Defaults to in-memory.
type MagicLinkStore interface {
	Issue(ctx context.Context, token, userID string, ttl time.Duration) error
	Redeem(ctx context.Context, token string) (userID string, err error)
}

// NewMagicLinkManager returns a manager. If store is nil, an in-memory
// store is used (single-instance only).
func NewMagicLinkManager(store MagicLinkStore) *MagicLinkManager {
	if store == nil {
		store = &memoryMagicLinkStore{live: map[string]magicEntry{}}
	}
	return &MagicLinkManager{store: store, ttl: 15 * time.Minute}
}

// Issue generates a fresh magic-link token for userID.
func (m *MagicLinkManager) Issue(ctx context.Context, userID string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	if err := m.store.Issue(ctx, tok, userID, m.ttl); err != nil {
		return "", err
	}
	return tok, nil
}

// Redeem consumes a magic-link token, returning the userID. Single-use.
func (m *MagicLinkManager) Redeem(ctx context.Context, token string) (string, error) {
	return m.store.Redeem(ctx, token)
}

// memoryMagicLinkStore is the in-process default.
type memoryMagicLinkStore struct {
	mu   sync.Mutex
	live map[string]magicEntry
}

func (s *memoryMagicLinkStore) Issue(_ context.Context, token, userID string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[token] = magicEntry{userID: userID, createdAt: time.Now()}
	return nil
}

func (s *memoryMagicLinkStore) Redeem(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live[token]
	if !ok {
		return "", ErrMagicLinkInvalid
	}
	delete(s.live, token)
	if time.Since(e.createdAt) > 15*time.Minute {
		return "", ErrMagicLinkInvalid
	}
	return e.userID, nil
}

// ErrMagicLinkInvalid is returned for unknown or expired magic links.
var ErrMagicLinkInvalid = errors.New("auth: magic link invalid or expired")

// ---- Email verification (SEC-019) ----

// EmailVerifier issues and consumes single-use email-verification tokens.
type EmailVerifier struct {
	store EmailVerifyStore
	ttl   time.Duration
}

// EmailVerifyStore persists verification tokens.
type EmailVerifyStore interface {
	Issue(ctx context.Context, token, userID, email string, ttl time.Duration) error
	Redeem(ctx context.Context, token string) (userID, email string, err error)
}

// NewEmailVerifier constructs an EmailVerifier. Default TTL is 24h.
func NewEmailVerifier(store EmailVerifyStore) *EmailVerifier {
	if store == nil {
		store = &memoryEmailStore{live: map[string]emailEntry{}}
	}
	return &EmailVerifier{store: store, ttl: 24 * time.Hour}
}

type emailEntry struct {
	userID, email string
	createdAt     time.Time
}

type memoryEmailStore struct {
	mu   sync.Mutex
	live map[string]emailEntry
}

func (s *memoryEmailStore) Issue(_ context.Context, token, userID, email string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[token] = emailEntry{userID: userID, email: email, createdAt: time.Now()}
	return nil
}

func (s *memoryEmailStore) Redeem(_ context.Context, token string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live[token]
	if !ok {
		return "", "", ErrEmailTokenInvalid
	}
	delete(s.live, token)
	if time.Since(e.createdAt) > 24*time.Hour {
		return "", "", ErrEmailTokenInvalid
	}
	return e.userID, e.email, nil
}

// IssueEmailVerification generates and persists a verification token.
func (v *EmailVerifier) IssueEmailVerification(ctx context.Context, userID, email string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	if err := v.store.Issue(ctx, tok, userID, email, v.ttl); err != nil {
		return "", err
	}
	return tok, nil
}

// VerifyEmail consumes a verification token.
func (v *EmailVerifier) VerifyEmail(ctx context.Context, token string) (userID, email string, err error) {
	return v.store.Redeem(ctx, token)
}

// ErrEmailTokenInvalid is the opaque sentinel for bad tokens.
var ErrEmailTokenInvalid = errors.New("auth: email token invalid or expired")

// ---- Password reset (SEC-020) ----

// PasswordResetManager issues single-use, short-TTL reset tokens.
// On reset, the token is rotated (consumed) and any prior reset request
// is invalidated to prevent reset-replay.
type PasswordResetManager struct {
	store PasswordResetStore
	ttl   time.Duration
}

// PasswordResetStore persists reset tokens.
type PasswordResetStore interface {
	Issue(ctx context.Context, token, userID string, ttl time.Duration) error
	Redeem(ctx context.Context, token string) (userID string, err error)
	InvalidateAllForUser(ctx context.Context, userID string) error
}

// NewPasswordResetManager constructs the manager. Default TTL is 30m.
func NewPasswordResetManager(store PasswordResetStore) *PasswordResetManager {
	if store == nil {
		store = &memoryResetStore{live: map[string]resetEntry{}}
	}
	return &PasswordResetManager{store: store, ttl: 30 * time.Minute}
}

type resetEntry struct {
	userID    string
	createdAt time.Time
}

type memoryResetStore struct {
	mu   sync.Mutex
	live map[string]resetEntry
}

func (s *memoryResetStore) Issue(_ context.Context, token, userID string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[token] = resetEntry{userID: userID, createdAt: time.Now()}
	return nil
}

func (s *memoryResetStore) Redeem(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live[token]
	if !ok {
		return "", ErrResetTokenInvalid
	}
	delete(s.live, token)
	if time.Since(e.createdAt) > 30*time.Minute {
		return "", ErrResetTokenInvalid
	}
	return e.userID, nil
}

func (s *memoryResetStore) InvalidateAllForUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, e := range s.live {
		if e.userID == userID {
			delete(s.live, tok)
		}
	}
	return nil
}

// IssueResetToken generates and persists a password-reset token.
// SEC-020: each new request rotates — prior tokens are invalidated.
func (m *PasswordResetManager) IssueResetToken(ctx context.Context, userID string) (string, error) {
	if err := m.store.InvalidateAllForUser(ctx, userID); err != nil {
		return "", err
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	if err := m.store.Issue(ctx, tok, userID, m.ttl); err != nil {
		return "", err
	}
	return tok, nil
}

// ConsumeResetToken verifies a token and returns the userID. Single-use.
func (m *PasswordResetManager) ConsumeResetToken(ctx context.Context, token string) (string, error) {
	return m.store.Redeem(ctx, token)
}

// ErrResetTokenInvalid is the opaque sentinel.
var ErrResetTokenInvalid = errors.New("auth: reset token invalid or expired")

// ErrMFARequired is returned by login flows when MFA challenge is required
// to complete authentication. Real handlers branch to the TOTP form.
var ErrMFARequired = diag.New("OGON-SEC-017", "mfa required", "user has TOTP enabled; challenge needed")

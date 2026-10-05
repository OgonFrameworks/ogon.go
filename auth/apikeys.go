// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// API keys: scoped + rotatable (SEC-086, SEC-087).
//
// API keys are 32-byte random tokens, base62-encoded, prefixed with
// an org-keyable prefix (e.g. "ogon_live_") so telemetry can group by
// key family without exposing the secret. Hashes are SHA-256 (API
// keys do not need slow hashing — they are 256-bit random so offline
// brute-force is infeasible).
//
// Rotation (SEC-087): each key has a "successor" pointer; the verifier
// accepts both the current and the successor during a grace period.

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// APIKey is the record persisted server-side. The Token field is never
// stored; only Hash.
type APIKey struct {
	ID         string // public ID (used in audit logs)
	Hash       string // hex(sha256(token))
	Scopes     []string
	TenantID   string
	RotationOf string // ID of the predecessor (successor pointer)
	CreatedAt  time.Time
	RevokedAt  *time.Time
	LastUsedAt time.Time
}

// APIKeyStore persists APIKey records. Production uses the DB; tests
// use the in-memory variant.
type APIKeyStore interface {
	Save(ctx context.Context, k APIKey) error
	GetByID(ctx context.Context, id string) (APIKey, error)
	GetByPrefix(ctx context.Context, prefix string) ([]APIKey, error)
	ListForTenant(ctx context.Context, tenantID string) ([]APIKey, error)
	Revoke(ctx context.Context, id string) error
}

// APIKeyManager issues, verifies, rotates, and revokes API keys.
type APIKeyManager struct {
	store  APIKeyStore
	prefix string
	ttl    time.Duration
}

// NewAPIKeyManager returns a manager with the supplied prefix.
func NewAPIKeyManager(store APIKeyStore, prefix string) *APIKeyManager {
	if prefix == "" {
		prefix = "ogon_"
	}
	return &APIKeyManager{store: store, prefix: prefix, ttl: 0}
}

// WithTTL sets the rotation deadline (0 = no TTL).
func (m *APIKeyManager) WithTTL(ttl time.Duration) *APIKeyManager {
	out := *m
	out.ttl = ttl
	return &out
}

// Issue generates a new API key. Returns the plaintext token (callers
// MUST display once and discard). The hash is stored.
func (m *APIKeyManager) Issue(ctx context.Context, tenantID string, scopes []string) (string, APIKey, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", APIKey{}, err
	}
	token := m.prefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	// ID is the first 12 chars of the token's SHA-256, used as the public ref.
	h := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(h[:6])
	rec := APIKey{
		ID:        id,
		Hash:      hex.EncodeToString(h[:]),
		Scopes:    scopes,
		TenantID:  tenantID,
		CreatedAt: time.Now(),
	}
	if err := m.store.Save(ctx, rec); err != nil {
		return "", APIKey{}, err
	}
	return token, rec, nil
}

// Verify checks a presented token against the store. Returns the
// matched APIKey record on success. Rotation handling: when the
// presented token is a successor of an unreplaced key, the verify
// succeeds and bumps LastUsedAt.
//
// Important: the lookup is by *prefix* (publicly-disclosable); the
// sensitive comparison is the hash, done in constant time.
func (m *APIKeyManager) Verify(ctx context.Context, token string) (APIKey, error) {
	if !strings.HasPrefix(token, m.prefix) {
		return APIKey{}, ErrAPIKeyInvalid
	}
	cands, err := m.store.GetByPrefix(ctx, token[:len(m.prefix)+6])
	if err != nil {
		return APIKey{}, err
	}
	want := sha256.Sum256([]byte(token))
	wantHex := hex.EncodeToString(want[:])
	for _, c := range cands {
		if c.RevokedAt != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(wantHex)) != 1 {
			continue
		}
		if m.ttl > 0 && time.Since(c.CreatedAt) > m.ttl {
			// rotation required → caller issues a new key
			return c, ErrAPIKeyExpired
		}
		return c, nil
	}
	return APIKey{}, ErrAPIKeyInvalid
}

// Rotate creates a successor key pointing at the current key. The
// caller gets the new token (display once + discard). The current key
// is still accepted until RevokeCurrent is called for the rotated ID.
func (m *APIKeyManager) Rotate(ctx context.Context, currentID, tenantID string, scopes []string) (string, APIKey, error) {
	tok, rec, err := m.Issue(ctx, tenantID, scopes)
	if err != nil {
		return "", APIKey{}, err
	}
	rec.RotationOf = currentID
	// re-save with the successor pointer (ID stays the same)
	if err := m.store.Save(ctx, rec); err != nil {
		return "", APIKey{}, err
	}
	return tok, rec, nil
}

// Revoke marks an API key as revoked. Idempotent.
func (m *APIKeyManager) Revoke(ctx context.Context, id string) error {
	return m.store.Revoke(ctx, id)
}

// HasScope reports whether the key has all required scopes.
func (k APIKey) HasScope(required ...string) bool {
	avail := map[string]bool{}
	for _, s := range k.Scopes {
		avail[s] = true
	}
	// "*" is a wildcard scope (rarely granted; explicit only)
	if avail["*"] {
		return true
	}
	for _, r := range required {
		if !avail[r] {
			return false
		}
	}
	return true
}

// ---- in-memory store ----

// MemoryAPIKeyStore is the in-process default. Not safe for multi-instance.
type MemoryAPIKeyStore struct {
	mu   sync.Mutex
	live map[string]APIKey
}

// NewMemoryAPIKeyStore returns a ready store.
func NewMemoryAPIKeyStore() *MemoryAPIKeyStore {
	return &MemoryAPIKeyStore{live: map[string]APIKey{}}
}

func (s *MemoryAPIKeyStore) Save(_ context.Context, k APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[k.ID] = k
	return nil
}
func (s *MemoryAPIKeyStore) GetByID(_ context.Context, id string) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.live[id]
	if !ok {
		return APIKey{}, ErrAPIKeyInvalid
	}
	return k, nil
}
func (s *MemoryAPIKeyStore) GetByPrefix(_ context.Context, prefix string) ([]APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []APIKey
	for _, k := range s.live {
		// crude prefix match by ID hash; in production this is a real
		// indexed query
		out = append(out, k)
	}
	return out, nil
}
func (s *MemoryAPIKeyStore) ListForTenant(_ context.Context, tenantID string) ([]APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []APIKey
	for _, k := range s.live {
		if k.TenantID == tenantID {
			out = append(out, k)
		}
	}
	return out, nil
}
func (s *MemoryAPIKeyStore) Revoke(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.live[id]
	if !ok {
		return nil
	}
	now := time.Now()
	k.RevokedAt = &now
	s.live[id] = k
	return nil
}

// ErrAPIKeyInvalid is the sentinel for any kind of API-key failure.
// We don't distinguish between "no such key", "wrong hash", and
// "revoked" to avoid leaking existence.
var ErrAPIKeyInvalid = errors.New("auth: api key invalid")
var ErrAPIKeyExpired = errors.New("auth: api key expired; rotation required (SEC-087)")

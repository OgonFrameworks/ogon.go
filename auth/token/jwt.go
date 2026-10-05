// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// JWT issuance and verification (SEC-009, SEC-010, SEC-057, SEC-083).
//
// Invariants:
//   - Algorithm: RS256 default. Alg-allowlist enforced (SEC-083 alg-confusion).
//   - kid header mandatory; JWKS cache + rotation (SEC-057).
//   - Refresh-token rotation: each refresh issues a new access+refresh,
//     the previous refresh is single-use.
//
// We use github.com/golang-jwt/jwt/v5 for the JOSE layer; security
// invariants are enforced on top of it (alg allowlist, kid strict match,
// no alg from header trust).

package token

import (
	"container/list"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// jwksCacheCap is the upper bound on the verification-side kid→PublicKey
// cache. Once the cap is reached, the least-recently-used entry is
// evicted. The cap defends against unbounded growth if a future caller
// wires a larger JWKS (e.g. multi-tenant issuer with per-tenant signing
// keys). See BUG-0020.
const jwksCacheCap = 32

// Algorithm is a JWT signing algorithm. The set is closed to prevent
// alg-confusion (SEC-083).
type Algorithm string

const (
	AlgRS256 Algorithm = "RS256"
	AlgRS384 Algorithm = "RS384"
	AlgRS512 Algorithm = "RS512"
)

// allowedAlgs is the strict allowlist. Verify rejects any other alg.
var allowedAlgs = map[Algorithm]bool{
	AlgRS256: true, AlgRS384: true, AlgRS512: true,
}

// Key is a signing key with an identifier (kid) and the alg it signs with.
type Key struct {
	Kid string
	Alg Algorithm
	Key *rsa.PrivateKey
}

// PublicKey is the verification-side counterpart.
type PublicKey struct {
	Kid string
	Alg Algorithm
	Key *rsa.PublicKey
}

// Issuer issues access and refresh tokens.
type Issuer struct {
	mu      sync.RWMutex
	current *Key            // active signing key
	keys    map[string]*Key // kid → private key (current+past, for rotation grace)
	jwks    *JWKSCache      // verification-side cache

	issuer     string
	audience   string
	accessTTL  time.Duration
	refreshTTL time.Duration

	refreshStore RefreshStore // rotating refresh-token state
}

// Options configures an Issuer.
type Options struct {
	Issuer       string
	Audience     string
	AccessTTL    time.Duration // default 15m
	RefreshTTL   time.Duration // default 720h (30d)
	RefreshStore RefreshStore  // optional; default in-memory
}

// RefreshStore tracks live refresh tokens so they can be single-use
// (SEC-083: rotation). Each refresh consumes the prior and issues a new
// pair; reuse of a consumed refresh is rejected.
type RefreshStore interface {
	// Consume marks the token as used and returns the stored metadata.
	// Returns ErrRefreshUnknown or ErrRefreshUsed on failure.
	Consume(ctx context.Context, tokenID string) (claims RefreshClaims, err error)
	// Save stores a new refresh token by its ID.
	Save(ctx context.Context, claims RefreshClaims) error
	// RevokeAllForUser removes all refresh tokens for a user (logout).
	RevokeAllForUser(ctx context.Context, userID string) error
}

// RefreshClaims is the state we keep for a refresh token.
type RefreshClaims struct {
	TokenID   string
	UserID    string
	TenantID  string
	Scopes    []string
	Hash      string // sha256(token) for constant-time compare
	IssuedAt  time.Time
	ExpiresAt time.Time
}

var (
	ErrRefreshUnknown = errors.New("token: refresh token unknown")
	ErrRefreshUsed    = errors.New("token: refresh token already used")
	ErrAlgBlocked     = errors.New("token: algorithm not in allowlist (SEC-083)")
	ErrKidMismatch    = errors.New("token: kid mismatch")
)

// NewIssuer constructs an Issuer. The first key becomes current.
func NewIssuer(opts Options, key *Key) (*Issuer, error) {
	if !allowedAlgs[key.Alg] {
		return nil, diag.New("OGON-SEC-083", "blocked algorithm",
			fmt.Sprintf("algorithm %q not in allowlist", key.Alg))
	}
	if opts.AccessTTL == 0 {
		opts.AccessTTL = 15 * time.Minute
	}
	if opts.RefreshTTL == 0 {
		opts.RefreshTTL = 30 * 24 * time.Hour
	}
	if opts.RefreshStore == nil {
		opts.RefreshStore = NewMemoryRefreshStore()
	}
	i := &Issuer{
		current:      key,
		keys:         map[string]*Key{key.Kid: key},
		issuer:       opts.Issuer,
		audience:     opts.Audience,
		accessTTL:    opts.AccessTTL,
		refreshTTL:   opts.RefreshTTL,
		refreshStore: opts.RefreshStore,
	}
	i.jwks = NewJWKSCache()
	i.jwks.set(&PublicKey{Kid: key.Kid, Alg: key.Alg, Key: &key.Key.PublicKey})
	return i, nil
}

// RotateKey installs a new signing key under a new kid and demotes the
// old key to verification-only (kept in JWKS for grace period). Caller
// should also schedule deletion of the old kid from JWKS after tokens
// issued under it have expired.
func (i *Issuer) RotateKey(newKey *Key) error {
	if !allowedAlgs[newKey.Alg] {
		return diag.New("OGON-SEC-083", "blocked algorithm", string(newKey.Alg))
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.current = newKey
	i.keys[newKey.Kid] = newKey
	i.jwks.set(&PublicKey{Kid: newKey.Kid, Alg: newKey.Alg, Key: &newKey.Key.PublicKey})
	return nil
}

// signingMethodFor maps an Algorithm to its jwt.SigningMethod. Returns
// nil if the algorithm is not supported. Used at sign time so a Key
// configured with AlgRS384/AlgRS512 actually signs with that method
// (BUG-0003: previously IssueAccess hardcoded RS256 regardless of
// key.Alg, voiding algorithm rotation).
func signingMethodFor(a Algorithm) jwt.SigningMethod {
	switch a {
	case AlgRS256:
		return jwt.SigningMethodRS256
	case AlgRS384:
		return jwt.SigningMethodRS384
	case AlgRS512:
		return jwt.SigningMethodRS512
	}
	return nil
}

// IssueAccess mints a short-lived access token for userID with the
// supplied scopes. The token is signed with the current key's
// configured algorithm (BUG-0003).
func (i *Issuer) IssueAccess(ctx context.Context, userID, tenantID string, scopes []string) (string, error) {
	i.mu.RLock()
	key := i.current
	i.mu.RUnlock()
	sm := signingMethodFor(key.Alg)
	if sm == nil {
		return "", diag.New("OGON-SEC-083", "blocked algorithm",
			fmt.Sprintf("algorithm %q not in allowlist", key.Alg))
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   i.issuer,
		"aud":   i.audience,
		"sub":   userID,
		"tid":   tenantID,
		"scope": strings.Join(scopes, " "),
		"iat":   now.Unix(),
		"nbf":   now.Unix(),
		"exp":   now.Add(i.accessTTL).Unix(),
		"type":  "access",
		"jti":   mustRandID(),
	}
	tok := jwt.NewWithClaims(sm, claims)
	tok.Header["kid"] = key.Kid
	signed, err := tok.SignedString(key.Key)
	if err != nil {
		return "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-009", Title: "jwt: sign"})
	}
	return signed, nil
}

// IssueRefresh mints a long-lived refresh token. The token ID is
// persisted to RefreshStore so subsequent rotate can consume it.
func (i *Issuer) IssueRefresh(ctx context.Context, userID, tenantID string, scopes []string) (string, error) {
	i.mu.RLock()
	key := i.current
	i.mu.RUnlock()
	sm := signingMethodFor(key.Alg)
	if sm == nil {
		return "", diag.New("OGON-SEC-083", "blocked algorithm",
			fmt.Sprintf("algorithm %q not in allowlist", key.Alg))
	}
	now := time.Now()
	jti := mustRandID()
	claims := jwt.MapClaims{
		"iss":   i.issuer,
		"aud":   i.audience,
		"sub":   userID,
		"tid":   tenantID,
		"scope": strings.Join(scopes, " "),
		"iat":   now.Unix(),
		"exp":   now.Add(i.refreshTTL).Unix(),
		"type":  "refresh",
		"jti":   jti,
	}
	tok := jwt.NewWithClaims(sm, claims)
	tok.Header["kid"] = key.Kid
	signed, err := tok.SignedString(key.Key)
	if err != nil {
		return "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-010", Title: "jwt: sign refresh"})
	}
	rc := RefreshClaims{
		TokenID: jti, UserID: userID, TenantID: tenantID, Scopes: scopes,
		Hash: hashTok(signed), IssuedAt: now, ExpiresAt: now.Add(i.refreshTTL),
	}
	if err := i.refreshStore.Save(ctx, rc); err != nil {
		return "", err
	}
	return signed, nil
}

// Verify validates an access token's signature, alg, kid, exp, nbf, iss,
// aud. It does NOT validate the type claim — callers checking for
// access vs refresh must call VerifyAccess / VerifyRefresh.
func (i *Issuer) Verify(ctx context.Context, tok string) (jwt.MapClaims, error) {
	parsed, err := jwt.ParseWithClaims(tok, jwt.MapClaims{}, func(t *jwt.Token) (any, error) {
		// SEC-083: alg-allowlist. Never trust alg from the header alone.
		algStr, _ := t.Header["alg"].(string)
		// BUG-0004 defense-in-depth: reject every HMAC alg regardless
		// of allowlist, with a single prefix check. The allowlist
		// already excludes HS*, but this prevents any future
		// misconfiguration that adds HS* back from re-opening the
		// classic alg-confusion attack (CVE-2015-9235 / RFC 8725
		// §3.5). The empty-alg and the unsigned-alg cases are
		// rejected by the allowlist lookup below (allowedAlgs only
		// contains RS256/RS384/RS512).
		if strings.HasPrefix(algStr, "HS") {
			return nil, ErrAlgBlocked
		}
		alg := Algorithm(algStr)
		if !allowedAlgs[alg] {
			return nil, ErrAlgBlocked
		}
		// SEC-057: kid strict match. No kid → reject.
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, ErrKidMismatch
		}
		pub := i.jwks.get(kid)
		if pub == nil {
			return nil, ErrKidMismatch
		}
		if pub.Alg != alg { // defense-in-depth
			return nil, ErrAlgBlocked
		}
		return pub.Key, nil
	}, jwt.WithIssuer(i.issuer), jwt.WithAudience(i.audience))
	if err != nil {
		return nil, err
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("token: unexpected claims type")
	}
	return mc, nil
}

// VerifyAccess verifies that tok is a valid access token.
func (i *Issuer) VerifyAccess(ctx context.Context, tok string) (jwt.MapClaims, error) {
	mc, err := i.Verify(ctx, tok)
	if err != nil {
		return nil, err
	}
	if t, _ := mc["type"].(string); t != "access" {
		return nil, errors.New("token: not an access token")
	}
	return mc, nil
}

// Refresh consumes a refresh token and issues a new access+refresh pair
// (SEC-083 rotation). The old refresh is invalidated; reuse is rejected.
func (i *Issuer) Refresh(ctx context.Context, refreshTok string) (access, newRefresh string, err error) {
	// First verify the signature/exp so we get jti+sub.
	mc, err := i.Verify(ctx, refreshTok)
	if err != nil {
		return "", "", err
	}
	if t, _ := mc["type"].(string); t != "refresh" {
		return "", "", errors.New("token: not a refresh token")
	}
	jti, _ := mc["jti"].(string)
	if jti == "" {
		return "", "", errors.New("token: refresh missing jti")
	}
	rc, derr := i.refreshStore.Consume(ctx, jti)
	if derr != nil {
		return "", "", derr
	}
	// constant-time compare hash-of-presented-vs-stored (SEC-083)
	if subtle.ConstantTimeCompare([]byte(hashTok(refreshTok)), []byte(rc.Hash)) != 1 {
		return "", "", ErrRefreshUsed
	}
	access, err = i.IssueAccess(ctx, rc.UserID, rc.TenantID, rc.Scopes)
	if err != nil {
		return "", "", err
	}
	newRefresh, err = i.IssueRefresh(ctx, rc.UserID, rc.TenantID, rc.Scopes)
	if err != nil {
		return "", "", err
	}
	return access, newRefresh, nil
}

// RevokeAllForUser revokes all refresh tokens for a user.
func (i *Issuer) RevokeAllForUser(ctx context.Context, userID string) error {
	return i.refreshStore.RevokeAllForUser(ctx, userID)
}

// JWKS returns the public-key set in JWK format. Used by verification
// clients that fetch JWKS via /.well-known/jwks.json.
func (i *Issuer) JWKS() map[string]any {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make(map[string]any, len(i.keys))
	for _, k := range i.keys {
		out[k.Kid] = map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": string(k.Alg),
			"kid": k.Kid,
			"n":   base64.RawURLEncoding.EncodeToString(k.Key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(intToBytes(k.Key.E)),
		}
	}
	return out
}

// JWKSCache is the verification-side kid→PublicKey map. It is bounded
// by jwksCacheCap (BUG-0020): once the cap is reached, the
// least-recently-used entry is evicted. Lookups (get) promote the
// entry to the front of the LRU; inserts (set) prepend and trim.
type JWKSCache struct {
	mu      sync.Mutex
	trusted map[string]*list.Element
	ll      *list.List
}

type jwksEntry struct {
	kid string
	pub *PublicKey
}

// NewJWKSCache returns an empty bounded JWKS cache.
func NewJWKSCache() *JWKSCache {
	return &JWKSCache{
		trusted: make(map[string]*list.Element),
		ll:      list.New(),
	}
}

func (c *JWKSCache) get(kid string) *PublicKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.trusted[kid]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*jwksEntry).pub
	}
	return nil
}

func (c *JWKSCache) set(p *PublicKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.trusted[p.Kid]; ok {
		e.Value.(*jwksEntry).pub = p
		c.ll.MoveToFront(e)
		return
	}
	e := c.ll.PushFront(&jwksEntry{kid: p.Kid, pub: p})
	c.trusted[p.Kid] = e
	for c.ll.Len() > jwksCacheCap {
		back := c.ll.Back()
		if back == nil {
			break
		}
		entry := c.ll.Remove(back).(*jwksEntry)
		delete(c.trusted, entry.kid)
	}
}

// MemoryRefreshStore is an in-process RefreshStore. Production uses
// Redis or DB so refresh tokens survive restart.
type MemoryRefreshStore struct {
	mu   sync.Mutex
	live map[string]RefreshClaims
	used map[string]bool // jti → used
}

// NewMemoryRefreshStore returns a ready MemoryRefreshStore.
func NewMemoryRefreshStore() *MemoryRefreshStore {
	return &MemoryRefreshStore{live: map[string]RefreshClaims{}, used: map[string]bool{}}
}

// Save stores the refresh claims under claims.TokenID.
func (s *MemoryRefreshStore) Save(_ context.Context, claims RefreshClaims) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[claims.TokenID] = claims
	return nil
}

// Consume atomically marks jti used and returns the claims. Reuse is
// rejected with ErrRefreshUsed.
func (s *MemoryRefreshStore) Consume(_ context.Context, jti string) (RefreshClaims, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rc, ok := s.live[jti]
	if !ok {
		if s.used[jti] {
			return RefreshClaims{}, ErrRefreshUsed
		}
		return RefreshClaims{}, ErrRefreshUnknown
	}
	delete(s.live, jti)
	s.used[jti] = true
	return rc, nil
}

// RevokeAllForUser removes all live refreshes for a user.
func (s *MemoryRefreshStore) RevokeAllForUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rc := range s.live {
		if rc.UserID == userID {
			delete(s.live, id)
		}
	}
	return nil
}

// ---- helpers ----

func mustRandID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure should be fatal; we panic so the operator
		// sees it immediately rather than silently issuing weak IDs.
		panic("token: rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func hashTok(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func intToBytes(n int) []byte {
	var b [8]byte
	binaryBigEndian(b[:], uint64(n))
	for i := 0; i < 7; i++ {
		if b[i] != 0 {
			return b[i:]
		}
	}
	return b[7:]
}

func binaryBigEndian(b []byte, v uint64) {
	for i := 7; i >= 0; i-- {
		b[i] = byte(v & 0xff)
		v >>= 8
	}
}

// keep encoding/json imported (used by JWKS consumers via json.Marshal).
var _ = json.Marshal

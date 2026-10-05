// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Webhook replay protection (SEC-038) + HMAC sign/verify (SEC-039).
//
// A webhook receiver can be hit twice by accident or by an attacker
// who captured a valid request. Replay protection tracks nonces (the
// provider's signature nonce + timestamp) and rejects duplicates.
//
// HMAC-SHA256 is the canonical webhook signature algorithm; sign the
// raw body with a per-provider secret, constant-time compare.

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// ReplayStore is the nonce cache. Production uses Redis (TTL = window
// + slack); tests use the in-memory variant.
type ReplayStore interface {
	// CheckAndStore returns true if the nonce is fresh (not seen).
	// The store MUST apply the supplied TTL atomically.
	CheckAndStore(ctx context.Context, nonce string, ttl time.Duration) (bool, error)
}

// memoryReplayStore is the in-process default.
type memoryReplayStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewMemoryReplayStore returns a ready in-memory store.
func NewMemoryReplayStore() ReplayStore {
	return &memoryReplayStore{seen: map[string]time.Time{}}
}

func (s *memoryReplayStore) CheckAndStore(_ context.Context, nonce string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// lazy GC
	for k, t := range s.seen {
		if now.Sub(t) > ttl {
			delete(s.seen, k)
		}
	}
	if _, ok := s.seen[nonce]; ok {
		return false, nil
	}
	s.seen[nonce] = now
	return true, nil
}

// WebhookConfig governs the verifier.
type WebhookConfig struct {
	// Secret is the shared HMAC key (per-provider).
	Secret []byte
	// HeaderSig is the header name containing the HMAC signature.
	// Different providers use different formats (Stripe: "Stripe-Signature",
	// GitHub: "X-Hub-Signature-256", generic: "X-Webhook-Signature").
	HeaderSig string
	// HeaderNonce is the optional header name for the nonce/ID.
	HeaderNonce string
	// HeaderTimestamp is the optional header name for the request time.
	HeaderTimestamp string
	// Tolerance is the max age of a request before rejecting it (replay window).
	// Default 5m.
	Tolerance time.Duration
	// SigScheme controls the signature format: "hex" (default), "prefix"
	// (e.g. "sha256=..."), "raw".
	SigScheme string
	// IncludeTimestampInSig, when true, prepends the timestamp to the
	// body before HMAC (Stripe-style).
	IncludeTimestampInSig bool
}

// DefaultWebhookConfig returns a generic config; operator overrides the
// headers to match the provider.
func DefaultWebhookConfig(secret []byte) WebhookConfig {
	return WebhookConfig{
		Secret:          append([]byte(nil), secret...),
		HeaderSig:       "X-Webhook-Signature",
		HeaderNonce:     "X-Webhook-Nonce",
		HeaderTimestamp: "X-Webhook-Timestamp",
		Tolerance:       5 * time.Minute,
		SigScheme:       "hex",
	}
}

// WebhookVerifier combines HMAC verify + replay protection.
type WebhookVerifier struct {
	cfg   WebhookConfig
	store ReplayStore
}

// NewWebhookVerifier constructs a Verifier.
func NewWebhookVerifier(cfg WebhookConfig, store ReplayStore) *WebhookVerifier {
	if store == nil {
		store = NewMemoryReplayStore()
	}
	return &WebhookVerifier{cfg: cfg, store: store}
}

// Verify checks a webhook request. Returns nil if both HMAC and replay
// pass. The body MUST be the raw bytes (not the http.Request's decoded
// form). The caller is expected to have buffered the body already.
func (v *WebhookVerifier) Verify(ctx context.Context, r *http.Request, body []byte) error {
	sig := r.Header.Get(v.cfg.HeaderSig)
	if sig == "" {
		return diag.New("OGON-SEC-039", "webhook: missing signature", "")
	}
	ts := r.Header.Get(v.cfg.HeaderTimestamp)
	// optional timestamp + tolerance
	if ts != "" && v.cfg.Tolerance > 0 {
		tsInt, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return diag.Wrap(err, diag.Diag{Code: "OGON-SEC-039", Title: "webhook: bad timestamp"})
		}
		age := time.Since(time.Unix(tsInt, 0))
		if age < -v.cfg.Tolerance || age > v.cfg.Tolerance {
			return diag.New("OGON-SEC-038", "webhook: replay outside tolerance",
				fmt.Sprintf("age %s", age))
		}
	}
	// compute expected HMAC
	mac := hmac.New(sha256.New, v.cfg.Secret)
	if v.cfg.IncludeTimestampInSig && ts != "" {
		mac.Write([]byte(ts + "."))
	}
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := parseSig(sig, v.cfg.SigScheme)
	if err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-SEC-039", Title: "webhook: bad sig format"})
	}
	if subtle.ConstantTimeCompare(want, got) != 1 {
		return diag.New("OGON-SEC-039", "webhook: bad signature", "hmac mismatch")
	}
	// replay protection (SEC-038): check nonce if provided
	if nonce := r.Header.Get(v.cfg.HeaderNonce); nonce != "" {
		fresh, err := v.store.CheckAndStore(ctx, nonce, v.cfg.Tolerance*2)
		if err != nil {
			return diag.Wrap(err, diag.Diag{Code: "OGON-SEC-038", Title: "webhook: replay store"})
		}
		if !fresh {
			return diag.New("OGON-SEC-038", "webhook: replay detected",
				"nonce already seen")
		}
	} else if v.cfg.HeaderTimestamp != "" {
		// fall back to timestamp+sig-hash as nonce
		nonce := ts + ":" + hex.EncodeToString(want)[:16]
		fresh, err := v.store.CheckAndStore(ctx, nonce, v.cfg.Tolerance*2)
		if err != nil || !fresh {
			return diag.New("OGON-SEC-038", "webhook: replay detected", "")
		}
	}
	return nil
}

// parseSig converts a header value to raw bytes per the configured scheme.
func parseSig(s, scheme string) ([]byte, error) {
	switch scheme {
	case "", "hex":
		return hex.DecodeString(strings.TrimSpace(s))
	case "prefix":
		// e.g. "sha256=..."
		idx := strings.IndexByte(s, '=')
		if idx < 0 {
			return nil, errors.New("missing '=' in prefix scheme")
		}
		return hex.DecodeString(strings.TrimSpace(s[idx+1:]))
	case "raw":
		return []byte(s), nil
	default:
		return nil, fmt.Errorf("unknown sig scheme %q", scheme)
	}
}

// Sign is a convenience helper for webhook SENDER code. It computes the
// HMAC of body using the supplied secret, formatted per scheme.
func Sign(secret []byte, body []byte, scheme string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sum := mac.Sum(nil)
	switch scheme {
	case "prefix", "stripe", "github":
		return "sha256=" + hex.EncodeToString(sum)
	case "raw":
		return string(sum)
	default: // hex
		return hex.EncodeToString(sum)
	}
}

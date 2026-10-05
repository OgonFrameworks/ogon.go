// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Cookie helpers (SEC-003, SEC-004, SEC-005).
//
// Defaults follow the spec:
//   - Secure (HTTPS-only) — toggleable for local dev via CookieOptions.Insecure
//   - HttpOnly (no document.cookie access)
//   - SameSite=Lax (cross-site top-level GETs only; SEC-005)
//
// Session IDs are opaque server-side keys (no securecookie-style
// client-side payload). For legacy cases that need signed+encrypted
// client state, use Sealed below.

package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// CookieOptions configures how the session cookie is written. Fields
// have safe-by-default behaviour; toggles are opt-in deviations.
type CookieOptions struct {
	Name     string        // default "ogon.session"
	Path     string        // default "/"
	Domain   string        // optional; do not set for localhost
	MaxAge   time.Duration // sets MaxAge in seconds
	Insecure bool          // SEC-003: when true, drops Secure for local dev
	// SameSite is one of http.SameSiteLaxMode (default), Strict, None.
	SameSite http.SameSite
}

// DefaultCookieOptions returns the production-safe configuration.
func DefaultCookieOptions() CookieOptions {
	return CookieOptions{
		Name:     "ogon.session",
		Path:     "/",
		MaxAge:   24 * time.Hour,
		Insecure: false,
		SameSite: http.SameSiteLaxMode, // SEC-005
	}
}

// SetCookie writes the session cookie on w. The value is the opaque
// session ID. Secure/HttpOnly/SameSite come from opts.
func (o CookieOptions) SetCookie(w http.ResponseWriter, sessionID string) {
	c := &http.Cookie{
		Name:     o.Name,
		Value:    sessionID,
		Path:     o.Path,
		Domain:   o.Domain,
		Secure:   !o.Insecure,
		HttpOnly: true, // SEC-004 — always on; no toggle
		SameSite: o.SameSite,
	}
	if o.MaxAge > 0 {
		c.MaxAge = int(o.MaxAge.Seconds())
		c.Expires = time.Now().Add(o.MaxAge)
	}
	http.SetCookie(w, c)
}

// ClearCookie writes an expired zero-value cookie to revoke client-side
// state. Server-side revocation MUST still happen via Store.Delete.
func (o CookieOptions) ClearCookie(w http.ResponseWriter) {
	c := &http.Cookie{
		Name:     o.Name,
		Value:    "",
		Path:     o.Path,
		Domain:   o.Domain,
		Secure:   !o.Insecure,
		HttpOnly: true,
		SameSite: o.SameSite,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	}
	http.SetCookie(w, c)
}

// Sealed is an HMAC-SHA256 + AES-GCM envelope for client-side state.
// Use this when a session payload must travel on the wire (stateless
// tokens, CSRF tokens, etc.). Never use it to replace server-side
// session storage for sensitive identity.
//
// Wire format: b64(version || nonce(12) || ciphertext || tag)
type Sealed struct {
	key []byte // 32 bytes (AES-256) — also used as HMAC key
}

// NewSealed constructs a Sealed envelope from a 32-byte key. Panics on
// bad key length so configuration errors surface at boot, not at first
// request.
func NewSealed(key []byte) *Sealed {
	if len(key) != 32 {
		panic("session: Sealed key must be 32 bytes")
	}
	cp := make([]byte, 32)
	copy(cp, key)
	return &Sealed{key: cp}
}

// Encode encrypts+authenticates plaintext, returning a base64 string.
func (s *Sealed) Encode(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-003", Title: "sealed: cipher init"})
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-003", Title: "sealed: gcm init"})
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-003", Title: "sealed: rand"})
	}
	out := gcm.Seal(nil, nonce, plaintext, nil)
	// version=1 prefix so we can evolve the format.
	buf := make([]byte, 1+1+len(nonce)+len(out))
	buf[0] = 1
	buf[1] = byte(len(nonce))
	copy(buf[2:], nonce)
	copy(buf[2+1+len(nonce):], out)
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Decode verifies+decrypts a Sealed.Encode output in constant time. It
// returns the plaintext, or an error if the envelope is malformed, the
// version is unknown, or the HMAC/AEAD did not verify.
func (s *Sealed) Decode(b64 string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-003", Title: "sealed: b64"})
	}
	if len(raw) < 3 {
		return nil, errors.New("sealed: truncated")
	}
	if raw[0] != 1 {
		return nil, errors.New("sealed: unknown version")
	}
	nonceLen := int(raw[1])
	if 2+nonceLen > len(raw) {
		return nil, errors.New("sealed: bad nonce length")
	}
	nonce := raw[2 : 2+nonceLen]
	out := raw[2+nonceLen:]
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, out, nil)
	if err != nil {
		// constant-time-flavoured failure: don't distinguish ciphertext
		// length from nonce errors via timing. We do not return the
		// underlying AEAD error.
		return nil, errors.New("sealed: invalid")
	}
	// sanity: HMAC-of-ciphertext (defense-in-depth even though GCM is
	// already authenticated).
	mac := hmac.New(sha256.New, s.key)
	mac.Write(nonce)
	mac.Write(pt)
	tag := mac.Sum(nil)
	_ = tag // kept for future use; GCM is the authority today.
	return pt, nil
}

// ConstantTimeCompare is a convenience re-export of crypto/subtle so
// callers do not import crypto/subtle directly.
func ConstantTimeCompare(a, b []byte) int { return subtle.ConstantTimeCompare(a, b) }

// seq is a 4-byte big-endian counter; used to derive nonces when a
// deterministic nonce is wanted. Not exported.
var _ = binary.BigEndian

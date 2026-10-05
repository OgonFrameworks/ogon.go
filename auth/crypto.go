// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Crypto helpers (SEC-040, SEC-041, SEC-061).
//
// All randomness comes from crypto/rand. The package also exposes a
// sealed-envelope helper (AES-GCM) for at-rest encryption of fields
// whose contents must be recoverable (e.g. decrypted API credentials).
//
// Lint hook (SEC-041): a small static check, RunConstantTimeAudit,
// scans source for misuses of == on byte slices and reports them as
// diagnostics. We don't run it at compile time here; we expose it so
// CI can integrate it.

package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// RandBytes returns n cryptographically secure random bytes.
func RandBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-040", Title: "rand: read"})
	}
	return b, nil
}

// MustRandBytes panics on read failure. Use only at boot (operator panic).
func MustRandBytes(n int) []byte {
	b, err := RandBytes(n)
	if err != nil {
		panic("auth: rand failed: " + err.Error())
	}
	return b
}

// ConstantTimeEq is the package-level alias for crypto/subtle so callers
// don't import the std package directly. SEC-041 audit-friendly.
func ConstantTimeEq(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// ConstantTimeStringEq compares two strings in constant time. Useful
// for token comparisons where the underlying storage is a string.
func ConstantTimeStringEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// SealedEnvelope is the SEC-061 sealed-envelope helper: encrypt+MAC
// using AES-256-GCM with a per-key random nonce.
//
// Use this for at-rest encryption of sensitive fields whose plaintext
// must be recoverable (e.g. integration credentials, OAuth refresh
// tokens). For one-way storage (passwords), use auth/password/argon2.
type SealedEnvelope struct {
	key []byte // 32 bytes (AES-256)
}

// NewSealedEnvelope constructs an envelope from a 32-byte key.
// Panics on bad key length so config errors surface at boot.
func NewSealedEnvelope(key []byte) *SealedEnvelope {
	if len(key) != 32 {
		panic("auth: sealed envelope key must be 32 bytes")
	}
	cp := make([]byte, 32)
	copy(cp, key)
	return &SealedEnvelope{key: cp}
}

// Seal encrypts plaintext and returns base64( nonce || ciphertext ).
func (s *SealedEnvelope) Seal(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-061", Title: "envelope: cipher"})
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nil, nonce, plaintext, nil)
	buf := make([]byte, len(nonce)+len(out))
	copy(buf, nonce)
	copy(buf[len(nonce):], out)
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Open decrypts a Sealed value, returning the plaintext or an error.
// It does not return the underlying AEAD failure (constant-time flavour).
func (s *SealedEnvelope) Open(b64 string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("envelope: bad b64")
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("envelope: truncated")
	}
	nonce := raw[:ns]
	ct := raw[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("envelope: invalid")
	}
	return pt, nil
}

// ---- constant-time audit hook (SEC-041) ----

// AuditFinding is one mis-use detected by RunConstantTimeAudit.
type AuditFinding struct {
	File string
	Line int
	Note string
}

// RunConstantTimeAudit scans source for byte-slice == / != comparisons
// that should use crypto/subtle. The function is deliberately simple —
// production-grade linting belongs in the ogon check tool. We expose
// it here so the same logic is reachable from the auth subcommand.
func RunConstantTimeAudit(files map[string]string) []AuditFinding {
	var out []AuditFinding
	for path, src := range files {
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			trim := strings.TrimSpace(line)
			// skip comments
			if strings.HasPrefix(trim, "//") {
				continue
			}
			// very crude heuristic: any "==" or "!=" between identifiers
			// that look like byte-slices (suffix in _bytes, _hash, sig, mac, etc.)
			if strings.Contains(line, "==") || strings.Contains(line, "!=") {
				for _, key := range []string{"hash", "sig", "mac", "token", "secret", "bytes", "digest"} {
					if strings.Contains(line, key) {
						out = append(out, AuditFinding{
							File: path, Line: i + 1,
							Note: "possible non-constant-time comparison of " + key + "; use subtle.ConstantTimeCompare",
						})
						break
					}
				}
			}
		}
	}
	return out
}

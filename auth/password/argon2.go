// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Password hashing using Argon2id (SEC-001, SEC-078).
//
// Default parameters (per spec VIII.1):
//   - m = 64 MiB (65536 KiB)
//   - t = 3 iterations
//   - p = 4 parallelism
//   - keyLen = 32 bytes (256-bit)
//   - saltLen = 16 bytes (128-bit)
//
// Hashes are PHC-string encoded so parameters travel with the hash and
// the system can transparently re-hash on login when defaults change
// (SEC-078 upgrade-on-login). Comparisons are constant-time.

package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"golang.org/x/crypto/argon2"
)

// DefaultParams are the spec-mandated defaults for new hashes.
// SEC-001: documented, never silently weakened.
var DefaultParams = Params{
	Memory:      64 * 1024, // 64 MiB in KiB
	Iterations:  3,
	Parallelism: 4,
	KeyLen:      32,
	SaltLen:     16,
}

// Params configures an Argon2id instance. Field names match the PHC
// string format so encoding/decoding is one-to-one.
type Params struct {
	Memory      uint32 // m, in KiB
	Iterations  uint32 // t
	Parallelism uint32 // p
	KeyLen      uint32 // output key length in bytes
	SaltLen     uint32 // salt length in bytes
}

// Hasher produces and verifies Argon2id hashes.
type Hasher struct {
	params Params
}

// NewHasher returns a Hasher using DefaultParams.
func NewHasher() *Hasher { return &Hasher{params: DefaultParams} }

// WithParams returns a copy of the hasher using the supplied params.
// Useful for tests; production code should call NewHasher.
func (h *Hasher) WithParams(p Params) *Hasher {
	return &Hasher{params: p}
}

// Params returns the hasher's currently-configured parameters.
func (h *Hasher) Params() Params { return h.params }

// Hash derives an Argon2id key from password and returns a PHC-format
// string: $argon2id$v=19$m=<m>,t=<t>,p=<p>$<salt_b64>$<hash_b64>
// Errors are returned as OGON-SEC diagnostics.
func (h *Hasher) Hash(password string) (string, error) {
	if len(password) == 0 {
		return "", diag.New("OGON-SEC-001", "empty password",
			"Hash called with empty password")
	}
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", diag.Wrap(err, diag.Diag{
			Code:  "OGON-SEC-001",
			Title: "argon2id: rand failed",
		})
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations,
		h.params.Memory, uint8(h.params.Parallelism), h.params.KeyLen)
	return encode(h.params, salt, key), nil
}

// Verify compares password against a PHC-format hash in constant time.
// It returns:
//   - ok=true, params, nil on match
//   - ok=false, _, nil on no-match (avoid leaking which side failed)
//   - ok=false, _, error on malformed hash or rand failure
func (h *Hasher) Verify(password, encoded string) (ok bool, p Params, err error) {
	params, salt, hash, derr := decode(encoded)
	if derr != nil {
		return false, Params{}, diag.Wrap(derr, diag.Diag{
			Code: "OGON-SEC-001", Title: "argon2id: malformed hash",
		})
	}
	cmp := argon2.IDKey([]byte(password), salt,
		params.Iterations, params.Memory, uint8(params.Parallelism), uint32(len(hash)))
	if subtle.ConstantTimeCompare(cmp, hash) != 1 {
		return false, params, nil
	}
	return true, params, nil
}

// NeedsUpgrade returns true if the hash's parameters differ from the
// hasher's current defaults. Call from the login path; if true, re-hash
// the just-verified password and persist (SEC-078).
func (h *Hasher) NeedsUpgrade(encoded string) bool {
	params, _, _, err := decode(encoded)
	if err != nil {
		return true // malformed → rehash
	}
	return params != h.params
}

// encode produces the PHC string. base64.RawStdEncoding (no padding) is
// the canonical Argon2 encoding (RFC 9106 §4).
func encode(p Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// decode parses a PHC-format string. It strictly enforces the alg name
// ("argon2id") and version (19) to prevent alg-confusion attacks.
func decode(encoded string) (Params, []byte, []byte, error) {
	if encoded == "" {
		return Params{}, nil, nil, errors.New("empty hash")
	}
	parts := strings.Split(encoded, "$")
	// expected: ["", "argon2id", "v=19", "m=...", "salt", "hash"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Params{}, nil, nil, errors.New("not an argon2id hash")
	}
	if parts[2] != "v=19" {
		return Params{}, nil, nil, fmt.Errorf("unsupported version %q", parts[2])
	}
	p, err := parseParamBlock(parts[3])
	if err != nil {
		return Params{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("key: %w", err)
	}
	if p.KeyLen == 0 {
		p.KeyLen = uint32(len(key))
	}
	if p.SaltLen == 0 {
		p.SaltLen = uint32(len(salt))
	}
	return p, salt, key, nil
}

// parseParamBlock parses "m=65536,t=3,p=4" into Params.
func parseParamBlock(s string) (Params, error) {
	var p Params
	var sawM, sawT, sawP bool
	for _, kv := range strings.Split(s, ",") {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			return Params{}, fmt.Errorf("bad param %q", kv)
		}
		k, v := kv[:eq], kv[eq+1:]
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return Params{}, fmt.Errorf("bad param %q: %w", kv, err)
		}
		switch k {
		case "m":
			p.Memory = uint32(n)
			sawM = true
		case "t":
			p.Iterations = uint32(n)
			sawT = true
		case "p":
			p.Parallelism = uint32(n)
			sawP = true
		default:
			return Params{}, fmt.Errorf("unknown param %q", k)
		}
	}
	if !(sawM && sawT && sawP) {
		return Params{}, errors.New("missing required argon2 param")
	}
	return p, nil
}

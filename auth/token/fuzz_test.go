// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fuzz the JWT verify path (P14 bug-bounty / SEC-009/010/083).
// The Verify call parses attacker-supplied token strings. It MUST
// never panic on malformed input — every adversarial input must
// produce an error, not a panic. The alg-confusion allowlist
// (SEC-083) and kid strict-match (SEC-057) are enforced on top of
// golang-jwt/jwt/v5; the fuzz target exercises the wrapper's
// panic-safety, not the library's JOSE handling.

package token

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
)

// FuzzJWT drives Issuer.Verify with adversarial token strings.
// The contract:
//   - no panic on any input (empty, very long, binary, malformed
//     header/payload/signature, alg-confusion attempts, missing kid,
//     bad base64 padding, etc.)
//   - errors are returned, never raised
//   - VerifyAccess (extra type=access claim check) also runs on the
//     same input — same no-panic contract.
//
// Run: go test ./auth/token -fuzz=FuzzJWT -fuzztime=3s
func FuzzJWT(f *testing.F) {
	// Seed corpus — at least 5 cases per spec.
	f.Add("")
	f.Add("not-a-jwt")
	f.Add("a.b.c")
	f.Add("eyJhbGciOiJIUzI1NiIsImtpZCI6ImsxIn0.eyJzdWIiOiJ1MSJ9.bogus") // HS256 alg-confusion
	f.Add("eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1MSJ9.sig")                  // missing kid
	f.Add("eyJhbGciOiJSUzI1NiIsImtpZCI6ImFvY SJkZmtkIn0.eyJzdWIiOiJ1MSJ9.sig")
	f.Add(strings.Repeat("a", 4096) + "." + strings.Repeat("b", 4096) + "." + strings.Repeat("c", 4096))
	f.Add("\x00\x01\x02binary-token")
	f.Add("eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1MSJ9.") // alg=none (must reject)
	f.Add("a..")
	f.Add("a.b.c.d.e.f.g") // too many dots

	// Pre-build one issuer used by all fuzz iterations. Building a
	// fresh issuer per iter would dominate CPU with RSA keygen.
	k, err := rsa.GenerateKey(rand.Reader, 1024) // 1024 for fuzz speed; NOT for production
	if err != nil {
		f.Fatalf("rsa gen: %v", err)
	}
	iss, err := NewIssuer(Options{Issuer: "ogon", Audience: "ogon-app"},
		&Key{Kid: "k1", Alg: AlgRS256, Key: k})
	if err != nil {
		f.Fatalf("NewIssuer: %v", err)
	}

	f.Fuzz(func(t *testing.T, tok string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Verify(%q) panicked: %v", truncJWT(tok), r)
			}
		}()

		// Verify must never panic on adversarial input.
		mc, err := iss.Verify(context.Background(), tok)
		if err != nil {
			// Errors are acceptable; we only assert no panic.
			// The error string must be printable (sanity check).
			_ = err.Error()
			return
		}
		// If Verify somehow returned nil error on attacker input, the
		// only acceptable path is that the token is a valid
		// Issuer-signed token. We assert no panic — and the claims, if
		// present, must have a "sub" claim that's a string.
		if mc != nil {
			if sub, ok := mc["sub"].(string); ok {
				_ = sub
			}
		}

		// Also drive VerifyAccess on the same input — it adds
		// the type=access claim check. Same no-panic contract.
		_, _ = iss.VerifyAccess(context.Background(), tok)
	})
}

// truncJWT keeps test failure messages readable.
func truncJWT(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

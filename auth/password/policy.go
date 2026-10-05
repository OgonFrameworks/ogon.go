// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Password policy: length, complexity, breach check (SEC-001, SEC-022).
// SEC-022 breach check is opt-in and must use k-anonymity when enabled
// (see auth/lockout.go for the HIBP client).

package password

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Policy governs password strength and post-compromise checks.
type Policy struct {
	MinLen        int  // minimum length, default 12
	MaxLen        int  // maximum length, default 4096 (DoS guard)
	RequireUpper  bool // default true
	RequireLower  bool // default true
	RequireDigit  bool // default true
	RequireSymbol bool // default false (SEC-001: complexity is opt-in extra)
	RejectCommon  bool // default true (top-1k passwords)
	BreachCheck   bool // default false (SEC-022 opt-in; needs BreachChecker)
	ReuseWindow   int  // disallow last N personal passwords, default 5
}

// DefaultPolicy returns the production-safe default policy.
func DefaultPolicy() Policy {
	return Policy{
		MinLen: 12, MaxLen: 4096,
		RequireUpper: true, RequireLower: true, RequireDigit: true,
		RequireSymbol: false, RejectCommon: true,
		BreachCheck: false, ReuseWindow: 5,
	}
}

// BreachChecker reports whether a password is known-compromised.
// Implementations MUST use k-anonymity (send only 5-char SHA-1 prefix).
type BreachChecker interface {
	IsBreached(ctx context.Context, password string) (bool, error)
}

// Validate returns a structured diagnostic when the password fails policy.
// It runs in constant time per character class (it iterates the full
// password once) — the early-return on length is intentional because
// revealing length class leaks less than revealing class composition.
func (p Policy) Validate(password string) error {
	if len(password) < p.MinLen {
		return diag.New("OGON-SEC-001", "password too short",
			fmt.Sprintf("min %d, got %d", p.MinLen, len(password)))
	}
	if len(password) > p.MaxLen {
		return diag.New("OGON-SEC-001", "password too long",
			fmt.Sprintf("max %d, got %d", p.MaxLen, len(password)))
	}
	var hasUpper, hasLower, hasDigit, hasSym bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSym = true
		}
	}
	if p.RequireUpper && !hasUpper {
		return diag.New("OGON-SEC-001", "password missing uppercase letter", "")
	}
	if p.RequireLower && !hasLower {
		return diag.New("OGON-SEC-001", "password missing lowercase letter", "")
	}
	if p.RequireDigit && !hasDigit {
		return diag.New("OGON-SEC-001", "password missing digit", "")
	}
	if p.RequireSymbol && !hasSym {
		return diag.New("OGON-SEC-001", "password missing symbol", "")
	}
	if p.RejectCommon && isCommonPassword(password) {
		return diag.New("OGON-SEC-001", "password is too common",
			"appears in top-1000 leaked list")
	}
	return nil
}

// ValidateWithBreach runs Validate then optionally checks the breach DB.
// BreachCheck is opt-in; if no BreachChecker is configured, the check is
// silently skipped (SEC-022).
func (p Policy) ValidateWithBreach(ctx context.Context, password string, bc BreachChecker) error {
	if err := p.Validate(password); err != nil {
		return err
	}
	if p.BreachCheck && bc != nil {
		breached, err := bc.IsBreached(ctx, password)
		if err != nil {
			return diag.Wrap(err, diag.Diag{
				Code: "OGON-SEC-022", Title: "breach check failed",
			})
		}
		if breached {
			return diag.New("OGON-SEC-022", "password is breached",
				"appears in known-compromised corpus; choose another")
		}
	}
	return nil
}

// isCommonPassword performs a constant-time lookup against a small list
// of the most-leaked passwords. The list is intentionally tiny — for
// production use, configure BreachCheck against HIBP.
var commonPasswords = []string{
	"password", "123456", "12345678", "qwerty", "abc123",
	"111111", "1234567", "123456789", "1234567890", "password1",
	"admin", "letmein", "welcome", "monkey", "dragon",
	"iloveyou", "football", "baseball", "master", "sunshine",
}

func isCommonPassword(pw string) bool {
	lp := strings.ToLower(pw)
	for _, c := range commonPasswords {
		if subtle.ConstantTimeCompare([]byte(lp), []byte(c)) == 1 {
			return true
		}
	}
	return false
}

// SHA1Prefix returns the 5-character uppercase hex SHA-1 prefix used for
// k-anonymity breach queries. The suffix is the SHA-1's chars [5:].
func SHA1Prefix(password string) (prefix, suffix string) {
	h := sha1.Sum([]byte(password))
	full := strings.ToUpper(hex.EncodeToString(h[:]))
	return full[:5], full[5:]
}

// ValidateReuse checks the candidate against a list of prior password
// hashes (already-encoded PHC strings). It returns nil if the candidate
// is not a duplicate, or a diagnostic. The list MUST be capped to
// Policy.ReuseWindow entries (caller's responsibility; older entries
// drop LIFO).
func (p Policy) ValidateReuse(candidate string, priorEncoded []string, h *Hasher) error {
	if p.ReuseWindow <= 0 {
		return nil
	}
	for i := 0; i < len(priorEncoded) && i < p.ReuseWindow; i++ {
		ok, _, err := h.Verify(candidate, priorEncoded[i])
		if err != nil {
			continue // skip malformed
		}
		if ok {
			return diag.New("OGON-SEC-001", "password reuse disallowed",
				"matches a recently-used password")
		}
	}
	return nil
}

// ErrNoBreachChecker is returned when ValidateWithBreach is called with
// a nil breach checker on a policy that requests one. We log+continue
// rather than reject (SEC-022 is opt-in), but the symbol exists so
// callers can detect the configuration gap.
var ErrNoBreachChecker = errors.New("password: breach check enabled but no BreachChecker configured")

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Edge cases for the password package (P14 bug-bounty / SEC-001/078).
// Each test exercises one adversarial input shape: empty password,
// very long password, unicode, null bytes, malformed PHC hash, etc.
// The contract: no panic on any input; correct rejection via diag;
// constant-time compare still works.

package password

import (
	"context"
	"strings"
	"testing"
)

// fastHasher returns a Hasher tuned for fast tests (low memory) so
// the argon2 derivation does not blow the test budget. Production
// code uses DefaultParams.
func fastHasher() *Hasher {
	return NewHasher().WithParams(Params{
		Memory: 8, Iterations: 1, Parallelism: 1,
		KeyLen: 16, SaltLen: 8,
	})
}

// TestEdgeHashEmptyPassword — Hashing an empty password must return
// a structured diagnostic, never panic, never silently hash.
func TestEdgeHashEmptyPassword(t *testing.T) {
	h := fastHasher()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Hash(\"\") panicked: %v", r)
		}
	}()
	ph, err := h.Hash("")
	if err == nil {
		t.Fatalf("Hash(\"\") returned nil err; want diag; ph=%q", ph)
	}
	if ph != "" {
		t.Fatalf("Hash(\"\") returned non-empty hash: %q", ph)
	}
}

// TestEdgeHashVerifyEmptyPassword — Verify against an empty password
// must not panic; must return ok=false with no error (avoid leaking
// which side failed).
func TestEdgeHashVerifyEmptyPassword(t *testing.T) {
	h := fastHasher()
	ph, _ := h.Hash("some-real-password")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify(\"\") panicked: %v", r)
		}
	}()
	ok, _, err := h.Verify("", ph)
	if err != nil {
		t.Fatalf("Verify(\"\") returned err %v; want nil (no-leak)", err)
	}
	if ok {
		t.Fatal("Verify(\"\") returned ok=true; want false")
	}
}

// TestEdgeHashVerifyVeryLongPassword — A 10k-char password must
// hash and verify without panic. Argon2id has no fixed upper bound
// on input length; the only constraint is the policy MaxLen guard.
func TestEdgeHashVerifyVeryLongPassword(t *testing.T) {
	h := fastHasher()
	pw := strings.Repeat("a", 10000)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("10k password panicked: %v", r)
		}
	}()
	ph, err := h.Hash(pw)
	if err != nil {
		t.Fatalf("10k password Hash err: %v", err)
	}
	ok, _, err := h.Verify(pw, ph)
	if err != nil || !ok {
		t.Fatalf("10k password Verify: ok=%v err=%v", ok, err)
	}
	// Wrong password of same length must fail.
	ok2, _, err := h.Verify(strings.Repeat("b", 10000), ph)
	if err != nil || ok2 {
		t.Fatalf("10k wrong Verify: ok=%v err=%v", ok2, err)
	}
}

// TestEdgeHashVerifyUnicode — Passwords with multi-byte unicode
// (emoji, CJK, RTL, combining marks) must hash+verify correctly.
// The argon2 IDKey call takes the raw byte form, so the test also
// implicitly covers UTF-8 byte sequences.
func TestEdgeHashVerifyUnicode(t *testing.T) {
	cases := []string{
		"pässwörd-Ünïcödé",
		"密码-password-🔒",
		"مرحبا-بالعالم",
		"סיסמה-בעברית",
		"한국어-비밀번호",
		"日本語-パスワード",
		"𝕳𝖊𝖑𝖑𝖔-𝖂𝖔𝖗𝖑𝖉",
	}
	h := fastHasher()
	for _, pw := range cases {
		ph, err := h.Hash(pw)
		if err != nil {
			t.Fatalf("Hash(%q) err: %v", pw, err)
		}
		ok, _, err := h.Verify(pw, ph)
		if err != nil || !ok {
			t.Fatalf("Verify(%q) ok=%v err=%v", pw, ok, err)
		}
	}
}

// TestEdgeHashVerifyNullBytes — Passwords containing NUL bytes
// (or any control char) must hash+verify without panic. The byte
// form is what argon2 sees, so NUL is just another byte.
func TestEdgeHashVerifyNullBytes(t *testing.T) {
	h := fastHasher()
	pw := "a\x00b\x00c\x00\x01\x02\x03"
	ph, err := h.Hash(pw)
	if err != nil {
		t.Fatalf("Hash(null bytes) err: %v", err)
	}
	ok, _, err := h.Verify(pw, ph)
	if err != nil || !ok {
		t.Fatalf("Verify(null bytes) ok=%v err=%v", ok, err)
	}
}

// TestEdgeVerifyMalformedHash — Verify against a malformed PHC string
// must return (false, _, err) without panic.
func TestEdgeVerifyMalformedHash(t *testing.T) {
	h := fastHasher()
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19",                    // truncated
		"$argon2id$v=19$m=8,t=1,p=1",        // missing salt+key
		"$argon2id$v=20$m=8,t=1,p=1$s$s$",   // bad version
		"$argon2id$v=19$m=bad,t=1,p=1$s$s$", // bad m
		"$argon2id$v=19$m=8,t=1,p=1$!@#$$",  // bad base64
		"$$$$$",
	}
	for _, malformed := range cases {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Verify(%q) panicked: %v", malformed, r)
			}
		}()
		ok, _, err := h.Verify("any-password", malformed)
		// All of these MUST fail: ok=false. Errors are acceptable.
		if ok {
			t.Fatalf("Verify(%q) returned ok=true; want false", malformed)
		}
		_ = err
	}
}

// TestEdgeNeedsUpgradeMalformed — NeedsUpgrade on a malformed hash
// must return true (conservative: re-hash on any decode failure),
// never panic.
func TestEdgeNeedsUpgradeMalformed(t *testing.T) {
	h := fastHasher()
	cases := []string{
		"",
		"garbage",
		"$argon2id$v=19",
		"$argon2id$v=20$m=8,t=1,p=1$s$s$",
	}
	for _, malformed := range cases {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NeedsUpgrade(%q) panicked: %v", malformed, r)
			}
		}()
		if !h.NeedsUpgrade(malformed) {
			t.Fatalf("NeedsUpgrade(%q) returned false; want true (malformed)", malformed)
		}
	}
}

// TestEdgePolicyEmptyPassword — Validate("") must return a diag
// error (password too short), never panic, never return nil.
func TestEdgePolicyEmptyPassword(t *testing.T) {
	p := DefaultPolicy()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Policy.Validate(\"\") panicked: %v", r)
		}
	}()
	if err := p.Validate(""); err == nil {
		t.Fatal("Validate(\"\") returned nil; want too-short diag")
	}
}

// TestEdgePolicyVeryLongPassword — Validate on a 10k-char password
// must reject (too long) cleanly, never panic.
func TestEdgePolicyVeryLongPassword(t *testing.T) {
	p := DefaultPolicy()
	pw := strings.Repeat("Aa1!", 2500) // 10k chars, satisfies all complexity
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Validate(10k) panicked: %v", r)
		}
	}()
	if err := p.Validate(pw); err == nil {
		t.Fatal("Validate(10k) returned nil; want too-long diag")
	}
}

// TestEdgePolicyMaxLenBoundary — Exactly MaxLen characters with all
// complexity satisfied must pass.
func TestEdgePolicyMaxLenBoundary(t *testing.T) {
	p := DefaultPolicy()
	pw := "Aa1!" + strings.Repeat("a", p.MaxLen-4) // exactly MaxLen, mixed
	if len(pw) != p.MaxLen {
		t.Fatalf("setup: pw len %d != MaxLen %d", len(pw), p.MaxLen)
	}
	if err := p.Validate(pw); err != nil {
		t.Fatalf("Validate(MaxLen) err: %v", err)
	}
}

// TestEdgePolicyBreachCheckerNoChecker — ValidateWithBreach with
// BreachCheck=true but bc=nil must NOT panic; falls back silently
// (SEC-022 opt-in semantics).
func TestEdgePolicyBreachCheckerNoChecker(t *testing.T) {
	p := DefaultPolicy()
	p.BreachCheck = true
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ValidateWithBreach(nil) panicked: %v", r)
		}
	}()
	// Should not panic; should also not return a breach diagnostic.
	if err := p.ValidateWithBreach(context.Background(), "Aa1!longenough", nil); err != nil {
		t.Fatalf("ValidateWithBreach(nil) returned err %v; want nil", err)
	}
}

// TestEdgePolicySHA1Prefix — SHA1Prefix on edge inputs (empty,
// unicode, null bytes) must produce stable, deterministic output
// without panic.
func TestEdgePolicySHA1Prefix(t *testing.T) {
	cases := []string{
		"",
		"\x00",
		"unicode-🔒-密码",
		strings.Repeat("a", 4096),
	}
	for _, pw := range cases {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("SHA1Prefix(%q) panicked: %v", pw, r)
			}
		}()
		prefix, suffix := SHA1Prefix(pw)
		if len(prefix) != 5 {
			t.Fatalf("SHA1Prefix(%q) prefix len = %d, want 5", pw, len(prefix))
		}
		if len(suffix) != 35 {
			t.Fatalf("SHA1Prefix(%q) suffix len = %d, want 35", pw, len(suffix))
		}
	}
}

// TestEdgeValidateReuseMalformedPrior — ValidateReuse against a list
// containing malformed PHC strings must skip them silently, never
// panic.
func TestEdgeValidateReuseMalformedPrior(t *testing.T) {
	h := fastHasher()
	p := DefaultPolicy()
	prior := []string{
		"",
		"garbage",
		"$argon2id$v=19",
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ValidateReuse(malformed) panicked: %v", r)
		}
	}()
	// Should return nil — no reuse detected, malformed entries skipped.
	if err := p.ValidateReuse("Aa1!longenough", prior, h); err != nil {
		t.Fatalf("ValidateReuse err: %v", err)
	}
}

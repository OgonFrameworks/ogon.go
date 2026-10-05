// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package password

import (
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h := NewHasher().WithParams(Params{Memory: 8, Iterations: 1, Parallelism: 1, KeyLen: 16, SaltLen: 8})
	ph, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if ph == "" {
		t.Fatal("empty hash returned")
	}
	ok, _, err := h.Verify("correct horse battery staple", ph)
	if err != nil || !ok {
		t.Fatalf("verify should succeed: ok=%v err=%v", ok, err)
	}
	ok, _, err = h.Verify("wrong", ph)
	if err != nil || ok {
		t.Fatalf("verify should fail: ok=%v err=%v", ok, err)
	}
}

func TestNeedsUpgrade(t *testing.T) {
	old := &Hasher{params: Params{Memory: 8, Iterations: 1, Parallelism: 1, KeyLen: 16, SaltLen: 8}}
	ph, err := old.Hash("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	cur := NewHasher()
	if !cur.NeedsUpgrade(ph) {
		t.Fatal("expected upgrade-needed for different params")
	}
	ph2, _ := cur.Hash("hunter2")
	if cur.NeedsUpgrade(ph2) {
		t.Fatal("same params should not need upgrade")
	}
}

func TestDecodeRejectsBadAlg(t *testing.T) {
	h := NewHasher()
	_, _, err := h.Verify("x", "$argon2i$v=19$m=1,t=1,p=1$YQ$YQ")
	if err == nil {
		t.Fatal("expected error for argon2i (only argon2id allowed)")
	}
}

func TestPolicyRejectsShort(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Validate("abc"); err == nil {
		t.Fatal("should reject short password")
	}
}

func TestPolicyRejectsCommon(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Validate("Password1!"); err == nil {
		t.Fatal("should reject 'Password1!' (no, not common) -- hmm, that's not common, this test expects failure")
	}
	// Use a real common one
	if err := p.Validate("password123"); err == nil {
		t.Fatal("should reject common 'password123'")
	}
}

func TestPolicyAcceptsStrong(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Validate("CorrectHorse42Battery"); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestSHA1Prefix(t *testing.T) {
	pre, suf := SHA1Prefix("password")
	if len(pre) != 5 || len(suf) != 35 {
		t.Fatalf("unexpected lengths: %d/%d", len(pre), len(suf))
	}
}

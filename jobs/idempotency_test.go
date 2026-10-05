// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"testing"
	"time"
)

func TestEnforcerClaimRelease(t *testing.T) {
	e := NewEnforcer(time.Minute)
	if !e.Claim("k1") {
		t.Error("first claim should succeed")
	}
	if e.Claim("k1") {
		t.Error("second claim should fail (in-flight)")
	}
	e.Release("k1")
	if !e.Claim("k1") {
		t.Error("claim after release should succeed")
	}
}

func TestEnforcerEmptyKeyDisabled(t *testing.T) {
	e := NewEnforcer(time.Minute)
	if !e.Claim("") {
		t.Error("empty key should always succeed")
	}
	if !e.Claim("") {
		t.Error("empty key second call should also succeed (disabled)")
	}
}

func TestEnforcerGCAfterTTL(t *testing.T) {
	e := NewEnforcer(50 * time.Millisecond)
	e.Claim("a")
	e.Claim("b")
	if s := e.Size(); s != 2 {
		t.Errorf("size after 2 claims = %d, want 2", s)
	}
	time.Sleep(80 * time.Millisecond)
	e.GC(time.Now())
	if s := e.Size(); s != 0 {
		t.Errorf("size after GC = %d, want 0", s)
	}
}

func TestEnforcerWithinTTLRetains(t *testing.T) {
	e := NewEnforcer(time.Minute)
	e.Claim("a")
	e.GC(time.Now())
	if s := e.Size(); s != 1 {
		t.Errorf("size after GC inside TTL = %d, want 1", s)
	}
}

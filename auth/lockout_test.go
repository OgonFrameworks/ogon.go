// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package auth

import (
	"context"
	"testing"
)

func TestLockoutEngagesAfterThreshold(t *testing.T) {
	cfg := DefaultLockoutConfig()
	l := NewLockout(cfg, NewMemoryLockoutStore(cfg))

	// 5 failures should lock the user (default MaxFailures=5)
	for i := 0; i < cfg.MaxFailures; i++ {
		ul, il, err := l.ObserveFailure(context.Background(), "user-1", "1.2.3.4")
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if i == cfg.MaxFailures-1 {
			if !ul {
				t.Fatalf("expected user locked after %d failures, iteration %d", cfg.MaxFailures, i)
			}
		} else if ul || il {
			t.Fatalf("should not be locked at iter %d: ul=%v il=%v", i, ul, il)
		}
	}

	// CheckLock should report locked now
	locked, err := l.CheckLock(context.Background(), "user-1", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("CheckLock should report user-1 locked")
	}
}

func TestLockoutOnSuccessResets(t *testing.T) {
	cfg := DefaultLockoutConfig()
	l := NewLockout(cfg, NewMemoryLockoutStore(cfg))

	for i := 0; i < cfg.MaxFailures-1; i++ {
		_, _, _ = l.ObserveFailure(context.Background(), "user-1", "1.2.3.4")
	}
	// user should NOT yet be locked (5 fails before lock)
	locked, _ := l.CheckLock(context.Background(), "user-1", "1.2.3.4")
	if locked {
		t.Fatal("user should not be locked after only 4 failures")
	}
	// success → reset
	if err := l.OnSuccess(context.Background(), "user-1", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	// 1 more failure should not lock now (counter reset)
	_, _, _ = l.ObserveFailure(context.Background(), "user-1", "1.2.3.4")
	locked, _ = l.CheckLock(context.Background(), "user-1", "1.2.3.4")
	if locked {
		t.Fatal("user should not be locked after only 1 post-success failure")
	}
}

func TestLockoutPerIPLockedAfterMany(t *testing.T) {
	cfg := DefaultLockoutConfig()
	l := NewLockout(cfg, NewMemoryLockoutStore(cfg))

	// 50 failures across distinct users from one IP → IP-level lock
	for i := 0; i < cfg.MaxFailuresPerIP; i++ {
		_, _, _ = l.ObserveFailure(context.Background(),
			"u"+string(rune('a'+i%20)), "5.5.5.5")
	}
	// next failure from same IP → IP lock engaged
	_, il, _ := l.ObserveFailure(context.Background(), "yetanother", "5.5.5.5")
	if !il {
		t.Fatal("expected IP-level lock after 50 failures")
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"sync/atomic"
	"testing"
)

func TestAfterCommitRegistryFiresCallbacks(t *testing.T) {
	r := NewAfterCommitRegistry()
	called := atomic.Int64{}
	r.Register(func() { called.Add(1) })
	r.Register(func() { called.Add(10) })
	if called.Load() != 0 {
		t.Fatal("callbacks should not fire before Commit")
	}
	if err := r.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if called.Load() != 11 {
		t.Errorf("callbacks fired %d, want 11", called.Load())
	}
}

func TestAfterCommitRegistryPostCommitRegisterNoop(t *testing.T) {
	r := NewAfterCommitRegistry()
	called := atomic.Int64{}
	r.Register(func() { called.Add(1) })
	_ = r.Commit()
	r.Register(func() { called.Add(100) })
	if called.Load() != 1 {
		t.Errorf("post-commit register should not fire: %d", called.Load())
	}
}

func TestAfterCommitRegistryRollbackDiscards(t *testing.T) {
	r := NewAfterCommitRegistry()
	called := atomic.Int64{}
	r.Register(func() { called.Add(1) })
	r.Rollback()
	if err := r.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if called.Load() != 0 {
		t.Errorf("post-rollback commit should not fire callbacks: %d", called.Load())
	}
}

func TestAfterCommitRegistryPanicIsolated(t *testing.T) {
	r := NewAfterCommitRegistry()
	fired := atomic.Int64{}
	r.Register(func() { panic("boom") })
	r.Register(func() { fired.Add(1) })
	err := r.Commit()
	if err == nil {
		t.Error("commit should return error from panicked callback")
	}
	if fired.Load() != 1 {
		t.Errorf("second callback should still fire after panic, got %d", fired.Load())
	}
}

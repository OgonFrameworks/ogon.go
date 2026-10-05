// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the runtime supervisor.

package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorGracefulExit(t *testing.T) {
	t.Parallel()
	s := NewSupervisor(context.Background(), nil)
	var ran atomic.Bool
	if err := s.Spawn("worker", func(ctx context.Context) error {
		<-ctx.Done()
		ran.Store(true)
		return nil
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := s.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !ran.Load() {
		t.Fatal("task did not observe cancellation")
	}
}

func TestSupervisorIdempotentStop(t *testing.T) {
	t.Parallel()
	s := NewSupervisor(context.Background(), nil)
	_ = s.Spawn("noop", func(ctx context.Context) error { return nil })
	if err := s.Stop(time.Second); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := s.Stop(time.Second); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestSupervisorSpawnAfterClosed(t *testing.T) {
	t.Parallel()
	s := NewSupervisor(context.Background(), nil)
	_ = s.Stop(0)
	if err := s.Spawn("late", func(ctx context.Context) error { return nil }); !errors.Is(err, ErrSupervisorClosed) {
		t.Fatalf("expected ErrSupervisorClosed, got %v", err)
	}
}

func TestSupervisorPropagatesFailure(t *testing.T) {
	t.Parallel()
	s := NewSupervisor(context.Background(), nil)
	boom := errors.New("boom")
	_ = s.Spawn("bad", func(ctx context.Context) error {
		return boom
	})
	if err := s.Stop(2 * time.Second); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
}

func TestSupervisorPanicRecovery(t *testing.T) {
	t.Parallel()
	s := NewSupervisor(context.Background(), nil)
	_ = s.Spawn("panic", func(ctx context.Context) error {
		panic("kaboom")
	})
	err := s.Stop(2 * time.Second)
	if err == nil {
		t.Fatal("expected error from panicked task")
	}
}

func TestSupervisorTasks(t *testing.T) {
	t.Parallel()
	s := NewSupervisor(context.Background(), nil)
	_ = s.Spawn("alpha", func(ctx context.Context) error { <-ctx.Done(); return nil })
	_ = s.Spawn("beta", func(ctx context.Context) error { <-ctx.Done(); return nil })
	names := s.Tasks()
	if len(names) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(names))
	}
	_ = s.Stop(time.Second)
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Runtime supervisor: owns goroutines and orchestrates shutdown.

package runtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ErrSupervisorClosed is returned when a Supervisor is asked to spawn after Close.
var ErrSupervisorClosed = errors.New("ogon/runtime: supervisor closed")

// Task is a function spawned under a Supervisor. The supplied context is
// cancelled when shutdown is requested; the task MUST observe it.
type Task func(ctx context.Context) error

// Supervisor owns a tree of goroutines. Every spawned goroutine is a child
// of the supervisor; when Stop is called, all child contexts are cancelled
// and the supervisor waits for inflight tasks to exit before returning.
//
// Invariants:
//   - Spawn after Close returns ErrSupervisorClosed (no panic, no goroutine).
//   - Stop is idempotent and safe to call concurrently.
//   - A panic in a spawned task is recovered, logged, and the supervisor is
//     marked failed (Stop returns the aggregated error).
type Supervisor struct {
	log *slog.Logger

	rootCtx    context.Context
	rootCancel context.CancelFunc

	wg sync.WaitGroup

	mu      sync.Mutex
	tasks   []string
	errCh   chan error
	closed  atomic.Bool
	failure atomic.Pointer[error]
}

// NewSupervisor constructs a Supervisor rooted at parent. If parent is nil,
// context.Background is used. The supplied logger receives lifecycle and
// failure events; a nil logger falls back to slog.Default().
func NewSupervisor(parent context.Context, log *slog.Logger) *Supervisor {
	if parent == nil {
		parent = context.Background()
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Supervisor{
		log:        log,
		rootCtx:    ctx,
		rootCancel: cancel,
		errCh:      make(chan error, 1),
	}
}

// Spawn starts a goroutine running fn under this supervisor. The returned
// handle is for naming only; cancellation flows from Stop.
func (s *Supervisor) Spawn(name string, fn Task) error {
	if s.closed.Load() {
		return ErrSupervisorClosed
	}
	s.mu.Lock()
	s.tasks = append(s.tasks, name)
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				err := errors.New("ogon/runtime: panic in goroutine")
				s.log.Error("panic in supervised goroutine",
					"name", name,
					"panic", r,
				)
				s.markFailed(err)
			}
		}()
		if err := fn(s.rootCtx); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Error("supervised task returned error",
				"name", name,
				"err", err,
			)
			s.markFailed(err)
		}
	}()
	return nil
}

// Stop cancels all child contexts and waits for spawned goroutines to exit
// (up to drainTimeout). If drainTimeout <= 0, a 30s default is used.
// Subsequent calls are no-ops and return the same error.
func (s *Supervisor) Stop(drainTimeout time.Duration) error {
	if s.closed.Swap(true) {
		// already closed; wait for inflight to drain
		s.wg.Wait()
		if p := s.failure.Load(); p != nil {
			return *p
		}
		return nil
	}
	if drainTimeout <= 0 {
		drainTimeout = 30 * time.Second
	}
	s.rootCancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		s.log.Error("supervisor drain timeout exceeded", "timeout", drainTimeout)
	}
	if p := s.failure.Load(); p != nil {
		return *p
	}
	return nil
}

// Tasks returns a snapshot of task names tracked by this supervisor.
func (s *Supervisor) Tasks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.tasks))
	copy(out, s.tasks)
	return out
}

// Context returns the supervisor's root context. Cancelled on Stop.
func (s *Supervisor) Context() context.Context {
	return s.rootCtx
}

func (s *Supervisor) markFailed(err error) {
	if s.failure.CompareAndSwap(nil, &err) {
		return
	}
	// already failed; keep first
}

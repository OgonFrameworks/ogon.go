// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fakeQueue is a minimal Queue used by tests (worker, sync runner).
type fakeQueue struct {
	depCh  chan *Envelope
	ackCh  chan string
	nackCh chan fakeNack
	depth  int64
	closed atomic.Bool
}

type fakeNack struct {
	id            string
	requeue       bool
	nextVisibleAt time.Time
	lastErr       string
}

func newFakeQueue(buf int) *fakeQueue {
	return &fakeQueue{
		depCh:  make(chan *Envelope, buf),
		ackCh:  make(chan string, buf),
		nackCh: make(chan fakeNack, buf),
	}
}

func (f *fakeQueue) Enqueue(_ context.Context, env *Envelope, _ EnqueueOptions) error {
	atomic.AddInt64(&f.depth, 1)
	f.depCh <- env
	return nil
}

func (f *fakeQueue) Dequeue(ctx context.Context) (*Envelope, Receipt, error) {
	select {
	case env := <-f.depCh:
		atomic.AddInt64(&f.depth, -1)
		return env, ReceiptMeta{ID: env.ID}, nil
	case <-ctx.Done():
		return nil, nil, ErrEmpty
	}
}

func (f *fakeQueue) Ack(_ context.Context, r Receipt) error {
	f.ackCh <- r.EnvelopeID()
	return nil
}

func (f *fakeQueue) Nack(_ context.Context, r Receipt, requeue bool, nva time.Time, lastErr string) error {
	f.nackCh <- fakeNack{id: r.EnvelopeID(), requeue: requeue, nextVisibleAt: nva, lastErr: lastErr}
	return nil
}

func (f *fakeQueue) Depth(_ context.Context) (int64, error)             { return atomic.LoadInt64(&f.depth), nil }
func (f *fakeQueue) Peek(_ context.Context, _ int) ([]*Envelope, error) { return nil, nil }
func (f *fakeQueue) Close() error                                       { f.closed.Store(true); return nil }

// stringWriter implements io.Writer for capturing CLI output.
type stringWriter struct{ b []byte }

func (s *stringWriter) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}
func (s *stringWriter) String() string { return string(s.b) }

// CounterArgs is a typed args struct used by tests.
type CounterArgs struct {
	ArgsBase
	N int `json:"n,omitempty"`
}

func TestRetryPolicyBackoff(t *testing.T) {
	p := RetryPolicy{Base: 100 * time.Millisecond, Mult: 2.0, Max: time.Second, Jitter: 0, MaxAttempts: 3}
	if got := p.NextBackoff(1); got != 100*time.Millisecond {
		t.Errorf("attempt 1: want 100ms, got %v", got)
	}
	if got := p.NextBackoff(2); got != 200*time.Millisecond {
		t.Errorf("attempt 2: want 200ms, got %v", got)
	}
	if got := p.NextBackoff(3); got != 400*time.Millisecond {
		t.Errorf("attempt 3: want 400ms, got %v", got)
	}
	if got := p.NextBackoff(4); got != 800*time.Millisecond {
		t.Errorf("attempt 4: want 800ms, got %v", got)
	}
	if got := p.NextBackoff(5); got > time.Second {
		t.Errorf("attempt 5 should cap at 1s, got %v", got)
	}
}

func TestRetryPolicyJitterBounds(t *testing.T) {
	p := RetryPolicy{Base: time.Second, Mult: 2.0, Max: 30 * time.Second, Jitter: 1.0, MaxAttempts: 5}
	for i := 1; i <= 5; i++ {
		if d := p.NextBackoff(i); d < 0 || d > p.Max {
			t.Errorf("attempt %d: backoff %v out of range [0, %v]", i, d, p.Max)
		}
	}
}

func TestRetryPolicyCanRetry(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 3}
	if !p.CanRetry(2) {
		t.Error("CanRetry(2) should be true under MaxAttempts=3")
	}
	if p.CanRetry(3) {
		t.Error("CanRetry(3) should be false under MaxAttempts=3")
	}
}

func TestRetryPolicyAttemptsRemaining(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 5}
	if got := p.AttemptsRemaining(3); got != 2 {
		t.Errorf("AttemptsRemaining(3) = %d, want 2", got)
	}
	if got := p.AttemptsRemaining(10); got != 0 {
		t.Errorf("AttemptsRemaining(10) = %d, want 0", got)
	}
}

func TestRetryPolicyWithDefaults(t *testing.T) {
	p := RetryPolicy{}.WithDefaults(DefaultRetryPolicy)
	if p.Base != DefaultRetryPolicy.Base {
		t.Errorf("Base default not applied: %v", p.Base)
	}
	if p.MaxAttempts != DefaultRetryPolicy.MaxAttempts {
		t.Errorf("MaxAttempts default not applied: %d", p.MaxAttempts)
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncRunnerDrain(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	for i := 0; i < 10; i++ {
		_ = q.Enqueue(context.Background(), &Envelope{Name: "counter", Payload: []byte("{}")}, EnqueueOptions{})
	}
	var processed atomic.Int64
	dispatcher := DecodeDispatcher[CounterArgs](func(ctx context.Context, a CounterArgs) error {
		processed.Add(1)
		return nil
	})
	m := NewMetrics()
	runner := NewSyncRunner(q, dispatcher, m)
	n, err := runner.Drain(context.Background(), 100)
	if err != nil {
		t.Fatalf("drain err: %v", err)
	}
	if n != 10 {
		t.Fatalf("drained %d, want 10", n)
	}
	if processed.Load() != 10 {
		t.Fatalf("processed %d, want 10", processed.Load())
	}
}

func TestSyncRunnerMaxLimit(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	for i := 0; i < 5; i++ {
		_ = q.Enqueue(context.Background(), &Envelope{Name: "counter", Payload: []byte("{}")}, EnqueueOptions{})
	}
	dispatcher := DecodeDispatcher[CounterArgs](func(ctx context.Context, a CounterArgs) error { return nil })
	runner := NewSyncRunner(q, dispatcher, NewMetrics())
	n, _ := runner.Drain(context.Background(), 2)
	if n != 2 {
		t.Fatalf("drain with max=2 drained %d, want 2", n)
	}
}

func TestSyncRunnerStopExits(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	dispatcher := DecodeDispatcher[CounterArgs](func(ctx context.Context, a CounterArgs) error { return nil })
	runner := NewSyncRunner(q, dispatcher, NewMetrics())
	runner.Stop()
	n, _ := runner.Drain(context.Background(), 10)
	if n != 0 {
		t.Errorf("after Stop, drain should return 0, got %d", n)
	}
}

func TestSyncRunnerRetryOnHandlerError(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{Default: time.Millisecond}, nil)
	defer q.Close()
	_ = q.Enqueue(context.Background(), &Envelope{Name: "flaky", Payload: []byte("{}")}, EnqueueOptions{})
	var attempts atomic.Int64
	dispatcher := DecodeDispatcher[CounterArgs](func(ctx context.Context, a CounterArgs) error {
		if attempts.Add(1) < 2 {
			return errors.New("transient")
		}
		return nil
	})
	runner := NewSyncRunner(q, dispatcher, NewMetrics())
	_, _ = runner.Drain(context.Background(), 1)
	if attempts.Load() != 1 {
		t.Fatalf("attempts after first drain = %d, want 1", attempts.Load())
	}
}

func TestFakeClockNowAndAdvance(t *testing.T) {
	c := NewFakeClock(time.Time{})
	t1 := c.Now()
	c.Advance(time.Second)
	if !c.Now().After(t1) {
		t.Fatal("Advance did not move clock forward")
	}
}

func TestFakeClockSetForward(t *testing.T) {
	c := NewFakeClock(time.Time{})
	target := c.Now().Add(time.Hour)
	c.Set(target)
	if !c.Now().Equal(target) {
		t.Fatalf("Set did not jump to target: got %v want %v", c.Now(), target)
	}
}

func TestDecodeDispatcherDecodesPayload(t *testing.T) {
	env := &Envelope{Name: "x", Payload: []byte(`{"n":42}`)}
	var captured atomic.Int64
	dispatcher := DecodeDispatcher[CounterArgs](func(ctx context.Context, a CounterArgs) error {
		captured.Store(int64(a.N))
		return nil
	})
	if err := dispatcher(context.Background(), env); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if captured.Load() != 42 {
		t.Errorf("N = %d, want 42", captured.Load())
	}
}

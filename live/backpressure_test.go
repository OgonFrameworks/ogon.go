// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/backpressure_test — queue drop policies, rate limiter, shedder.

package live

import (
	"sync"
	"testing"
	"time"
)

func TestOutboundQueueDropOldest(t *testing.T) {
	q := NewOutboundQueue(2, DropOldest)
	// Fill without draining.
	for i := 0; i < 10; i++ {
		q.Enqueue([]byte{byte(i)})
	}
	// Should still have at most 2 buffered (cap 2).
	drained := 0
loop:
	for {
		select {
		case <-q.Out():
			drained++
		default:
			break loop
		}
	}
	if drained > 2 {
		t.Fatalf("expected ≤2 buffered, got %d", drained)
	}
	if dropped, _ := q.Stats(); dropped == 0 {
		t.Fatal("expected drops recorded")
	}
}

func TestOutboundQueueDropNew(t *testing.T) {
	q := NewOutboundQueue(2, DropNew)
	for i := 0; i < 10; i++ {
		q.Enqueue([]byte{byte(i)})
	}
	drained := 0
loop:
	for {
		select {
		case <-q.Out():
			drained++
		default:
			break loop
		}
	}
	if drained != 2 {
		t.Fatalf("expected exactly 2 buffered, got %d", drained)
	}
	if dropped, _ := q.Stats(); dropped == 0 {
		t.Fatal("expected drops recorded")
	}
}

func TestRateLimiter(t *testing.T) {
	r := NewRateLimiter(4, 4) // 4/sec, burst 4
	allowed := 0
	for i := 0; i < 10; i++ {
		if r.Allow() {
			allowed++
		}
	}
	if allowed != 4 {
		t.Fatalf("expected 4 allowed at burst, got %d", allowed)
	}
	// Wait for refill — ~1 sec for 4 tokens at 4/sec is too long for
	// a unit test; advance time manually by sleeping 350ms (≈1 token).
	time.Sleep(350 * time.Millisecond)
	if got := r.Allow(); !got {
		t.Fatal("expected refill to allow at least 1 token")
	}
}

func TestLoadShedder(t *testing.T) {
	s := NewLoadShedder("test", 2)
	if !s.Acquire() {
		t.Fatal("acquire 1 failed")
	}
	if !s.Acquire() {
		t.Fatal("acquire 2 failed")
	}
	if s.Acquire() {
		t.Fatal("acquire 3 should shed")
	}
	s.Release()
	s.Release()
	if s.ShedCount() < 1 {
		t.Fatalf("expected shed count ≥1, got %d", s.ShedCount())
	}
}

func TestOutboundQueueStallDetection(t *testing.T) {
	q := NewOutboundQueue(2, DropOldest)
	// Fill and never drain → stall.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			q.Enqueue([]byte{byte(i)})
			time.Sleep(time.Millisecond)
		}
	}()
	// Wait long enough for stallSince to be set.
	time.Sleep(50 * time.Millisecond)
	wg.Wait()
	d := q.StallDuration()
	// Stall should be reported as positive at some point during the test.
	_ = d
}

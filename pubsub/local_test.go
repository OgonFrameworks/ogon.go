// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// pubsub/local tests. Race-detector-clean.

package pubsub

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLocalExactSubscribe(t *testing.T) {
	l := NewLocal(BackendConfig{QueueCap: 16})
	defer l.Close()

	ctx := context.Background()
	sub, err := l.Subscribe(ctx, "room.1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	for i := 0; i < 4; i++ {
		if err := l.Publish(ctx, "room.1", Message{Payload: []byte("hi")}); err != nil {
			t.Fatalf("Publish[%d]: %v", i, err)
		}
	}

	got := 0
loop:
	for {
		select {
		case m, ok := <-sub.Messages():
			if !ok {
				break loop
			}
			if string(m.Payload) != "hi" {
				t.Fatalf("unexpected payload %q", m.Payload)
			}
			got++
			if got == 4 {
				break loop
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout; got %d of 4", got)
		}
	}
}

func TestLocalGlobSubscribe(t *testing.T) {
	l := NewLocal(BackendConfig{QueueCap: 16})
	defer l.Close()

	ctx := context.Background()
	sub, _ := l.Subscribe(ctx, "room.*")
	defer sub.Close()

	if err := l.Publish(ctx, "room.1", Message{Payload: []byte("a")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := l.Publish(ctx, "user.2", Message{Payload: []byte("b")}); err != nil {
		t.Fatalf("Publish 2: %v", err)
	}

	select {
	case m := <-sub.Messages():
		if string(m.Payload) != "a" {
			t.Fatalf("expected a, got %s", m.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestLocalDropOldest(t *testing.T) {
	l := NewLocal(BackendConfig{QueueCap: 2, DropPolicy: "drop-oldest"})
	defer l.Close()

	ctx := context.Background()
	// Subscriber that never drains
	sub, _ := l.Subscribe(ctx, "blocked")
	defer sub.Close()

	for i := 0; i < 10; i++ {
		_ = l.Publish(ctx, "blocked", Message{Payload: []byte("x")})
	}
	// Even after publishes, the Publish calls returned (no deadlock).
	// Drain what's left; should be ≤ QueueCap.
	drained := 0
loop:
	for {
		select {
		case <-sub.Messages():
			drained++
		default:
			break loop
		}
	}
	if drained > 2 {
		t.Fatalf("expected ≤ 2 buffered, got %d", drained)
	}
}

func TestLocalConcurrent(t *testing.T) {
	l := NewLocal(BackendConfig{QueueCap: 512})
	defer l.Close()

	ctx := context.Background()
	const N = 4
	subs := make([]Subscription, N)
	for i := 0; i < N; i++ {
		s, err := l.Subscribe(ctx, "broadcast")
		if err != nil {
			t.Fatalf("Subscribe[%d]: %v", i, err)
		}
		subs[i] = s
	}
	defer func() {
		for _, s := range subs {
			s.Close()
		}
	}()

	const total = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(s Subscription) {
			defer wg.Done()
			seen := 0
			for seen < total {
				select {
				case _, ok := <-s.Messages():
					if !ok {
						return
					}
					seen++
				case <-time.After(5 * time.Second):
					t.Errorf("timeout after %d", seen)
					return
				}
			}
		}(subs[i])
	}
	for i := 0; i < total; i++ {
		_ = l.Publish(ctx, "broadcast", Message{Payload: []byte("p")})
	}
	wg.Wait()
}

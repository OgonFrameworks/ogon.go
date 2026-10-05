// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/presence_test — presence TTL + diff broadcast.

package live

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPresenceHeartbeatEmitsJoin(t *testing.T) {
	var mu sync.Mutex
	var joined, left []PresenceEntry
	p := NewPresence(PresenceConfig{
		TTL:        200 * time.Millisecond,
		SweepEvery: 50 * time.Millisecond,
		OnDiff: func(_ string, j, l []PresenceEntry) {
			mu.Lock()
			joined = append(joined, j...)
			left = append(left, l...)
			mu.Unlock()
		},
	})
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.SweepLoop(ctx)
	p.Heartbeat("room.1", "alice", "c1", nil)

	// Should see one join diff.
	deadline := time.After(500 * time.Millisecond)
	for {
		mu.Lock()
		j := len(joined)
		mu.Unlock()
		if j >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no join diff emitted")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Wait for TTL sweep to evict; should see one leave diff.
	deadline = time.After(800 * time.Millisecond)
	for {
		mu.Lock()
		l := len(left)
		mu.Unlock()
		if l >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no leave diff emitted after TTL")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPresenceVisibilityPredicate(t *testing.T) {
	p := NewPresence(PresenceConfig{
		TTL: time.Second,
		Visibility: func(viewer *Connection, e PresenceEntry) bool {
			// Hide user "admin" from non-admin viewers.
			if e.UserID == "admin" {
				return viewer != nil && viewer.User.ID == "admin"
			}
			return true
		},
	})
	defer p.Close()
	p.Heartbeat("room.1", "admin", "c1", nil)
	p.Heartbeat("room.1", "alice", "c2", nil)

	viewer := &Connection{User: User{ID: "alice"}}
	members := p.Members("room.1", viewer)
	for _, m := range members {
		if m.UserID == "admin" {
			t.Fatal("admin should be hidden from alice")
		}
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 visible member, got %d", len(members))
	}
}

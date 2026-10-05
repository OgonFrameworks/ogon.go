// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/channel_test — channel registry and join/leave hooks.

package live

import (
	"context"
	"errors"
	"testing"
)

func TestChannelSubscribeUnsubscribe(t *testing.T) {
	h := newTestHub(t)
	ctx := context.Background()
	ch := h.channels.GetOrCreate("room.1")
	c := newConnection("c1", User{ID: "u1"}, h)
	if err := ch.Subscribe(ctx, c); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if got := len(ch.Subscribers()); got != 1 {
		t.Fatalf("subs: want 1, got %d", got)
	}
	ch.Unsubscribe(ctx, c)
	if got := len(ch.Subscribers()); got != 0 {
		t.Fatalf("after unsubscribe: want 0, got %d", got)
	}
}

func TestChannelJoinHookDeny(t *testing.T) {
	h := newTestHub(t)
	ctx := context.Background()
	ch := h.channels.GetOrCreate("room.private")
	ch.SetJoinHook(func(_ context.Context, _ *Connection, _ string) error {
		return errors.New("denied")
	})
	c := newConnection("c1", User{ID: "u1"}, h)
	if err := ch.Subscribe(ctx, c); err == nil {
		t.Fatal("expected denial from join hook")
	}
}

func TestChannelRegistryGlob(t *testing.T) {
	r := NewChannelRegistry()
	_ = r.GetOrCreate("room.1")
	_ = r.GetOrCreate("room.2")
	_ = r.GetOrCreate("user.1")
	matches := r.MatchGlob("room.*")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
}

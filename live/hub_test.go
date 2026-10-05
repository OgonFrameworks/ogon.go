// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/hub_test — Hub-level smoke tests, race-detector clean.

package live

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/pubsub"
)

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	backend := pubsub.NewLocal(pubsub.BackendConfig{QueueCap: 256})
	cfg := HubConfig{
		WorkerPoolSize:  4,
		QueueCap:        64,
		DropPolicy:      DropOldest,
		MsgPerSec:       1024,
		SlowClientEvict: 5 * time.Second,
		MaxConns:        100,
		MaxConnsPerUser: 5,
		ResumeWindowCap: 64,
		ResumeTTL:       10 * time.Second,
	}
	h := NewHub(cfg, backend, nil, nil)
	t.Cleanup(func() {
		_ = h.Close(context.Background())
	})
	return h
}

func TestHubRegisterUnregister(t *testing.T) {
	h := newTestHub(t)
	c1 := newConnection("c1", User{ID: "u1"}, h)
	if err := h.Register(c1); err != nil {
		t.Fatalf("Register c1: %v", err)
	}
	if got := h.Conns(); got != 1 {
		t.Fatalf("Conns: want 1, got %d", got)
	}
	c2 := newConnection("c2", User{ID: "u1"}, h)
	if err := h.Register(c2); err != nil {
		t.Fatalf("Register c2: %v", err)
	}
	if got := h.Conns(); got != 2 {
		t.Fatalf("Conns: want 2, got %d", got)
	}
	h.Unregister(c1)
	if got := h.Conns(); got != 1 {
		t.Fatalf("Conns after unregister: want 1, got %d", got)
	}
}

func TestHubMaxConnsPerUser(t *testing.T) {
	h := newTestHub(t)
	for i := 0; i < h.cfg.MaxConnsPerUser; i++ {
		c := newConnection("c"+itoa(i), User{ID: "u1"}, h)
		if err := h.Register(c); err != nil {
			t.Fatalf("Register %d: %v", i, err)
		}
	}
	c := newConnection("cX", User{ID: "u1"}, h)
	if err := h.Register(c); err == nil {
		t.Fatal("expected per-user cap to reject, got nil")
	}
}

func TestHubBroadcast(t *testing.T) {
	h := newTestHub(t)
	ctx := context.Background()
	alice := newConnection("a", User{ID: "alice"}, h)
	bob := newConnection("b", User{ID: "bob"}, h)
	_ = h.Register(alice)
	_ = h.Register(bob)

	if err := alice.Subscribe(ctx, "room.1"); err != nil {
		t.Fatalf("alice Subscribe: %v", err)
	}
	if err := bob.Subscribe(ctx, "room.1"); err != nil {
		t.Fatalf("bob Subscribe: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"text": "hi"})
	env := &Envelope{Type: TypeMessage, Topic: "room.1", Payload: payload}
	n := h.BroadcastToChannel(ctx, "room.1", env)
	if n != 2 {
		t.Fatalf("BroadcastToChannel: want 2 queued, got %d", n)
	}

	// Each conn should receive the message within a second.
	waitForMsg(t, alice, "hi")
	waitForMsg(t, bob, "hi")
}

func TestHubSendToUser(t *testing.T) {
	h := newTestHub(t)
	ctx := context.Background()
	c1 := newConnection("c1", User{ID: "alice"}, h)
	c2 := newConnection("c2", User{ID: "alice"}, h)
	_ = h.Register(c1)
	_ = h.Register(c2)
	env := &Envelope{Type: TypeMessage, Payload: []byte(`"dm"`)}
	if n := h.SendToUser(ctx, "alice", env); n != 2 {
		t.Fatalf("SendToUser: want 2 queued, got %d", n)
	}
	waitForMsg(t, c1, "dm")
	waitForMsg(t, c2, "dm")
}

func TestHubSendToGroup(t *testing.T) {
	h := newTestHub(t)
	ctx := context.Background()
	c1 := newConnection("c1", User{ID: "a", Groups: []string{"ops"}}, h)
	c2 := newConnection("c2", User{ID: "b", Groups: []string{"ops", "dev"}}, h)
	c3 := newConnection("c3", User{ID: "c", Groups: []string{"dev"}}, h)
	_ = h.Register(c1)
	_ = h.Register(c2)
	_ = h.Register(c3)
	env := &Envelope{Type: TypeMessage, Payload: []byte(`"group"`)}
	if n := h.SendToGroup(ctx, "ops", env); n != 2 {
		t.Fatalf("SendToGroup ops: want 2 queued, got %d", n)
	}
	waitForMsg(t, c1, "group")
	waitForMsg(t, c2, "group")
}

// itoa is a stdlib-less int→string for IDs (avoids fmt in tests on hot path).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// waitForMsg drains the conn's outbound queue looking for a message
// whose payload contains want.
func waitForMsg(t *testing.T, c *Connection, want string) {
	t.Helper()
	for {
		select {
		case frame, ok := <-c.outbound.Out():
			if !ok {
				t.Fatalf("conn %s queue closed before %q", c.ID, want)
			}
			env, err := DecodeEnvelope(frame)
			if err != nil {
				continue
			}
			if contains(string(env.Payload), want) {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for %q on conn %s", want, c.ID)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

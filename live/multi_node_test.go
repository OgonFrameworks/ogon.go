// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/multi_node_test — multi-node test harness STUB (LIVE-043).
//
// The real harness runs N ogon processes against a shared Redis
// backplane, broadcasts from node 1, and asserts receipt on node N.
// It requires a live Redis instance which is not available in the
// sandbox; this test stub is skipped with a documented reason.
//
// To enable: set OGON_LIVE_REDIS_ADDR and run `go test -tags=liveintegration`.

//go:build liveintegration

package live

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/pubsub"
)

func TestMultiNodePubSub(t *testing.T) {
	addr := os.Getenv("OGON_LIVE_REDIS_ADDR")
	if addr == "" {
		t.Skip("set OGON_LIVE_REDIS_ADDR to run multi-node test (LIVE-043)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sink, err := pubsub.NewRedis(ctx, pubsub.RedisConfig{Addr: addr})
	if err != nil {
		t.Skipf("redis unavailable: %v (LIVE-043 stub)", err)
	}
	defer sink.Close()

	sub, err := sink.Subscribe(ctx, "room.1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	if err := sink.Publish(ctx, "room.1", pubsub.Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case m := <-sub.Messages():
		if string(m.Payload) != "hello" {
			t.Fatalf("got %s", m.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

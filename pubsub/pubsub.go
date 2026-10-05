// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// pubsub — abstract Pub/Sub interface used by the live subsystem.
//
// Two backends are shipped:
//   - Local:  in-process, sharded sync.Map of subscriber channels
//   - Redis:  sharded pubsub backplane for multi-node (LIVE-009/010)
//
// The interface is transport-agnostic so live/* code never needs to know
// whether it is talking to a single node or a Redis cluster. Subscribers
// receive Messages on a bounded channel; the backend is responsible for
// drop-oldest backpressure (LIVE-012) so a slow subscriber cannot stall
// the publisher goroutine.

package pubsub

import "context"

// Message is the unit of pubsub delivery. Payload is opaque bytes; the
// live codec (live/protocol.go) is responsible for framing JSON inside it.
// Headers carry optional routing metadata (e.g. originating node, trace id).
type Message struct {
	Topic   string
	Payload []byte
	Headers map[string]string
}

// Subscription is a handle returned by Subscribe. Messages MUST be drained
// by the caller; failing to do so triggers drop-oldest inside the backend.
// Close unsubscribes and releases the underlying channel.
type Subscription interface {
	// Messages returns a receive-only channel of Messages. The channel is
	// closed when Close is called or the backend shuts down.
	Messages() <-chan Message
	// Close releases the subscription. Safe to call multiple times.
	Close() error
}

// Backend is the abstraction live/* depends on.
//
// Invariants:
//   - Publish must not block on slow subscribers (bounded queue, drop-oldest).
//   - Subscribe matches topic patterns. Two match modes are supported:
//     exact ("room.42") and glob ("room.*"). Backends MUST implement at
//     least exact match; glob support is required by the live channel
//     registry (LIVE-002).
//   - Close is idempotent and safe for concurrent callers.
type Backend interface {
	// Publish delivers msg to all subscribers matching msg.Topic.
	Publish(ctx context.Context, topic string, msg Message) error
	// Subscribe registers interest in the supplied patterns (exact or glob).
	// Returns a Subscription whose Messages channel is bounded.
	Subscribe(ctx context.Context, patterns ...string) (Subscription, error)
	// Close shuts the backend down, draining in-flight publishes.
	Close() error
}

// BackendConfig configures construction of a Backend.
type BackendConfig struct {
	// QueueCap is the bound on each subscriber's Messages channel.
	// Default 256. Larger values trade memory for tolerance to consumer
	// latency spikes.
	QueueCap int
	// DropPolicy applies when the queue is full:
	//   "drop-oldest" (default) — dequeue the oldest message, then enqueue
	//   "drop-new"             — drop the incoming message
	//   "block"                — block publisher until space (use sparingly)
	DropPolicy string
}

// withDefaults returns cfg with zero values replaced by safe defaults.
func (c BackendConfig) withDefaults() BackendConfig {
	out := c
	if out.QueueCap <= 0 {
		out.QueueCap = 256
	}
	if out.DropPolicy == "" {
		out.DropPolicy = "drop-oldest"
	}
	return out
}

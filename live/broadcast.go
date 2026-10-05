// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/broadcast — broadcast-to-channel, direct user send, user-group
// send (LIVE-006/007/008).
//
// All three primitives are thin wrappers around the channel registry
// and the fanout worker pool. They share:
//
//   - a span for observability (LIVE-026)
//   - the per-channel LoadShedder (LIVE-047)
//   - the per-channel ResumeWindow (LIVE-015) — broadcasts append to
//     the window so reconnecting clients can replay.
//   - per-conn rate limiting (LIVE-042) and queue backpressure (LIVE-012).

package live

import (
	"context"
	"encoding/json"
	"time"
)

// BroadcastToChannel sends an envelope to every subscriber of channel.
// Returns the number of conns the message was queued to.
func (h *Hub) BroadcastToChannel(ctx context.Context, channel string, env *Envelope) int {
	span := h.spans.StartSpan("live.broadcast")
	span.SetAttr("channel", channel)
	defer span.End()

	ch := h.channels.GetOrCreate(channel)
	if !ch.Shedder().Acquire() {
		h.metrics.MsgShed()
		return 0
	}
	defer ch.Shedder().Release()

	// Append to resume window; assign cursor.
	if env.Cursor == 0 {
		env.Cursor = ch.Resume().Append(env.Payload)
	}

	frame, err := env.Encode()
	if err != nil {
		return 0
	}

	subs := ch.Subscribers()
	queued := 0
	start := time.Now()
	for _, c := range subs {
		h.enqueueFanout(fanoutJob{conn: c, frame: frame})
		queued++
	}
	h.metrics.ObserveDelivery(time.Since(start))
	return queued
}

// SendToUser sends an envelope to all connections belonging to userID.
// Returns the number of conns queued.
func (h *Hub) SendToUser(ctx context.Context, userID string, env *Envelope) int {
	span := h.spans.StartSpan("live.user.send")
	span.SetAttr("user", userID)
	defer span.End()

	frame, err := env.Encode()
	if err != nil {
		return 0
	}
	h.mu.RLock()
	conns := make([]*Connection, 0, 4)
	for _, c := range h.conns {
		if c.User.ID == userID {
			conns = append(conns, c)
		}
	}
	h.mu.RUnlock()
	queued := 0
	for _, c := range conns {
		h.enqueueFanout(fanoutJob{conn: c, frame: frame})
		queued++
	}
	return queued
}

// SendToGroup sends an envelope to all connections of users in a group.
// Returns the number of conns queued.
func (h *Hub) SendToGroup(ctx context.Context, group string, env *Envelope) int {
	span := h.spans.StartSpan("live.group.send")
	span.SetAttr("group", group)
	defer span.End()

	frame, err := env.Encode()
	if err != nil {
		return 0
	}
	h.mu.RLock()
	conns := make([]*Connection, 0, 8)
	for _, c := range h.conns {
		for _, g := range c.User.Groups {
			if g == group {
				conns = append(conns, c)
				break
			}
		}
	}
	h.mu.RUnlock()
	queued := 0
	for _, c := range conns {
		h.enqueueFanout(fanoutJob{conn: c, frame: frame})
		queued++
	}
	return queued
}

// encodePresencePayload renders a presence diff entry as JSON.
func encodePresencePayload(kind string, e PresenceEntry) (json.RawMessage, error) {
	doc := map[string]any{
		"kind":     kind,
		"user_id":  e.UserID,
		"conn_id":  e.ConnID,
		"metadata": e.Metadata,
	}
	return json.Marshal(doc)
}

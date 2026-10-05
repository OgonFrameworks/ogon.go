// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/channel — topic registry with glob subscribe (LIVE-002) and
// join/leave authorization hooks (LIVE-003).
//
// A Channel is a named topic (e.g. "room.42"). Subscriptions are
// matched against glob patterns ("room.*"). Each channel owns:
//
//   - its subscriber set (sync.Map of *Connection)
//   - its ResumeWindow (LIVE-015)
//   - its LoadShedder (LIVE-047)
//   - optional Join/Leave hooks (LIVE-003)
//
// The ChannelRegistry holds all live channels; lookup is O(1) by
// exact name. Glob subscribe is implemented by iterating the registry
// — bounded by channel count, not message rate.

package live

import (
	"context"
	"errors"
	"path"
	"sync"
	"sync/atomic"
)

// JoinHook is invoked when a connection subscribes to a channel.
// Returning a non-nil error denies the join; the connection receives
// an "error" envelope explaining why (LIVE-003).
type JoinHook func(ctx context.Context, conn *Connection, channel string) error

// LeaveHook is invoked when a connection unsubscribes or is evicted.
// Errors are logged but do not prevent the leave.
type LeaveHook func(ctx context.Context, conn *Connection, channel string)

// Channel is the per-topic coordination struct.
type Channel struct {
	name string

	mu   sync.RWMutex
	subs map[string]*Connection // connID -> *Connection

	resume  *ResumeWindow
	shedder *LoadShedder

	joinHook  JoinHook
	leaveHook LeaveHook

	closed atomic.Bool
}

// NewChannel constructs a Channel with sensible defaults.
func NewChannel(name string) *Channel {
	return &Channel{
		name:    name,
		subs:    make(map[string]*Connection),
		resume:  NewResumeWindow(128, 30*1e9), // 128 events, 30s
		shedder: NewLoadShedder(name, 1024),
	}
}

// Name returns the channel's topic name.
func (c *Channel) Name() string { return c.name }

// SetJoinHook installs the join authorization hook.
func (c *Channel) SetJoinHook(h JoinHook) { c.joinHook = h }

// SetLeaveHook installs the leave hook.
func (c *Channel) SetLeaveHook(h LeaveHook) { c.leaveHook = h }

// Subscribe adds a connection as a subscriber. The join hook (if any)
// is invoked first; denial returns the hook's error.
func (c *Channel) Subscribe(ctx context.Context, conn *Connection) error {
	if c.closed.Load() {
		return errors.New("ogon/live: channel closed")
	}
	if c.joinHook != nil {
		if err := c.joinHook(ctx, conn, c.name); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.subs[conn.ID] = conn
	c.mu.Unlock()
	return nil
}

// Unsubscribe removes a connection.
func (c *Channel) Unsubscribe(ctx context.Context, conn *Connection) {
	c.mu.Lock()
	delete(c.subs, conn.ID)
	c.mu.Unlock()
	if c.leaveHook != nil {
		c.leaveHook(ctx, conn, c.name)
	}
}

// Subscribers returns a snapshot of current subscribers (best-effort).
func (c *Channel) Subscribers() []*Connection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Connection, 0, len(c.subs))
	for _, c := range c.subs {
		out = append(out, c)
	}
	return out
}

// Resume returns the channel's resume window.
func (c *Channel) Resume() *ResumeWindow { return c.resume }

// Shedder returns the channel's load shedder.
func (c *Channel) Shedder() *LoadShedder { return c.shedder }

// ChannelRegistry holds all live channels keyed by name.
type ChannelRegistry struct {
	mu sync.RWMutex
	m  map[string]*Channel
}

// NewChannelRegistry constructs an empty registry.
func NewChannelRegistry() *ChannelRegistry {
	return &ChannelRegistry{m: make(map[string]*Channel)}
}

// GetOrCreate returns the channel by name, creating it if absent.
func (r *ChannelRegistry) GetOrCreate(name string) *Channel {
	r.mu.RLock()
	c, ok := r.m[name]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok = r.m[name]; ok {
		return c
	}
	c = NewChannel(name)
	r.m[name] = c
	return c
}

// Get returns the channel by name, or nil if absent.
func (r *ChannelRegistry) Get(name string) *Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m[name]
}

// MatchGlob returns all channels whose name matches pattern (path.Match).
func (r *ChannelRegistry) MatchGlob(pattern string) []*Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Channel
	for name, c := range r.m {
		if ok, _ := path.Match(pattern, name); ok {
			out = append(out, c)
		}
	}
	return out
}

// All returns a snapshot of all channels.
func (r *ChannelRegistry) All() []*Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Channel, 0, len(r.m))
	for _, c := range r.m {
		out = append(out, c)
	}
	return out
}

// Close shuts down all channels (best-effort).
func (r *ChannelRegistry) Close(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.m {
		c.closed.Store(true)
	}
}

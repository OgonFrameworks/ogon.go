// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/presence — presence with TTL heartbeats, diff broadcast,
// privacy rules (LIVE-004/005/048).
//
// Presence tracks who is "in" each channel: which user, with what
// metadata (role, custom fields), and when their last heartbeat was.
// A periodic sweeper evicts members whose TTL has elapsed; the eviction
// fans out a "presence" diff envelope to remaining subscribers so every
// client can update its member list.
//
// Privacy rules (LIVE-048):
//   - VisibilityPredicate: per-channel callback decides whether a
//     connection may see a given user's presence. Use this to hide
//     moderators from anonymous users, etc.
//   - MetadataRedactor: per-user callback strips sensitive fields
//     before the metadata is broadcast.

package live

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// PresenceEntry is a member's presence record.
type PresenceEntry struct {
	UserID   string
	ConnID   string
	Metadata map[string]any
	LastSeen time.Time
}

// VisibilityPredicate decides if viewer may see entry.
type VisibilityPredicate func(viewer *Connection, entry PresenceEntry) bool

// MetadataRedactor returns a sanitized copy of metadata for viewer.
type MetadataRedactor func(viewer *Connection, metadata map[string]any) map[string]any

// Presence tracks members per channel.
type Presence struct {
	mu      sync.Mutex
	members map[string]map[string]PresenceEntry // channel -> userID -> entry

	ttl        time.Duration
	sweepEvery time.Duration
	closed     atomic.Bool

	stop chan struct{}

	visibility VisibilityPredicate
	redactor   MetadataRedactor

	// diff broadcaster: called with (channel, joined, left) so the
	// hub can fan out the "presence" envelope. The function is invoked
	// under no lock — the callback MUST be non-blocking.
	onDiff func(channel string, joined, left []PresenceEntry)
}

// PresenceConfig configures presence construction.
type PresenceConfig struct {
	TTL        time.Duration
	SweepEvery time.Duration
	Visibility VisibilityPredicate
	Redactor   MetadataRedactor
	OnDiff     func(channel string, joined, left []PresenceEntry)
}

// NewPresence constructs a Presence tracker. Sweeping goroutine must
// be spawned by the hub via Supervisor — the constructor returns the
// struct only.
func NewPresence(cfg PresenceConfig) *Presence {
	if cfg.TTL <= 0 {
		cfg.TTL = 30 * time.Second
	}
	if cfg.SweepEvery <= 0 {
		cfg.SweepEvery = 5 * time.Second
	}
	p := &Presence{
		members:    make(map[string]map[string]PresenceEntry),
		ttl:        cfg.TTL,
		sweepEvery: cfg.SweepEvery,
		visibility: cfg.Visibility,
		redactor:   cfg.Redactor,
		onDiff:     cfg.OnDiff,
		stop:       make(chan struct{}),
	}
	return p
}

// Heartbeat refreshes a member's lastSeen timestamp. If the member is
// new, a "joined" diff is emitted (after the function returns).
func (p *Presence) Heartbeat(channel, userID, connID string, meta map[string]any) {
	p.mu.Lock()
	m, ok := p.members[channel]
	if !ok {
		m = make(map[string]PresenceEntry)
		p.members[channel] = m
	}
	_, was := m[userID]
	m[userID] = PresenceEntry{
		UserID:   userID,
		ConnID:   connID,
		Metadata: meta,
		LastSeen: time.Now(),
	}
	p.mu.Unlock()
	if !was && p.onDiff != nil {
		// emit join diff
		p.onDiff(channel, []PresenceEntry{m[userID]}, nil)
	}
}

// Leave removes a member and emits a "left" diff.
func (p *Presence) Leave(channel, userID string) {
	p.mu.Lock()
	m, ok := p.members[channel]
	if !ok {
		p.mu.Unlock()
		return
	}
	entry, ok := m[userID]
	if !ok {
		p.mu.Unlock()
		return
	}
	delete(m, userID)
	p.mu.Unlock()
	if p.onDiff != nil {
		p.onDiff(channel, nil, []PresenceEntry{entry})
	}
}

// Members returns the current presence for a channel, filtered and
// redacted for the viewer.
func (p *Presence) Members(channel string, viewer *Connection) []PresenceEntry {
	p.mu.Lock()
	m, ok := p.members[channel]
	if !ok {
		p.mu.Unlock()
		return nil
	}
	snapshot := make([]PresenceEntry, 0, len(m))
	for _, e := range m {
		snapshot = append(snapshot, e)
	}
	p.mu.Unlock()

	out := snapshot[:0]
	for _, e := range snapshot {
		if p.visibility != nil && !p.visibility(viewer, e) {
			continue
		}
		if p.redactor != nil {
			e.Metadata = p.redactor(viewer, e.Metadata)
		}
		out = append(out, e)
	}
	return out
}

// SweepLoop is the sweeper goroutine. MUST be spawned via the runtime
// supervisor; ctx cancellation exits the loop.
func (p *Presence) SweepLoop(ctx context.Context) error {
	ticker := time.NewTicker(p.sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.stop:
			return nil
		case <-ticker.C:
			p.sweep()
		}
	}
}

func (p *Presence) sweep() {
	now := time.Now()
	type diff struct {
		channel string
		left    []PresenceEntry
	}
	var diffs []diff
	p.mu.Lock()
	for channel, m := range p.members {
		for id, e := range m {
			if now.Sub(e.LastSeen) > p.ttl {
				delete(m, id)
				diffs = append(diffs, diff{channel: channel, left: []PresenceEntry{e}})
			}
		}
		if len(m) == 0 {
			delete(p.members, channel)
		}
	}
	p.mu.Unlock()
	if p.onDiff != nil {
		for _, d := range diffs {
			p.onDiff(d.channel, nil, d.left)
		}
	}
}

// Close stops the sweep loop.
func (p *Presence) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	close(p.stop)
}

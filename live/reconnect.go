// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/reconnect — cursor-based resume window (LIVE-014/015).
//
// Each channel assigns monotonically increasing cursors to published
// events. A ring buffer of the last N events (TTL-evicted) is kept so
// a reconnecting client can replay missed events by sending its last
// seen cursor in the "resume" envelope.
//
// Invariants:
//   - Cursors are per-channel, never global.
//   - The resume window is bounded; old events age out by TTL or cap.
//   - Replay is best-effort: if the cursor is older than the oldest
//     event in the window, the server sends a "resume_expired" error
//     envelope and the client must do a full re-sync.

package live

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrResumeExpired is returned when a client requests replay from a
// cursor older than the oldest event in the resume window. The client
// must do a full re-sync (LIVE-015).
var ErrResumeExpired = errors.New("ogon/live: resume cursor expired")

// ResumeWindow holds recent events per channel for cursor-based resume.
type ResumeWindow struct {
	cap    int
	ttl    time.Duration
	mu     sync.Mutex
	events []resumeEntry
	next   uint64 // monotonic cursor counter
}

type resumeEntry struct {
	cursor  uint64
	expires time.Time
	payload []byte
}

// NewResumeWindow constructs a resume window.
func NewResumeWindow(capacity int, ttl time.Duration) *ResumeWindow {
	if capacity <= 0 {
		capacity = 128
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &ResumeWindow{cap: capacity, ttl: ttl}
}

// Append stores a payload, returns the assigned cursor.
func (w *ResumeWindow) Append(payload []byte) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	c := w.next
	w.events = append(w.events, resumeEntry{
		cursor:  c,
		expires: time.Now().Add(w.ttl),
		payload: append([]byte(nil), payload...),
	})
	// Bound the ring. Copy the live tail into a fresh slice so the
	// leading slots are released to the GC (BUG-0019: previously
	// `w.events = w.events[len-w.cap:]` shifted the slice header
	// forward but kept the backing array — over a long-running
	// channel the slice header moved deep into a non-shrinking
	// backing array).
	if len(w.events) > w.cap {
		live := w.events[len(w.events)-w.cap:]
		fresh := make([]resumeEntry, len(live))
		copy(fresh, live)
		w.events = fresh
	}
	return c
}

// Replay returns all events with cursor > after, evicting expired ones.
// Returns ErrResumeExpired if `after` is older than the oldest surviving
// event in the window (meaning the client missed events we no longer have).
func (w *ResumeWindow) Replay(after uint64) ([][]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	out := w.events[:0]
	for _, e := range w.events {
		if e.expires.After(now) {
			out = append(out, e)
		}
	}
	w.events = out
	if len(w.events) == 0 {
		if after == 0 {
			return nil, nil
		}
		return nil, ErrResumeExpired
	}
	oldest := w.events[0].cursor
	if after != 0 && after < oldest-1 {
		return nil, ErrResumeExpired
	}
	var res [][]byte
	for _, e := range w.events {
		if e.cursor > after {
			res = append(res, e.payload)
		}
	}
	return res, nil
}

// LastCursor returns the highest assigned cursor (0 if none).
func (w *ResumeWindow) LastCursor() uint64 { return atomic.LoadUint64(&w.next) }

// Close releases all events.
func (w *ResumeWindow) Close(_ context.Context) {
	w.mu.Lock()
	w.events = nil
	w.mu.Unlock()
}

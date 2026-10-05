// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon logs` dev tail. Implements OBS-025.
//
// In dev mode, `ogon logs` tails the in-process ring buffer of slog
// records. The ring is populated by a side-channel slog.Handler wired
// in by InitLogTailHook. The CLI calls Drain() and prints in real-time.
//
// In prod, the CLI asks the OS log driver (journalctl / kubectl logs)
// rather than this in-process ring — the ring is dev-only to avoid
// capturing prod PII in memory. (OBS-043)

package obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// LogTailConfig configures the in-process ring buffer.
type LogTailConfig struct {
	// Capacity is the number of records to retain. Default 512.
	Capacity int
	// Level is the minimum level captured (Debug by default).
	Level slog.Level
}

// logRecord is the in-memory form of a captured slog record. We capture
// the rendered message + attrs to avoid retaining references to live
// objects (and to keep the ring allocation cheap). (OBS-044 budget)
type logRecord struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   map[string]string
}

// LogTailRing is the in-process log ring used by `ogon logs`. (OBS-025)
type LogTailRing struct {
	mu          sync.Mutex
	buf         []logRecord
	cap         int
	overflow    int64
	subscribers map[int]chan logRecord
	nextSubID   int
}

var (
	logTailOnce sync.Once
	logTailRing *LogTailRing
)

// InitLogTailHook installs the ring + a slog.Handler that mirrors records
// into it. Call once at framework boot in dev mode. (OBS-025)
//
// The returned *slog.Logger forwards to both the supplied inner logger
// AND the ring; existing code that uses NewLogger can be wrapped with
// this hook without changing its call sites.
func InitLogTailHook(inner *slog.Logger, cfg LogTailConfig) *slog.Logger {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 512
	}
	ring := GetLogTailRing()
	ring.mu.Lock()
	ring.cap = cfg.Capacity
	ring.buf = nil
	ring.mu.Unlock()
	h := &tailHandler{inner: inner.Handler(), ring: ring, level: cfg.Level}
	return slog.New(h)
}

// GetLogTailRing returns the package-level ring (lazily allocated).
func GetLogTailRing() *LogTailRing {
	logTailOnce.Do(func() {
		logTailRing = &LogTailRing{
			cap:         512,
			subscribers: make(map[int]chan logRecord),
		}
	})
	return logTailRing
}

// Append adds a record to the ring + fans out to subscribers. (OBS-025)
func (r *LogTailRing) Append(rec logRecord) {
	r.mu.Lock()
	if len(r.buf) >= r.cap {
		r.buf = r.buf[1:]
		r.overflow++
	}
	r.buf = append(r.buf, rec)
	subs := make([]chan logRecord, 0, len(r.subscribers))
	for _, ch := range r.subscribers {
		subs = append(subs, ch)
	}
	r.mu.Unlock()
	for _, ch := range subs {
		// Non-blocking: subscribers that can't keep up get dropped. (OBS-044)
		select {
		case ch <- rec:
		default:
		}
	}
}

// Drain returns a snapshot of the ring's contents (oldest first).
func (r *LogTailRing) Drain() []logRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]logRecord, len(r.buf))
	copy(out, r.buf)
	return out
}

// Subscribe returns a channel that receives new records as they arrive.
// The returned cancel func unregisters the subscription. (OBS-025)
func (r *LogTailRing) Subscribe() (<-chan logRecord, func()) {
	r.mu.Lock()
	id := r.nextSubID
	r.nextSubID++
	ch := make(chan logRecord, 64)
	r.subscribers[id] = ch
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subscribers, id)
		r.mu.Unlock()
		close(ch)
	}
}

// OverflowCount returns the number of records dropped due to ring full.
func (r *LogTailRing) OverflowCount() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.overflow
}

// tailHandler is a slog.Handler that mirrors records into the ring + the
// inner handler. The ring copy is non-blocking so the hot path never
// waits on the dev-only buffer. (OBS-044)
type tailHandler struct {
	inner slog.Handler
	ring  *LogTailRing
	level slog.Level
}

func (h *tailHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *tailHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &tailHandler{inner: h.inner.WithAttrs(attrs), ring: h.ring, level: h.level}
}

func (h *tailHandler) WithGroup(name string) slog.Handler {
	return &tailHandler{inner: h.inner.WithGroup(name), ring: h.ring, level: h.level}
}

func (h *tailHandler) Handle(ctx context.Context, rec slog.Record) error {
	// Capture into the ring before forwarding to inner so the order is
	// preserved when the inner handler does async work (e.g. OTLP batch).
	if rec.Level >= h.level {
		recAttrs := make(map[string]string, rec.NumAttrs())
		rec.Attrs(func(a slog.Attr) bool {
			recAttrs[a.Key] = a.Value.String()
			return true
		})
		h.ring.Append(logRecord{
			Time:    rec.Time,
			Level:   rec.Level,
			Message: rec.Message,
			Attrs:   recAttrs,
		})
	}
	return h.inner.Handle(ctx, rec)
}

// TailLogs writes the ring's contents to w (oldest first). When follow
// is true, blocks on ctx and writes new records as they arrive. (OBS-025)
func TailLogs(ctx context.Context, w io.Writer, follow bool) error {
	ring := GetLogTailRing()
	for _, r := range ring.Drain() {
		writeRecord(w, r)
	}
	if !follow {
		return nil
	}
	ch, cancel := ring.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r, ok := <-ch:
			if !ok {
				return nil
			}
			writeRecord(w, r)
		}
	}
}

func writeRecord(w io.Writer, r logRecord) {
	_, _ = fmt.Fprintf(w, "%s %s %s", r.Time.Format(time.RFC3339), r.Level.String(), r.Message)
	for k, v := range r.Attrs {
		_, _ = fmt.Fprintf(w, " %s=%s", k, v)
	}
	_, _ = fmt.Fprintln(w)
}

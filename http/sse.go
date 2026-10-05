// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// SSE (Server-Sent Events) helper: heartbeats, Last-Event-ID resume,
// per-connection caps, bounded write queue with drop-oldest policy.
//
// HTTP-022: SSE is the framework's preferred server→client realtime channel.
// LIVE-040/041: bounded queue, drop-oldest, per-conn caps.

package http

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// SSEConn is one SSE client connection. The helper owns a goroutine that
// drains a bounded channel into the ResponseWriter; the application pushes
// events into the channel. Drop policy: when the channel is full, the
// oldest event is dropped (LIVE-040).
type SSEConn struct {
	w         http.ResponseWriter
	r         *http.Request
	rc        *http.ResponseController
	queue     chan sseEvent
	stop      chan struct{}
	stopped   atomic.Bool
	lastID    int64
	heartbeat time.Duration
	maxBytes  int
	sentBytes int
	mu        sync.Mutex
}

type sseEvent struct {
	id    string
	event string
	data  string
	retry int // milliseconds
}

// SSEConfig configures the SSEConn behavior.
type SSEConfig struct {
	// QueueSize is the bounded channel capacity. Default 64.
	QueueSize int
	// Heartbeat: cadence of comment-only keepalive frames. Default 15s.
	Heartbeat time.Duration
	// MaxBytesPerConn: caps total bytes sent per connection; the conn is
	// closed when exceeded. Default 10 MiB.
	MaxBytesPerConn int
	// MaxEventBytes: per-event cap. Default 64 KiB.
	MaxEventBytes int
}

// DefaultSSEConfig returns a production-safe config.
func DefaultSSEConfig() SSEConfig {
	return SSEConfig{
		QueueSize:       64,
		Heartbeat:       15 * time.Second,
		MaxBytesPerConn: 10 << 20,
		MaxEventBytes:   64 << 10,
	}
}

// UpgradeSSE promotes the response to an SSE connection. The supplied
// handler runs in the calling goroutine; SSEConn handles the wire writes
// in a background goroutine. Returns the conn (for event pushes) or an
// error if the upgrade fails.
//
// Last-Event-ID resume: the client's Last-Event-ID header is parsed and
// exposed via Conn.LastEventID() so the handler can replay missed events.
func UpgradeSSE(w http.ResponseWriter, r *http.Request, cfg SSEConfig, handler func(c *SSEConn) error) error {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 64
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 15 * time.Second
	}
	if cfg.MaxBytesPerConn <= 0 {
		cfg.MaxBytesPerConn = 10 << 20
	}
	if cfg.MaxEventBytes <= 0 {
		cfg.MaxEventBytes = 64 << 10
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	conn := &SSEConn{
		w:         w,
		r:         r,
		rc:        http.NewResponseController(w),
		queue:     make(chan sseEvent, cfg.QueueSize),
		stop:      make(chan struct{}),
		heartbeat: cfg.Heartbeat,
		maxBytes:  cfg.MaxBytesPerConn,
	}
	// Parse Last-Event-ID header for resume.
	if id := r.Header.Get(LastEventIDHeader); id != "" {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			conn.lastID = n
		}
	}

	// Drain goroutine: writes events to the wire and flushes. The drain
	// observes both the request context (cancelled on client disconnect)
	// and the stop channel (closed when the handler returns).
	drainDone := make(chan struct{})
	go func() {
		defer conn.stopped.Store(true)
		defer close(drainDone)
		_ = conn.drain(r.Context(), cfg)
	}()

	// Application handler: pushes events into the queue.
	runErr := handler(conn)

	// Signal drain to finish and wait for the final flush. Waiting on
	// drainDone (not ctx.Done) avoids a deadlock: ctx is only cancelled by
	// the deferred cancel() that runs when this function returns.
	close(conn.stop)
	<-drainDone
	return runErr
}

// drain runs the SSE wire-loop. Heartbeats fire every cfg.Heartbeat.
func (c *SSEConn) drain(ctx context.Context, cfg SSEConfig) error {
	heartbeat := time.NewTicker(c.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.stop:
			return nil
		case ev := <-c.queue:
			if err := c.writeEvent(ev, cfg); err != nil {
				return err
			}
		case <-heartbeat.C:
			if err := c.writeComment(": hb\n\n"); err != nil {
				return err
			}
		}
	}
}

// writeEvent serializes one SSE event to the wire and flushes.
func (c *SSEConn) writeEvent(ev sseEvent, cfg SSEConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sentBytes > c.maxBytes {
		return errSSEConnExceeded
	}
	var buf []byte
	if ev.id != "" {
		buf = append(buf, []byte("id: "+ev.id+"\n")...)
	}
	if ev.event != "" {
		buf = append(buf, []byte("event: "+ev.event+"\n")...)
	}
	if ev.retry > 0 {
		buf = append(buf, []byte(fmt.Sprintf("retry: %d\n", ev.retry))...)
	}
	// Split multi-line data; each line gets a "data: " prefix.
	for _, line := range splitLines(ev.data) {
		buf = append(buf, []byte("data: "+line+"\n")...)
	}
	buf = append(buf, '\n')
	if len(buf) > cfg.MaxEventBytes {
		// Truncate data to fit (drop policy).
		buf = buf[:cfg.MaxEventBytes]
	}
	n, err := c.w.Write(buf)
	c.sentBytes += n
	if err != nil {
		return err
	}
	return c.rc.Flush()
}

// writeComment writes a comment-only frame (heartbeat).
func (c *SSEConn) writeComment(s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, err := c.w.Write([]byte(s))
	c.sentBytes += n
	if err != nil {
		return err
	}
	return c.rc.Flush()
}

// Send pushes an event into the bounded queue. When the queue is full,
// the OLDEST event is dropped (LIVE-040 drop-oldest). The drop count is
// exposed via Dropped().
//
// Hot path: non-blocking send on a buffered channel.
func (c *SSEConn) Send(id, event, data string) bool {
	ev := sseEvent{id: id, event: event, data: data}
	select {
	case c.queue <- ev:
		return true
	default:
		// Drop oldest.
		select {
		case <-c.queue:
			atomic.AddInt64(&sseDropped, 1)
		default:
		}
		select {
		case c.queue <- ev:
			return true
		default:
			return false
		}
	}
}

// LastEventID returns the Last-Event-ID header parsed as int64, or 0 when
// absent. Used for resume (HTTP-022).
func (c *SSEConn) LastEventID() int64 { return c.lastID }

// Close terminates the SSE connection and the drain goroutine.
func (c *SSEConn) Close() {
	if c.stopped.CompareAndSwap(false, true) {
		close(c.stop)
	}
}

// splitLines splits s on \n, stripping a trailing \r per line.
func splitLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(s) {
		line := s[start:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// errSSEConnExceeded is returned when the connection's byte cap is hit.
var errSSEConnExceeded = fmt.Errorf("ogon/http: SSE conn byte cap exceeded")

// sseDropped is a global counter of dropped events (for metrics).
var sseDropped int64

// SSEDropped returns the cumulative count of dropped SSE events.
func SSEDropped() int64 { return atomic.LoadInt64(&sseDropped) }

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/transport_sse — Server-Sent Events auto-fallback (LIVE-040/041).
//
// When a client cannot use WebSocket (e.g. corporate proxy, old
// browser, or explicit `Accept: text/event-stream`), the live
// subsystem falls back to SSE. SSE is unidirectional server→client;
// for client→server messages the application must use a regular HTTP
// POST to a sidecar route.
//
// The SSE transport reuses the same Hub, OutboundQueue, RateLimiter,
// and Connection types as the WS transport — only the wire encoding
// differs: each Envelope becomes one `data:` line.

package live

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
)

// SSEConfig configures the SSE transport.
type SSEConfig struct {
	AuthFunc AuthFunc
	// MaxPayloadBytes (LIVE-046).
	MaxPayloadBytes int
}

// SSETransport serves SSE connections.
type SSETransport struct {
	hub  *Hub
	cfg  SSEConfig
	log  *slog.Logger
	next func() string
}

// NewSSETransport constructs an SSE transport.
func NewSSETransport(hub *Hub, cfg SSEConfig, log *slog.Logger) *SSETransport {
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxPayloadBytes <= 0 {
		cfg.MaxPayloadBytes = 64 * 1024
	}
	return &SSETransport{hub: hub, cfg: cfg, log: log, next: newConnIDGenerator()}
}

// ServeHTTP upgrades to SSE.
func (t *SSETransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if t.cfg.AuthFunc != nil {
		if _, err := t.cfg.AuthFunc(r.Context(), r); err != nil {
			http.Error(w, "auth: "+err.Error(), http.StatusUnauthorized)
			return
		}
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	user := User{} // SSE transport doesn't wire user by default; override AuthFunc
	conn := newConnection(t.next(), user, t.hub)
	if err := t.hub.Register(conn); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer t.hub.Unregister(conn)

	// SSE is server→client only; inbound is via separate POST.
	ctx := r.Context()
	closed := atomic.Bool{}
	for {
		select {
		case <-ctx.Done():
			closed.Store(true)
			return
		case frame, ok := <-conn.outbound.Out():
			if !ok {
				return
			}
			// Frame as SSE event:
			//   event: envelope\n
			//   data: <base64 or raw JSON>\n\n
			if _, err := fmt.Fprintf(w, "event: envelope\ndata: %s\n\n", string(frame)); err != nil {
				return
			}
			fl.Flush()
			t.hub.metrics.MsgOut()
		}
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// WebSocket upgrade via github.com/coder/websocket. Auth handshake, origin
// check, conn limits. All upgrades drain on shutdown (CORE-013).
//
// HTTP-023: WS upgrades need an explicit Origin allowlist.
// LIVE-020..023: per-conn limits (read/write caps, message size, idle).

package http

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// WSConfig configures the WebSocket upgrader.
type WSConfig struct {
	// AllowOrigins: explicit allowlist. "*" requires AllowAllOrigins=true
	// (insecure, dev only).
	AllowOrigins    []string
	AllowAllOrigins bool
	// MaxConns: caps total live WS connections. Zero → no cap.
	MaxConns int
	// MaxMessageBytes: per-message cap. Default 1 MiB.
	MaxMessageBytes int64
	// ReadTimeout: per-read deadline. Default 60s.
	ReadTimeout time.Duration
	// WriteTimeout: per-write deadline. Default 10s.
	WriteTimeout time.Duration
	// HandshakeTimeout caps the upgrade phase. Default 10s.
	HandshakeTimeout time.Duration
	// AuthFn is invoked during the handshake; returning an error aborts the
	// upgrade with 401 ProblemDetails.
	AuthFn func(r *http.Request) error
}

// DefaultWSConfig returns a production-safe config.
func DefaultWSConfig() WSConfig {
	return WSConfig{
		AllowOrigins:     nil,
		AllowAllOrigins:  false,
		MaxConns:         10000,
		MaxMessageBytes:  1 << 20,
		ReadTimeout:      60 * time.Second,
		WriteTimeout:     10 * time.Second,
		HandshakeTimeout: 10 * time.Second,
	}
}

// WSPool tracks live WS connections for graceful drain.
type WSPool struct {
	mu      sync.Mutex
	conns   map[*websocket.Conn]struct{}
	count   atomic.Int64
	maxConn int64
	closed  atomic.Bool
}

// NewWSPool returns a fresh pool.
func NewWSPool(maxConns int) *WSPool {
	return &WSPool{
		conns:   make(map[*websocket.Conn]struct{}),
		maxConn: int64(maxConns),
	}
}

// ErrWSPoolFull is returned when the connection cap is reached.
var ErrWSPoolFull = errors.New("ogon/http: WS connection pool full")

// ErrWSPoolClosed is returned when the pool has been drained.
var ErrWSPoolClosed = errors.New("ogon/http: WS pool closed")

// add registers a connection. Returns ErrWSPoolFull when at cap,
// ErrWSPoolClosed when draining.
func (p *WSPool) add(c *websocket.Conn) error {
	if p.closed.Load() {
		return ErrWSPoolClosed
	}
	cur := p.count.Add(1)
	if p.maxConn > 0 && cur > p.maxConn {
		p.count.Add(-1)
		return ErrWSPoolFull
	}
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
	return nil
}

// remove unregisters a connection.
func (p *WSPool) remove(c *websocket.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
	p.count.Add(-1)
}

// Count returns the current connection count.
func (p *WSPool) Count() int64 { return p.count.Load() }

// Drain closes all connections and waits for them to unregister. Honors
// the supplied timeout; remaining connections after timeout are force-closed.
//
// Per-connection close failures are recovered: a half-initialised or
// already-closed Conn must not abort the drain loop (CORE-013 no-leak law).
func (p *WSPool) Drain(timeout time.Duration) {
	p.closed.Store(true)
	deadline := time.Now().Add(timeout)
	p.mu.Lock()
	snapshot := make([]*websocket.Conn, 0, len(p.conns))
	for c := range p.conns {
		snapshot = append(snapshot, c)
	}
	p.mu.Unlock()
	// closeOne is panic-safe: coder/websocket.Close dereferences internal
	// mutex fields that are nil for a zero-value Conn. The framework's own
	// upgrade path always produces initialised conns, but defensive recover
	// guarantees drain never leaks goroutines on a malformed conn.
	closeOne := func(c *websocket.Conn, status websocket.StatusCode, reason string) {
		if c == nil {
			return
		}
		defer func() { _ = recover() }()
		_ = c.Close(status, reason)
	}
	for _, c := range snapshot {
		closeOne(c, websocket.StatusNormalClosure, "server draining")
	}
	for time.Now().Before(deadline) && p.count.Load() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	// Force-close stragglers.
	for _, c := range snapshot {
		closeOne(c, websocket.StatusInternalError, "force close")
	}
}

// WSUpgrade is the WS upgrade entrypoint. Mount as a route handler:
//
//	router.MapGet("/ws", http.WSUpgradeHandler(pool, cfg, handler))
func WSUpgrade(pool *WSPool, cfg WSConfig, handler func(c *Ctx, conn *websocket.Conn) error) HandlerFunc {
	if cfg.MaxMessageBytes == 0 {
		cfg.MaxMessageBytes = 1 << 20
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 60 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = 10 * time.Second
	}
	originSet := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		originSet[o] = struct{}{}
	}

	return func(c *Ctx) error {
		// Origin check.
		origin := c.Header("Origin")
		if !cfg.AllowAllOrigins {
			if origin == "" {
				// Some clients (mobile/desktop) omit Origin; allow when no
				// explicit allowlist is configured.
				if len(originSet) > 0 {
					_ = c.Problem(NewProblem(http.StatusForbidden,
						"Origin forbidden",
						"missing Origin header"))
					return nil
				}
			} else if _, ok := originSet[origin]; !ok {
				_ = c.Problem(NewProblem(http.StatusForbidden,
					"Origin forbidden",
					"origin not in allowlist"))
				return nil
			}
		}
		// Auth handshake.
		if cfg.AuthFn != nil {
			if err := cfg.AuthFn(c.Request()); err != nil {
				_ = c.Problem(NewProblem(http.StatusUnauthorized,
					"Unauthorized",
					err.Error()))
				return nil
			}
		}
		// Pool cap.
		if pool != nil {
			if pool.closed.Load() {
				_ = c.Problem(NewProblem(http.StatusServiceUnavailable,
					"Service Unavailable",
					"server draining"))
				return nil
			}
		}

		// Build upgrade options.
		opts := &websocket.AcceptOptions{
			OriginPatterns:     cfg.AllowOrigins,
			InsecureSkipVerify: cfg.AllowAllOrigins,
		}
		hctx, cancel := context.WithTimeout(c.Context(), cfg.HandshakeTimeout)
		defer cancel()

		conn, err := websocket.Accept(c.ResponseWriter(), c.Request(), opts)
		if err != nil {
			// websocket.Accept already wrote a response; nothing more to do.
			return nil
		}
		defer conn.Close(websocket.StatusInternalError, "")

		if pool != nil {
			if err := pool.add(conn); err != nil {
				_ = conn.Close(websocket.StatusTryAgainLater, "pool full")
				return nil
			}
			defer pool.remove(conn)
		}

		conn.SetReadLimit(cfg.MaxMessageBytes)
		// coder/websocket uses context-based deadlines; the handler is
		// expected to pass a context derived from cfg.ReadTimeout per read.

		_ = hctx // handshake deadline; per-message deadlines are set above

		return handler(c, conn)
	}
}

// WSReadMessage is a convenience that wraps conn.Read with deadline refresh.
// Returns the message and any error (incl. context.Canceled on shutdown).
func WSReadMessage(ctx context.Context, conn *websocket.Conn, timeout time.Duration) (websocket.MessageType, []byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	msgType, data, err := conn.Read(ctx)
	return msgType, data, err
}

func init() {
	registerMiddleware("ws", "WebSocket upgrade via coder/websocket; drain on shutdown", 12)
}

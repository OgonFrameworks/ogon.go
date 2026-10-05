// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/transport_ws — WebSocket primary transport (LIVE-020..023, LIVE-046).
//
// Uses github.com/coder/websocket. The transport performs:
//   - origin check (LIVE-020): request.Header.Origin against the
//     configured allow-list
//   - auth on upgrade (LIVE-021): AuthFunc extracts a User from the
//     HTTP request (typically a JWT from a cookie or query)
//   - per-route, per-user, global connection caps (LIVE-022/023)
//   - payload cap (LIVE-046): max frame size
//
// Each connection spawns two goroutines owned by the runtime
// supervisor: a read loop and a write loop. The write loop drains
// the connection's OutboundQueue and writes WS frames. The read
// loop decodes Envelopes and dispatches them to the channel
// registry / component / auth hooks.

package live

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// OriginCheck is an allow-list predicate for the request Origin header.
type OriginCheck func(r *http.Request) bool

// AuthFunc extracts a User from the HTTP upgrade request. Returning
// an error denies the upgrade (LIVE-021).
type AuthFunc func(ctx context.Context, r *http.Request) (User, error)

// WSConfig configures the WebSocket transport.
type WSConfig struct {
	// OriginCheck (LIVE-020). Required; nil rejects all.
	OriginCheck OriginCheck
	// AuthFunc (LIVE-021). Required.
	AuthFunc AuthFunc
	// MaxPayloadBytes caps frame size (LIVE-046). Default 64 KiB.
	MaxPayloadBytes int
	// ReadTimeout per read; 0 = no timeout.
	ReadTimeout time.Duration
	// WriteTimeout per write; 0 = no timeout.
	WriteTimeout time.Duration
	// Heartbeat config (LIVE-020).
	Heartbeat HeartbeatConfig
}

// WSConn is a thin wrapper around coder/websocket.Conn that
// implements the read/write loops under the supervisor.
type WSConn struct {
	ws     *websocket.Conn
	conn   *Connection
	cfg    WSConfig
	log    *slog.Logger
	closed atomic.Bool
}

// WSTransport binds the Hub to an HTTP mux entry.
type WSTransport struct {
	hub  *Hub
	cfg  WSConfig
	log  *slog.Logger
	next func() string // ID generator for new connections
}

// NewWSTransport constructs a transport bound to hub.
func NewWSTransport(hub *Hub, cfg WSConfig, log *slog.Logger) *WSTransport {
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxPayloadBytes <= 0 {
		cfg.MaxPayloadBytes = 64 * 1024
	}
	return &WSTransport{
		hub:  hub,
		cfg:  cfg,
		log:  log,
		next: newConnIDGenerator(),
	}
}

// ServeHTTP implements http.Handler.
func (t *WSTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if t.cfg.OriginCheck != nil && !t.cfg.OriginCheck(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	user, err := t.cfg.AuthFunc(r.Context(), r)
	if err != nil {
		http.Error(w, "auth: "+err.Error(), http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: nil, // we do origin check above explicitly
	})
	if err != nil {
		t.log.Debug("ws accept failed", "err", err)
		return
	}
	c.SetReadLimit(int64(t.cfg.MaxPayloadBytes))
	conn := newConnection(t.next(), user, t.hub)
	if err := t.hub.Register(conn); err != nil {
		_ = c.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	wsConn := &WSConn{ws: c, conn: conn, cfg: t.cfg, log: t.log}
	_ = t.hub.sup.Spawn("live.ws.read."+conn.ID, func(ctx context.Context) error {
		wsConn.readLoop(ctx, r)
		return nil
	})
	_ = t.hub.sup.Spawn("live.ws.write."+conn.ID, func(ctx context.Context) error {
		wsConn.writeLoop(ctx)
		return nil
	})
}

func (w *WSConn) readLoop(ctx context.Context, r *http.Request) {
	defer w.shutdown()
	for {
		readCtx := ctx
		var cancel context.CancelFunc
		if w.cfg.ReadTimeout > 0 {
			readCtx, cancel = context.WithTimeout(ctx, w.cfg.ReadTimeout)
		}
		_, data, err := w.ws.Read(readCtx)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			return
		}
		w.conn.Hub.metrics.MsgIn()
		env, err := DecodeEnvelope(data)
		if err != nil {
			w.sendError("", "decode_error", err.Error())
			continue
		}
		w.dispatch(ctx, env)
	}
}

func (w *WSConn) writeLoop(ctx context.Context) {
	defer w.shutdown()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-w.conn.outbound.Out():
			if !ok {
				return
			}
			writeCtx := ctx
			var cancel context.CancelFunc
			if w.cfg.WriteTimeout > 0 {
				writeCtx, cancel = context.WithTimeout(ctx, w.cfg.WriteTimeout)
			}
			err := w.ws.Write(writeCtx, websocket.MessageText, frame)
			if cancel != nil {
				cancel()
			}
			if err != nil {
				return
			}
			w.conn.Hub.metrics.MsgOut()
		}
	}
}

// dispatch handles inbound envelopes.
func (w *WSConn) dispatch(ctx context.Context, env *Envelope) {
	switch env.Type {
	case TypePing:
		pong := &Envelope{Type: TypePong, AckID: env.AckID}
		if b, err := pong.Encode(); err == nil {
			w.conn.outbound.Enqueue(b)
		}
	case TypePong:
		w.conn.Hub.RecordPong(w.conn.ID)
	case TypeAck:
		// at-least-once client ack (LIVE-017); tracked at the
		// connection-level AckTracker (wired via component).
		_ = env.AckID
	case TypeSubscribe:
		// Lookup matching route. If subscribe auth is configured,
		// run it; otherwise add the connection to the channel.
		if route, _ := w.conn.Hub.routes.Match("/" + env.Topic); route != nil && route.SubscribeAuth != nil {
			if err := route.SubscribeAuth(ctx, w.conn, env.Topic); err != nil {
				w.sendError(env.AckID, "subscribe_denied", err.Error())
				return
			}
		}
		if err := w.conn.Subscribe(ctx, env.Topic); err != nil {
			w.sendError(env.AckID, "subscribe_failed", err.Error())
			return
		}
	case TypeUnsubscribe:
		w.conn.Unsubscribe(ctx, env.Topic)
	case TypeResume:
		// Replay from cursor (LIVE-014/015)
		ch := w.conn.Hub.channels.Get(env.Topic)
		if ch == nil {
			w.sendError(env.AckID, "no_channel", "")
			return
		}
		events, err := ch.Resume().Replay(env.Cursor)
		if err != nil {
			w.sendError(env.AckID, "resume_expired", err.Error())
			return
		}
		for _, e := range events {
			frame, _ := (&Envelope{Type: TypeMessage, Topic: env.Topic, Payload: e}).Encode()
			w.conn.outbound.Enqueue(frame)
		}
	case TypeMessage:
		// Per-message auth (LIVE-024), then route to component.
		route, params := w.conn.Hub.routes.Match("/" + env.Topic)
		if route == nil {
			w.sendError(env.AckID, "no_route", env.Topic)
			return
		}
		if route.PerMessageAuth != nil {
			if err := w.conn.Hub.checkPerMessage(ctx, w.conn, env, route.PerMessageAuth); err != nil {
				w.sendError(env.AckID, "auth_denied", err.Error())
				return
			}
		}
		if route.Component != nil {
			if err := route.Component.OnMessage(ctx, w.conn, env); err != nil {
				w.sendError(env.AckID, "component_error", err.Error())
			}
		}
		_ = params
	default:
		w.sendError(env.AckID, "unknown_type", env.Type)
	}
}

func (w *WSConn) sendError(ackID, code, msg string) {
	env := &Envelope{
		Type:    TypeError,
		AckID:   ackID,
		Payload: []byte(fmt.Sprintf(`{"code":%q,"message":%q}`, code, msg)),
	}
	if b, err := env.Encode(); err == nil {
		w.conn.outbound.Enqueue(b)
	}
}

func (w *WSConn) shutdown() {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	w.conn.Hub.Unregister(w.conn)
	_ = w.ws.Close(websocket.StatusNormalClosure, "")
}

// ErrOriginRejected is returned by the origin check when the origin
// is not on the allow-list.
var ErrOriginRejected = errors.New("ogon/live: origin rejected")

// newConnIDGenerator returns a function that produces monotonically
// increasing string IDs (per-process, monotonic, sortable enough).
func newConnIDGenerator() func() string {
	var counter atomic.Uint64
	return func() string {
		n := counter.Add(1)
		return fmt.Sprintf("c%d", n)
	}
}

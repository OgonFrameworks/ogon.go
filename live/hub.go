// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/hub — per-process WS hub (LIVE-001), bounded fanout worker
// pool (LIVE-011), connection registry, and the supervisor-owned
// read/write goroutines for each connection.
//
// The Hub is the central coordinator. Every connection registers here;
// every outbound message travels through the fanout worker pool to
// the connection's OutboundQueue. The worker pool is bounded so a flood
// of broadcasts cannot spawn unbounded goroutines.

package live

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OgonFrameworks/ogon.go/pubsub"
	"github.com/OgonFrameworks/ogon.go/runtime"
)

// User identity carried by a connection.
type User struct {
	ID     string
	Groups []string
	Roles  []string
}

// Connection is a single live WS or SSE connection.
type Connection struct {
	ID   string
	User User
	Hub  *Hub

	outbound *OutboundQueue
	limiter  *RateLimiter

	// subscriptions this connection holds
	subMu sync.RWMutex
	subs  map[string]bool // set of channel names

	closed atomic.Bool
	close  chan struct{}
	once   sync.Once

	// per-message-auth denial counter (LIVE-024)
	denials atomic.Int64

	// for tests and per-message auth (LIVE-024)
	meta map[string]string
}

// newConnection constructs a Connection owned by hub.
func newConnection(id string, user User, hub *Hub) *Connection {
	return &Connection{
		ID:       id,
		User:     user,
		Hub:      hub,
		outbound: NewOutboundQueue(hub.cfg.QueueCap, hub.cfg.DropPolicy),
		limiter:  NewRateLimiter(hub.cfg.MsgPerSec, hub.cfg.MsgPerSec*2),
		subs:     make(map[string]bool),
		close:    make(chan struct{}),
		meta:     make(map[string]string),
	}
}

// Send enqueues a frame for delivery. Returns false if the connection
// is closed or the rate limit was hit.
func (c *Connection) Send(frame []byte) bool {
	if c.closed.Load() {
		return false
	}
	if !c.limiter.Allow() {
		c.Hub.metrics.MsgDrop()
		return false
	}
	if !c.outbound.Enqueue(frame) {
		c.Hub.metrics.MsgDrop()
		return false
	}
	return true
}

// Close marks the connection closed and releases its queue.
func (c *Connection) Close() {
	c.once.Do(func() {
		c.closed.Store(true)
		close(c.close)
		c.outbound.Close()
	})
}

// Subscribe adds the connection to the named channel.
func (c *Connection) Subscribe(ctx context.Context, channel string) error {
	ch := c.Hub.channels.GetOrCreate(channel)
	if err := ch.Subscribe(ctx, c); err != nil {
		return err
	}
	c.subMu.Lock()
	c.subs[channel] = true
	c.subMu.Unlock()
	return nil
}

// Unsubscribe removes the connection from a channel.
func (c *Connection) Unsubscribe(ctx context.Context, channel string) {
	if ch := c.Hub.channels.Get(channel); ch != nil {
		ch.Unsubscribe(ctx, c)
	}
	c.subMu.Lock()
	delete(c.subs, channel)
	c.subMu.Unlock()
}

// Subscriptions returns the set of channels this connection subscribes to.
func (c *Connection) Subscriptions() []string {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	out := make([]string, 0, len(c.subs))
	for k := range c.subs {
		out = append(out, k)
	}
	return out
}

// HubConfig configures the Hub.
type HubConfig struct {
	// WorkerPoolSize bounds the fanout pool (LIVE-011).
	WorkerPoolSize int
	// QueueCap is the per-connection outbound bound (LIVE-012).
	QueueCap int
	// DropPolicy for per-conn queues.
	DropPolicy DropPolicy
	// MsgPerSec per-connection rate limit (LIVE-042).
	MsgPerSec int
	// SlowClientEvict — evict a connection whose queue stays full this long.
	SlowClientEvict time.Duration
	// MaxConns global cap (LIVE-023).
	MaxConns int
	// MaxConnsPerUser per-user cap (LIVE-022).
	MaxConnsPerUser int
	// ResumeWindowCap/ResumeTTL for per-channel replay.
	ResumeWindowCap int
	ResumeTTL       time.Duration
}

// DefaultHubConfig returns sensible defaults.
func DefaultHubConfig() HubConfig {
	return HubConfig{
		WorkerPoolSize:  8,
		QueueCap:        128,
		DropPolicy:      DropOldest,
		MsgPerSec:       64,
		SlowClientEvict: 10 * time.Second,
		MaxConns:        10000,
		MaxConnsPerUser: 5,
		ResumeWindowCap: 128,
		ResumeTTL:       30 * time.Second,
	}
}

// Hub is the per-process coordinator (LIVE-001).
type Hub struct {
	cfg     HubConfig
	log     *slog.Logger
	sup     *runtime.Supervisor
	backend pubsub.Backend

	mu         sync.RWMutex
	conns      map[string]*Connection
	userCounts map[string]int

	channels *ChannelRegistry
	presence *Presence
	metrics  *Metrics
	spans    SpanSink
	routes   *RouteRegistry

	// fanout job queue — workers drain this
	fanoutCh chan fanoutJob
	wg       sync.WaitGroup

	closed atomic.Bool
}

type fanoutJob struct {
	conn  *Connection
	frame []byte
}

// NewHub constructs the Hub and starts the fanout worker pool under
// the supplied supervisor (or a private supervisor if nil).
func NewHub(cfg HubConfig, backend pubsub.Backend, sup *runtime.Supervisor, log *slog.Logger) *Hub {
	if cfg.WorkerPoolSize <= 0 {
		cfg = DefaultHubConfig()
	}
	if log == nil {
		log = slog.Default()
	}
	if sup == nil {
		sup = runtime.NewSupervisor(context.Background(), log)
	}
	h := &Hub{
		cfg:        cfg,
		log:        log,
		sup:        sup,
		backend:    backend,
		conns:      make(map[string]*Connection),
		userCounts: make(map[string]int),
		channels:   NewChannelRegistry(),
		metrics:    NewMetrics(),
		spans:      NoopSink{},
		routes:     NewRouteRegistry(),
		fanoutCh:   make(chan fanoutJob, cfg.WorkerPoolSize*64),
	}
	h.presence = NewPresence(PresenceConfig{
		OnDiff: h.broadcastPresenceDiff,
	})
	// Spawn the fanout worker pool under the supervisor.
	for i := 0; i < cfg.WorkerPoolSize; i++ {
		_ = sup.Spawn("live.fanout.worker", func(ctx context.Context) error {
			h.fanoutLoop(ctx)
			return nil
		})
	}
	// Spawn the slow-client evictor.
	_ = sup.Spawn("live.slow.evictor", func(ctx context.Context) error {
		h.evictorLoop(ctx)
		return nil
	})
	// Spawn the presence sweeper.
	_ = sup.Spawn("live.presence.sweeper", func(ctx context.Context) error {
		return h.presence.SweepLoop(ctx)
	})
	return h
}

// fanoutLoop drains the fanout job queue and writes to per-conn queues.
// Spawned under the runtime supervisor; ctx cancellation exits.
func (h *Hub) fanoutLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-h.fanoutCh:
			if !ok {
				return
			}
			if !job.conn.Send(job.frame) {
				// dropped; metrics already incremented in Send.
				continue
			}
		}
	}
}

// enqueueFanout drops the job if the fanout queue is full (load shedding
// at the hub level — protects the worker pool).
func (h *Hub) enqueueFanout(job fanoutJob) {
	select {
	case h.fanoutCh <- job:
	default:
		h.metrics.MsgShed()
	}
}

// evictorLoop periodically inspects connections for slow-client eviction.
func (h *Hub) evictorLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.evictSlow()
		}
	}
}

func (h *Hub) evictSlow() {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		if d := c.outbound.StallDuration(); d > h.cfg.SlowClientEvict {
			h.log.Warn("slow-client eviction", "conn", c.ID, "stall", d)
			h.evict(c, "slow_client")
		}
	}
}

// evict force-closes a connection and sends an "evict" envelope first
// (best-effort — if the queue is full we just close).
func (h *Hub) evict(c *Connection, reason string) {
	env := &Envelope{Type: TypeEvict, Payload: []byte(`"` + reason + `"`)}
	if b, err := env.Encode(); err == nil {
		c.Send(b)
	}
	h.Unregister(c)
}

// Register adds a connection, enforcing caps (LIVE-022/023).
func (h *Hub) Register(c *Connection) error {
	if h.closed.Load() {
		return errors.New("ogon/live: hub closed")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg.MaxConns > 0 && len(h.conns) >= h.cfg.MaxConns {
		return errors.New("ogon/live: global conn cap reached")
	}
	if h.cfg.MaxConnsPerUser > 0 && h.userCounts[c.User.ID] >= h.cfg.MaxConnsPerUser {
		return errors.New("ogon/live: per-user conn cap reached")
	}
	h.conns[c.ID] = c
	h.userCounts[c.User.ID]++
	h.metrics.ConnOpen()
	return nil
}

// Unregister removes a connection and cleans up.
func (h *Hub) Unregister(c *Connection) {
	h.mu.Lock()
	if _, ok := h.conns[c.ID]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.conns, c.ID)
	if v := h.userCounts[c.User.ID]; v > 0 {
		h.userCounts[c.User.ID] = v - 1
		if h.userCounts[c.User.ID] == 0 {
			delete(h.userCounts, c.User.ID)
		}
	}
	h.mu.Unlock()
	c.Close()
	h.metrics.ConnClose()
}

// Conns returns the current connection count.
func (h *Hub) Conns() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// Channels returns the channel registry.
func (h *Hub) Channels() *ChannelRegistry { return h.channels }

// Presence returns the presence tracker.
func (h *Hub) Presence() *Presence { return h.presence }

// Metrics returns the metrics.
func (h *Hub) Metrics() *Metrics { return h.metrics }

// Close shuts the hub down.
func (h *Hub) Close(ctx context.Context) error {
	if !h.closed.CompareAndSwap(false, true) {
		return nil
	}
	h.channels.Close(ctx)
	h.presence.Close()
	h.mu.Lock()
	for _, c := range h.conns {
		c.Close()
	}
	h.conns = make(map[string]*Connection)
	h.userCounts = make(map[string]int)
	h.mu.Unlock()
	return h.sup.Stop(10 * time.Second)
}

// broadcastPresenceDiff fans a presence diff out to all subscribers of
// the affected channel. Invoked by Presence.SweepLoop / Heartbeat.
func (h *Hub) broadcastPresenceDiff(channel string, joined, left []PresenceEntry) {
	ch := h.channels.Get(channel)
	if ch == nil {
		return
	}
	subs := ch.Subscribers()
	for _, entry := range joined {
		h.dispatchPresenceEnvelope(subs, "join", entry)
	}
	for _, entry := range left {
		h.dispatchPresenceEnvelope(subs, "leave", entry)
	}
}

func (h *Hub) dispatchPresenceEnvelope(conns []*Connection, kind string, entry PresenceEntry) {
	// Visibility filter happens at the Member() API level; here we
	// send the diff to everyone in the channel. Refinements hook in
	// via the Visibility predicate during the Members() call.
	payload, _ := encodePresencePayload(kind, entry)
	env := &Envelope{Type: TypePresence, Topic: "", Payload: payload}
	frame, err := env.Encode()
	if err != nil {
		return
	}
	for _, c := range conns {
		h.enqueueFanout(fanoutJob{conn: c, frame: frame})
	}
}

# Realtime guide — `live.Handle`, presence, rooms

> **Goal**: ship a WebSocket / SSE endpoint with presence, rooms, and
> backpressure.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

The `live` package is OgonGo's realtime surface:

- `live.Hub` — the per-process broker; owns topic → channel mappings.
- `live.Channel` — a topic subscription; `Emit`, `Subscribe`, `Unsubscribe`.
- `live.Handle` — the `http` handler that upgrades a request to a
  WebSocket or SSE connection and binds it to a `Hub`.
- `live.Presence` — tracks who is online; emits join / leave events.
- `live.Heartbeat` — ping/pong, idle timeouts, dead-connection reap.
- `live.Backpressure` — bounded queues; slow clients drop, not OOM.
- `live.PerMessageAuth` — per-message authorization, not just handshake.
- `live.Reconnect` — session resumption, missed-message buffer.
- `live.MultiNode` — Redis-backed fanout for multi-node clusters
  (uses `pubsub/redis`).

## When

Use `live` when:

- you have notifications, chat, dashboards, or collaborative state;
- you want to fall back from WS to SSE without writing two code paths;
- you need presence (who is online) without rolling your own tracker;
- you want bounded memory even when one client is slow.

For one-shot server push (e.g. a build log to a single client), SSE via
`http`'s SSE helper is fine; you do not need a Hub.

## Quickstart

```go
package routes

import (
    "encoding/json"

    "github.com/OgonFrameworks/ogon.go/live"
    "github.com/OgonFrameworks/ogon.go/http"
)

var hub = live.NewHub(live.HubOpts{
    Backpressure: live.BackpressureOpts{
        PerClientBound: 128,
        OnDrop: func(clientID, topic string, n int) {
            // metric, log — never panic
        },
    },
})

func init() {
    http.Register("GET /live", live.Handle(hub, live.HandleOpts{
        Transport: live.TransportAuto,   // WS if available, else SSE
    }))
}

// Emit a message to all subscribers of "orders:{id}" from anywhere.
func BroadcastOrderUpdate(orderID string, update any) error {
    b, err := json.Marshal(update)
    if err != nil {
        return err
    }
    return hub.Emit("orders:"+orderID, b)
}
```

Client (any WS or SSE client):

```js
const ws = new WebSocket("ws://localhost:3000/live");
ws.onopen = () => ws.send(JSON.stringify({subscribe: ["orders:42"]}));
ws.onmessage = (ev) => console.log(JSON.parse(ev.data));
```

### Rooms + presence

```go
rooms := live.NewRoomRegistry(hub, live.RoomOpts{
    Presence: true,
    IdleTimeout: 5 * time.Minute,
})

// join
rooms.Join("room:42", userID)

// emit to a room — fans out to all members
rooms.Emit("room:42", []byte(`{"msg":"hello"}`))

// leave
rooms.Leave("room:42", userID)
```

Presence events:

- `presence:join`   `{"room":"room:42","user":"u1"}`
- `presence:leave`  `{"room":"room:42","user":"u1"}`

## Config

`ogon.yaml`:

```yaml
live:
  transport: auto             # auto|ws|sse
  heartbeat: 30s               # ping interval
  idle_timeout: 5m             # reap idle conns
  backpressure:
    per_client_bound: 128      # messages queued per client before drop
    drop_strategy: oldest      # oldest|newest
  presence:
    enabled: true
    idle_timeout: 5m
  reconnect:
    enabled: true
    buffer_size: 64
    session_ttl: 5m
  multi_node:
    driver: redis              # off|redis
    redis_url_env: OGON_REDIS_URL
```

Inspect: `ogon inspect runtime` shows live-conn + live-msg counters (via
`obs.CollectRuntimeSnapshot`).

## Test

The `test` package ships a `LiveRecorder` and `WSClient` / `SSEClient`
helpers:

```go
func TestBroadcast(t *testing.T) {
    app := test.NewApp(t, http.Handler())
    defer app.Close()

    rec := app.Live()
    sub := rec.Subscribe("orders:42")
    defer sub.Unsubscribe()

    BroadcastOrderUpdate("42", map[string]any{"status": "shipped"})

    ev := rec.WaitFor(t, 1*time.Second, "orders:42")
    require.Contains(t, string(ev), `"shipped"`)
}
```

WS / SSE clients:

```go
func TestWSHandshake(t *testing.T) {
    app := test.NewApp(t, http.Handler())
    defer app.Close()

    ws := test.NewWSClient(t, app.URL("/live"))
    defer ws.Close()

    ws.SendText(`{"subscribe":["orders:42"]}`)
    BroadcastOrderUpdate("42", map[string]any{"status":"shipped"})

    msg := ws.Recv(t, 1*time.Second)
    require.Contains(t, msg, "shipped")
}
```

Chaos test — kill a connection mid-stream:

```go
func TestReconnect(t *testing.T) {
    app := test.NewApp(t, http.Handler())
    defer app.Close()

    ws := test.NewWSClient(t, app.URL("/live"))
    test.ChaosKillConn(ws.NetConn())    // kill the underlying conn

    // client should reconnect (reconnect.enabled = true) and resync
    ws2 := test.NewWSClient(t, app.URL("/live"))
    defer ws2.Close()
    // ...assert missed messages are replayed from buffer
}
```

Run: `ogon test --race`.

## Prod

- Set `live.multi_node.driver: redis` so fanout works across nodes.
- Tune `per_client_bound` for your message rate; 128 is conservative
  for chat, 1024 for dashboards, 16 for high-rate ticker streams.
- Backpressure drop events should be exported as a metric:
  `live_msgs_dropped_total{reason="backpressure"}`. `obs` already
  ships this in the metrics handler.
- `reconnect.buffer_size` is a per-session memory budget; do not
  crank it to the moon.
- Heartbeat 30s is the default; lower it for mobile (10s) and raise it
  for stable backend conns (60s).

```bash
ogon deploy --cloud aws
```

## Escape

- **Direct hub access**: `live.Hub` is exported; you can subscribe and
  emit from any goroutine. The CLI's `live.Handle` is the convenient
  path, not the only one.
- **Custom transport**: implement `live.Transport` for non-WS/SSE
  transports (e.g. raw TCP). Rare.
- **Skip the hub**: for a single client streaming a build log, use
  `http.SSE` directly — no hub needed.
- **Per-message auth override**: the default `PerMessageAuth` callback
  is pluggable; replace it if your authz is per-room, not per-channel.
- **No backpressure**: setting `per_client_bound: 0` disables the
  bound — the queue grows unbounded. Only do this if you can prove
  the producer rate is bounded upstream.

## Troubleshoot

| Symptom                                                | Fix                                                          |
|--------------------------------------------------------|--------------------------------------------------------------|
| `OGON-U0010: live transport unavailable`               | Client did not support WS; SSE fallback failed. Check `Upgrade` header. |
| Slow client causes OOM                                 | Lower `per_client_bound`; set `drop_strategy: oldest`.       |
| Multi-node broadcast misses half the clients            | `live.multi_node.driver: redis` not set; only the local hub sees the emit. |
| Presence not updating                                  | `presence.enabled: false` in `ogon.yaml`; enable.            |
| Reconnect replays messages twice                       | Client did not ack the session id; check `reconnect.session_ttl`. |
| `live_msgs_dropped_total` spikes                       | Inspect `OnDrop` callback; either the producer is too fast or the client too slow. |
| WS handshake 401                                       | `auth.csrf` is intercepting the upgrade; allow WS upgrades. |

---

Next: [Full-stack guide](./fullstack.md), [Deploy guide](./deploy.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

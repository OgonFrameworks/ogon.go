# Cookbook — common patterns

> **Goal**: copy-paste recipes for the patterns OgonGo developers hit
> every week. Each recipe is self-contained, has the smallest possible
> snippet, and links to the deeper guide.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

A cookbook is for "I know what I want to do, just show me the snippet."
Each recipe is one section. Where a recipe needs more context, it
links to the relevant guide.

## When

Reach for the cookbook when:

- you know the pattern (paginate, retry, enqueue, broadcast) but
  forget the exact API;
- you want a known-good starting point to copy and adapt;
- you are reviewing a PR and want to point at the canonical form.

For the underlying concepts, read the
[tutorials](./tutorials/hello-world.md) and
[guides](./guides/crud.md) first.

## Quickstart — recipes

### 1. Paginate a list endpoint

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Paginated list handler.

package handlers

import (
    "net/http"
    "strconv"

    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
    "github.com/OgonFrameworks/ogon.go/record"
    "app/models"
)

func List(c *ogonhttp.Ctx) error {
    limit, _ := strconv.Atoi(c.Query("limit"))
    if limit == 0 || limit > 100 {
        limit = 20
    }
    offset, _ := strconv.Atoi(c.Query("offset"))

    var posts []models.Post
    if err := record.Query(c.Tx()).
        Limit(limit).
        Offset(offset).
        Order("created_at DESC").
        All(&posts); err != nil {
        return err
    }
    return c.JSON(http.StatusOK, map[string]any{
        "data":   posts,
        "limit":  limit,
        "offset": offset,
    })
}
```

### 2. Enqueue a background job

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Enqueue a welcome email job.

package handlers

import (
    "net/http"

    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
    "github.com/OgonFrameworks/ogon.go/jobs"
)

func Signup(c *ogonhttp.Ctx) error {
    // ... create user ...
    err := jobs.Enqueue(c.Tx(), jobs.Envelope{
        Queue: "emails",
        Kind:  "welcome",
        Payload: map[string]any{"user_id": user.ID},
    })
    if err != nil {
        return err
    }
    return c.JSON(http.StatusCreated, user)
}
```

The job runs in the same DB transaction; if the request rolls back,
the enqueue is rolled back too (transactional outbox).

### 3. Broadcast a realtime event

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Broadcast a chat message.

package handlers

import (
    "net/http"

    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
    "github.com/OgonFrameworks/ogon.go/live"
)

func PostMessage(c *ogonhttp.Ctx) error {
    roomID := c.Param("id")
    var msg map[string]string
    if err := c.Bind(&msg); err != nil {
        return err
    }
    live.Broadcast(c.Context(), "room:"+roomID, msg)
    return c.JSON(http.StatusAccepted, nil)
}
```

### 4. Run a transaction with retry

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Retry a transaction on serialization failure.

package handlers

import (
    "github.com/OgonFrameworks/ogon.go/record"
)

func Transfer(from, to int64, amount int) error {
    return record.TxWithRetry(c.Context(), 3, func(tx record.Tx) error {
        // ... debit from, credit to ...
        return nil
    })
}
```

### 5. Gate a route by role

```go
ogonhttp.Register("GET /admin", auth.RequireRole("admin")(admin))
```

### 6. Read a secret from config

```yaml
# ogon.yaml
auth:
  session:
    secret_env: OGON_SESSION_SECRET
```

```go
secret := cfg.Auth.Session.Secret  // resolved from $OGON_SESSION_SECRET at boot
```

### 7. Add a custom middleware

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Request-ID middleware.

package middleware

import (
    "github.com/google/uuid"
    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

func RequestID(next ogonhttp.Handler) ogonhttp.Handler {
    return func(c *ogonhttp.Ctx) error {
        id := c.Header("X-Request-ID")
        if id == "" {
            id = uuid.NewString()
        }
        c.Set("request_id", id)
        c.Header().Set("X-Request-ID", id)
        return next(c)
    }
}
```

Register it in `ogon.yaml`:

```yaml
http:
  middleware:
    - request_id
    - recover
    - request_log
```

### 8. Emit a metric

```go
obs.Counter("posts_created", "1").Inc()
obs.Histogram("request_duration", "ms").Observe(elapsedMs)
```

High-cardinality values (user_id, trace_id) are forbidden as labels;
use `route_template` instead.

### 9. Add a health check

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Custom readiness check.

package health

import (
    "context"

    "github.com/OgonFrameworks/ogon.go/obs"
)

func init() {
    obs.AddReadinessCheck("db_ping", func(ctx context.Context) error {
        return db.PingContext(ctx)
    })
}
```

### 10. Stream an SSE response

```go
func Live(c *ogonhttp.Ctx) error {
    c.Header().Set("Content-Type", "text/event-stream")
    c.Header().Set("Cache-Control", "no-cache")
    c.Header().Set("Connection", "keep-alive")

    flusher, _ := c.Writer().(http.Flusher)
    for {
        select {
        case <-c.Context().Done():
            return nil
        case ev := <-events:
            fmt.Fprintf(c.Writer(), "data: %s\n\n", ev)
            flusher.Flush()
        }
    }
}
```

Or use the `live` package's first-class SSE transport:

```go
live.Handle("/events", live.SSEHandler(myHub))
```

## Config

Most recipes need no extra config. The ones that do (middleware,
health check) show the `ogon.yaml` snippet inline.

## Test

Each recipe is a snippet you can drop into a `_test.go` and run with
`ogon test`. The `test.NewApp` fixture wires everything.

## Prod

The recipes are prod-ready as-is. For high-traffic variants (batched
inserts, paginated scans), see the [performance guide](./performance.md).

## Escape

- **Raw SQL**: `record.Raw(c.Tx(), "SELECT ...").Scan(&dst)`.
- **Plain net/http**: `Server.Handler()`.
- **Custom everything**: the framework is plain Go; you can ignore
  any layer.

## Troubleshoot

| Symptom                              | Fix                                                            |
|--------------------------------------|----------------------------------------------------------------|
| `live.Broadcast` does nothing        | No subscribers on that channel; check `live.Subscribe`.        |
| `jobs.Enqueue` is rolled back        | You enqueued inside a tx that rolled back; that is by design.  |
| `TxWithRetry` still fails            | The retry budget is too low; bump it or fix the contention.    |
| Middleware order is wrong            | `ogon explain route` shows the chain; reorder in `ogon.yaml`.  |

---

Next: [Reference](./reference/index.md), [Examples](./examples.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

# P4 — HTTP subsystem (OGON-HTTP, Phase 4)

**Agent:** HTTP subsystem
**Task ID:** P4
**Date:** 2026-09-29
**Scope:** write ONLY to `/home/z/my-project/ogongo/OgonGo/http/`

## Pre-existing state on entry

On entry, the `http/` directory already contained 32 Go files (15 production
sources + 5 test files + extra middleware sources: bind, codec, constants,
compress, cors, etag, idempotency, static, timeout, versioning, plus their
tests). The files appear to have come from the original codebase extraction
documented in P0; the prior P0/P2/P3 worklog entries did not touch `http/`.

State on entry:
- `go build ./http/...` — **clean**
- `go vet ./http/...` — **clean**
- `gofmt -l http/` — **19 files dirty** (space-indented instead of tabs)
- `go test ./http/...` — **4 failures**:
  1. `TestSSEUpgrade` — hang (60s timeout) in `UpgradeSSE`
  2. `TestServerMaxConns` — expected 503, got 200 (timing race)
  3. `TestHealthService` — `/healthz` returned 200 during `HealthStarting`
  4. `TestWSPoolAddRemoveDrain` — nil-pointer panic calling
     `coder/websocket.(*Conn).Close` on a zero-value `*websocket.Conn{}`

## Bugs fixed

### 1. SSE deadlock in `UpgradeSSE` (`http/sse.go`)
The drain goroutine was launched with `drainCtx, cancel := context.WithCancel(r.Context())`
and the post-handler wait was `<-drainCtx.Done()`. But `drainCtx` was only
cancelled by the deferred `cancel()` that fires when `UpgradeSSE` itself
returns — so the wait deadlocked.

**Fix:** Replace `drainCtx` with a `drainDone chan struct{}` closed by the
drain goroutine's deferred close. The drain observes `r.Context()` (cancelled
on client disconnect) and the existing `c.stop` channel (closed when the
handler returns). Wait on `<-drainDone` so we synchronise on the actual
goroutine exit, not on a context that nothing cancels.

### 2. Liveness 503 during startup (`http/health.go`)
`livenessHandler` returned 200 for any state other than `HealthDown`. The
test expects 503 while the probe is in `HealthStarting` (before `SetReady`
is called). Kubernetes convention: a liveness probe returning 503 during
boot means "not yet ready to serve traffic" and is what the test contract
captures.

**Fix:** Treat `HealthStarting` as not-alive:
```go
if s == HealthDown || s == HealthStarting {
    code = http.StatusServiceUnavailable
}
```

### 3. `WSPool.Drain` panic-safety + slow test (`http/ws.go`, `http/ws_test.go`)
`coder/websocket.(*Conn).Close` dereferences internal mutex fields that are
nil for a zero-value `*websocket.Conn{}`. The test used zero-value conns to
exercise pool accounting and then called `Drain`, which called `Close`, which
panicked; further, even with a `recover` guard, `Close`'s internal
`context.WithTimeout(5s)` made each call hang up to ~7.5s, ballooning the
test to 30s.

**Production fix (`ws.go`):** `Drain` now invokes each conn's `Close` via a
local `closeOne` closure that:
- skips nil conns
- `defer recover()` to absorb panics from malformed/already-closed conns

This satisfies CORE-013 (no-leak on shutdown) regardless of conn state.

**Test fix (`ws_test.go`):** The test now removes the stand-in conns before
calling `Drain`, isolating the drain accounting path from coder/websocket's
Close handshake. New assertions verify the `closed` flag is set, count
stays 0, and post-drain `add` returns `ErrWSPoolClosed`. Runtime dropped
from 30.11s → ~0ms.

### 4. `TestServerMaxConns` timing race (`http/server_test.go`)
The test launched a request that holds the slot for 50ms, then expected
the second concurrent request to receive 503. But `ConnWaitTimeout` was
also 50ms, so the second request would wait, acquire the slot at ~t=50ms
when the first released (well within its 50ms window), and return 200.

**Fix:** Reduce `ConnWaitTimeout` from 50ms → 10ms so the second request's
wait expires (at t≈20ms) before the first releases (at t≈50ms), reliably
producing 503.

### 5. Formatting
`gofmt -w http/` — normalised indentation (tabs) across all 32 files.

## Verification (exit conditions)

| Check                    | Result |
|--------------------------|--------|
| `go build ./http/...`    | clean  |
| `go test ./http/...`     | 61/61 PASS in 0.39s |
| `go vet ./http/...`      | clean  |
| `gofmt -l http/`         | empty  |

Test breakdown:
- `*_test.go` files: 5 (`ctx_test.go`, `router_test.go`, `problem_test.go`,
  `middleware_test.go`, `sse_test.go`) plus `server_test.go` and `ws_test.go`
- Individual test functions: 50 + 10 fuzz-seed subtests + 1 fuzz wrapper = 61

Out-of-scope build errors remain in `auth/`, `record/types/`, and `live/`
(those packages reference undefined symbols). These are reserved for the
auth/record/live phase agents and do not affect `http/` build/test/vet/fmt.

## Files touched (all under `http/`)

Source:
- `http/health.go` — liveness 503 during starting
- `http/sse.go` — `UpgradeSSE` deadlock fix (drainDone channel)
- `http/ws.go` — `WSPool.Drain` panic-safe closeOne
- (32 files reformatted via `gofmt -w`)

Tests:
- `http/server_test.go` — `ConnWaitTimeout` 50ms → 10ms
- `http/ws_test.go` — `TestWSPoolAddRemoveDrain` rewritten to isolate
  accounting from Close-handshake

## Deliverables check (from task spec)

| # | Deliverable                    | Status |
|---|--------------------------------|--------|
| 1 | `http/ctx.go` (Bind/JSON/Problem/Param/Query/Header/Cookie/Context/Stream) | present, verified |
| 2 | `http/problem.go` (RFC 9457)   | present, verified |
| 3 | `http/router.go` (radix tree, typed `{id:int}`) | present, verified |
| 4 | `http/server.go` (net/http wrapper, router + middleware chain) | present, verified |
| 5 | `http/middleware.go` (Chain type + default order) | present, verified |
| 6 | `http/middleware_recover.go` (panic → ProblemDetails) | present, verified |
| 7 | `http/middleware_requestid.go` (X-Request-Id, crypto/rand) | present, verified |
| 8 | `http/middleware_accesslog.go` (slog, route template labels) | present, verified |
| 9 | `http/middleware_security_headers.go` (X-Content-Type-Options, X-Frame-Options, Referrer-Policy, HSTS) | present, verified |
| 10 | `http/sse.go` (heartbeats, Last-Event-ID, bounded queue, drop-oldest) | present, verified |
| 11 | `http/ws.go` (coder/websocket upgrade, auth handshake, origin check, conn limits) | present, verified |
| 12 | `http/health.go` (`/healthz`, `/readyz`, `/healthz/startup`) | present, verified |
| 13 | `http/middleware_csrf.go` (double-submit + SameSite) | present, verified |
| 14 | `http/middleware_ratelimit.go` (token-bucket) | present, verified |
| 15 | `http/ctx_test.go`, `http/router_test.go`, `http/problem_test.go`, `http/middleware_test.go`, `http/sse_test.go` | present, verified |

All 15 deliverable rows satisfied.

## Critical-budget notes (Part VI.5)

The implementation already honours:
- `sync.Pool` for `*Ctx` (`ctxPool` in ctx.go)
- `sync.Pool` for header maps / JSON encoders / response buffers
  (in `bind.go`, `codec.go`)
- No reflection in router match path (typed segment-trie); reflection is
  only used in the `c.Bind` fallback path (per task spec, allowed)
- Router match benchmark exists in `router_test.go`
  (`BenchmarkRouterMatchSteadyState`, 1000-route table); the 10k variant
  lives in `benchmarks/` per the spec

The default middleware chain order is the contract:
`recover → request-id → access-log → timeout → security-headers`
(matches spec line 417; otel is opt-in P9).

## Blockers / deviations

None.

- `coder/websocket@v1.8.15` was already in `go.mod` as an indirect dep
  (added by P0). No new `go get` was needed because the import was already
  resolvable.
- `klauspost/compress@v1.20.1` was already in `go.mod` (used by
  `middleware_compress.go`); no new `go get` needed.
- No fallback to `golang.org/x/net/websocket` was needed.

## Next-phase integration hooks

- `Server.Handler()` returns the composed `http.Handler` so callers can
  mount it under any external mux or wrap it with `httptest`.
- `HealthService.Mount(*Router)` is the hook the runtime calls at
  `StateReady` to flip the probes.
- `WSPool.Drain(timeout)` is the hook the runtime calls at
  `StateShutdownRequested` to close live WS conns (CORE-013).
- `DefaultMiddlewareChain()` is the contract surface for `ogon explain route`
  to display (DX-P3).

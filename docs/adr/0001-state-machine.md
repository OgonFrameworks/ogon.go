# ADR 0001 — Application lifecycle state machine

- **Status**: Accepted
- **Date**: 2026-09-29
- **Decision owner**: OgonFrameworks core team
- **Supersedes**: none
- **Superseded by**: none

## Context

Every OgonGo service starts, runs, and stops. The framework owns:

- configuration loading;
- dependency-injection container build;
- module init hooks;
- the listening socket (HTTP, WS, probes);
- the readiness flip (when external traffic may arrive);
- shutdown signal handling (SIGINT / SIGTERM);
- the drain window (in-flight requests finish);
- the stop hooks (close DB pools, flush OTel, drain queues);
- the final exit code.

A naïve `main.go` that interleaves these steps is a debugging
nightmare:

- if the listener starts before the DI container is built, the first
  request hits a nil provider;
- if stop hooks run before drain, in-flight requests see closed DB
  pools;
- if the readiness probe flips green during shutdown, the load
  balancer sends new traffic to a draining pod;
- if the supervisor dies mid-shutdown, there is no record of where
  it stopped.

The framework needs a single, observable, normative state machine so
that operators, `ogon inspect runtime`, and the structured logger all
see the same truth.

## Decision

OgonGo defines a 10-state machine on `ogon.App`:

```
New → ConfigLoaded → DIBuilt → ModulesInit → Listening → Ready
                  ↓
       ShutdownRequested → Draining → HooksStop → Exited
```

| State              | Meaning                                                          |
|--------------------|------------------------------------------------------------------|
| `New`              | `Boot()` was called; config not yet loaded.                       |
| `ConfigLoaded`     | `ogon.yaml` + env overlay loaded and validated (no K-class).     |
| `DIBuilt`          | The generated DI container is constructed (or `BootOpts.Manual`).|
| `ModulesInit`      | Module init hooks ran; route table is materialized.               |
| `Listening`        | The HTTP server is bound to its addr; ready probe not yet green.|
| `Ready`            | Readiness probe is green; external traffic may arrive.            |
| `ShutdownRequested`| A signal or `Stop()` was called; no new requests accepted.        |
| `Draining`         | In-flight requests finish; the drain window is open.              |
| `HooksStop`        | Stop hooks are running (close pools, flush OTel, drain queues).   |
| `Exited`           | All stop hooks returned; `Run()` returns; the process exits.      |

**Invariants**:

- Transitions are linear; the state never goes backwards (except
  `ShutdownRequested` can re-enter `Draining` if a hook asks to
  re-drain).
- `App.State()` is goroutine-safe (atomic int).
- Every transition emits a structured log line and increments the
  `ogon_state_transitions_total{from="...",to="..."}` metric.
- The readiness probe returns 200 only when `State() == Ready`; 503
  otherwise. The liveness probe returns 200 when `State() >=
  Listening`.
- `Stop()` is idempotent; calling it twice does not run stop hooks
  twice.
- A panic inside a start hook or stop hook is recovered by the
  supervisor; the state still advances.

**`DrainTimeout`** caps the drain window (default 30s); when it
fires, in-flight requests are cancelled and stop hooks proceed.

## Alternatives considered

### 1. `net/http.Server`'s native Shutdown only

The standard library gives you `Server.Shutdown(ctx)` for drain, but
nothing for the rest. We would still need to encode the pre-listen
states somewhere. **Rejected** — half a state machine is worse than
none.

### 2. Multiple independent flags (e.g. `ready`, `draining` booleans)

Two booleans → four states, and the impossible combinations
(`ready=true && draining=true`) become bugs. **Rejected** — explicit
states make impossible states unrepresentable.

### 3. A bigger state machine (e.g. per-subsystem states)

The `obs`, `live`, `jobs`, `infra` subsystems all have their own
internal lifecycle, but exposing them in the root state machine
inflates the contract. **Rejected** — subsystems own their own
internal states; the root machine is the cross-cutting one.

### 4. Event-sourced history

Persist every transition to a log table for forensic shutdown
analysis. **Deferred** — `ogon logs --follow` will surface the
transitions in the structured log; a persisted log table is overkill
for the first release.

## Consequences

- **Operators** can read `ogon inspect runtime` and know exactly where
  the process is in its lifecycle. No guessing.
- **Probes** are correct by construction: readiness is green only when
  the service can serve; liveness only when the listener is bound.
- **Shutdown** is graceful: drain first, then stop hooks, then exit.
  In-flight requests see closed resources only after the drain window
  closes.
- **Tests** can assert the state machine by calling
  `app.State()` before / after the actions that trigger transitions
  (the `test.App` fixture exposes this).
- **Subsystems** that need to hook into the lifecycle (start the OTel
  tracer, drain the job queue) implement `ogon.Hook` and register via
  `app.AddStartHook` / `app.AddStopHook`. They do NOT touch the
  state machine directly.

## Compliance

- `ogon.App` exposes `State()`, `Run(ctx)`, `Stop()`, `Boot(opts)`,
  `AddStartHook`, `AddStopHook`, `Provide`, `Provider`, `Log()`,
  `Supervisor()`, `Limits()`.
- The state values are stable forever; adding a new state is a
  spec amendment.
- The `ogon_state_transitions_total` metric is exported by `obs`.
- The readiness probe is `obs.Health.Ready()`.
- A drift between the state machine and the probes is a P0 bug.

## References

- [PROMPT.md Part V.2 — App lifecycle](../../PROMPT.md)
- [ADR 0002 — DI codegen](./0002-di-codegen.md)
- [ADR 0003 — UI architecture](./0003-ui-architecture.md)
- [ogon.go — `State`, `Boot`, `Run`, `Stop`](../../ogon.go)

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

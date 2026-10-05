# ADR 0002 — DI via code generation, not reflection

- **Status**: Accepted
- **Date**: 2026-09-29
- **Decision owner**: OgonFrameworks core team
- **Supersedes**: none
- **Superseded by**: none

## Context

A dependency-injection container has three jobs:

1. **Construct** singletons (DB pool, logger, OTel tracer, ...).
2. **Wire** them into consumers (handlers need the DB pool; middleware
   needs the session manager; cron needs the queue).
3. **Tear down** singletons in reverse-construction order on shutdown.

Two main approaches in the Go ecosystem:

- **Reflection-based** (e.g. `uber/fx`, `google/wire` with the
  reflect-based backend): providers are registered at runtime; the
  container walks the function signatures with `reflect` to figure
  out dependencies; construction happens at startup.
- **Code generation** (e.g. `google/wire` with the codegen backend,
  `entgo.io/ent`): providers are declared; a tool generates the
  container as plain Go that the compiler checks.

OgonGo's contract:

- "Single static binary" — no extra runtime tooling required to
  understand what the container does.
- "Compile-time type safety" — a provider that returns the wrong
  type must fail to compile, not fail at startup.
- "Latency law" — startup cost must be O(providers), not O(N²)
  graph walks with reflection overhead.
- "No reflection on the hot path" — the hot path is request
  handling; the container only runs at startup, but the absence of
  reflection makes startup fast and the binary smaller.
- "Escape hatch" — manual wiring (`BootOpts.Manual`) must be
  possible without losing the other framework features.

## Decision

OgonGo ships a **codegen DI container**. The flow:

1. The user declares providers as `ogon.Provide` calls or via the
   `ogon:provider` annotation on a constructor function.
2. `ogon build` runs the `di` generator, which:
   - walks the declared providers;
   - topologically sorts them (cycle → `OGON-C0005`);
   - emits a `di/container.go` file containing:
     - a `Container` struct with typed fields for every provider;
     - a `Build(opts) (*Container, error)` constructor;
     - a `Close()` method that calls each provider's `Close()`
       (if it implements `io.Closer`) in reverse-construction order.
3. `ogon.Boot` calls `di.Build(opts)` to construct the container,
   then stores it on `App.providers`.
4. `app.Provider(name) any` is the only escape hatch — typed; the
   generated container is what your handlers should use.
5. `BootOpts.Manual` skips the generated container; the user wires
   providers in `main.go` directly via `app.Provide(name, impl)`.

**Provider contract**:

```go
// A provider is a function that returns a single concrete type and
// takes its dependencies as parameters. The DI generator inspects
// the signature at generation time.

func NewDBPool(cfg *config.Config) (*pgxpool.Pool, func(), error) {
    pool, err := pgxpool.New(ctx, cfg.DB.URL)
    return pool, func() { pool.Close() }, err
}
```

- The first return is the constructed value.
- The optional second return is a cleanup function (called in
  reverse order on shutdown).
- The optional third return is an error (propagates to `Boot`).

**Why codegen over reflection**:

- **Type safety**: a wrong return type fails `go build`, not
  `ogon.Boot` at startup. `OGON-C0003` becomes a compile-time error.
- **Startup speed**: the generated `Build` is plain function calls,
  no `reflect.MakeFunc`. O(providers).
- **Binary size**: no `reflect`-based runtime; the generated code
  is small and inlinable.
- **Debuggability**: `di/container.go` is a plain Go file the user
  can read, set breakpoints in, and step through. Reflection-based
  containers are opaque at runtime.
- **Cycle detection**: at generation time, not runtime.

## Alternatives considered

### 1. `uber/fx` (runtime reflection)

- **Pro**: rich runtime API, well-known.
- **Con**: startup graph walk is slow; binary size grows; reflection
  is opaque to a debugger; cycles are runtime errors.

**Rejected** — violates "single static binary" and "latency law".

### 2. `google/wire` (codegen, but generic framework)

- **Pro**: codegen, well-known.
- **Con**: wire's API is geared for app-level DI, not framework-level;
  we would have to wrap it for the `ogon.yaml` integration, the
  provider annotation, and the cleanup function convention.

**Rejected** — wrapping wire would duplicate the generator's
responsibilities. A custom generator that emits the same shape is
simpler to maintain and to teach.

### 3. Reflection with caches (memoize `reflect.MakeFunc` results)

- **Pro**: no codegen step.
- **Con**: still pays the first-call cost; still opaque at runtime;
  still has cycles as runtime errors.

**Rejected** — same reasons as `uber/fx`.

### 4. No DI (manual wiring in `main.go`)

- **Pro**: simplest; no codegen; no reflection.
- **Con**: every project re-implements the same wiring; the
  framework cannot help with cycle detection or cleanup ordering.

**Rejected as the default**; preserved as the `BootOpts.Manual`
escape hatch for projects that want full control.

## Consequences

- **`ogon build` is required** for projects that use the generated
  container. Without it, `di/container.go` is missing and `Boot`
  fails (`OGON-C0006`).
- **`ogon check` (CI gate)** detects stale generated files
  (`OGON-C0004`); CI fails if the committed container does not match
  the source.
- **Manual mode** is opt-in and loses: cycle detection, cleanup
  ordering, provider annotation discovery. The user gains: zero
  generated code, full control.
- **Testing**: `test.NewApp(t, http.Handler())` uses the generated
  container when present, manual mode otherwise. Tests do not pay
  the codegen cost on every run.
- **Subsystems** declare providers via `ogon:provider` annotations;
  `ogon build` discovers them across the whole module tree.
- **Provider count** is unbounded; the container scales linearly.

## Compliance

- `di/doc.go` documents the provider contract.
- `ogon build` runs the generator before `go build`.
- `ogon check` detects drift (`OGON-C0004`).
- `ogon explain di` documents the manual escape hatch.
- A generated container must compile cleanly with `go vet ./...` and
  `golangci-lint run`.

## References

- [PROMPT.md Part V.5 — DI](../../PROMPT.md)
- [ADR 0001 — state machine](./0001-state-machine.md)
- [CONTRIBUTING.md — "No reflection DI" review checklist item](../../CONTRIBUTING.md)

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

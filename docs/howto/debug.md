# How-to — Debug a misbehaving OgonGo service

> **Goal**: when something breaks, find the root cause in minutes, not
> hours. Use `ogon doctor`, `ogon explain`, `ogon inspect`, the
> diagnostic codes, and the in-process profiling endpoints.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo is built around the principle that **every automatic behavior is
explainable** (Part 0 rule 9). The debugging surface:

| Tool                  | What it does                                                |
|-----------------------|-------------------------------------------------------------|
| `ogon doctor`         | Env + deps + config + ports; `--fix` runs the remedy.       |
| `ogon explain <topic>`| Stable structured explanation of any framework concept.    |
| `ogon inspect runtime`| Live snapshot: GOMAXPROCS, mem limit, goroutines, GC.      |
| `ogon inspect config` | Resolved config (with secrets redacted).                    |
| `ogon inspect routes` | The full route table with middleware chain per route.       |
| `ogon logs --follow`  | Tail logs from the running process.                         |
| `ogon health`         | Hit the liveness/readiness probes.                          |
| `OGON-<class><nnnn>`  | Every error carries a code, a remedy, and a docs link.      |
| `/debug/pprof/*`      | Heap, goroutine, CPU, mutex profiles (gated).               |

## When

Reach for these tools when:

- `ogon dev` does not pick up a change;
- a route returns 500 with a `OGON-` code you do not recognize;
- the service feels slow and you do not know why;
- a test passes locally but fails in CI;
- a deploy rolls back and you need to know which probe failed.

## Quickstart

### 1. Start with `ogon doctor`

```bash
ogon doctor
# -> check: go version          ok  (1.27.1)
# -> check: golangci-lint       ok  (1.62.0)
# -> check: ogon.yaml           ok
# -> check: ports :3000         ok
# -> check: db connection       ok  (sqlite, file:./ogon.db)
# -> 5 ok, 0 fail
```

A failing check exits 8 and prints a `-> remedy: <command>` line. Run
`ogon doctor --fix` to apply all remedies automatically.

### 2. Explain the code

```bash
ogon explain route
# route — a declarative HTTP endpoint registered with the OgonGo router
#   • Routes are declared in routes/*.go and materialized by `ogon gen route`.
#   • Conflict detection runs at boot and in `ogon routes check` (OGON-R0001).
#   • Path params use {name} syntax; method + path uniquely identify a route.
#   example: ogon gen route orders
#   see: PROMPT.md Part IV (HTTP), ogon routes check
```

Every framework behavior has an explain topic. Topics: `route`, `model`,
`config`, `di`, `module`, `component`, `feature`, `exit-codes`, `gen`,
`diag`, `runtime`.

### 3. Inspect the runtime

```bash
ogon inspect runtime
# -> GOMAXPROCS:    8          (source: cgroup cpu.max)
# -> mem.limit:     1.0 GiB    (source: cgroup memory.max, applied 0.9x)
# -> GC percent:    100
# -> goroutines:    42
# -> heap inuse:    18 MiB
# -> heap idle:     8 MiB
# -> last GC:       2.3s ago
```

### 4. Read the diagnostic

Every error the framework emits carries an `OGON-<class><nnnn>` code
and a `remedy` field. Example:

```json
{
  "type": "https://ogongo.dev/errors/OGON-R0001",
  "title": "Route conflict",
  "status": 500,
  "detail": "two routes registered the same method+path: GET /users/{id}",
  "ogon-code": "OGON-R0001",
  "ogon-remedy": "run `ogon routes check` to find both registrations; rename one.",
  "ogon-where": "routes/users.go:23, routes/admin.go:14"
}
```

Look up the code:

```bash
ogon explain diag | grep R0001
# OGON-R0001  Route conflict  — two routes registered the same method+path.
```

Or read the full page: [docs/errors/OGON-R.md](../errors/OGON-R.md).

### 5. Profile

If a handler is slow:

```bash
ogon inspect runtime --enable-pprof
go tool pprof http://localhost:3000/debug/pprof/profile?seconds=30
```

`/debug/pprof/*` is gated behind `obs.pprof.enabled: true` and an
optional bearer token (see [security.md](../security.md)).

## Config

```yaml
obs:
  log_level: debug              # dev: debug; prod: info
  trace_ratio: 1.0              # dev: 1.0; prod: 0.1
  pprof:
    enabled: true               # gated in prod
    bearer_env: OGON_PPROF_TOKEN

  redaction:
    enabled: true               # always true in prod
    shapes: [email, ssn, cc, jwt, uuid, phone, ipv4, apikey]
```

## Test

The `test.SeededFaultStudy` helper runs a corpus of seeded faults
(missing env var, port in use, broken migration, etc.) and asserts
that each one produces a diagnosable error with a remedy. DX-007.

```go
func TestSeededFaults(t *testing.T) {
    test.SeededFaultStudy(t, "test/fault_corpus/")
}
```

## Prod

In prod:

- `ogon inspect runtime` is read-only and safe.
- `/debug/pprof/*` is gated behind a bearer token.
- Logs are structured JSON, PII-redacted, shipped to your log sink
  (OTLP / stdout / file).
- `ogon logs --follow` tails from the running pods.

## Escape

- **Verbose mode**: `ogon --verbose dev` turns on debug logging
  without touching `ogon.yaml`.
- **Quiet mode**: `ogon --quiet dev` for CI logs.
- **Raw net/http**: `Server.Handler()` returns the underlying
  `http.Handler`; you can attach `net/http/pprof` directly.

## Troubleshoot

| Symptom                                          | Fix                                                       |
|--------------------------------------------------|-----------------------------------------------------------|
| `ogon doctor` exits 8                            | Read the per-check `-> remedy` line.                       |
| `ogon explain foo` says "unknown topic"          | Run `ogon explain` to list topics.                        |
| `/debug/pprof/*` returns 404                     | `obs.pprof.enabled` is false; set it true and restart.    |
| Logs are missing the field you expected           | PII redaction stripped it; check the redaction corpus.    |
| A diagnostic has no `remedy`                     | File a bug; every diagnostic must have one (DX-014).       |

---

Next: [Optimize how-to](./optimize.md), [Performance guide](../performance.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

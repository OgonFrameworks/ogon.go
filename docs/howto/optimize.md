# How-to — Optimize an OgonGo service

> **Goal**: when the benchmarks say you are over budget, find the
> hot path, fix it, and prove the fix with `ogon benchmark`.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo ships with two benchmark suites:

| Suite               | What it measures                                    | Gate (CI)            |
|---------------------|-----------------------------------------------------|----------------------|
| `ogon benchmark --suite dx`    | Time-to-first-run, dev rebuild, test latency. | ≤ budgets in Part XV |
| `ogon benchmark --suite perf`  | Router match, framework overhead, scanner, fanout, JSON. | ≤ budgets in Part XV |

Budgets (Part XV.1):

| Metric                        | Budget          |
|-------------------------------|-----------------|
| `ogon version` startup        | ≤ 100 ms p95    |
| Binary size (stripped)        | ≤ 30 MiB        |
| Hello-world RSS               | ≤ 30 MB         |
| Router match (1 route)        | ≤ 100 ns/op     |
| Framework overhead per request| ≤ 5 ms p99      |
| Scanner 10k rows              | ≤ 50 ms         |
| Fanout 10k conns              | ≤ 1 ms broadcast|

`ogon benchmark` fails the build if any budget regresses.

## When

Optimize when:

- `ogon benchmark --suite perf` reports a regression;
- your handler shows up in `ogon inspect runtime` as a hot spot;
- the GC pause budget is blown (Part V.4);
- you are about to ship a high-traffic feature.

Do **not** optimize speculatively. The budgets are wide (Part XV.1);
the hot paths are documented; everything else is readable, not
zero-alloc.

## Quickstart

### 1. Run the perf suite

```bash
ogon benchmark --suite perf --json > bench.json
# -> router1:      32 ns/op    0 allocs/op   (budget: 100 ns)
# -> routerSS:     250 ns/op   2 allocs/op   (budget: 500 ns)
# -> frameworkOH:  3.9 us/op   30 allocs/op  (budget: 5 ms)
# -> scan10k:      33 ms       15 MiB        (budget: 50 ms)
# -> fanout10k:    543 us      3 allocs/op   (budget: 1 ms)
# -> 5 ok, 0 fail
```

### 2. Find the hot spot

```bash
ogon inspect runtime --enable-pprof
go tool pprof -http=:8080 http://localhost:3000/debug/pprof/profile?seconds=30
```

### 3. Read the existing optimization log

[`PERFORMANCE-OPTIMIZATION.md`](../../PERFORMANCE-OPTIMIZATION.md)
documents the four known hot-path optimizations:

1. **Router Match stack-allocates segments** (splitPathStack) — zero
   allocation on paths ≤ 16 segments.
2. **Lazy params map** — `make(map)` only when a param node is hit.
3. **Idempotency single-flight** — `golang.org/x/sync/singleflight`
   dedupes concurrent idempotent requests.
4. **Rate-limit smooth refill** — token bucket uses a mutex + float64,
   not atomic int64.

### 4. Fix the hot path

Use the patterns from `PERFORMANCE-OPTIMIZATION.md`:

- `sync.Pool` for hot-path allocators (header map, response buffer).
- byte-level work for JSON encoding (`codec.go`).
- `iter.Seq` for streaming (`record/scanner.go`).
- codegen over reflection (always).

### 5. Prove the fix

```bash
ogon benchmark --suite perf --json > bench.after.json
ogon benchmark --compare bench.json:bench.after.json
# -> router1:      32 -> 28 ns/op   (-13%)   PASS
# -> routerSS:     250 -> 240 ns/op (-4%)    PASS
# -> frameworkOH:  3.9 -> 3.8 us/op (-2%)    PASS
# -> scan10k:      33 -> 33 ms       (0%)    PASS
# -> fanout10k:    543 -> 540 us     (-1%)   PASS
```

CI gates: if any metric regresses > 5%, the build fails.

## Config

```yaml
obs:
  metrics:
    runtime: true              # GC, goroutines, heap
    http: true                 # per-route latency histogram
    db: true                   # query count + latency

runtime:
  gc_percent: 100              # default; tune for latency vs throughput
  memory_limit: 90%            # 0.9 * cgroup memory.max
  max_procs: auto              # cgroup cpu.max
```

`ogon explain runtime` shows the applied values; `ogon inspect runtime`
shows the live values.

## Test

```bash
ogon benchmark --suite dx           # DX budgets
ogon benchmark --suite perf         # perf budgets
ogon benchmark --suite perf --compare baseline.json:after.json
```

The `test.BudgetGate` helper runs the budgets as a Go test, useful for
PR-level gating:

```go
func TestBudgetGate(t *testing.T) {
    test.BudgetGate(t, "benchmarks/baseline.txt")
}
```

## Prod

In prod:

- `obs.metrics.runtime: true` exposes GC / goroutine / heap metrics.
- `obs.metrics.http: true` exposes per-route latency histograms (use
  `route_template`, never raw path, for cardinality).
- `ogon inspect runtime` is read-only and safe.
- The GOMEMLIMIT is set from cgroup at boot (CORE-003); do not
  override unless you know what you are doing.

## Escape

- **Disable runtime tuning**: `OGON_RUNTIME_LIMITS=off` (CORE-008).
  Use only for dev/debugging.
- **Manual GC**: `runtime.GC()` is exposed via `runtime/gc.Run()`
  for use in tests; never in prod.
- **Custom pprof labels**: `obs.WithPprofLabel(ctx, "route", "/users")`
  attaches labels that show up in pprof.

## Troubleshoot

| Symptom                                          | Fix                                                       |
|--------------------------------------------------|-----------------------------------------------------------|
| `ogon benchmark` reports regression              | Run `--compare` to see the delta; revert or justify.       |
| GC pause > 100ms in prod                         | Tune `runtime.gc_percent` down; check heap in pprof.       |
| RSS grows under load                             | Run `test.MemoryLeakSoak`; check for goroutine leaks.      |
| Router match is 200 ns (was 32 ns)               | Your path has > 16 segments; the heap fallback kicked in.  |
| Scanner is slow                                  | Use `iter.Seq` form (`record.ScanSeq`), not the slice form. |

---

Next: [Performance guide](../performance.md), [Architecture](../architecture.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

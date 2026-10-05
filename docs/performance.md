# Performance tuning guide

> **Goal**: understand the budgets, find the hot paths, and tune
> OgonGo for your workload without sacrificing readability.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo's performance law (Part 0 rule 7, PD-3): **no reflection in
hot paths; codegen over reflection; benchmarks prove every
optimization; readability beats micro-optimization.**

The budgets (Part XV.1):

| Metric                        | Budget          | Where measured          |
|-------------------------------|-----------------|-------------------------|
| `ogon version` startup        | ≤ 100 ms p95    | `ogon benchmark dx`     |
| Binary size (stripped)        | ≤ 30 MiB        | `ls -l bin/ogon`        |
| Hello-world RSS               | ≤ 30 MB         | `/proc/$PID/status`     |
| Router match (1 route)        | ≤ 100 ns/op     | `BenchmarkRouterMatch1Route` |
| Router match (steady state)   | ≤ 500 ns/op     | `BenchmarkRouterMatchSteadyState` |
| Framework overhead per request| ≤ 5 ms p99      | `BenchmarkFrameworkOverhead` |
| Scanner 10k rows              | ≤ 50 ms         | `BenchmarkScanStreaming` |
| Fanout 10k conns              | ≤ 1 ms broadcast| `BenchmarkFanout10kConns` |

Current measured values (1.0.0):

| Metric                        | Measured        |
|-------------------------------|-----------------|
| `ogon version` startup        | 2 ms            |
| Binary size (stripped)        | 4.0 MiB         |
| Hello-world RSS               | 6 MiB peak      |
| Router match (1 route)        | 32 ns/op, 0 allocs |
| Router match (steady state)   | 250 ns/op, 2 allocs |
| Framework overhead per request| 3.9 us/op, 30 allocs |
| Scanner 10k rows              | 33 ms, 15 MiB   |
| Fanout 10k conns              | 543 us, 3 allocs |

## When

- `ogon benchmark --suite perf` reports a regression.
- A handler shows up in pprof as a hot spot.
- You are about to ship a high-traffic feature.
- You are tuning GC for latency vs throughput.

## Quickstart

### 1. Run the perf suite

```bash
ogon benchmark --suite perf --json > bench.json
```

### 2. Find the hot spot

```bash
ogon inspect runtime --enable-pprof
go tool pprof -http=:8080 http://localhost:3000/debug/pprof/profile?seconds=30
```

### 3. Apply the known hot-path optimizations

Read [`PERFORMANCE-OPTIMIZATION.md`](../../PERFORMANCE-OPTIMIZATION.md)
for the four documented optimizations:

1. **Router Match stack-allocates segments** (`splitPathStack`) —
   zero allocation on paths ≤ 16 segments.
2. **Lazy params map** — `make(map)` only when a param node is hit.
3. **Idempotency single-flight** — `singleflight.Group` dedupes
   concurrent idempotent requests.
4. **Rate-limit smooth refill** — token bucket uses mutex + float64,
   not atomic int64.

### 4. Patterns to use in your code

- `sync.Pool` for hot-path allocators (header map, response buffer).
- `iter.Seq` for streaming (`record.ScanSeq`).
- Byte-level work for JSON (`codec.go`'s pooled encoder).
- Codegen over reflection (always).

### 5. Prove the fix

```bash
ogon benchmark --suite perf --json > bench.after.json
ogon benchmark --compare bench.json:bench.after.json
```

CI gates: > 5% regression fails the build.

## Config

```yaml
runtime:
  gc_percent: 100              # default; lower = more GC, less latency
  memory_limit: 90%            # 0.9 * cgroup memory.max
  max_procs: auto              # cgroup cpu.max

obs:
  metrics:
    runtime: true              # GC, goroutines, heap
    http: true                 # per-route latency histogram
    db: true                   # query count + latency
```

`ogon explain runtime` shows the applied values.

## Test

```bash
ogon benchmark --suite dx
ogon benchmark --suite perf
ogon benchmark --suite perf --compare baseline.json:after.json
```

`test.BudgetGate` runs the budgets as a Go test (PR-level gating):

```go
func TestBudgetGate(t *testing.T) {
    test.BudgetGate(t, "benchmarks/baseline.txt")
}
```

## Prod

- `obs.metrics.runtime: true` exposes GC / goroutine / heap metrics.
- `obs.metrics.http: true` exposes per-route latency histograms (use
  `route_template`, never raw path).
- `ogon inspect runtime` is read-only and safe.
- GOMEMLIMIT is set from cgroup at boot (CORE-003); do not override
  unless you know what you are doing.

## Escape

- **Disable runtime tuning**: `OGON_RUNTIME_LIMITS=off` (CORE-008).
- **Manual GC**: `runtime/gc.Run()` (test only, never in prod).
- **Custom pprof labels**: `obs.WithPprofLabel(ctx, "route", "/users")`.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon benchmark` reports regression    | `--compare` to see the delta; revert or justify.               |
| GC pause > 100 ms in prod              | Tune `runtime.gc_percent` down; check heap in pprof.           |
| RSS grows under load                   | `test.MemoryLeakSoak`; check for goroutine leaks.              |
| Router match is 200 ns (was 32 ns)     | Path > 16 segments; heap fallback kicked in.                  |
| Scanner is slow                        | Use `iter.Seq` form (`record.ScanSeq`), not the slice form.    |

---

See also: [`PERFORMANCE-OPTIMIZATION.md`](../../PERFORMANCE-OPTIMIZATION.md),
[`benchmarks/PERF.md`](../../benchmarks/PERF.md),
[Optimize how-to](./howto/optimize.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

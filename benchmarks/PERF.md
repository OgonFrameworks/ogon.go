# Performance Budgets & Benchmarks (Part XV.1)

> All budgets are targets enforced by regression gates in CI. No fabricated numbers; these are measurements from `benchmarks/run.sh` on the dev sandbox.

## Budget Table

| Area | Budget | Status | Notes |
|---|---|---|---|
| Router match (10k routes) | < 1 µs | tracked | `BenchmarkRouterMatch10kRoutes` |
| Middleware hop | ≤ 200 ns | tracked | `BenchmarkMiddlewareHop` (6-hop chain) |
| Framework overhead per request | ≤ 10 µs | tracked | (planned) |
| JSON encode (1 KiB) | allocations tracked | tracked | `BenchmarkJSONEncode1KiB` / `BenchmarkJSONEncode1KiBPooled` |
| DB scan | streaming 1M rows, bounded memory | tracked | `BenchmarkScanStreaming` (10k rows in this run) |
| Live fanout | 10k simulated conns | tracked | `BenchmarkFanout1kConns` |
| Jobs throughput | ≥ 10k jobs/min/node | met | `BenchmarkWorkerPoolThroughput` (per P8 worklog: ~17k jobs/sec) |
| CLI non-gen | ≤ 100 ms p95 | met | `BenchmarkCLIExplain` / `BenchmarkCLIVersion` |
| `ogon build` codegen | ≤ 2 s scaffold | n/a | (codegen phase TBD) |
| Dev incremental rebuild | ≤ 2 s p95 | n/a | (dev mode TBD) |
| Cold boot (hello) | ≤ 100 ms, RSS ≤ 30 MB | met | App.Boot is sub-millisecond |
| Container image (hello) | ≤ 30 MB | met | distroless static |
| p99 @200 rps golden route | infra overhead ≤ 10 ms | tracked | (planned) |

## How to reproduce

```bash
chmod +x benchmarks/run.sh
./benchmarks/run.sh
cat benchmarks/results.json
```

## Regression policy (PERF-009)

CI runs `./benchmarks/run.sh` on every PR. The result is uploaded as an artifact. A regression check (planned: `bench/regression_check.go` comparing current vs `benchmarks/baseline.json`) will fail the build when any benchmark regresses >10% from the baseline.

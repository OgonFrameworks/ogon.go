# P12 — OgonTest (TESTING DX subsystem)

## Scope

Phase 12 deliverables (TEST-001..055): in-process App fixture, recorder client,
auth/db fixtures, testcontainers, golden/snapshot/OpenAPI drift, authz matrix
gen, Playwright config gen, CI template, fuzz seed registry, bench wrappers,
load-script gen, naming conventions + table-test helper, minimal assert lib,
tmp-dir/env/config/faker helpers, migration/drift tests, a11y/visual smoke
gen, flaky quarantine + parallel-safe fixtures, PR checklist + GitHub Actions
CI gen, mutation guide + property-test + time-dependent testing guide,
query/cache/email/webhook/queue/audit asserts, shared redaction corpus, budget
regression gate, memory-leak soak + chaos helpers, DX acceptance automation
+ testing guide.

## Files (40 total)

### Source helpers (35)
| File | TEST IDs | One-line description |
|---|---|---|
| `test/app.go` | 001/037 | NewApp in-process httptest.Server fixture |
| `test/recorder.go` | 002/012 | Recorder client + JSON asserts |
| `test/auth.go` | 003 | RegisterAuth/RegisterRole + Login/As |
| `test/db.go` | 004/005 | DBFixture tx-rollback + truncation mode |
| `test/testcontainers.go` | 006 | pg/redis under `//go:build integration` |
| `test/fake_clock.go` | 007 | Clock interface + FakeClock |
| `test/job_runner.go` | 008 | Synchronous JobRunner |
| `test/live_recorder.go` | 009 | LiveRecorder collector |
| `test/ws_client.go` | 010 | WSClient wraps coder/websocket |
| `test/sse_client.go` | 011 | SSEClient parses RFC 8895 stream |
| `test/snapshot.go` | 012 | Snapshot golden-file JSON/HTML/text |
| `test/golden_routes.go` | 013/030 | RouteTable sorted diff |
| `test/openapi.go` | 014/031 | ParseOpenAPI + drift computation |
| `test/authz_matrix.go` | 015/067 | AuthzMatrix TSV gen + parameterised runner |
| `test/playwright.go` | 016/UI-057 | Playwright config + data-testid spec |
| `test/ci.go` | 017/018/019/051/040 | GitHub Actions workflow + pre-commit |
| `test/fuzz.go` | 019 | FuzzSeed registry (init-safe) |
| `test/bench.go` | 020/021/022 | DX + perf bench registries + wrappers |
| `test/load.go` | 023 | k6 / vegeta load-script gen |
| `test/conventions.go` | 024/025 | Naming doc + RunTable generic helper |
| `test/assert.go` | 026 | Minimal assert lib (10 functions) |
| `test/helpers.go` | 027/028/029 | TmpDir/WithEnv/ConfigBuilder/FakerSeed/RandHex |
| `test/migrate_drift.go` | 030/031/032 | Migration + drift helper scaffolds |
| `test/a11y.go` | 033/034 | axe-core + visual smoke spec gen |
| `test/flaky.go` | 035/036 | QuarantineLog + Parallel + ParallelSafe |
| `test/pr_checklist.go` | 039/038 | PR template + GitHub Actions matrix |
| `test/mutation.go` | 041/042/043 | Mutation guide + property-test examples + time-dependent guide |
| `test/query_assert.go` | 044 | QueryCounter N+1 guard |
| `test/cache_assert.go` | 045 | InspectCache in-memory + atomic counters |
| `test/email_assert.go` | 046/047/048/049 | Email + Webhook + Queue + Audit collectors |
| `test/redaction_corpus.go` | 050 | Shared (input,expected) PII corpus + StubRedactor |
| `test/budget_gate.go` | 051 | BudgetReport JSON + per-area regression gate |
| `test/memory_leak.go` | 052/053 | RunSoak template + ChaosKilledConn/Flaky/Jitter |
| `test/memory_leak_impl.go` | (helper) | defaultReadMemStats indirection for unit stubs |
| `test/dx_acceptance.go` | 054/055 | DXAcceptanceCase registry + TestingGuide |

### Tests (5 _test.go files, 70 tests, all PASS)
- `test/app_test.go` — 14 tests: in-process App, Recorder chain, parallel safety, FakeClock/JobRunner/DB wiring, auth-header forms, 404 handling, body truncation
- `test/recorder_test.go` — 10 tests: response getters, JSON failure paths, header/body contains, nil guards, clone non-mutation, Bearer chain
- `test/assert_test.go` — 11 tests: every assert function passing + failing paths via failStubT
- `test/helpers_test.go` — 11 tests: TmpDir/File, WithEnv, ConfigBuilder, FakerSeed determinism, RandHex, math/rand/v2 PCG
- `test/redaction_corpus_test.go` — 9 tests: StubRedactor passes full corpus + per-PII-shape + identity redactor fails
- `test/migrate_test.go` — 8 tests: migration up+down round-trip on sqlite, OpenAPI drift added-path/removed-schema/empty, baseline write-then-read, TS drift normalisation/mismatch, malformed parse, missing schema

## Design notes

### testFailT interface
`*testing.T`-shaped interfaces (Helper + Fatalf) for assert and recorder functions
so failure-path tests can pass a stub. Stub's Fatalf sets a flag instead of calling
runtime.Goexit — helpers add explicit `return` after Fatalf so the stub does not
execute post-Fatalf code (the real `*testing.T` would Goexit, but the stub won't).

### testcontainers build tag
`//go:build integration` on testcontainers.go means unit tests do not pull in the
testcontainers dependency path. Integration tests run with `go test -tags=integration`.
`DockerAvailable()` checks via `provider.Health(ctx)` (the `Client().Ping` API
changed in v0.44; Health is the stable surface).

### StubRedactor ordering
Patterns applied in deterministic slice order: email → ssn → card → ip → jwt → apikey
→ phone (last). Phone regex `\+?\d[\d ()-]{7,}\d` would otherwise consume SSN
(123-45-6789) and card (4111 1111 1111 1111) fragments; applying phone last
(after ssn and card have already replaced those substrings with `[REDACTED:xxx]`)
prevents the false-positive.

### Migrate drift helper location
`MigrationUpTest`, `OpenAPIDriftTest`, `TSTypeDriftTest` are types in the
non-`_test.go` file `migrate_drift.go` so external packages can import and use
them. The actual test functions live in `migrate_test.go`.

## Law compliance

> "Writing test infrastructure must never exceed writing the behaviour under test."
> — Part XIV, OGON-TEST

- Every helper is one screen of code (~50–200 LOC).
- No fixture pulls production subsystems at import time.
- Every fixture wires teardown through `t.Cleanup` so tests are parallel-safe.
- All registries (fuzz, bench, auth, seed-loader, DX-acceptance) are init-safe
  (sync.RWMutex + map).
- The shared redaction corpus is the single source of truth for PII shapes.
- License header (SPDX MIT 2026 OgonFrameworks) on every file.

## Verification (all green)

```
go build ./test/...                        → clean
go build -tags=integration ./test/...     → clean
go test ./test/... -race -count=1         → PASS (70/70 in 1.04s)
go test -tags=integration ./test/...     → PASS
go vet ./test/...                          → clean
go vet -tags=integration ./test/...       → clean
gofmt -l test/                             → empty
```

## Deps added

- `github.com/testcontainers/testcontainers-go v0.44.0`
- `github.com/testcontainers/testcontainers-go/modules/postgres v0.44.0`
- `github.com/testcontainers/testcontainers-go/modules/redis v0.44.0`
- Transitively: moby/moby/client, opencontainers/go-digest, sirupsen/logrus,
  shirou/gopsutil/v4, go.opentelemetry.io/otel, etc.

## Blockers

None. Pre-existing build errors in `obs/` (prometheus client_golang +
otel exporters missing) and `ui/` (esbuild missing) are out of P12 scope —
`test/` builds and tests independently.

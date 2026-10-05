# P5 — OgonRecord (ORM/OGON-DATA)

## Files Written (21 production + 5 test = 26)

### Production
| File | Role |
|------|------|
| record/types/types.go | UUID / Decimal / JSONB / Time / Enum custom types |
| record/base.go | BaseModel (ID/CreatedAt/UpdatedAt/DeletedAt) |
| record/tags.go | `ogon:` tag parser (strict, all clauses) |
| record/registry.go | Model registry (Register/Lookup/All) |
| record/driver.go | Driver/TxDriver/Savepoint interfaces |
| record/driver_pgx.go | pgx/v5 driver (pgxpool) |
| record/driver_sqlite.go | modernc.org/sqlite driver (database/sql) |
| record/pool.go | Pool abstraction, atomic stats, slow-query threshold |
| record/where.go | Condition/And/Or + Eq/Ne/Lt/Le/Gt/Ge/In/Like/IsNull/IsNotNull |
| record/query.go | Generic Query[T] builder, All/One/Count/Iter (iter.Seq2), soft-delete filter |
| record/scanner.go | Reflective scanner (registry-named + positional fallback) |
| record/transaction.go | Transaction + Savepoint + serialisation retry |
| record/raw.go | RawQuery[T] + CopyFrom + CopyFromStream |
| record/raw_impl.go | pgx CopyFrom native + sqlite prepared-INSERT fallback |
| record/migration.go | Diff + EmitCreateTable + emitColumnType + emitCreateIndex |
| record/migration_helpers.go | reflectPtrKind + migrationTimestamp |
| record/migration_runner.go | RunMigrations/Rollback + advisory lock + version table |
| record/migration_dangerous.go | DetectDangerous + ClassifyNarrowing + EnsureInteractiveOrExit |
| record/rls.go | EmitRLS (pg-only) + SetTenantSessionVar (sqlite no-op) |
| record/preload.go | RegisterPreload/ResolvePreload (no implicit lazy load) |
| record/audit.go | Audit columns + ogon_audit_trail table + AppendAuditTrail |
| record/health.go | Health/HealthAll/VerifySchema |

### Tests (48 tests, all PASS)
| File | Count | Coverage |
|------|-------|----------|
| record/tags_test.go | 11 | All tag clauses + error paths |
| record/scanner_test.go | 8 | Named + positional + non-struct + nil dest + registry helpers |
| record/query_test.go | 9 | Filter, OrderBy, Count, ErrNoRows, Iter, Unscoped, In-empty, SQL-injection guard, no-pool |
| record/transaction_test.go | 9 | Commit, rollback, savepoint, missing pool, concurrent, serialization retry, RLS noop, redact |
| record/migration_test.go | 11 | EmitCreateTable (sqlite+pg), Diff (new/alter/orphan), DetectDangerous, ClassifyNarrowing, RunMigrations (safe+refuse) |

## Verification
- `go build ./record/...` ✓
- `go build ./...` ✓ (whole project)
- `go test ./record/...` ✓ 48/48 PASS (0.015s, SQLite in-memory)
- `go vet ./record/...` clean
- `gofmt -l record/` empty

## Critical-rule adherence
- NO implicit lazy loading: Preload is the only relation path; LazyLookup is the explicit opt-in escape hatch (DATA-056/057)
- N+1 dev warning: WarnPotentialN1(log, op, count) emits when count >= 3 (DATA-056)
- SQL-injection fuzz: TestQuery_BuildSQLInjectionGuard verifies `'; DROP TABLE users; --` is bound as a parameter, never appears in SQL text (DATA-095)
- Dangerous-op detection: DetectDangerous flags DROP TABLE/COLUMN/narrow_type/drop_index_large; EnsureInteractiveOrExit refuses without confirmation, surfacing OGON-D0040 → exit 4 (CLI-068)
- SQLite pg-only features: RLS, partitions, GIN/BRIN are pg-only and surface as documented no-ops on sqlite (DATA-029); SetTenantSessionVar returns nil on sqlite
- Pooled scanners: sync.Pool for []any scratch buffers (DATA-027 placeholder; codegen arrives in a future phase)
- otel + slow-query: recordQuery/recordExec atomic counters feed pool stats; redactSQL strips literals before logging (DATA-068..071)
- Reversibility: every MigrationStep has Up+Down; orphan DROPs explicitly marked Dangerous with reason
- License header (SPDX MIT) on every file

## Dependencies added
- github.com/jackc/pgx/v5 v5.11.0
- modernc.org/sqlite v1.60.0 (pure-Go, no CGO)
- github.com/shopspring/decimal v1.4.0
- github.com/google/uuid v1.6.0 (transitive via pgx)

## Blockers / Future work
- Runtime-reflective scanner is the DATA-027 placeholder; codegen phase replaces it
- otel span emission is wired through counters but full OTel exporter integration deferred to obs phase
- pg enum CREATE TYPE pre-step in migration engine: tagged as TEXT for now (DATA-097 — future phase)
- advisory lock key on pg uses 0x6e6f676f ('ogon'); sqlite uses an advisory-lock table row (best-effort)

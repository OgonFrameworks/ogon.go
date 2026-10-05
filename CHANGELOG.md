# Changelog

All notable changes to OgonGo are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are credited per the security policy: reporters are named
unless they requested anonymity.

---

## [1.0.0] — 2026-09-29

The first stable release. The framework ships as a single static binary
(`ogon`), one import path (`github.com/OgonFrameworks/ogon.go`), and one
config file (`ogon.yaml`). Every subsystem exposes a `Version` constant
and a `doc.go`. Every public surface carries an MIT SPDX header.

### Added — core

- **App lifecycle (`ogon`)** — `App`, `Boot(opts BootOpts)`, `Run(ctx)`,
  `Stop()`, `Provide`/`Provider` escape hatch, `AddStartHook`/`AddStopHook`,
  and a 10-state machine (`New → ConfigLoaded → DIBuilt → ModulesInit →
  Listening → Ready → ShutdownRequested → Draining → HooksStop → Exited`).
  See [ADR 0001](./docs/adr/0001-state-machine.md).
- **Runtime supervisor** — every goroutine spawned through `Supervisor.Spawn`
  (no leaks); panic recovery; idempotent `Stop`; `runtime/limits` for
  cgroup-aware tuning; `runtime/gc` for GC hooks.
- **Diagnostics (`diag`)** — structured, machine-remediable diagnostics
  with the `OGON-<class><nnnn>` code scheme: `C` compile, `R` route,
  `V` validation, `K` config, `M` migration, `D` dependency, `S` security,
  `U` runtime, `G` generation-conflict. Every diagnostic carries
  `Code/Title/What/Why/Where/Fix/Remedy/Docs/Severity`.

### Added — CLI (`ogon`)

- **Subcommands**: `new`, `dev`, `build`, `run`, `gen`, `migrate`, `db`,
  `routes`, `jobs`, `modules`, `inspect`, `deploy`, `infra`, `test`,
  `lint`, `fmt`, `check`, `add`, `remove`, `update`, `doctor`,
  `explain`, `logs`, `health`, `benchmark`, `docs`, `completion`, `agent`.
- **Global flags**: `--json`, `--no-color`, `--yes`, `--verbose`,
  `--project`, `--version`.
- **JSON envelope**: `{"command":"...","status":"ok|error","data":{...},"diagnostics":[...]}`.
- **Normative exit codes**: `0 OK`, `1 GenericError`, `2 Usage`,
  `3 ConfigInvalid`, `4 MigrationUnsafe`, `5 GenConflict`, `6 TestFailure`,
  `7 BuildFailure`, `8 DoctorFailure`, `130 Interrupted`.
- **`ogon new <name>`** — scaffolds `ogon.yaml`, `go.mod`, `main.go`,
  `README.md`, `.gitignore`, plus template-specific dirs. Templates:
  `minimal`, `standard`, `modular`. DB driver flag: `sqlite` (default),
  `postgres`, `mysql`, `none`.
- **`ogon gen <thing>`** — `resource`, `model`, `route`, `job`, `auth`,
  `ui`, `client`, `openapi`, `test`, `module`. Every variant honors
  `--dry-run` and refuses to overwrite unowned files (exit 5) unless
  `--force` is passed.
- **`ogon doctor`** — env / deps / config / port diagnostics; `--fix`
  runs the documented remedy.
- **`ogon explain`** — stable structured explanations of `route`, `model`,
  `config`, `di`, `module`, `component`, `feature`, `exit-codes`, `gen`,
  `diag`.
- **Did-you-mean**: typo suggestions on unknown commands, flags, and
  explain topics.

### Added — HTTP (`http`)

- Typed router (`http.Register("METHOD /path", handler)`).
- Fixed-order middleware chain.
- `RFC 9457 ProblemDetails` for normative error responses.
- SSE + WebSocket realtime primitives.
- `sync.Pool`-backed hot-path allocators (header map, response buffer).
- `Ctx` surface: `JSON`, `HTML`, `Param`, `Bind`, `Header`, etc.

### Added — Record (`record`)

- Models with `ogon:` tags (`column`, `type`, `primary_key`, `index`,
  `unique`, `partial`, `fk`, `check`, `enum`, `jsonb`, `rls`, `audit`,
  `tenant_id`, `nullable`, validators).
- Drivers: `pgx` (Postgres) and `sqlite` (modernc.org/sqlite, pure-Go).
- Migration engine with `MigrationUpTest`, `OpenAPIDriftTest`,
  `TSTypeDriftTest`. Dangerous operations are gated by `OGON-M0001`.
- RLS (row-level security), audit hooks, preload, scanner, raw escape
  hatch.
- Custom types: `UUID`, `Decimal`, `JSONB`, `Time`, `Enum`.

### Added — Auth (`auth`)

- Sessions (`auth/session`), tokens (`auth/token`), passwords
  (argon2id, `auth/password`), passkeys (WebAuthn, `auth/passkey`),
  OAuth (`auth/oauth`).
- MFA, FIPS-mode crypto, rate limit, lockout, PII redaction,
  supply-chain guards.
- CSRF, CSP, headers.

### Added — Authz (`authz`)

- RBAC, tenant scoping, policy DSL (`authz/policy`).

### Added — Realtime (`live`)

- Hub, channels, presence, per-message auth, backpressure, reconnect,
  multi-node coordination, semantics, heartbeat, observability hooks.

### Added — Jobs (`jobs`)

- Queues (in-proc, db `FOR UPDATE SKIP LOCKED`, redis `BRPOP`), retries
  with exponential backoff + jitter, DLQ, idempotency enforcer, cron
  with leader election, outbox relay, workflows, tenant scoping,
  poison quarantine, retry-storm guard, payload-key encryption
  (AES-256-GCM), timeout dispatch, fuzz targets.
- Throughput: ~17,800 jobs/sec/node vs the 167 jobs/sec budget —
  ~100× headroom.

### Added — UI (`ui`)

- Isomorphic components, compiler, tiny JS runtime, sanitize.
- Server-held state + tiny JS runtime (see
  [ADR 0003](./docs/adr/0003-ui-architecture.md)).
- `fullstack/` template ships `.ogon` components, SSR, hydration,
  forms.

### Added — Observability (`obs`)

- Structured JSON / text logger with `ParseLevel`, `ParseFormat`,
  `LoggerFromEnv`.
- PII redaction (email / ssn / cc / jwt / uuid / phone / ipv4 / apikey)
  — runtime + authoring-time lint + shared corpus
  (`test.RedactionCorpus()`).
- OpenTelemetry tracer with parent-based sampling (dev=1.0, prod=0.1),
  slow-span force-recording, traceparent roundtrip, span attr allowlist.
- Prometheus metrics with cardinality guard, HTTP middleware, probe
  exclusion, runtime snapshot.
- Health probes (liveness / readiness / startup / auth), check timeout.
- Crash reporter, OTLP / stdout / file exporters, log overhead budget
  gate (`OBS-044`), Grafana + Prometheus alert templates, OTel collector
  sample.

### Added — Infra (`infra`)

- Docker, compose, k8s, helm, terraform, cloudflare, secrets,
  deploy_guides, cost, env_matrix, idempotent, marker, release,
  security, backup, diff. `ogon infra gen <target>` and `ogon infra diff`.

### Added — Cache, Pub/Sub, DI, Config, Modules, MCP, Test

- `cache/` — pluggable cache interface.
- `pubsub/` — local + redis drivers.
- `di/` — generated DI container (see
  [ADR 0002](./docs/adr/0002-di-codegen.md)). Codegen, not reflection;
  manual mode (`BootOpts.Manual`) bypasses generation.
- `config/` — `ogon.yaml` walked up from cwd; env overlay
  (`ogon.<env>.yaml`); redact; interpolate; explain.
- `modules/` — pluggable module manifest; `ogon add` / `ogon remove`.
- `mcp/` — machine agent surface; `ogon agent dump` and `ogon agent serve`.
- `test/` — in-process App fixture, recorder client, auth/db fixtures,
  testcontainers (postgres / redis), fake clock, job runner, live
  recorder, WS / SSE clients, snapshot, golden routes, OpenAPI drift,
  authz matrix gen, playwright config, CI templates, fuzz seed registry,
  bench wrappers, load-script gen, naming conventions + table-test
  helper, minimal assert lib, tmp-dir/env/config/faker helpers,
  migration/drift tests, a11y/visual smoke gen, flaky quarantine +
  parallel-safe fixtures, PR checklist + GitHub Actions CI gen,
  mutation guide + property-test + time-dependent testing guide,
  query/cache/email/webhook/queue/audit asserts, shared redaction
  corpus, budget regression gate, memory-leak soak + chaos helpers,
  DX acceptance automation + testing guide.

### Added — documentation

- `README.md`, `llms.txt`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `SECURITY.md`, this `CHANGELOG.md`.
- `docs/quickstart.md` — 5-minute tour.
- `docs/guides/crud.md`, `auth.md`, `realtime.md`, `fullstack.md`,
  `deploy.md`.
- `docs/errors/OGON-{C,R,V,K,M,D,S,U,G}.md` — one page per error class.
- `docs/adr/0001-state-machine.md`, `0002-di-codegen.md`,
  `0003-ui-architecture.md`.
- `AGENTS.md` — repo map for AI agents.

### Documentation conventions (DOC-018)

Every doc page covers, in order: **what**, **when**, **quickstart**,
**config**, **test**, **prod**, **escape**, **troubleshoot**. Every
markdown file ends with the MIT license footer line.

### Internal

- License header (`SPDX-License-Identifier: MIT`) on every Go file.
- Per-phase agent work records in `agent-ctx/`.

---

## Release notes by phase

| Phase | Subsystem                  | Notes                                                            |
|-------|----------------------------|------------------------------------------------------------------|
| P0    | Environment setup         | Go 1.27.1, act, apptainer, spec downloaded.                     |
| P1    | Foundation                | go.mod, diag, runtime supervisor, license headers everywhere.    |
| P2    | Config                    | ogon.yaml loader, env overlay, redact, interpolate, explain.     |
| P3    | CLI root                  | ogon command tree, exit codes, JSON envelope, did-you-mean.       |
| P4    | HTTP                      | Typed router, middleware, ProblemDetails, SSE/WS, sync.Pool.     |
| P5    | Record (ORM)             | Models, migrations, RLS, audit, pgx + sqlite drivers.            |
| P6    | Auth                      | Sessions, OAuth, passkeys, MFA, FIPS, PII.                        |
| P7    | Pub/Sub                   | Local + redis drivers.                                            |
| P8    | Jobs                      | Queues, retries, DLQ, cron, outbox, workflows, tenant.            |
| P9    | Live (realtime)           | Hub, channels, presence, backpressure, reconnect.                |
| P10   | Observability             | Logs, traces, metrics, PII redaction, health.                    |
| P11   | Infra                     | Docker, compose, k8s, helm, terraform, cloudflare, secrets.     |
| P12   | Test DX                   | Fixtures, recorder, golden, drift, fuzz, CI templates, soak.      |
| P13   | UI                        | Isomorphic components, compiler, runtime, sanitize.              |
| P14   | Modules / MCP             | Module manifest, machine agent surface.                          |
| P15   | Fullstack template        | .ogon components, SSR, hydration, forms.                          |
| P16   | Benchmarks                | dx + perf suites; budget gates.                                  |
| P17   | Docs polish + verify      | README, llms.txt, CONTRIBUTING, COC, SECURITY, CHANGELOG, guides, |
|       |                            | error pages, ADRs, AGENTS.md, real-world CLI verification.        |

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

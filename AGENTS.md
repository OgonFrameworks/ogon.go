# AGENTS.md — repo map for AI agents

> If you are an AI agent (or a human reading this) working on this
> repository, this is the canonical map. It mirrors `llms.txt` but is
> organized for navigation, not for ingestion.

## Directory layout

```
ogon.go/                  # root package — App, Boot, Run, Stop, hooks
├── auth/                 # sessions, OAuth, passkeys, MFA, FIPS, PII
│   ├── oauth/            # OAuth2 providers
│   ├── passkey/          # WebAuthn
│   ├── password/         # argon2id
│   └── session/          # session manager
├── authz/                # RBAC, tenant scoping
│   └── policy/           # policy DSL
├── cache/                # cache interface (pluggable)
├── cli/                  # the ogon command tree
│   ├── cli.go            # root + Execute() + version + did-you-mean
│   ├── exit_codes.go     # normative exit code constants
│   ├── output.go         # JSON envelope + Table / KVList / Tree renderers
│   ├── suggest.go       # did-you-mean edit-distance matcher
│   ├── noninteractive.go # --yes prompt skipping
│   ├── commands_new.go     # ogon new
│   ├── commands_dev.go     # ogon dev
│   ├── commands_build.go   # ogon build
│   ├── commands_gen.go     # ogon gen <thing>
│   ├── commands_migrate.go # ogon migrate run|rollback|status|create|diff
│   ├── commands_doctor.go  # ogon doctor
│   ├── commands_explain.go # ogon explain <topic>
│   ├── commands_infra.go   # ogon infra gen|diff
│   ├── commands_testcmd.go # ogon test
│   └── commands_misc.go    # run / lint / fmt / check / add / remove /
│                          # update / db / routes / jobs / modules /
│                          # inspect / deploy / logs / health /
│                          # benchmark / docs / completion / agent
├── cmd/ogon/main.go      # process entrypoint; calls cli.Execute()
├── config/               # ogon.yaml loader + env overlay + redact
├── context/              # reference codebases (next.js, aspnetcore, spring) — DO NOT EDIT
├── di/                   # generated DI container (codegen)
├── diag/                 # structured diagnostics (OGON-<class><nnnn>)
├── fullstack/            # fullstack template
├── http/                 # typed router, middleware chain, ProblemDetails, SSE/WS
├── infra/                # docker, compose, k8s, helm, terraform, cloudflare
│   ├── compose/  docker/  k8s/  helm/  terraform/  cloudflare/
│   └── actions/          # GitHub Actions CI generation
├── internal/             # internal helpers (NOT public)
│   ├── diag/  errcodes/  explain/
├── jobs/                 # queues, retries, DLQ, cron, outbox, workflows, tenant
├── live/                 # hub, channels, presence, backpressure, reconnect
├── mcp/                  # machine agent surface (dump + serve)
├── minimal/  modular/  standard/  templates/   # project templates
├── modules/              # pluggable module manifest
├── obs/                  # logs, traces, metrics, PII redaction, health
├── pubsub/               # local + redis drivers
├── record/               # ORM, migrations, RLS, audit
│   └── types/            # UUID, Decimal, JSONB, Time, Enum
├── runtime/              # supervisor (goroutine ownership), limits, gc
│   ├── gc/  limits/
├── test/                 # in-process app fixture, recorder, golden, drift
├── ui/                   # isomorphic components, compiler, runtime, sanitize
│   ├── compiler/  runtime/  sanitize/
├── docs/
│   ├── quickstart.md
│   ├── guides/  crud.md  auth.md  realtime.md  fullstack.md  deploy.md
│   ├── errors/  OGON-{C,R,V,K,M,D,S,U,G}.md
│   └── adr/     0001-state-machine.md  0002-di-codegen.md  0003-ui-architecture.md
├── agent-ctx/            # per-phase agent work records (P2..P17)
├── examples/             # auth, crud, fullstack, realtime
├── ogon.go               # public entrypoint (App, Boot, Run, Stop, Hook, State)
├── ogon_test.go          # root-package tests
├── go.mod  go.sum
├── Makefile
├── README.md  CONTRIBUTING.md  CODE_OF_CONDUCT.md  SECURITY.md  CHANGELOG.md
├── llms.txt              # machine-readable summary (this file's sibling)
├── PROMPT.md             # the spec (read once)
├── features.md           # feature list
└── AGENTS.md             # THIS FILE
```

## Key public types (entry surface)

| Type                     | Package       | Purpose                                                  |
|--------------------------|---------------|----------------------------------------------------------|
| `ogon.App`               | `ogon`        | Root coordinator. `Boot(opts) → app; Run(ctx); Stop()`. |
| `ogon.BootOpts`          | `ogon`        | ConfigPath, Env, Logger, Parent, DrainTimeout, Manual.   |
| `ogon.State`             | `ogon`        | New|ConfigLoaded|...|Exited. See ADR 0001.                |
| `ogon.Hook`              | `ogon`        | {Name, OnStart, OnStop}.                                  |
| `runtime.Supervisor`     | `runtime`     | Spawn(name, Task); Stop; Close; owns every goroutine.     |
| `runtime.Task`           | `runtime`     | func(ctx) error.                                          |
| `diag.Diag`              | `diag`        | Code, Title, What, Why, Where, Fix, Remedy, Docs, Severity.|
| `diag.Severity`          | `diag`        | Info|Warning|Error.                                       |
| `http.Ctx`               | `http`        | JSON, HTML, Param, Bind, Problem, Tx.                     |
| `http.Server`            | `http`        | Register("METHOD /path", h); Use(mw); Handler().         |
| `record.BaseModel`       | `record`      | ID/CreatedAt/UpdatedAt/DeletedAt.                         |
| `record.Driver`          | `record`      | pgx + sqlite implementations.                             |
| `live.Hub`               | `live`        | Emit, Subscribe; broker for SSE/WS.                       |
| `live.Handle`            | `live`        | http.Handler that upgrades to WS/SSE.                     |
| `jobs.Queue`             | `jobs`        | Enqueue, Dequeue, Ack, Nack.                              |
| `jobs.Envelope`          | `jobs`        | Job envelope with Attempts, IdempotencyKey, TenantID.     |

## Common workflows

### 1. Add a route

```bash
ogon gen route orders --dry-run
ogon gen route orders
# edit routes/orders.go + handlers/orders.go
ogon dev
```

### 2. Add a CRUD resource

```bash
ogon gen resource User --dry-run
ogon gen resource User
ogon migrate run
ogon test
```

### 3. Add an auth provider

```bash
ogon gen auth email --dry-run
ogon gen auth email
# edit auth/email.go + routes/auth.go
ogon test --race
```

### 4. Add a background job

```bash
ogon gen job send-welcome-email --dry-run
ogon gen job send-welcome-email
# edit jobs/send-welcome-email.go
ogon dev    # the supervisor picks up the job
```

### 5. Add a UI component (fullstack template only)

```bash
ogon gen ui button --dry-run
ogon gen ui button
# edit ui/components/button.ogon
ogon dev    # compiler hot-reloads
```

### 6. Add an infrastructure target

```bash
ogon infra gen docker --dry-run
ogon infra gen docker
ogon infra diff    # empty = no drift
git add Dockerfile && git commit -m "chore(infra): ship dockerfile"
```

### 7. Deploy

```bash
ogon deploy --cloud aws --dry-run --verbose
ogon deploy --cloud aws --rollback
ogon health
```

## Agent-safe vs unsafe operations

### Agent-safe (always)

- `ogon --version` / `--help` / `explain` / `inspect` / `routes list`
- `ogon agent dump` — full machine context (manifest + diagnostics) in one JSON call (AGENT-002)
- `ogon agent validate` — manifest freshness check, no writes (AGENT-017)
- `ogon mcp` / `ogon agent serve` — MCP server over stdio; every tool is read-only or dry-run (AGENT-005)
- `ogon new <name> --template minimal|standard|modular --dry-run`
- `ogon gen <thing> <name> --dry-run` (plan only, no writes)
- `ogon migrate diff` / `status`
- `ogon doctor` (no `--fix`)
- `ogon fmt` (no `--fix`), `ogon lint`, `ogon check`
- `ogon test`, `ogon benchmark`
- `ogon infra diff`

### Agent-safe with care (verify intent first)

- `ogon new <name>` (without `--dry-run`) — writes files; safe if
  `name` is fresh and the dir is empty.
- `ogon gen <thing> <name>` (without `--dry-run`) — writes files;
  refuses on unowned files unless `--force`.
- `ogon build` — compiles; no mutation outside `bin/`.

### Agent-unsafe (require human approval)

- `ogon migrate run` / `rollback` — mutates the database.
- `ogon migrate create` — writes migration files (committed).
- `ogon deploy` — builds + pushes + applies to a cloud.
- `ogon infra gen` (without `--dry-run`) — writes committed files.
- `ogon db reset` — destructive.
- `ogon update` — AST-migrates project source.
- `ogon fmt --fix`, `ogon lint --fix`, `ogon doctor --fix`.
- `ogon add <module>` / `ogon remove <module>` — mutates manifest.
- `ogon agent dump --write` — persists `.ogon/ogon.json` (committed).
- `ogon agent validate --fix` — regenerates `.ogon/ogon.json` (committed); requires `--yes` in CI (AGENT-008).

**Golden rule**: every mutating command supports `--dry-run`; use
it before `--yes`.

## MCP tool catalog (AGENT-005)

The `ogon mcp` server (alias: `ogon agent serve`) speaks JSON-RPC 2.0
over stdio (protocol version `2024-11-05`). The tool set is read-only
plus deterministic dry-run by default — no destructive tool is exposed.
Agents that need to mutate use the CLI via the host shell, which
enforces `--yes` / `--dry-run` per AGENT-007/008.

| Tool           | Method                   | Behavior                                                            |
|----------------|--------------------------|--------------------------------------------------------------------|
| `ogon.explain` | `tools/call`             | Byte-stable explain output for a thing (route/model/config/magic). |
| `ogon.inspect` | `tools/call`             | Inspect live app state: `routes|models|config|runtime|modules`.    |
| `ogon.gen`     | `tools/call`             | Deterministic codegen plan; **always dry-run, never writes**.      |
| `ogon.doctor`  | `tools/call`             | Local-env checks with ok/warn/fail per check + remediation hint.   |
| `ogon.test`    | `tools/call`             | List tests that exercise a source file or route; **never runs**.   |

Server lifecycle: `initialize` → `notifications/initialized` →
`tools/list` → `tools/call` (repeat) → EOF or SIGTERM. Responses are
line-delimited JSON-RPC; the server preserves request order via a
synchronous dispatch loop.

## Agent surface versioning (AGENT-022)

The agent surface (`ogon agent dump` shape, `.ogon/ogon.json` schema,
`ogon mcp` tool catalog, `ogon explain` output contract) is
independently versioned from the framework via `agent.AgentSurfaceVersion`
(currently `1.0.0`). Adding a tool or a manifest field is a minor bump;
changing a tool's input shape or removing a field is a major bump.
Callers can verify compatibility with `agent.SurfaceCompat(expected)`.

## When you are stuck

1. **Read `PROMPT.md`** — the spec. It is the canonical reference.
2. **Read `llms.txt`** — the machine-readable summary.
3. **Run `ogon explain <topic>`** — the CLI self-documents concepts.
4. **Run `ogon doctor`** — surfaces env / deps / config drift.
5. **Run `ogon agent dump`** — emits the full machine context (manifest + diagnostics) in one JSON call.
6. **Read the relevant `docs/guides/*.md`**.
7. **Read the relevant `docs/errors/OGON-*.md`**.
8. **Read the relevant `docs/adr/*.md`** for the why behind a design.
9. **Read `agent-ctx/P<n>-*.md`** — the per-phase work records tell
   you what was built, why, and what ships in a later phase.

## License & conduct

- Every Go file has an `SPDX-License-Identifier: MIT` header.
- Every markdown file ends with the MIT footer line.
- See [CONTRIBUTING.md](./CONTRIBUTING.md) for the PR contract.
- See [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md) for conduct.
- See [SECURITY.md](./SECURITY.md) for vulnerability reporting.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

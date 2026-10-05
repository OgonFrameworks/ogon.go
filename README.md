# OgonGo

> A single-binary, batteries-included Go web framework for teams that ship
> production services — typed routing, generated DI, realtime, jobs, auth,
> observability, infra-as-code, and a CLI that owns the whole lifecycle.

[![Build Status](https://img.shields.io/badge/build-passing-brightgreen)](https://github.com/OgonFrameworks/ogon.go)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](./LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.27.1-00add8)](https://go.dev/dl/)
[![Version](https://img.shields.io/badge/version-1.0.0-blue)](#)

---

## Why OgonGo?

Most Go web stacks glue five libraries together and call it a framework.
OgonGo is one binary, one config file (`ogon.yaml`), one CLI (`ogon`), and
one import path (`github.com/OgonFrameworks/ogon.go`). It is opinionated
about the boring parts (auth, migrations, realtime, observability,
deployment) and silent about everything else.

- **Single static binary** — `ogon` owns codegen, build, dev, test, deploy.
- **One import path** — every public symbol lives under
  `github.com/OgonFrameworks/ogon.go/<subsystem>`.
- **Generated DI** — no reflection, no service-locator guessing; types are
  checked at compile time.
- **Stable exit codes & diagnostics** — every error has an `OGON-` code,
  a remedy, and a docs page. `ogon explain <code>` always works.
- **Server-held UI state** — small JS runtime, isomorphic components,
  zero hidden hydration thrash.
- **Subsystems grow independently** — every subsystem exposes a `Version`
  constant and a doc.go that documents its surface.

---

## Install

### Homebrew (macOS / Linux)

```bash
brew install ogonframeworks/tap/ogon
```

### Scoop (Windows)

```powershell
scoop bucket add ogonframeworks https://github.com/OgonFrameworks/scoop-bucket
scoop install ogon
```

### apt (Debian / Ubuntu)

```bash
curl -fsSL https://ogongo.dev/gpg.key | sudo gpg --dearmor -o /usr/share/keyrings/ogon.gpg
echo "deb [signed-by=/usr/share/keyrings/ogon.gpg] https://apt.ogongo.dev stable main" | sudo tee /etc/apt/sources.list.d/ogon.list
sudo apt update && sudo apt install ogon
```

### go install (anywhere with Go 1.27+)

```bash
go install github.com/OgonFrameworks/ogon.go/cmd/ogon@latest
```

### Verify

```bash
ogon --version          # ogon 1.0.0
ogon --version --json   # {"command":"ogon","status":"ok","data":{"version":"1.0.0"}}
ogon doctor             # environment + deps + config + ports diagnostics
```

---

## Quickstart (≤ 5 minutes)

```bash
# 1. Scaffold a new project (standard template = routes/, models/, jobs/)
ogon new myservice --template standard --git
cd myservice

# 2. Run the dev supervisor (watch + regen + rebuild + restart)
ogon dev
```

That is it — you now have a running OgonGo service on `:3000`.

### Write your first route

Create `routes/hello.go`:

```go
package routes

import (
    "net/http"
    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

func init() {
    ogonhttp.Register("GET /hello", func(c *ogonhttp.Ctx) error {
        return c.JSON(http.StatusOK, map[string]string{"hello": "world"})
    })
}
```

The dev supervisor picks up the new file, regenerates the route table, and
hot-reloads — no manual restart. Open <http://localhost:3000/hello>.

### Write your first resource

```bash
ogon gen resource User --dry-run   # show the plan
ogon gen resource User             # write model + route + handler + test
ogon migrate run                   # apply the schema
ogon test                          # go test + fixtures + optional junit
```

Done. You have a CRUD resource end-to-end.

---

## Feature overview

| Subsystem | Package | What it gives you |
|---|---|---|
| **App lifecycle** | `ogon` (root) | Boot, Run, Stop, hooks, state machine |
| **HTTP** | `http` | Typed router, fixed middleware chain, ProblemDetails, SSE/WS |
| **Record (ORM)** | `record` | Models, migrations, RLS, audit, query builder, drivers (pgx, sqlite) |
| **Auth** | `auth` | Sessions, OAuth, passkeys, MFA, FIPS, rate limit, lockout, PII |
| **Authz** | `authz` | RBAC, tenant scoping, policy engine |
| **Realtime** | `live` | Hub, channels, presence, per-message auth, backpressure |
| **Jobs** | `jobs` | Queues, retries, DLQ, idempotency, cron, outbox, workflows |
| **UI** | `ui` | Isomorphic components, compiler, tiny JS runtime, sanitize |
| **Observability** | `obs` | Structured logs, OTel traces, Prometheus metrics, PII redaction |
| **Infra** | `infra` | Docker, compose, k8s, helm, terraform, cloudflare, secrets |
| **Cache** | `cache` | Pluggable cache interface |
| **Pub/Sub** | `pubsub` | Local, Redis drivers |
| **DI** | `di` | Generated container (codegen, not reflection) |
| **Config** | `config` | `ogon.yaml` walked up from cwd, env overlay, redaction |
| **Modules** | `modules` | Pluggable module manifest |
| **Test** | `test` | In-process app fixture, recorder, golden, drift, redaction corpus |
| **Runtime** | `runtime` | Supervisor (goroutine ownership), limits, GC |
| **Diag** | `diag` | Structured, machine-remediable diagnostics (`OGON-<class><nnnn>`) |
| **CLI** | `cli` | `ogon` — codegen, build, dev, test, deploy, explain, doctor |
| **MCP** | `mcp` | Machine agent surface (dump, serve) |

---

## Documentation

- **[Quickstart](./docs/quickstart.md)** — 5-minute tour.
- **Guides** — [CRUD](./docs/guides/crud.md), [Auth](./docs/guides/auth.md),
  [Realtime](./docs/guides/realtime.md), [Full-stack UI](./docs/guides/fullstack.md),
  [Deploy](./docs/guides/deploy.md).
- **Errors** — one page per class: [C](./docs/errors/OGON-C.md),
  [R](./docs/errors/OGON-R.md), [V](./docs/errors/OGON-V.md),
  [K](./docs/errors/OGON-K.md), [M](./docs/errors/OGON-M.md),
  [D](./docs/errors/OGON-D.md), [S](./docs/errors/OGON-S.md),
  [U](./docs/errors/OGON-U.md), [G](./docs/errors/OGON-G.md).
- **ADRs** — [0001 state machine](./docs/adr/0001-state-machine.md),
  [0002 DI codegen](./docs/adr/0002-di-codegen.md),
  [0003 UI architecture](./docs/adr/0003-ui-architecture.md).
- **[CHANGELOG](./CHANGELOG.md)** — what changed, by phase.
- **[Contributing](./CONTRIBUTING.md)** — how to ship a PR.
- **[Security policy](./SECURITY.md)** — how to report a vulnerability.
- **[llms.txt](./llms.txt)** — machine-readable summary for AI agents.

The CLI itself is self-documenting:

```bash
ogon --help                  # top-level help
ogon explain route           # explain a concept
ogon explain exit-codes      # list every normative exit code
ogon inspect runtime         # live runtime snapshot
ogon doctor                  # environment + deps + config diagnostics
ogon docs <query>            # open / search local docs
```

---

## Project layout

```
ogon.go/                  # root package — App, Boot, Run, Stop, hooks
├── auth/                 # sessions, OAuth, passkeys, MFA, FIPS, PII
├── authz/                # RBAC, tenant scoping
├── cache/                # cache interface
├── cli/                  # the `ogon` command tree
├── cmd/ogon/             # main.go entrypoint
├── config/               # ogon.yaml loader, env overlay, redaction
├── context/              # reference codebases (Next.js, aspnetcore, spring)
├── di/                   # generated DI container
├── diag/                 # structured diagnostics (OGON-* codes)
├── fullstack/            # fullstack template
├── http/                 # typed router, middleware, SSE/WS
├── infra/                # docker, compose, k8s, helm, terraform, cloudflare
├── jobs/                 # queues, retries, DLQ, cron, outbox, workflows
├── live/                 # hub, channels, presence, backpressure
├── mcp/                  # machine agent surface
├── modules/              # pluggable modules
├── obs/                  # logs, traces, metrics, PII redaction
├── pubsub/               # local, redis drivers
├── record/               # ORM, migrations, RLS, audit
├── runtime/              # supervisor, limits, gc
├── test/                 # in-process fixture, recorder, golden, drift
├── ui/                   # isomorphic components, compiler, runtime, sanitize
├── docs/                 # quickstart, guides, errors, ADRs
└── agent-ctx/            # per-phase agent work records
```

---

## License

MIT — see [LICENSE](./LICENSE). Every source file carries an
`SPDX-License-Identifier: MIT` header. This README is MIT-licensed.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

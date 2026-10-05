# Quickstart

> **Goal**: from zero to a running OgonGo service with one route and one
> passing test in under 5 minutes.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo is a single-binary Go web framework. The `ogon` CLI owns the whole
project lifecycle: scaffolding, dev server, code generation, build, test,
infra-as-code, and deploy. You write Go; the CLI writes the glue.

## When

Use OgonGo when you want:

- a typed router and middleware chain instead of `if r.URL.Path == ...`;
- generated DI instead of `reflect`-based injection;
- one config file walked up from the current dir;
- realtime (SSE / WS), jobs, auth, observability, and infra-as-code in
  one import path;
- a CLI that prints diagnostics with `OGON-` codes and remedies, so the
  next agent / human can fix the problem without re-reading the source.

If you want a thin layer over `net/http` and nothing else, OgonGo is
overkill.

## Quickstart

```bash
# 1. install (any one)
brew install ogonframeworks/tap/ogon          # macOS / Linux
scoop install ogon                            # Windows
go install github.com/OgonFrameworks/ogon.go/cmd/ogon@latest   # any Go ≥ 1.27

# 2. verify
ogon --version          # ogon 1.0.0
ogon doctor             # environment + deps + config + ports

# 3. scaffold a project
ogon new myservice --template standard --git
cd myservice

# 4. run the dev supervisor (watch + regen + rebuild + restart)
ogon dev
```

You now have a service on `http://localhost:3000`. The dev supervisor:

- watches `*.go`, `ogon.yaml`, `routes/`, `models/`, `jobs/`;
- regenerates the route table + DI container on change;
- rebuilds and gracefully restarts the binary;
- streams structured logs to stderr.

### Write your first route

Create `routes/hello.go`:

```go
package routes

import (
    "net/http"

    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

func init() {
    ogonhttp.Register("GET /hello", hello)
}

func hello(c *ogonhttp.Ctx) error {
    return c.JSON(http.StatusOK, map[string]string{"hello": "world"})
}
```

The supervisor picks it up within 100ms and re-serves. `curl :3000/hello`
returns `{"hello":"world"}`.

### Generate your first resource

```bash
ogon gen resource User --dry-run   # show the plan
ogon gen resource User             # write model + route + handler + test
ogon migrate run                   # apply the schema
ogon test                           # green
```

Done. You have a CRUD resource end-to-end with tests.

## Config

`ogon.yaml` is the project's declarative root:

```yaml
project: myservice
template: standard
database:
  driver: sqlite                  # sqlite|postgres|mysql|none
  url: file:./ogon.db             # or postgres://...@host/db?sslmode=...
version: "1.0.0"

http:
  addr: ":3000"
  read_timeout: 5s
  write_timeout: 30s

auth:
  session:
    secret_env: OGON_SESSION_SECRET
  rate_limit:
    per_ip: 60/min

obs:
  log_level: info
  trace_ratio: 0.1

features:
  - oauth
  - passkey
```

- Walked up from the cwd until found; `--project <dir>` overrides.
- Environment overlay: `ogon.<env>.yaml` (e.g. `ogon.prod.yaml`).
- Secrets via `*_env` keys — never inline.
- Inspect: `ogon inspect config`. Validate: `ogon check`.

## Test

```bash
ogon test                           # go test + fixtures, no junit
ogon test --race --json             # race + JSON output for CI
ogon test -run TestUser             # filter
ogon test --junit ./build/junit.xml # emit junit XML
```

The in-process App fixture lives in the `test` package:

```go
func TestHello(t *testing.T) {
    app := test.NewApp(t, myHandler)
    defer app.Close()

    r := app.Recorder().Get("/hello")
    r.AssertStatus(t, http.StatusOK)
    r.AssertJSON(t, `{"hello":"world"}`)
}
```

Snapshots, golden files, OpenAPI drift, authz matrix tests, redaction
corpus, memory-leak soak, fuzz seed registry — all in the `test` package.

## Prod

```bash
ogon build                          # codegen → fmt → compile, stamps version
ogon deploy --cloud aws             # build → push → apply → verify
ogon deploy --cloud aws --rollback  # rollback on failure
ogon infra gen docker                # emit Dockerfile + compose
ogon infra gen k8s                   # emit k8s manifests
ogon deploy --dry-run --cloud gcp   # plan only, no mutation
```

The deploy pipeline ships deterministic artifacts; the rollback flag
restores the previous release automatically if a probe fails. See
[docs/guides/deploy.md](./guides/deploy.md).

## Escape

- **Manual DI**: set `BootOpts{Manual: true}` to bypass the generated
  container and wire providers in `main.go` directly.
- **Raw SQL**: `record.Raw(...)` and `record.RawTx(...)` escape the
  query builder without leaving the transaction.
- **HTTP escape hatch**: `Server.Handler()` returns the underlying
  `http.Handler` so you can plug OgonGo into an existing `net/http`
  server.
- **Plain Go**: any package in the framework can be imported and used
  without the CLI — the CLI is a convenience, not a runtime dependency.

## Troubleshoot

| Symptom                                           | Fix                                                  |
|---------------------------------------------------|------------------------------------------------------|
| `ogon doctor` exits 8                             | Read the per-check `→` remedy; `--fix` runs it.      |
| `ogon new testapp --template minimal --no-git`    | There is no `--no-git` flag — omit `--git` (default false). |
| `OGON-K0001: not in an ogon project`              | `cd` to the project root, or pass `--project <dir>`. |
| `OGON-G0001: unowned file` (exit 5)               | `ogon gen --force`, or move/delete the file.         |
| `OGON-M0001: migration unsafe` (exit 4)            | Read the destructive-op detection; pass `--yes`.     |
| `ogon dev` does not pick up changes                | Check `ogon.yaml` is in the cwd's ancestor path.     |
| `ogon gen resource User` wrote files I did not want| Re-run with `--dry-run` next time; restore from git. |

Run `ogon explain <topic>` for structured help on any concept; `ogon
explain exit-codes` lists every normative exit code.

---

Next: [CRUD guide](./guides/crud.md), [Auth guide](./guides/auth.md),
[Realtime guide](./guides/realtime.md),
[Full-stack guide](./guides/fullstack.md), [Deploy guide](./guides/deploy.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

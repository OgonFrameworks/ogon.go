# Tutorial — Hello World in 5 minutes

> **Goal**: from a clean machine to a running OgonGo service that responds
> `{"hello":"world"}` to `GET /hello`. Along the way you will install the
> CLI, scaffold a project, run the dev supervisor, write a typed route,
> and shut it down gracefully.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

A "hello world" in OgonGo is one `ogon new` plus one Go function. The CLI
owns project layout, dev-server lifecycle, code generation, and graceful
shutdown. You write Go that takes a typed `*ogonhttp.Ctx` and returns an
`error`; the framework does the rest.

## When

This tutorial is the first thing to read when:

- you have never run an OgonGo project before;
- you want to verify the install works (the binary, the toolchain, the
  network, the port — all green before you write real code);
- you want to see the dev supervisor's watch + reload loop in action;
- you want a known-good baseline before `ogon gen resource`.

If you already have an OgonGo project on disk, jump to
[the CRUD tutorial](./crud.md) instead.

## Quickstart

### 1. Install the CLI

Pick one — they all install the same `ogon` binary on `PATH`:

```bash
# macOS / Linuxbrew
brew install ogonframeworks/tap/ogon

# Windows
scoop install ogon

# Any platform with Go >= 1.27.1
go install github.com/OgonFrameworks/ogon.go/cmd/ogon@latest
```

Verify:

```bash
ogon --version          # ogon 1.0.0
ogon doctor             # env + deps + config + ports
```

`ogon doctor` exits 0 if everything is wired. Exit 8 means a check
failed — the per-check `->` line tells you exactly what to do, and
`--fix` runs the remedy.

### 2. Scaffold a project

```bash
ogon new hello --template minimal --git
cd hello
```

The `minimal` template writes:

```
hello/
├── go.mod
├── ogon.yaml
├── main.go
├── routes/
│   └── health.go
├── README.md
└── .gitignore
```

No YAML, no env, no database. The framework's zero-config default app
binds `:3000`, serves `/healthz`, and traps SIGTERM.

### 3. Run the dev supervisor

```bash
ogon dev
# -> ogon dev: watching ./... (regen + rebuild + restart)
# -> ogon dev: listening on http://localhost:3000
```

In another shell:

```bash
curl :3000/healthz
# {"status":"ok","version":"1.0.0"}
```

### 4. Write your first route

Create `routes/hello.go`:

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Hello-world route registration.

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

The supervisor picks it up within 100 ms and re-serves:

```bash
curl :3000/hello
# {"hello":"world"}
```

### 5. Stop it

`Ctrl-C` or `kill -TERM $(pgrep ogon-dev)`. The supervisor drains
in-flight requests, closes the listener, runs `OnStop` hooks, and
exits 0 within `drain_timeout` (default 20 s).

## Config

`ogon.yaml` is intentionally tiny for the `minimal` template:

```yaml
project: hello
template: minimal
version: "1.0.0"

http:
  addr: ":3000"
  read_timeout: 5s
  write_timeout: 30s

obs:
  log_level: info
```

You do not need to touch it for this tutorial. To switch ports:

```bash
OGON_HTTP_ADDR=:4000 ogon dev
```

## Test

OgonGo ships an in-process app fixture that does not require a port:

```go
// routes/hello_test.go
package routes

import (
    "net/http"
    "testing"

    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
    "github.com/OgonFrameworks/ogon.go/test"
)

func TestHello(t *testing.T) {
    app := test.NewApp(t, ogonhttp.Handler())
    defer app.Close()

    r := app.Recorder().Get("/hello")
    r.AssertStatus(t, http.StatusOK)
    r.AssertJSON(t, `{"hello":"world"}`)
}
```

Run:

```bash
ogon test                    # go test + fixtures
```

## Prod

```bash
ogon build                   # codegen -> fmt -> compile, stamps version
./hello                      # run the built binary
ogon infra gen docker        # emit Dockerfile + compose
ogon deploy --cloud aws      # build -> push -> apply -> verify
```

The built binary has the same wiring as `ogon dev`; only log format and
reload differ (AT-018).

## Escape

- **Plain Go**: any package in the framework can be imported and used
  without the CLI — the CLI is a convenience, not a runtime dependency.
- **HTTP escape hatch**: `Server.Handler()` returns the underlying
  `http.Handler` so you can plug OgonGo into an existing `net/http`
  server.
- **Manual DI**: set `BootOpts{Manual: true}` to bypass the generated
  container and wire providers in `main.go` directly.

## Troubleshoot

| Symptom                                       | Fix                                                       |
|-----------------------------------------------|-----------------------------------------------------------|
| `ogon` not on PATH                            | Re-run `go install ...@latest`; check `$(go env GOPATH)/bin`. |
| `ogon doctor` exits 8                         | Read the per-check `->` remedy; `--fix` runs it.          |
| Port `:3000` already in use                    | `OGON_HTTP_ADDR=:4000 ogon dev`, or stop the other process. |
| `curl :3000/hello` returns 404                 | `routes/hello.go` saved in the wrong package, or `ogon dev` not running. |
| `Ctrl-C` does not exit cleanly                 | Drain is stuck on a long-running hook; check `ogon inspect runtime`. |

---

Next: [CRUD tutorial](./crud.md), [Auth tutorial](./auth.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

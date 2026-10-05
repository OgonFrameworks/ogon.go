# Style guide

> **Goal**: the code and docs style rules every OgonGo file follows.
> Enforced by `ogon fmt`, `ogon lint`, `ogon check`, and reviewers.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo's style law (Part 0 rules 2–8, Part XVIII):

### Go

- **License header** on every `.go` file, verbatim:
  ```go
  // SPDX-License-Identifier: MIT
  // Copyright (c) 2026 OgonFrameworks. All rights reserved.
  //
  // <one-line description of the subsystem this file provides>
  ```
- **`gofmt`** + **`go vet`** + **`golangci-lint`** (approved set:
  `govet, staticcheck, errcheck, gosimple, ineffassign, unused,
  gocritic, gosec, bodyclose, rowserrcheck, noctx, prealloc,
  copyloopvar`). Zero warnings.
- **Custom lints**: no `go` statements without supervisor; no `any`
  at boundaries; no `fmt.Sprintf` in hot paths; gen-marker integrity.
- **No panics in library code.** Panics only for programmer-error
  invariants, and only behind `ogon/internal`.
- **No `interface{}` / `any` at public API boundaries** where generics
  or concrete types work. `any` is permitted only for codec
  boundaries (JSON payloads) and declared plugin seams.
- **No reflection in hot paths** (router, ORM scanning,
  serialization, DI runtime). Reflection is allowed only inside `ogon
  build` code generators.
- **No goroutine leaks.** Every spawned goroutine is owned by
  `runtime.Supervisor`.
- **Comments** are for invariants that cannot be expressed in types.
  No narration comments (`// increment i` is a firing offense).
- **Commit style**: conventional commits; DCO not required (MIT org).

### Docs

- **License footer** on every markdown file:
  ```markdown
  <!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->
  ```
- **Standard feature-page sections** (DOC-018): what, when,
  quickstart, config, test, prod, escape, troubleshoot.
- **No marketing** — engineering law only (Part 0 rule 12).
- **Code blocks** are complete and copy-pasteable; never partial
  snippets that need context the reader does not have.
- **Tables** for tabular data; **diagrams** (Mermaid) for structure.

## When

- Before opening a PR.
- Before adding a new subsystem.
- When reviewing a PR.

## Quickstart

```bash
ogon fmt            # gofmt + gen-fmt (--fix to apply)
ogon lint           # golangci-lint (--fix for autofixes)
ogon check          # CI gate: vet + gen-up-to-date + config-valid
```

## Config

`.golangci.yml` at the repo root pins the approved linter set. Do not
weaken it.

## Test

CI runs `ogon fmt --check` (would it change files?), `ogon lint`, and
`ogon check`. All three must pass.

## Prod

The style is enforced on every PR; there is no "prod-only" relaxation.

## Escape

- **Lint waiver**: file an issue with a justification memo; the
  waiver is recorded in `.golangci.waivers.yml` and reviewed per
  release.
- **Custom linter**: drop a Go file in `tools/linters/` implementing
  the `analysis.Analyzer` interface; `ogon lint` will pick it up.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon fmt --check` fails               | Run `ogon fmt --fix` and commit.                               |
| `ogon lint` flags a `go` statement     | Route through `Supervisor.Spawn`, or file a waiver.            |
| `ogon check` fails on gen-up-to-date   | Run `ogon build` to regenerate, then commit.                   |
| Reviewer says "no `any` at boundary"    | Use a concrete type or a type parameter.                       |

---

See also: [`/CONTRIBUTING.md`](../CONTRIBUTING.md),
[Docs style guide](./docs-style-guide.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

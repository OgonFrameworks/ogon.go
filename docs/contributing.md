# Contributing (docs mirror)

> The canonical contributing guide lives at
> [`/CONTRIBUTING.md`](../../CONTRIBUTING.md). This page is the
> docs-side mirror so the docs site search indexes it.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo is opinionated on purpose, and contributions that sharpen
those opinions (or fix bugs that violate them) are the most welcome.
The contributing guide is the contract for every PR.

## When

- Before opening your first PR.
- Before adding a new subsystem.
- Before bumping the framework version.

## Quickstart

```bash
# 1. branch from main
git checkout -b feat/http-xforwarded-for

# 2. write code + tests + docs
# 3. run the gate
ogon fmt && ogon lint && ogon check && ogon test && go vet ./...

# 4. commit (conventional commits, signed)
git commit -S -m "feat(http): add X-Forwarded-For trust list"

# 5. push and open a PR
```

## Config

No config — the contributing guide is the contract.

## Test

CI re-runs the gate (`ogon fmt && ogon lint && ogon check && ogon
test && go vet ./...`). A CI failure you did not see locally means
your local env has drifted; run `ogon doctor`.

## Prod

A passing local run is the contract. Reviewers apply the checklist
verbatim (see [`/CONTRIBUTING.md`](../../CONTRIBUTING.md) §3).

## Escape

- **Skip a check**: not allowed for merged PRs. For previews, mark
  the PR as `draft:` and the CI gate is advisory.
- **Bring your own lint**: drop a `.golangci.yml` and the CI picks it
  up; do not weaken the approved set (Part XVIII).

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon check` fails on `gen-up-to-date`| Run `ogon build` to regenerate, then commit.                   |
| `ogon lint` flags a `go` statement    | Route it through `Supervisor.Spawn`; if impossible, file a waiver.|
| `ogon test` is flaky                  | Use `test.FlakyQuarantine`; do not disable.                    |

---

See also: [`/CONTRIBUTING.md`](../../CONTRIBUTING.md),
[Code of Conduct](../CODE_OF_CONDUCT.md), [Style guide](./style-guide.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

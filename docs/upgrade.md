# Upgrade guide

> **Goal**: keep an OgonGo project on the latest stable release with
> minimal manual work. This page is the human-readable companion to
> `ogon update`.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

`ogon update` is the CLI's upgrade engine (Part XIX.4). It produces an
**upgrade plan** for every release bump:

1. **Version-distance report** — what's old, what's new.
2. **Breaking-change list** — every breaking change between the two
   versions, with codemod availability.
3. **AST codemods** — applied mechanically where possible.
4. **Config schema migration** — `ogon.yaml` keys renamed/moved.
5. **Infra diff** — generated Dockerfile / k8s / Terraform changes.
6. **DB migration plan** — pending schema changes (you run them with
   `ogon migrate run`, not `ogon update`).
7. **Residual manual items** — with docs links.

`--diff-only` prints the plan; `--yes` applies it.

## When

- Before bumping the framework version in `go.mod`.
- When a new minor is released.
- When a deprecation window closes (Part XIX.5: 12 months minimum).

## Quickstart

```bash
# 1. pin the new version
go get github.com/OgonFrameworks/ogon.go@v1.0.0

# 2. plan
ogon update --diff-only

# 3. apply
ogon update --yes

# 4. verify
ogon check && ogon test && ogon build
```

## Config

`ogon update` reads `ogon.json` (framework version pin) and
`go.mod`. No additional config needed.

## Test

`ogon update --diff-only` exits 0 if the plan is empty. The
`test.MigrationCorpus` runs `ogon update` against a set of seeded
old-version projects and asserts the result is clean. AT-015.

## Prod

1. `ogon check` — config valid, gen up-to-date.
2. `ogon test` — all tests pass.
3. `ogon build` — binary builds.
4. `ogon deploy --cloud aws --rollback` — ship with auto-rollback.

DB migrations are applied via `ogon migrate run` in the migration
Job, **not** at boot.

## Escape

- `ogon update --skip-codemod <name>` — skip a specific codemod.
- Drop a Go file in `tools/codemods/` implementing `update.Codemod`
  to bring your own.
- `git reset --hard <sha>` to roll back; the framework never mutates
  the DB during `ogon update`.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon update` reports 0 changes         | Already on latest; check `ogon --version`.                     |
| Codemod fails to apply                  | Read the error; `git restore`, apply by hand.                  |
| Manual step link is broken              | File a docs bug; the link is auto-generated.                   |
| `ogon test` fails after update          | Codemod was incomplete; read the diff and finish by hand.      |

---

See also: [Migration: 0.x to 1.0](./migration/0.x-to-1.0.md),
[Changelog](./changelog.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

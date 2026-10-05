# Error codes — index

> **Goal**: every `OGON-<class><nnnn>` code in one place, with its
> title, exit code, and a link to the per-class page.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo's diagnostic scheme is `OGON-<class><nnnn>` (Part II.7). Every
diagnostic carries:

- `Code` — `OGON-<class><nnnn>` (stable, never reassigned).
- `Title` — short human label.
- `What` — what happened.
- `Why` — why the framework thinks it happened.
- `Where` — file:line where the framework detected it.
- `Fix` — a shell command or one-line remedy.
- `Remedy` — longer prose remedy.
- `Docs` — link to the per-class page.
- `Severity` — `Info` | `Warning` | `Error`.

Classes:

| Class | Letter | Page                          |
|-------|--------|-------------------------------|
| Compile | C    | [`OGON-C.md`](../errors/OGON-C.md) |
| Route   | R    | [`OGON-R.md`](../errors/OGON-R.md) |
| Validation | V | [`OGON-V.md`](../errors/OGON-V.md) |
| Config  | K    | [`OGON-K.md`](../errors/OGON-K.md) |
| Migration | M  | [`OGON-M.md`](../errors/OGON-M.md) |
| Dependency | D | [`OGON-D.md`](../errors/OGON-D.md) |
| Security | S   | [`OGON-S.md`](../errors/OGON-S.md) |
| Runtime | U    | [`OGON-U.md`](../errors/OGON-U.md) |
| Generation | G | [`OGON-G.md`](../errors/OGON-G.md) |

## When

- You hit an error you do not recognize.
- You are reviewing a PR that adds a new error path.
- You are writing a tutorial and want to link to a specific code.

## Quickstart

```bash
# list every code
ogon explain diag

# look up one code
ogon explain diag | grep OGON-R0001
# OGON-R0001  Route conflict  — two routes registered the same method+path.

# from your shell
ogon explain exit-codes | grep 4
#   4  MigrationUnsafe  — destructive migration blocked without --yes
```

Every diagnostic in CLI output and HTTP responses carries the code
inline; you do not need to grep the docs to find it.

## Config

No config — the code scheme is fixed.

## Test

`diag/codes/codes_test.go` asserts every code is registered, has a
title, a remedy, and a docs link. DX-008/DX-014: every failure mode
has a next action.

## Prod

Codes are stable across releases (Part XIX.1). A code is never
reassigned; a deprecated code is marked `Deprecated` in the table and
kept in the registry forever.

## Escape

If a diagnostic lacks a remedy, file a bug — that is a regression
(DX-014).

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon explain diag` does not list a code | The code is internal-only; check the per-class page.          |
| A diagnostic has no `remedy` field     | File a bug; every diagnostic must have one (DX-014).           |
| Two codes have the same number          | Impossible — the registry enforces uniqueness at build time.   |

---

See also: per-class pages linked above, [DX-008 doctor
completeness](../howto/debug.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

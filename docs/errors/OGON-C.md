# Error class `OGON-C` — Compile / type-system errors

> Diagnostics that fire at build time, when generated code disagrees with
> the source it was derived from, or when the framework cannot produce a
> compilable program.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-C` diagnostics are emitted by `ogon build` (and `ogon check` in
CI). They prevent a broken build from reaching `ogon deploy`. The
"compile" class is broader than just `go build`: it also covers the
codegen step that produces the DI container, the route table, and the
migration runners.

| Code         | Title                                | Exit | When                                                                  |
|--------------|--------------------------------------|------|------------------------------------------------------------------------|
| OGON-C0001   | `go build` failed                    | 7    | The generated program does not compile.                                |
| OGON-C0002   | Unknown command / flag / gen kind    | 2    | The user typed something the CLI does not recognize.                    |
| OGON-C0003   | Type mismatch in DI provider        | 7    | A provider's return type does not satisfy the consumer's parameter.    |
| OGON-C0004   | Generated file out of date           | 7    | `ogon check` detected the committed file is older than its source.    |
| OGON-C0005   | Cycle in DI graph                    | 7    | Provider A depends on B which depends on A.                            |
| OGON-C0006   | Missing import path                 | 7    | A generated reference points at a package that does not exist.         |

---

## When

`OGON-C` fires when:

- you run `ogon build` and the source + generated code disagree;
- you run `ogon check` in CI and the committed generated files are stale;
- you run `ogon gen` and the plan would produce uncompilable code;
- you mistype a command / flag / gen kind.

It does **not** fire at request time — runtime errors are `OGON-U`.

---

## Examples

### OGON-C0001 — `go build` failed

```
[OGON-C0001] go build failed
  what:    ./routes/orders.go:23:10: undefined: handlers.List
  why:     handlers.List is referenced but not defined
  where:   routes/orders.go:23
  fix:     define handlers.List, or run `ogon gen route orders --force`
  docs:    docs/errors/OGON-C.md
```

### OGON-C0002 — Unknown command / flag / gen kind

```bash
$ ogon gen resrc User
[OGON-C0002] unknown gen kind
  what:    "resrc" is not a known generator
  expected: resource|model|route|job|auth|ui|client|openapi|test|module
  found:   resrc
  fix:     did you mean: resource
```

### OGON-C0004 — Generated file out of date

```bash
$ ogon check
[OGON-C0004] generated file out of date
  what:    di/container.go is older than routes/orders.go
  why:     the DI container must be regenerated when routes change
  fix:     run `ogon build` or `ogon gen di`
```

---

## Remedy

1. **Read the diagnostic** — every `OGON-C` carries `what`, `why`,
   `where`, `fix`. The `fix` line is a shell command you can paste.
2. **Apply the fix** — most `OGON-C` fixes are `ogon build`, `ogon gen
   <thing>`, or a hand edit the diagnostic names.
3. **Re-run `ogon check`** — CI gate; a clean local run means a clean
   CI run.

If the fix line says "ship in a later phase", the diagnostic is `info`
severity and the failure is non-blocking.

---

## Escape

- **Manual mode**: `BootOpts{Manual: true}` skips the generated DI
  container; you wire providers in `main.go`. This bypasses
  `OGON-C0003`, `OGON-C0005`, `OGON-C0006` entirely.
- **Skip generation**: write the file by hand; the CLI will not touch
  it unless you pass `--force`. The CLI marks hand-written files as
  "unowned" and refuses to overwrite them (`OGON-G0001`).
- **Disable the route table generator**: set `gen.routes.enabled:
  false` in `ogon.yaml`; you register routes manually.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| `OGON-C0004` after a fresh clone                      | `ogon build` once to regen; commit the result.                |
| `OGON-C0005` cycle                                   | Refactor one provider to take a smaller dep; or break the cycle with a lazy provider. |
| `OGON-C0006` missing import                          | `go get <pkg>` then `ogon build`.                              |
| Fix line says "ship in a later phase"                 | The diagnostic is `info` severity; non-blocking.              |

---

Next: [OGON-R route errors](./OGON-R.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

# Contributing to OgonGo

First: thank you. OgonGo is opinionated on purpose, and contributions that
sharpen those opinions (or fix bugs that violate them) are the most welcome.

This document is the contract for every PR. Read it once; it stays stable.

---

## 1. Branch & commit

1. **Branch from `main`.** Never branch from a feature branch unless you
   explicitly need that feature's unstable surface.
2. **One concern per PR.** Mixing a bugfix with a refactor with a feature
   makes review impossible.
3. **Conventional commits, enforced by `ogon check` in CI:**
   - `feat(http): add X-Forwarded-For trust list`
   - `fix(record): correct sqlite savepoint rollback order`
   - `docs(adr): add 0004 cache eviction`
   - `chore(deps): bump pgx to v5.12`
   - `refactor(obs): move redaction into shared corpus`
4. **Sign your commits.** `git commit -S` with your GPG / SSH key.
   Unsigned commits are CI-passable but won't be merged into release
   branches. See <https://git-scm.com/book/en/v2/Git-Basics-Signing-Work>.
5. **Keep history linear.** Rebase onto `main` before pushing; the
   maintainer will squash-merge PRs that touch a single concern.

---

## 2. Before you push

Run, in order, from the project root:

```bash
ogon fmt            # gofmt + gen-fmt (use --fix to apply)
ogon lint           # golangci-lint (use --fix for autofixes)
ogon check          # CI gate: vet + gen-up-to-date + config-valid
ogon test           # go test + fixtures
go vet ./...        # belt-and-suspenders
```

A passing local run is the contract. CI re-runs all of the above; a CI
failure that you did not see locally means your local env has drifted.

---

## 3. Code review checklist

Reviewers apply this list verbatim. Address each item before requesting
re-review.

- [ ] **License header** — every new `.go` file starts with
      `// SPDX-License-Identifier: MIT` and the
      `Copyright (c) 2026 OgonFrameworks. All rights reserved.` line.
- [ ] **No goroutine leaks** — every `go func()` is either routed through
      `runtime.Supervisor.Spawn(name, task)` or bounded by a `done` channel
      with a documented teardown.
- [ ] **No PII in span attrs / log fields** — use the shared
      `test.RedactionCorpus()` to prove a new attr is safe; runtime redaction
      is the last line of defense, not the first.
- [ ] **Bounded metric labels** — high-cardinality values (user_id,
      trace_id, tenant_id, raw_path) are never metric labels; use the
      `route_template` form.
- [ ] **Stable exit codes** — new failure modes get an `OGON-<class><nnnn>`
      code, a remedy string, and an entry in the relevant
      `docs/errors/OGON-*.md` page.
- [ ] **Deterministic generation** — same input → same bytes. If your
      generator emits a timestamp, it is opt-in via a flag, not default.
- [ ] **Latency law** — non-generating CLI commands complete in ≤ 100ms
      p95; the cold path of every HTTP handler does not allocate in the
      hot path (use `sync.Pool` where the `http` package already does).
- [ ] **Tests** — every public function ships with table tests; new PII
      shapes add an entry to the shared redaction corpus.
- [ ] **Docs** — every new public package gets a `doc.go` and an entry in
      `llms.txt`; every new error class gets a `docs/errors/OGON-*.md` page.
- [ ] **No reflection DI** — new providers wire through the generated
      container or the `Provide` escape hatch (DX-P4). No `reflect`-based
      injection.
- [ ] **Migration safety** — destructive migrations are gated by
      `OGON-M0001` and require `--yes`; the down migration is always
      reversible or refuses to ship.

---

## 4. Conventional commits — full grammar

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

| `type`    | when                                                |
|-----------|-----------------------------------------------------|
| `feat`    | new feature visible to users or downstream packages |
| `fix`     | bug fix                                             |
| `docs`    | documentation only                                  |
| `refactor`| no behavior change                                  |
| `perf`    | benchmark-measured improvement                      |
| `test`    | tests only                                          |
| `chore`   | build, deps, tooling                                |
| `revert`  | revert a previous commit                            |

`scope` is the subsystem name (`http`, `record`, `auth`, `jobs`, `live`,
`obs`, `infra`, `test`, `ui`, `cli`, `config`, `di`, `diag`, `runtime`,
`modules`, `mcp`, `pubsub`, `cache`, `authz`). Unknown scopes fail `ogon check`.

**Breaking changes** bump the footer:

```
feat(http): replace Context.Logger with *slog.Logger

BREAKING CHANGE: Context.Logger returns *slog.Logger, not the wrapper type.
```

---

## 5. License header requirement

Every `.go` file (including `_test.go`, `doc.go`, `fuzz.go`) begins with:

```go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// <one-line subsystem summary>
```

Markdown files (this one, README, guides, ADRs, error pages) end with a
footer line:

```
<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->
```

---

## 6. PR template (auto-filled by `ogon` in a later phase)

```
## What
<one paragraph>

## Why
<one paragraph; reference the issue or ADR>

## How
<bulleted walkthrough of the diff>

## Verification
- [ ] ogon fmt
- [ ] ogon lint
- [ ] ogon check
- [ ] ogon test
- [ ] go vet ./...
- [ ] docs updated (llms.txt + relevant guide / error page / ADR)
```

---

## 7. Becoming a maintainer

There is no formal process. Sustained, high-quality contributions across at
least three subsystems — plus a record of careful review on others' PRs —
will get you invited to the maintainers team. We name maintainers in
`CHANGELOG.md` per release.

---

## 8. Code of conduct

Participation in this project is governed by the
[Contributor Covenant 2.1](./CODE_OF_CONDUCT.md). Be excellent.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

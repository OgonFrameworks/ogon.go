# Error class `OGON-D` — Dependency errors

> Diagnostics that fire when external dependencies are missing or wrong:
> Go itself, golangci-lint, git, Docker, Terraform, the OCI registry, etc.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-D` covers toolchain + external-tool diagnostics. The `ogon doctor`
command runs the full battery; individual commands surface a relevant
subset.

| Code         | Title                                | Exit | When                                                                  |
|--------------|--------------------------------------|------|------------------------------------------------------------------------|
| OGON-D0001   | External tool missing                | 8    | `go` / `git` / `golangci-lint` / `docker` not on PATH.                 |
| OGON-D0002   | Go version too old                   | 8    | `go version` < 1.27.1.                                                  |
| OGON-D0003   | git init failed                      | 1    | `ogon new --git` could not init the repo (no git, or disk error).     |
| OGON-D0004   | Docker daemon unreachable            | 1    | `ogon deploy --cloud local` cannot reach the daemon.                  |
| OGON-D0005   | Registry unreachable                 | 1    | `ogon deploy` cannot reach `$OGON_REGISTRY_URL`.                       |
| OGON-D0006   | Cloud credentials missing             | 1    | `AWS_*` / `GOOGLE_APPLICATION_CREDENTIALS` / `AZURE_*` unset.           |
| OGON-D0010   | K8s cluster unreachable              | 1    | `kubectl` cannot reach the cluster.                                    |
| OGON-D0011   | Terraform backend locked             | 1    | Another `terraform apply` is in progress.                              |

---

## When

`OGON-D` fires:

- in `ogon doctor` (which exits 8 on any unfixable failure);
- in any command that shells out to an external tool;
- in CI before any of the gates (`ogon check`).

---

## Examples

### OGON-D0001 — External tool missing

```bash
$ ogon doctor
ogon doctor — 5 check(s)
  ✗ go: go not on PATH
    → install Go 1.27+ from https://go.dev/dl/
[OGON-D0001] doctor found unfixable problems
```

### OGON-D0002 — Go version too old

```bash
$ ogon doctor
  ✗ go: go1.21.0 found, need 1.27.1+
    → upgrade from https://go.dev/dl/
```

### OGON-D0005 — Registry unreachable

```bash
$ ogon deploy --cloud aws
[OGON-D0005] registry unreachable
  what:    could not reach https://ghcr.io/v2/ — connection refused
  why:     the host is unreachable; check VPN / firewall / DNS
  fix:     retry, or set OGON_REGISTRY_URL to a reachable endpoint
```

### OGON-D0006 — Cloud credentials missing

```bash
$ ogon deploy --cloud aws
[OGON-D0006] cloud credentials missing
  what:    AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY are not all set
  fix:     export the AWS_* envs, or run `aws configure`
```

---

## Remedy

`ogon doctor` is the contract — every failure carries a `→ fix` line.
Run `ogon doctor --fix` to apply the documented remedy where one
exists (limited to safe verbs: `go`, `mkdir`, `touch`, `git`).

For runtime dep failures (D0004/D0005/D0006/D0010/D0011), the remedy
is environment-side: export the right envs, start the daemon, check
your VPN.

---

## Escape

- **No git**: `ogon new` without `--git` skips the git init step.
- **No Docker**: `ogon deploy --cloud local` requires Docker; use
  `--cloud aws` (or any cloud) instead.
- **No golangci-lint**: `ogon lint` exits with D0001. You can fall
  back to `go vet ./...` (`ogon check` runs it too).
- **Manual credentials**: `ogon infra secrets set AWS_ACCESS_KEY_ID
  --from-literal "$KEY"` stores the credential in the configured
  secret backend instead of relying on env vars.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| `OGON-D0001` despite `which go` succeeding             | The shell the CLI runs in has a different `PATH`; check `ogon doctor` output for the exact PATH it searched. |
| `OGON-D0002` but `go version` says 1.27.1              | Multiple Go installs on PATH; the wrong one wins. Use `export PATH=$HOME/go/bin:$PATH`. |
| `OGON-D0004` intermittent                           | The Docker daemon is overloaded; restart it.                  |
| `OGON-D0011` terraform locked                        | Another `apply` is running; wait for it to finish or release the lock. |

---

Next: [OGON-S security errors](./OGON-S.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

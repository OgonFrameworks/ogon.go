# Error class `OGON-K` — Config errors

> Diagnostics that fire when `ogon.yaml` is missing, malformed, or
> references unknown keys or unset secrets.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-K` covers the config layer: the `ogon.yaml` loader, environment
overlay (`ogon.<env>.yaml`), secret resolution, feature flags, and
the `ogon check` CI gate. Config errors are exit 3 (`ExitConfigInvalid`).

| Code         | Title                                | Exit | When                                                                  |
|--------------|--------------------------------------|------|------------------------------------------------------------------------|
| OGON-K0001   | Not in an OgonGo project             | 3    | `ogon.yaml` not found walking up from cwd.                              |
| OGON-K0002   | Config key unknown                   | 3    | An unknown key in `ogon.yaml` (typo, or a removed key).                |
| OGON-K0003   | Invalid value                        | 3    | A known key has a value outside its allowed set.                       |
| OGON-K0004   | Type mismatch                        | 3    | A key expects an int but got a string.                                 |
| OGON-K0005   | Secret missing                       | 3    | A `*_env` key references an env var that is unset.                     |
| OGON-K0006   | Feature flag unknown                  | 3    | `features:` references a flag the framework does not know.             |
| OGON-K0010   | Registry URL missing                 | 3    | `infra.registry.url_env` is unset on `ogon deploy`.                    |

---

## When

`OGON-K` fires:

- at boot (`ogon.Boot`) if the config cannot be loaded;
- in `ogon check` (CI gate);
- in `ogon doctor` (which surfaces the most common K-class issues
  with a `→ fix` line).

---

## Examples

### OGON-K0001 — Not in an OgonGo project

```bash
$ cd /tmp && ogon doctor
[OGON-K0001] not in an ogon project
  what:    no ogon.yaml found walking up from /tmp
  remedy:  run `ogon new <name>` to scaffold one, or pass --project <dir>
```

### OGON-K0003 — Invalid value

```bash
$ ogon new myservice --template foo
[OGON-K0003] invalid template
  what:    template must be one of minimal|standard|modular
  expected: minimal|standard|modular
  found:   foo
```

### OGON-K0005 — Secret missing

```yaml
auth:
  session:
    secret_env: OGON_SESSION_SECRET
```

```bash
$ ogon check
[OGON-K0005] secret missing
  what:    OGON_SESSION_SECRET is referenced by auth.session.secret_env but is unset
  fix:     export OGON_SESSION_SECRET, or run `ogon infra secrets set OGON_SESSION_SECRET --generate 32`
```

### OGON-K0010 — Registry URL missing

```bash
$ ogon deploy --cloud aws
[OGON-K0010] registry URL missing
  what:    infra.registry.url_env (OGON_REGISTRY_URL) is unset
  fix:     export OGON_REGISTRY_URL=... or set in ogon.prod.yaml
```

---

## Remedy

1. **For K0001** — `cd` to the project root, or `ogon new <name>`.
2. **For K0002 / K0003 / K0004** — `ogon inspect config` shows the
   resolved value; `ogon check` names the bad key.
3. **For K0005** — `ogon infra secrets set <NAME> --generate 32`
   generates and stores a secret in the configured backend.
4. **For K0006** — drop the unknown flag from `features:` in
   `ogon.yaml`.
5. **For K0010** — `export OGON_REGISTRY_URL=...` or set it in
   `ogon.prod.yaml`.

---

## Escape

- **`--project <dir>`**: every project-scoped command honors this
  override; useful when you are operating on a project from outside
  its tree.
- **Manual config**: `BootOpts{ConfigPath: "/path/to/ogon.yaml"}` skips
  the upward walk.
- **Strict-mode off**: there is no global flag; K-class errors are
  non-negotiable because they break the contract. The right move is to
  fix the config, not to silence the diagnostic.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| `OGON-K0001` on `ogon dev` inside the project         | The cwd is not under the project tree; `cd` to the project root. |
| `OGON-K0005` despite the env being set                | The env var is set in a different shell; check with `env \| grep OGON_`. |
| `OGON-K0002` after an upgrade                         | A key was renamed in the new version; see CHANGELOG.md.        |
| `ogon check` says config valid but `ogon dev` fails   | The env overlay (`ogon.dev.yaml`) has the bad key; check both. |

---

Next: [OGON-M migration errors](./OGON-M.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

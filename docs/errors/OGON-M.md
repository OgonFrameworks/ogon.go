# Error class `OGON-M` — Migration errors

> Diagnostics that fire when a migration is unsafe, irreversible, or
> conflicts with the applied state.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-M` covers the migration engine in `record/`. The CLI commands are
`ogon migrate run|rollback|status|create|diff`. The dangerous-op detector
flags destructive migrations and requires `--yes`.

| Code         | Title                                | Exit | When                                                                  |
|--------------|--------------------------------------|------|------------------------------------------------------------------------|
| OGON-M0001   | Migration unsafe / needs confirm      | 4    | Destructive operation (DROP, TRUNCATE, ALTER ... DROP COLUMN with data).|
| OGON-M0002   | Migration irreversible                | 4    | The down migration is missing or no-op on a destructive up.             |
| OGON-M0003   | Migration conflict                    | 4    | Two migrations at the same version number.                             |
| OGON-M0004   | Migration file malformed              | 4    | The .sql or .go migration file has bad syntax.                          |
| OGON-M0005   | Drift detected                        | 4    | `ogon migrate diff` found applied state ≠ source.                      |
| OGON-M0006   | Migration timeout                     | 4    | The migration ran longer than `migrate.timeout`.                       |
| OGON-M0007   | Driver does not support DDL in tx     | 4    | SQLite cannot roll back some DDL; the engine aborts.                    |

---

## When

`OGON-M` fires:

- on `ogon migrate run` / `ogon migrate rollback`;
- on `ogon migrate diff` (drift detection);
- on `ogon check` (CI gate, includes migration safety check);
- at boot if the app's start hook runs `migrate run` automatically.

---

## Examples

### OGON-M0001 — Migration unsafe

```bash
$ ogon migrate run
[OGON-M0001] migration unsafe — needs confirm
  what:    0042_drop_users_table.up.sql contains DROP TABLE users
  why:     DROP TABLE is destructive
  remedy:  pass --yes to confirm; or rewrite the migration to be additive
  docs:    docs/errors/OGON-M.md
```

### OGON-M0002 — Migration irreversible

```bash
$ ogon migrate rollback
[OGON-M0002] migration irreversible
  what:    0042_drop_users_table.down.sql is empty
  why:     the down migration does not restore the dropped table
  fix:     write a down migration, or accept the loss with --yes --force
```

### OGON-M0005 — Drift detected

```bash
$ ogon migrate diff
[OGON-M0005] drift detected
  what:    the database has migrations 0001..0041 applied; source has 0001..0042
  why:     0042_drop_users_table is not yet applied
  fix:     run `ogon migrate run`
```

### OGON-M0007 — Driver does not support DDL in tx

```bash
$ ogon migrate run
[OGON-M0007] driver does not support DDL in tx
  what:    SQLite cannot roll back CREATE TABLE; the engine aborted
  fix:     run with `--no-tx` for this migration only; or split DDL into its own migration
```

---

## Remedy

1. **For M0001 / M0002** — read the diagnostic; either pass `--yes`
   (you accept the loss), or rewrite the migration to be additive.
2. **For M0003** — pick one of the conflicting migrations, bump its
   version number.
3. **For M0004** — fix the SQL; `ogon migrate create` rewrites the
   template.
4. **For M0005** — `ogon migrate run` applies the pending migrations.
5. **For M0006** — raise `migrate.timeout` in `ogon.yaml` or split the
   migration into smaller batches.
6. **For M0007** — split the DDL into its own migration; run with
   `--no-tx` only for that one.

---

## Escape

- **Skip migrations**: `database.driver: none` in `ogon.yaml` turns
  the engine off entirely.
- **Manual SQL**: `record.Raw(c.Tx(), "CREATE TABLE ...")` is the
  documented escape hatch. Same transaction, same driver.
- **`--no-tx`**: `ogon migrate run --no-tx` runs the migration outside
  a transaction. Use only when the driver cannot roll back the DDL.
- **`--to <version>`**: `ogon migrate rollback --to 0041` rolls back
  to a specific version, not one step.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| `OGON-M0001` on every prod migration                 | You are dropping columns; either rewrite to additive or accept the loss with `--yes` in the deploy pipeline. |
| `OGON-M0005` after a deploy                           | The deploy ran the migration; the diff tool may be looking at a different DB. Check `OGON_DB_URL`. |
| `OGON-M0007` on Postgres                              | Should never fire on Postgres; file a bug.                    |
| Migration hangs                                      | `OGON-M0006` will fire after `migrate.timeout`; investigate the lock. |

---

Next: [OGON-D dependency errors](./OGON-D.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

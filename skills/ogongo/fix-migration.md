# fix-migration

Recover from a migration conflict (schema drift) in an OgonGo project.

## When to use

- ogon migrate diff reports a conflict.
- A migration was applied out-of-band (manual SQL).
- The schema_migrations table is out of sync with the live schema.

## Preconditions

- .ogon/ogon.json exists.
- The DB is reachable.
- A backup of the DB exists (run `ogon db reset` only after backup).

## Steps

1. Read the migration plan:
   `ogon mcp tools/call ogon.explain {"thing":"migration 0.9.0 1.0.0"}`
2. Diff the live schema:
   `ogon migrate diff`
3. Resolve the conflict:
   - Option A (auto): `ogon migrate run --yes` (destructive DDL prompts; CI requires --yes).
   - Option B (manual): edit the migration file then `ogon migrate amend`.
4. Run the pre-commit gate:
   `ogon check`
5. Run affected tests:
   `ogon test`

## Verify

- ogon check exits 0.
- ogon test passes.
- ogon migrate status reports all migrations applied.
- No file in generated/migrations is hand-edited (regenerate via `ogon gen migration`).

## Escape hatches

- To skip a destructive DDL step, mark it `--skip` in the migration file.
- To squash history, use `ogon migrate squash` (irreversible; back up first).
- To roll back, use `ogon migrate rollback <n>` (destructive; prompts).

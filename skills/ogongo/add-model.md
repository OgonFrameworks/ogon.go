# add-model

Add a new OgonRecord model + migration to an OgonGo project and run
the migration.

## When to use

- A new persistent type is required (e.g. Product, Order).
- The model has fields + indexes + relations.
- A SQL migration is required.

## Preconditions

- .ogon/ogon.json exists. If not, run `ogon agent dump --write`.
- The DB is reachable (run `ogon doctor` first).

## Steps

1. Read the manifest:
   `ogon mcp tools/call ogon.inspect {"what":"models"}`
2. Plan the resource:
   `ogon mcp tools/call ogon.gen {"kind":"resource","name":"Product"}`
3. Review the plan. The plan includes:
   - app/models/product.go
   - app/handlers/product_handler.go
   - generated/migrations/product.sql
4. Apply the plan via the CLI (CI requires --yes):
   `ogon gen resource Product --yes`
5. Edit app/models/product.go to add fields + indexes + relations.
6. Regenerate the migration diff:
   `ogon migrate diff`
7. Run the migration (CI requires --yes; destructive DDL prompts):
   `ogon migrate run --yes`
8. Run the pre-commit gate:
   `ogon check`
9. Run affected tests:
   `ogon test --filter ./app/...`

## Verify

- ogon check exits 0.
- ogon test passes.
- ogon explain model Product returns fields + table + endpoint.
- The migration is recorded in the schema_migrations table.

## Escape hatches

- To override the generated table name, set the `db:"table=foo"` tag.
- To skip a migration step, mark it `--skip` in the migration file.

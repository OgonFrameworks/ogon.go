# OgonGo skills

Agent surface version: 1.0.0 (schema 1.0.0)

## What this is

A curated set of scripted agent workflows for OgonGo. Each skill is a
narrow task with a stable shape: what to do, when to do it, the exact
CLI commands, and how to verify success. Agents follow these verbatim
until they have a reason to deviate, then explain the deviation in the
commit message.

## When to use a skill

Pick the skill whose name matches the task. If no skill matches, ask
before improvising — the framework may already have an explain path.

## How

1. Read `.ogon/ogon.json` (the machine manifest) for project context.
2. Use `ogon mcp` tools (read-only by default; codegen is dry-run).
3. Apply changes via the CLI with `--yes` in CI mode.
4. Run `ogon check` before commit.
5. Run `ogon test --filter <path>` for affected tests.

## Verify

- `ogon check` exit 0
- `ogon test` passing
- `ogon explain <thing>` covers any magic the change touched
- No file in generated/ is hand-edited

## Skills index

- [add-endpoint](add-endpoint.md) — Add a new HTTP endpoint that returns JSON and verify ogon test passes
- [add-job](add-job.md) — Add a background job and verify it enqueues + processes
- [add-model](add-model.md) — Add a new OgonRecord model + migration and run the migration
- [fix-migration](fix-migration.md) — Recover from a migration conflict (schema drift)

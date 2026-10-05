// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// skills.go provides the per-task agent skills surface (AGENT-006).
// The skills live under skills/ogongo/ as markdown files in the
// awesome-agent-skills format. This file provides:
//   - The canonical SKILL.md content (top-level index) via the
//     SkillsRootDoc() function, so embedders and tests can verify the
//     shape without reading the on-disk file.
//   - Per-task skill bodies (add-endpoint, add-model, add-job,
//     fix-migration) via SkillBody(name).
//   - WriteSkills(root) writes all skills to disk under skills/ogongo/.
//
// The skills are intentionally narrow: each one documents one scripted
// agent workflow with the exact CLI commands + verification steps.

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillsDir is the canonical skills directory (relative to project root).
const SkillsDir = "skills/ogongo"

// SkillsRootDoc returns the canonical SKILL.md content. Byte-stable.
// The shape follows the awesome-agent-skills format: a top-level
// "what / when / how / verify" structure plus an index of per-task
// skills.
func SkillsRootDoc() string {
	var b strings.Builder
	b.WriteString("# OgonGo skills\n\n")
	fmt.Fprintf(&b, "Agent surface version: %s (schema %s)\n\n",
		AgentSurfaceVersion, ManifestVersion)
	b.WriteString("## What this is\n\n")
	b.WriteString("A curated set of scripted agent workflows for OgonGo. Each skill is a\n")
	b.WriteString("narrow task with a stable shape: what to do, when to do it, the exact\n")
	b.WriteString("CLI commands, and how to verify success. Agents follow these verbatim\n")
	b.WriteString("until they have a reason to deviate, then explain the deviation in the\n")
	b.WriteString("commit message.\n\n")
	b.WriteString("## When to use a skill\n\n")
	b.WriteString("Pick the skill whose name matches the task. If no skill matches, ask\n")
	b.WriteString("before improvising — the framework may already have an explain path.\n\n")
	b.WriteString("## How\n\n")
	b.WriteString("1. Read `.ogon/ogon.json` (the machine manifest) for project context.\n")
	b.WriteString("2. Use `ogon mcp` tools (read-only by default; codegen is dry-run).\n")
	b.WriteString("3. Apply changes via the CLI with `--yes` in CI mode.\n")
	b.WriteString("4. Run `ogon check` before commit.\n")
	b.WriteString("5. Run `ogon test --filter <path>` for affected tests.\n\n")
	b.WriteString("## Verify\n\n")
	b.WriteString("- `ogon check` exit 0\n")
	b.WriteString("- `ogon test` passing\n")
	b.WriteString("- `ogon explain <thing>` covers any magic the change touched\n")
	b.WriteString("- No file in generated/ is hand-edited\n\n")
	b.WriteString("## Skills index\n\n")
	for _, name := range SkillNames() {
		fmt.Fprintf(&b, "- [%s](%s.md) — %s\n", name, name, SkillShortDescription(name))
	}
	return b.String()
}

// SkillShortDescription returns the one-line description for a skill.
func SkillShortDescription(name string) string {
	switch name {
	case "add-endpoint":
		return "Add a new HTTP endpoint that returns JSON and verify ogon test passes"
	case "add-model":
		return "Add a new OgonRecord model + migration and run the migration"
	case "add-job":
		return "Add a background job and verify it enqueues + processes"
	case "fix-migration":
		return "Recover from a migration conflict (schema drift)"
	case "SKILL":
		return "Top-level skill index (this file)"
	}
	return ""
}

// SkillNames returns the canonical skill names in lexical order,
// excluding the top-level "SKILL" entry (which is the index).
func SkillNames() []string {
	return []string{"add-endpoint", "add-job", "add-model", "fix-migration"}
}

// SkillBody returns the canonical markdown body for the named skill.
// Byte-stable for identical inputs (AGENT-009).
func SkillBody(name string) (string, error) {
	switch name {
	case "add-endpoint":
		return skillAddEndpoint(), nil
	case "add-model":
		return skillAddModel(), nil
	case "add-job":
		return skillAddJob(), nil
	case "fix-migration":
		return skillFixMigration(), nil
	case "SKILL":
		return SkillsRootDoc(), nil
	}
	return "", fmt.Errorf("agent: unknown skill %q (known: %s)",
		name, strings.Join(append(SkillNames(), "SKILL"), ", "))
}

// WriteSkills writes the full skill set under root/skills/ogongo/.
// The directory is created if missing. Files are overwritten only when
// their content differs from the on-disk content (minimal-diff,
// CLI-064). Returns the list of files written.
func WriteSkills(root string) ([]string, error) {
	if root == "" {
		return nil, fmt.Errorf("agent: WriteSkills needs root")
	}
	dir := filepath.Join(root, SkillsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("agent: mkdir %s: %w", dir, err)
	}
	names := append(SkillNames(), "SKILL")
	sort.Strings(names)
	var written []string
	for _, name := range names {
		body, err := SkillBody(name)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, name+".md")
		existing, err := os.ReadFile(path)
		if err == nil && string(existing) == body {
			continue // minimal-diff
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return nil, fmt.Errorf("agent: write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}

// skillAddEndpoint returns the canonical add-endpoint skill body.
func skillAddEndpoint() string {
	return `# add-endpoint

Add a new HTTP endpoint to an OgonGo project that returns JSON and
verify that ogon test passes.

## When to use

- A new HTTP route is required (e.g. "/api/widgets").
- The endpoint returns JSON or HTML.
- The endpoint does NOT require a new model (use add-model for that).

## Preconditions

- .ogon/ogon.json exists. If not, run ` + "`ogon agent dump --write`" + `.
- ogon.yaml present at project root.
- No uncommitted changes to generated/.

## Steps

1. Read the manifest:
   ` + "`ogon mcp tools/call ogon.inspect {\"what\":\"routes\"}`" + `
2. Plan the resource:
   ` + "`ogon mcp tools/call ogon.gen {\"kind\":\"resource\",\"name\":\"Widget\"}`" + `
3. Review the plan. The plan is dry-run; no files are written.
4. Apply the plan via the CLI (CI requires --yes):
   ` + "`ogon gen resource Widget --yes`" + `
5. Edit app/handlers/widget_handler.go to implement the handler body.
6. Run the pre-commit gate:
   ` + "`ogon check`" + `
7. Run affected tests:
   ` + "`ogon test --filter ./app/handlers/...`" + `

## Verify

- ogon check exits 0.
- ogon test passes.
- ogon explain route /api/widgets returns the route's handler +
  middleware + generated adapters.
- No file in generated/ is hand-edited.

## Escape hatches

- To register a route manually, add it to app/routes.go; the manifest
  picks it up on the next ` + "`ogon agent dump --write`" + `.
- To override generated serialization, implement a custom handler in
  app/handlers/ and let the generated adapter delegate to it.
`
}

// skillAddModel returns the canonical add-model skill body.
func skillAddModel() string {
	return `# add-model

Add a new OgonRecord model + migration to an OgonGo project and run
the migration.

## When to use

- A new persistent type is required (e.g. Product, Order).
- The model has fields + indexes + relations.
- A SQL migration is required.

## Preconditions

- .ogon/ogon.json exists. If not, run ` + "`ogon agent dump --write`" + `.
- The DB is reachable (run ` + "`ogon doctor`" + ` first).

## Steps

1. Read the manifest:
   ` + "`ogon mcp tools/call ogon.inspect {\"what\":\"models\"}`" + `
2. Plan the resource:
   ` + "`ogon mcp tools/call ogon.gen {\"kind\":\"resource\",\"name\":\"Product\"}`" + `
3. Review the plan. The plan includes:
   - app/models/product.go
   - app/handlers/product_handler.go
   - generated/migrations/product.sql
4. Apply the plan via the CLI (CI requires --yes):
   ` + "`ogon gen resource Product --yes`" + `
5. Edit app/models/product.go to add fields + indexes + relations.
6. Regenerate the migration diff:
   ` + "`ogon migrate diff`" + `
7. Run the migration (CI requires --yes; destructive DDL prompts):
   ` + "`ogon migrate run --yes`" + `
8. Run the pre-commit gate:
   ` + "`ogon check`" + `
9. Run affected tests:
   ` + "`ogon test --filter ./app/...`" + `

## Verify

- ogon check exits 0.
- ogon test passes.
- ogon explain model Product returns fields + table + endpoint.
- The migration is recorded in the schema_migrations table.

## Escape hatches

- To override the generated table name, set the ` + "`db:\"table=foo\"`" + ` tag.
- To skip a migration step, mark it ` + "`--skip`" + ` in the migration file.
`
}

// skillAddJob returns the canonical add-job skill body.
func skillAddJob() string {
	return `# add-job

Add a background job to an OgonGo project and verify it enqueues +
processes + retries + dead-letter-queues correctly.

## When to use

- A new background task is required (e.g. welcome_email, daily_report).
- The job has retries + DLQ semantics.
- The job does NOT require a new model (use add-model for that).

## Preconditions

- .ogon/ogon.json exists. If not, run ` + "`ogon agent dump --write`" + `.
- The jobs queue is configured (run ` + "`ogon doctor`" + ` first).

## Steps

1. Plan the job:
   ` + "`ogon mcp tools/call ogon.gen {\"kind\":\"job\",\"name\":\"welcome_email\"}`" + `
2. Review the plan. The plan includes app/jobs/welcome_email_job.go.
3. Apply the plan via the CLI (CI requires --yes):
   ` + "`ogon gen job welcome_email --yes`" + `
4. Edit app/jobs/welcome_email_job.go to fill the Run body.
5. Run the pre-commit gate:
   ` + "`ogon check`" + `
6. Run affected tests:
   ` + "`ogon test --filter ./app/jobs/...`" + `

## Verify

- ogon check exits 0.
- ogon test passes.
- Enqueue + process + retry + DLQ can be observed in <=5 lines of test.
- ogon explain jobs returns the job's queue + retry policy + DLQ topic.

## Escape hatches

- To override the retry policy, set it in app/jobs/welcome_email_job.go.
- To enqueue from outside the framework, publish to the queue topic
  directly (the broker interface is public).
`
}

// skillFixMigration returns the canonical fix-migration skill body.
func skillFixMigration() string {
	return `# fix-migration

Recover from a migration conflict (schema drift) in an OgonGo project.

## When to use

- ogon migrate diff reports a conflict.
- A migration was applied out-of-band (manual SQL).
- The schema_migrations table is out of sync with the live schema.

## Preconditions

- .ogon/ogon.json exists.
- The DB is reachable.
- A backup of the DB exists (run ` + "`ogon db reset`" + ` only after backup).

## Steps

1. Read the migration plan:
   ` + "`ogon mcp tools/call ogon.explain {\"thing\":\"migration 0.9.0 1.0.0\"}`" + `
2. Diff the live schema:
   ` + "`ogon migrate diff`" + `
3. Resolve the conflict:
   - Option A (auto): ` + "`ogon migrate run --yes`" + ` (destructive DDL prompts; CI requires --yes).
   - Option B (manual): edit the migration file then ` + "`ogon migrate amend`" + `.
4. Run the pre-commit gate:
   ` + "`ogon check`" + `
5. Run affected tests:
   ` + "`ogon test`" + `

## Verify

- ogon check exits 0.
- ogon test passes.
- ogon migrate status reports all migrations applied.
- No file in generated/migrations is hand-edited (regenerate via ` + "`ogon gen migration`" + `).

## Escape hatches

- To skip a destructive DDL step, mark it ` + "`--skip`" + ` in the migration file.
- To squash history, use ` + "`ogon migrate squash`" + ` (irreversible; back up first).
- To roll back, use ` + "`ogon migrate rollback <n>`" + ` (destructive; prompts).
`
}

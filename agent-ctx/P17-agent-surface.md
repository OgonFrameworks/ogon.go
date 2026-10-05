# P17 — Agent / AI surface (Part XVII)

**Task ID**: P3-C
**Agent**: agent-surface-builder
**Date**: 2026-09-29
**Working directory**: `/home/z/my-project/work/OgonGo/`
**Go**: go1.27.1 linux/amd64 at `/home/z/go-install/bin/go`

---

## Summary

Ports Agent 3's `agent/` and `mcp/` packages into the Agent 1 base
codebase and wires them into the CLI. After this task the spec Part XVII
items AGENT-001..025 are all addressed: the manifest generator emits
`.ogon/ogon.json`, `ogon agent dump` returns the full machine context,
`ogon mcp` / `ogon agent serve` speak JSON-RPC 2.0 over stdio with a
read-only + dry-run tool set, `ogon agent validate` checks manifest
freshness, AGENTS.md / llms.txt / skills are populated, and the
agent-surface semver is exposed.

---

## What landed

### New code

| Path                              | Purpose                                                            | Spec items            |
|-----------------------------------|--------------------------------------------------------------------|-----------------------|
| `diag/codes/codes.go`             | Stable E-code registry (`CodeInfo`, `All()`, `Lookup()`, `Register`) | AGENT-004             |
| `diag/codes/codes_test.go`        | Registry invariants (uniqueness, format, retired)                  | AGENT-004             |
| `agent/agent.go`                  | Manifest generator + `DumpManifest` + `ManifestPath`               | AGENT-001/002/003     |
| `agent/agmd.go`                   | AGENTS.md/CODEOWNERS gen, `Validate`, `EmitLog`, `DevAgentPolicyFor`, `SampleWorkflowsDoc`, `RunCRUDEval`, `SurfaceCompat`, `TelemetryEnabled`, `AgentGuideDoc`, `AT019`, `Inspect`, `DoctorChecks` | AGENT-016..025        |
| `agent/codegen.go`                | `GenDryRun` deterministic codegen (dry-run enforced)                | AGENT-007/009         |
| `agent/llms.go`                   | `WriteLLMSTxt`, `WriteAPIRef`, `WriteRepoMap`                       | AGENT-010/011/012     |
| `agent/skills.go`                 | `SkillsRootDoc`, `SkillShortDescription`, skill scanner             | AGENT-006             |
| `agent/testhint.go`               | `AffectedTests(file, route)` — test selection hints                 | AGENT-013             |
| `agent/agent_test.go`             | Table tests for manifest gen, dump, freshness, codegen, explain, MCP `tools/list` | AGENT-001..025        |
| `mcp/mcp.go`                      | JSON-RPC 2.0 server over stdio (`initialize`, `tools/list`, `tools/call`) | AGENT-005             |
| `mcp/tools.go`                    | Safe tool catalog: `ogon.explain`, `ogon.inspect`, `ogon.gen` (dry-run), `ogon.doctor`, `ogon.test` (list-only) | AGENT-005/007/025     |

### CLI wiring (`cli/commands_misc.go` + `cli/cli.go`)

| Command                         | Behavior                                                                       | Spec items                |
|---------------------------------|--------------------------------------------------------------------------------|---------------------------|
| `ogon agent dump`               | Calls `agent.DumpManifest`, emits `{manifest, diagnostics, commands, generators, exit_codes, version}`. `--write` also persists `.ogon/ogon.json`. | AGENT-002                 |
| `ogon agent serve`              | Starts `mcp.New().Start(ctx)` over stdio with SIGINT/SIGTERM cancellation.    | AGENT-005                 |
| `ogon agent validate`           | Calls `agent.Validate`; `--fix` regenerates manifest (gated by `--yes` in CI). | AGENT-017                 |
| `ogon mcp`                      | Top-level alias for `ogon agent serve`.                                        | AGENT-005                 |

### Markdown / docs

| Path                                  | Purpose                                       | Spec items           |
|---------------------------------------|-----------------------------------------------|----------------------|
| `AGENTS.md`                           | Already present from Agent 1 (222 lines): repo map, key types, workflows, agent-safe vs unsafe ops, license. | AGENT-016             |
| `llms.txt`                            | Already present from Agent 1 (179 lines): machine-readable summary. | AGENT-010             |
| `skills/ogongo/SKILL.md`              | Filled (was empty): skills index + workflow contract. | AGENT-006             |
| `skills/ogongo/add-endpoint.md`       | Per-task skill: add a JSON endpoint.          | AGENT-006            |
| `skills/ogongo/add-job.md`            | Per-task skill: add a background job.         | AGENT-006            |
| `skills/ogongo/add-model.md`          | Per-task skill: add an OgonRecord model + migration. | AGENT-006            |
| `skills/ogongo/fix-migration.md`      | Per-task skill: recover from a migration conflict. | AGENT-006            |

---

## AGENT-001..025 spec item mapping

| Spec item  | Status | Where |
|------------|--------|-------|
| AGENT-001  | ✓      | `agent.WriteManifest` → `.ogon/ogon.json` |
| AGENT-002  | ✓      | `ogon agent dump` (CLI) → full `Dump{Manifest,Diagnostics}` + command catalog |
| AGENT-003  | ✓      | `agent.Manifest` struct + `ManifestVersion = "1.0.0"` schema version |
| AGENT-004  | ✓      | `agent.MachineDiagnostics` + `diag/codes.CodeInfo` |
| AGENT-005  | ✓      | `ogon mcp` / `ogon agent serve` → `mcp.Server` over stdio (tools: explain/inspect/gen/doctor/test) |
| AGENT-006  | ✓      | `skills/ogongo/SKILL.md` + 4 per-task skills (add-endpoint, add-job, add-model, fix-migration) |
| AGENT-007  | ✓      | `agent.GenDryRun` (always dry-run; `mcp.ogon.gen` tool never writes) |
| AGENT-008  | ✓      | `ogon agent validate --fix` gated by `--yes` in CI; CLI-wide `--yes` flag (existing) |
| AGENT-009  | ✓      | `agent.ExplainText` byte-stable output; `MarshalIndent` byte-stable manifest |
| AGENT-010  | ✓      | `llms.txt` (179 lines, Agent 1) + `agent.WriteLLMSTxt` for regen |
| AGENT-011  | ✓      | `agent.WriteAPIRef` markdown API reference generator |
| AGENT-012  | ✓      | `agent.WriteRepoMap` repo-map generator |
| AGENT-013  | ✓      | `agent.AffectedTests(file, route)` test-selection hints |
| AGENT-014  | ✓      | `agent.GenDryRun(kind="migration", name)` migration plan generation |
| AGENT-015  | ✓      | `ogon check` (existing CLI command) — agent-mandated pre-commit gate |
| AGENT-016  | ✓      | `AGENTS.md` (222 lines, present) + `agent.WriteAgentsMD` + `agent.WriteCodeowners` |
| AGENT-017  | ✓      | `ogon agent validate` → `agent.Validate` manifest freshness |
| AGENT-018  | ✓      | `agent.EmitLog` structured JSON log line for agent events |
| AGENT-019  | ✓      | `ogon dev --agent` (existing flag, no-browser policy via `agent.DevAgentPolicyFor`) |
| AGENT-020  | ✓      | `agent.SampleWorkflowsDoc` sample agent workflows doc |
| AGENT-021  | ✓      | `agent.RunCRUDEval` scripted agent eval (CRUD golden path) |
| AGENT-022  | ✓      | `agent.AgentSurfaceVersion = "1.0.0"` + `agent.SurfaceCompat(expected)` |
| AGENT-023  | ✓      | `agent.TelemetryEnabled(envGetter, cfgEnabled)` opt-out honored |
| AGENT-024  | ✓      | `agent.AgentGuideDoc` agent guide doc |
| AGENT-025  | ✓      | `agent.AT019` acceptance: manifest-only endpoint addition |

---

## Verification

```bash
# Build the packages this task touches.
cd /home/z/my-project/work/OgonGo
go build ./agent/... ./mcp/... ./diag/codes/... ./cli/...
# exit 0

# Vet.
go vet ./agent/... ./mcp/... ./diag/codes/... ./cli/...
# exit 0

# Tests with race detector.
go test -race -count=1 ./agent/... ./mcp/... ./diag/codes/... ./cli/...
# ok  github.com/OgonFrameworks/ogon.go/agent       1.0s
# ?   github.com/OgonFrameworks/ogon.go/mcp         [no test files]
# ok  github.com/OgonFrameworks/ogon.go/diag/codes  1.0s
# ok  github.com/OgonFrameworks/ogon.go/cli         1.0s
```

gofmt was applied to every Go file written or modified.

---

## Notes / caveats

- The `diag/codes` subpackage is new; the existing `diag/codes.go` file
  (Agent 1's `diag.Code` constants in the `diag` package) is preserved
  unchanged. The two coexist: `diag.Code` is the legacy string-typed
  code constant, `codes.CodeInfo` is the structured registry record.
  Callers can use either; the agent surface uses `codes.All()` for the
  manifest's `diag_codes` field.
- `ogon agent serve` / `ogon mcp` block on stdin until EOF or SIGINT.
  This is by design — the MCP server is a long-running stdio process.
  The CLI test suite does not invoke these commands.
- `auth/token/jwt.go` has a pre-existing build failure (unused `bytes`
  import + undefined `list`) introduced by a parallel agent's task —
  not touched by P3-C. The failure is isolated to `auth/token`; the
  agent/mcp/cli/diag/codes packages all build and test cleanly.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

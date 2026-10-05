// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// tools.go defines the safe tool set exposed by the OgonGo MCP server
// (AGENT-005, AGENT-025). The catalog is intentionally read-only plus
// deterministic dry-run: no destructive tool is exposed (spec Part
// XVII "make OgonGo apps unusually easy for humans and software agents
// to understand and modify safely"). Agents that need to mutate use
// the CLI via the host shell, which enforces --yes / --dry-run.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/OgonFrameworks/ogon.go/agent"
)

// DefaultTools returns the safe tool catalog exposed by the MCP server.
// The set is frozen per the agent surface semver contract (AGENT-022):
// adding a tool is a minor bump; changing a tool's shape is a major bump.
//
// Catalog:
//   - ogon.explain     — stable explain output for a thing (route/model/...)
//   - ogon.inspect     — structured inspection of the live app state
//   - ogon.gen         — deterministic codegen (dry-run enforced)
//   - ogon.doctor      — environment diagnostics
//   - ogon.test        — affected test listing (does NOT execute tests)
//
// Every tool returns a ToolResult with both Text (human/agent readable)
// and Structured (machine-readable JSON map) payloads.
func DefaultTools() []Tool {
	return []Tool{
		{
			Name:        "ogon.explain",
			Description: "Explain a thing (route, model, config, di, module, error-code, magic) with stable, byte-reproducible output. Read-only.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thing": map[string]any{
						"type":        "string",
						"description": "The thing to explain, e.g. 'route /api/users', 'model User', 'config http.addr', 'magic'.",
					},
				},
				"required": []string{"thing"},
			},
			Handler: toolExplain,
		},
		{
			Name:        "ogon.inspect",
			Description: "Inspect the live app state: routes, models, config, runtime, or modules. Read-only.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"what": map[string]any{
						"type":        "string",
						"description": "One of: routes, models, config, runtime, modules.",
						"enum":        []string{"routes", "models", "config", "runtime", "modules"},
					},
				},
				"required": []string{"what"},
			},
			Handler: toolInspect,
		},
		{
			Name:        "ogon.gen",
			Description: "Run a deterministic code generator. Always dry-run; this tool never writes files. Returns the planned file tree so the agent can review before applying via the CLI.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{
						"type":        "string",
						"description": "Generator kind: resource, job, auth, page, module, migration.",
						"enum":        []string{"resource", "job", "auth", "page", "module", "migration"},
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Generator name argument (e.g. 'User' for `ogon gen resource User').",
					},
				},
				"required": []string{"kind", "name"},
			},
			Handler: toolGen,
		},
		{
			Name:        "ogon.doctor",
			Description: "Run the local-environment doctor. Returns the check list with status per check (ok|warn|fail) plus a remediation hint per failure.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Handler: toolDoctor,
		},
		{
			Name:        "ogon.test",
			Description: "List tests that exercise the named source file or route. Does NOT run tests; agents use the CLI to execute. Use --list to get the test file paths only.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{
						"type":        "string",
						"description": "Source file path relative to project root (e.g. 'app/routes.go').",
					},
					"route": map[string]any{
						"type":        "string",
						"description": "HTTP route path (e.g. '/api/users'); agent will resolve to handler file.",
					},
				},
			},
			Handler: toolTestList,
		},
	}
}

// toolExplain implements `ogon.explain`.
func toolExplain(ctx context.Context, args map[string]any) (*ToolResult, error) {
	thing, ok := args["thing"].(string)
	if !ok || thing == "" {
		return nil, errors.New("mcp: ogon.explain requires 'thing' (string)")
	}
	// We delegate to the agent package's explain registry (AGENT-009).
	body, err := agent.ExplainText(thing)
	if err != nil {
		return nil, err
	}
	structured := map[string]any{
		"thing":   thing,
		"output":  body,
		"version": agent.AgentSurfaceVersion,
	}
	return &ToolResult{Text: body, Structured: structured}, nil
}

// toolInspect implements `ogon.inspect`.
func toolInspect(ctx context.Context, args map[string]any) (*ToolResult, error) {
	what, ok := args["what"].(string)
	if !ok {
		return nil, errors.New("mcp: ogon.inspect requires 'what' (string)")
	}
	switch what {
	case "routes", "models", "config", "runtime", "modules":
	default:
		return nil, fmt.Errorf("mcp: ogon.inspect 'what' must be one of routes|models|config|runtime|modules, got %q", what)
	}
	body, structured, err := agent.Inspect(what)
	if err != nil {
		return nil, err
	}
	return &ToolResult{Text: body, Structured: structured}, nil
}

// toolGen implements `ogon.gen` — always dry-run, never writes files
// (AGENT-007: deterministic agent-safe codegen with dry-run default).
func toolGen(ctx context.Context, args map[string]any) (*ToolResult, error) {
	kind, ok := args["kind"].(string)
	if !ok || kind == "" {
		return nil, errors.New("mcp: ogon.gen requires 'kind' (string)")
	}
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return nil, errors.New("mcp: ogon.gen requires 'name' (string)")
	}
	plan, err := agent.GenDryRun(kind, name)
	if err != nil {
		return nil, err
	}
	// Marshal the plan to a stable text representation.
	body, _ := json.MarshalIndent(plan, "", "  ")
	structured := map[string]any{
		"kind":    kind,
		"name":    name,
		"dry_run": true,
		"plan":    plan,
	}
	return &ToolResult{
		Text:       string(body),
		Structured: structured,
	}, nil
}

// toolDoctor implements `ogon.doctor`.
func toolDoctor(ctx context.Context, args map[string]any) (*ToolResult, error) {
	checks := agent.DoctorChecks()
	body, _ := json.MarshalIndent(checks, "", "  ")
	structured := map[string]any{
		"checks":  checks,
		"version": agent.AgentSurfaceVersion,
	}
	return &ToolResult{Text: string(body), Structured: structured}, nil
}

// toolTestList implements `ogon.test` (list only; never executes).
func toolTestList(ctx context.Context, args map[string]any) (*ToolResult, error) {
	file, _ := args["file"].(string)
	route, _ := args["route"].(string)
	if file == "" && route == "" {
		return nil, errors.New("mcp: ogon.test requires 'file' or 'route'")
	}
	tests, err := agent.AffectedTests(file, route)
	if err != nil {
		return nil, err
	}
	body, _ := json.MarshalIndent(map[string]any{
		"file":  file,
		"route": route,
		"tests": tests,
	}, "", "  ")
	structured := map[string]any{
		"file":  file,
		"route": route,
		"tests": tests,
	}
	return &ToolResult{Text: string(body), Structured: structured}, nil
}

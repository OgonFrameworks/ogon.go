// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// codegen.go provides the agent-safe codegen dry-run surface (AGENT-007,
// AGENT-008, AGENT-009). The agent surface ALWAYS defaults to dry-run:
// no file is ever written through `ogon mcp` or `ogon agent`. Real
// writes happen via the CLI, which enforces --yes for destructive ops.

package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag/codes"
)

// GenPlan is the deterministic, agent-readable plan produced by
// GenDryRun. The plan describes every file the generator intends to
// write, with byte-stable content for identical inputs (CLI-061,
// AGENT-009).
type GenPlan struct {
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	DryRun      bool             `json:"dry_run"`
	Files       []GenPlannedFile `json:"files"`
	Notes       []string         `json:"notes,omitempty"`
	GeneratedAt string           `json:"generated_at"`
}

// GenPlannedFile is one file in a GenPlan. Action is one of:
//   - "create" — file does not exist; will be created
//   - "update" — file exists; content differs; will be overwritten
//   - "skip"   — file exists and is byte-identical to the plan; no-op
type GenPlannedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
}

// GenDryRun produces a deterministic plan for the named generator kind
// without touching the filesystem (AGENT-007). The plan is the
// canonical agent surface for codegen: an agent inspects the plan and,
// if satisfied, asks the user (or applies the CLI) to commit it.
//
// Supported kinds: resource, job, auth, page, module, migration. The
// kind set is frozen per AGENT-022; new kinds require a minor bump.
func GenDryRun(kind, name string) (*GenPlan, error) {
	if kind == "" {
		return nil, errors.New("agent: GenDryRun kind is required")
	}
	if name == "" {
		return nil, errors.New("agent: GenDryRun name is required")
	}
	plan := &GenPlan{
		Kind:        kind,
		Name:        name,
		DryRun:      true, // AGENT-007: dry-run is the default; never override.
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	switch kind {
	case "resource":
		plan.Files = planGenResource(name)
	case "job":
		plan.Files = planGenJob(name)
	case "auth":
		plan.Files = planGenAuth(name)
	case "page":
		plan.Files = planGenPage(name)
	case "module":
		plan.Files = planGenModule(name)
	case "migration":
		plan.Files = planGenMigration(name)
	default:
		return nil, fmt.Errorf("agent: GenDryRun unknown kind %q", kind)
	}
	sort.SliceStable(plan.Files, func(i, j int) bool {
		return plan.Files[i].Path < plan.Files[j].Path
	})
	plan.Notes = append(plan.Notes,
		"dry-run only: no files written. Apply via `ogon gen "+kind+" "+name+"` (requires --yes in CI).")
	return plan, nil
}

// planGenResource produces the planned files for `ogon gen resource`.
// The output is deterministic over (kind, name) and matches the shape
// the CLI's `ogon gen resource` produces (CLI-060).
func planGenResource(name string) []GenPlannedFile {
	pkg := strings.ToLower(name)
	return []GenPlannedFile{
		{
			Path:    "app/models/" + pkg + ".go",
			Action:  "create",
			Content: resourceModelSource(name),
			Reason:  "OgonRecord model declaration",
		},
		{
			Path:    "app/handlers/" + pkg + "_handler.go",
			Action:  "create",
			Content: resourceHandlerSource(name),
			Reason:  "CRUD handler",
		},
		{
			Path:    "generated/migrations/" + pkg + ".sql",
			Action:  "create",
			Content: resourceMigrationSource(name),
			Reason:  "schema migration",
		},
	}
}

// planGenJob produces the planned files for `ogon gen job`.
func planGenJob(name string) []GenPlannedFile {
	pkg := strings.ToLower(name)
	return []GenPlannedFile{
		{
			Path:    "app/jobs/" + pkg + "_job.go",
			Action:  "create",
			Content: jobSource(name),
			Reason:  "background job body",
		},
	}
}

// planGenAuth produces the planned files for `ogon gen auth`.
func planGenAuth(name string) []GenPlannedFile {
	return []GenPlannedFile{
		{
			Path:    "app/auth/auth.go",
			Action:  "create",
			Content: authSource(name),
			Reason:  "auth wiring",
		},
	}
}

// planGenPage produces the planned files for `ogon gen page`.
func planGenPage(name string) []GenPlannedFile {
	pkg := strings.ToLower(name)
	return []GenPlannedFile{
		{
			Path:    "app/ui/" + pkg + ".ogon",
			Action:  "create",
			Content: pageSource(name),
			Reason:  "OgonUI single-file component",
		},
	}
}

// planGenModule produces the planned files for `ogon gen module`.
func planGenModule(name string) []GenPlannedFile {
	pkg := strings.ToLower(name)
	return []GenPlannedFile{
		{
			Path:    "modules/" + pkg + "/manifest.yaml",
			Action:  "create",
			Content: moduleManifestSource(name),
			Reason:  "module manifest",
		},
	}
}

// planGenMigration produces the planned files for `ogon gen migration`.
func planGenMigration(name string) []GenPlannedFile {
	pkg := strings.ToLower(name)
	return []GenPlannedFile{
		{
			Path:    "generated/migrations/" + pkg + ".sql",
			Action:  "create",
			Content: migrationSource(name),
			Reason:  "schema migration file",
		},
	}
}

// btick is a regular Go string containing a single backtick. We use
// it to break out of raw string literals where the template needs to
// emit a literal backtick (e.g. Go struct field tags).
const btick = "`"

// resourceModelSource returns the deterministic source for a generated
// OgonRecord model. Byte-stable for identical (name) inputs (CLI-061).
func resourceModelSource(name string) string {
	return fmt.Sprintf(`// Code generated by ogon gen resource %[1]s; DO NOT EDIT.

package models

import "github.com/OgonFrameworks/ogon.go/record"

// %[1]s is an OgonRecord model generated by `+btick+`ogon gen resource %[1]s`+btick+`.
type %[1]s struct {
        record.Model
        ID   int64  `+btick+`db:"id,pk"`+btick+`
        Name string `+btick+`db:"name"`+btick+`
}
`, name)
}

func resourceHandlerSource(name string) string {
	pkg := strings.ToLower(name)
	return fmt.Sprintf(`// Code generated by ogon gen resource %[1]s; DO NOT EDIT.

package handlers

import (
        "net/http"

        ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

// Register%[1]sRoutes wires the CRUD endpoints for %[1]s.
func Register%[1]sRoutes(mux *ogonhttp.ServeMux) {
        mux.HandleFunc("GET /api/%[2]s", list%[1]s)
        mux.HandleFunc("POST /api/%[2]s", create%[1]s)
        mux.HandleFunc("GET /api/%[2]s/{id}", show%[1]s)
        mux.HandleFunc("PUT /api/%[2]s/{id}", update%[1]s)
        mux.HandleFunc("DELETE /api/%[2]s/{id}", delete%[1]s)
}

func list%[1]s(w http.ResponseWriter, r *http.Request)   {}
func create%[1]s(w http.ResponseWriter, r *http.Request)  {}
func show%[1]s(w http.ResponseWriter, r *http.Request)    {}
func update%[1]s(w http.ResponseWriter, r *http.Request)  {}
func delete%[1]s(w http.ResponseWriter, r *http.Request)  {}
`, name, pkg)
}

func resourceMigrationSource(name string) string {
	table := snakeCase(name) + "s"
	return fmt.Sprintf(`-- Code generated by ogon gen resource %[1]s; DO NOT EDIT.

CREATE TABLE %[2]s (
  id    BIGSERIAL PRIMARY KEY,
  name  TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`, name, table)
}

func jobSource(name string) string {
	pkg := strings.ToLower(name)
	return fmt.Sprintf(`// Code generated by ogon gen job %[1]s; DO NOT EDIT.

package jobs

import "context"

// %[1]sJob is a background job. Edit the Run body to implement it.
type %[1]sJob struct{}

// Name returns the job's stable identifier.
func (j *%[1]sJob) Name() string { return "%[2]s" }

// Run executes the job with the given payload.
func (j *%[1]sJob) Run(ctx context.Context, payload []byte) error {
        _ = payload
        return nil
}
`, name, pkg)
}

func authSource(name string) string {
	return fmt.Sprintf(`// Code generated by ogon gen auth %[1]s; DO NOT EDIT.

package auth

// Wiring for the requested auth flows. Edit the bodies to customize.
// Replace the placeholder flow list with the flows you requested.
var requestedFlows = []string{"session"}
`, name)
}

func pageSource(name string) string {
	pkg := strings.ToLower(name)
	return fmt.Sprintf(`<!-- Code generated by ogon gen page %[1]s; DO NOT EDIT. -->
<script type="application/javascript">
  // %[1]s page component
  export function mount(root) {
    root.innerHTML = "<h1>%[1]s</h1><p>Edit app/ui/%[2]s.ogon</p>";
  }
</script>
<template>
  <h1>%[1]s</h1>
</template>
`, name, pkg)
}

func moduleManifestSource(name string) string {
	pkg := strings.ToLower(name)
	return fmt.Sprintf(`# Code generated by ogon gen module %[1]s; DO NOT EDIT.
name: %[2]s
version: 0.1.0
description: OgonGo module %[2]s
license: MIT
`, name, pkg)
}

func migrationSource(name string) string {
	pkg := strings.ToLower(name)
	return fmt.Sprintf(`-- Code generated by ogon gen migration %[1]s; DO NOT EDIT.
-- Replace this stub with your DDL.
-- id: %[2]s
CREATE TABLE %[2]s_example (
  id BIGSERIAL PRIMARY KEY
);
`, name, pkg)
}

// --- AGENT-009: stable explain output contract ---

// ExplainText returns a deterministic, byte-stable explanation for the
// named thing. The same input MUST produce the same stdout across
// versions of the same agent surface (AGENT-022).
//
// This function is the agent surface analogue of `ogon explain`. It is
// intentionally simple: it parses the thing into (topic, rest) and
// dispatches to a registered explainer. The output is plain text with
// a stable shape: header line, body, footer line.
func ExplainText(thing string) (string, error) {
	topic, rest := splitExplainThing(thing)
	explainer, ok := explainers[topic]
	if !ok {
		// AGENT-009: stable error messages too.
		return "", fmt.Errorf("agent: explain: unknown topic %q (known: %s)",
			topic, strings.Join(explainTopics(), ", "))
	}
	body, err := explainer(rest)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ogon explain %s\n", thing)
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "# (agent surface %s, schema %s)\n", AgentSurfaceVersion, ManifestVersion)
	return b.String(), nil
}

// splitExplainThing splits "route /api/users" → ("route", "/api/users").
// The first whitespace-delimited token is the topic; the rest is passed
// verbatim (whitespace-collapsed).
func splitExplainThing(thing string) (topic, rest string) {
	thing = strings.TrimSpace(thing)
	if thing == "" {
		return "", ""
	}
	idx := strings.IndexAny(thing, " \t")
	if idx < 0 {
		return thing, ""
	}
	return thing[:idx], strings.TrimSpace(thing[idx+1:])
}

// explainer is one explain producer.
type explainer func(rest string) (string, error)

// explainers is the registered set. The map is appended to at init()
// time by other files in the agent package (testhint.go for affected-
// route hints, agmd.go for module tips, etc.).
var explainers = map[string]explainer{}

// RegisterExplainer registers an explain producer for a topic. Called
// at init() time by other files in the agent package.
func RegisterExplainer(topic string, fn explainer) {
	if _, dup := explainers[topic]; dup {
		panic("agent: duplicate explainer for topic " + topic)
	}
	explainers[topic] = fn
}

// explainTopics returns the registered topics in lexical order.
func explainTopics() []string {
	out := make([]string, 0, len(explainers))
	for t := range explainers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// init registers the canonical explainers. Topics that need filesystem
// access (route, model, config) return stable synthetic output when
// the project is not detected; agents MUST verify the agent surface
// version in the footer to detect synthetic output.
func init() {
	RegisterExplainer("magic", explainMagic)
	RegisterExplainer("error-code", explainErrorCode)
	RegisterExplainer("agent-surface", explainAgentSurface)
	RegisterExplainer("manifest", explainManifest)
}

// explainMagic lists every framework magic behavior in deterministic
// order. Every entry in the list MUST be backed by an explain topic so
// that DX-P5 ("magic is observable") is satisfiable.
func explainMagic(rest string) (string, error) {
	var b strings.Builder
	b.WriteString("Framework magic index (each item has an explain path):\n")
	for _, m := range magicBehaviors {
		fmt.Fprintf(&b, "  - %s — `ogon explain %s`\n", m.Summary, m.Topic)
	}
	return b.String(), nil
}

// magicBehavior is one framework magic behavior with its explain topic.
type magicBehavior struct {
	Topic   string
	Summary string
}

// magicBehaviors is the canonical, sorted list of framework magic
// behaviors. Adding a behavior is a minor agent-surface bump.
var magicBehaviors = []magicBehavior{
	{Topic: "di", Summary: "DI graph wiring (zero reflection, codegen at build)"},
	{Topic: "route", Summary: "Route registration + middleware chain"},
	{Topic: "model", Summary: "OgonRecord model → table binding + migration"},
	{Topic: "migration", Summary: "AST-vs-schema diff → reversible SQL"},
	{Topic: "config", Summary: "5-layer precedence (default←file←env←flag←inline)"},
	{Topic: "module", Summary: "Module manifest + DI contribution"},
	{Topic: "lifecycle", Summary: "Boot/Run/Shutdown with supervisor drain"},
	{Topic: "live", Summary: "WebSocket/SSE fanout protocol"},
	{Topic: "jobs", Summary: "Background jobs retry + DLQ"},
	{Topic: "auth", Summary: "Session/JWT/Passkey wiring via AddAuthentication"},
	{Topic: "ui", Summary: ".ogon single-file components → SSR + hydrate"},
	{Topic: "obs", Summary: "OTel/metrics/health redaction defaults"},
	{Topic: "infrastructure", Summary: "ogon infra gen → committed, editable artifacts"},
}

// explainErrorCode returns the class + doc URL for an E-code.
func explainErrorCode(rest string) (string, error) {
	code := strings.TrimSpace(rest)
	if code == "" {
		return "", errors.New("agent: explain error-code <CODE> (e.g. OGON-E3001)")
	}
	info, ok := codes.Lookup(code)
	if !ok {
		return "", fmt.Errorf("agent: unknown E-code %q", code)
	}
	return fmt.Sprintf("Code:    %s\nClass:   %s\nTitle:   %s\nDocs:    %s\n",
		info.Code, info.Class, info.ShortTitle, info.DocURL), nil
}

// explainAgentSurface reports the agent surface version + capabilities.
func explainAgentSurface(rest string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Agent surface version: %s\n", AgentSurfaceVersion)
	fmt.Fprintf(&b, "Manifest schema:      %s\n", ManifestVersion)
	fmt.Fprintf(&b, "MCP protocol version: %s\n", "2024-11-05")
	b.WriteString("\nCapabilities:\n")
	b.WriteString("  - .ogon/ogon.json manifest (deterministic, byte-stable)\n")
	b.WriteString("  - ogon mcp (JSON-RPC over stdio, safe tools only)\n")
	b.WriteString("  - ogon agent dump (full machine context in one call)\n")
	b.WriteString("  - ogon agent validate (manifest freshness)\n")
	b.WriteString("  - ogon explain (stable output contract)\n")
	b.WriteString("  - skills/ogongo/SKILL.md + per-task skills\n")
	return b.String(), nil
}

// explainManifest returns a stable description of the manifest schema.
func explainManifest(rest string) (string, error) {
	var b strings.Builder
	b.WriteString("Manifest schema (./.ogon/ogon.json):\n")
	b.WriteString("  schema_version: string — manifest schema version\n")
	b.WriteString("  agent_surface:  string — agent interface version\n")
	b.WriteString("  generated_at:   string — RFC3339 timestamp\n")
	b.WriteString("  framework:      string — OgonGo framework version\n")
	b.WriteString("  go_version:     string — runtime.GoVersion()\n")
	b.WriteString("  os / arch:      string — runtime.GOOS / GOARCH\n")
	b.WriteString("  project:        {name, root}\n")
	b.WriteString("  routes:         [{method, path, handler, middleware, openapi_id, auth}]\n")
	b.WriteString("  models:         [{name, table, fields, indexes, relations, endpoint}]\n")
	b.WriteString("  config_schema:  [{key, type, default, source}]\n")
	b.WriteString("  modules:        [{name, version, path}]\n")
	b.WriteString("  diag_codes:     codes.All() snapshot\n")
	b.WriteString("  test_map:       {source_file: [test_files]}\n")
	b.WriteString("  skills:         [{name, path, description}]\n")
	return b.String(), nil
}

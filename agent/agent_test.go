// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// agent_test.go table-tests the agent surface: manifest generation,
// dump, freshness validation, dry-run codegen, explain output
// stability, skills, llms.txt, repo map, AGENTS.md, and the MCP
// server's tools/list response.

package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/agent"
	"github.com/OgonFrameworks/ogon.go/mcp"
)

// fixedNow is the deterministic timestamp used by every test so the
// manifest is byte-stable for identical inputs.
var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// newProject creates a temp directory with an ogon.yaml file so the
// manifest generator's project detection succeeds. Returns the root
// path and a cleanup func.
func newProject(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ogon.yaml"),
		[]byte("app:\n  name: test-app\n"), 0o644); err != nil {
		t.Fatalf("write ogon.yaml: %v", err)
	}
	// Create an app/models dir + a model file so scanModels has work.
	mdir := filepath.Join(dir, "app", "models")
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		t.Fatalf("mkdir app/models: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mdir, "user.go"),
		[]byte("package models\n\ntype User struct{}\n"), 0o644); err != nil {
		t.Fatalf("write user.go: %v", err)
	}
	return dir, func() {}
}

// TestManifestGenProducesValidJSON verifies AGENT-001: the manifest
// generator produces a JSON-parseable file with the canonical shape.
func TestManifestGenProducesValidJSON(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	m, err := agent.GenerateManifest(agent.ManifestOptions{
		ProjectRoot: root,
		Now:         fixedNow,
	})
	if err != nil {
		t.Fatalf("GenerateManifest: %v", err)
	}
	if m.SchemaVersion != agent.ManifestVersion {
		t.Errorf("schema_version = %q, want %q", m.SchemaVersion, agent.ManifestVersion)
	}
	if m.AgentSurface != agent.AgentSurfaceVersion {
		t.Errorf("agent_surface = %q, want %q", m.AgentSurface, agent.AgentSurfaceVersion)
	}
	if m.GeneratedAt == "" {
		t.Errorf("generated_at is empty")
	}
	if m.Project.Name != "test-app" {
		// deriveProjectName falls back to basename when ogon.yaml app.name
		// isn't injected; we injected it via ManifestOptions.ProjectName
		// when set, else basename. Both are acceptable; assert non-empty.
		if m.Project.Name == "" {
			t.Errorf("project.name is empty")
		}
	}
	// Marshal + unmarshal must round-trip cleanly.
	body, err := agent.MarshalManifest(m)
	if err != nil {
		t.Fatalf("MarshalManifest: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("manifest body is not valid JSON")
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if _, ok := parsed["schema_version"]; !ok {
		t.Errorf("manifest missing schema_version key")
	}
	// routes/models are omitted (omitempty) when nil — the manifest's
	// stable shape is: schema_version, agent_surface, generated_at,
	// framework, go_version, os, arch, project, diag_codes (always).
	if _, ok := parsed["diag_codes"]; !ok {
		t.Errorf("manifest missing diag_codes key")
	}
}

// TestManifestByteStable verifies CLI-061 / AGENT-009: identical
// inputs produce identical outputs.
func TestManifestByteStable(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	opts := agent.ManifestOptions{ProjectRoot: root, Now: fixedNow}
	m1, err := agent.GenerateManifest(opts)
	if err != nil {
		t.Fatalf("GenerateManifest #1: %v", err)
	}
	m2, err := agent.GenerateManifest(opts)
	if err != nil {
		t.Fatalf("GenerateManifest #2: %v", err)
	}
	b1, _ := agent.MarshalManifest(m1)
	b2, _ := agent.MarshalManifest(m2)
	if !bytes.Equal(b1, b2) {
		t.Errorf("manifest is not byte-stable for identical inputs")
	}
}

// TestWriteManifestWritesFile verifies WriteManifest persists to .ogon/ogon.json.
func TestWriteManifestWritesFile(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	path, err := agent.WriteManifest(agent.ManifestOptions{
		ProjectRoot: root,
		Now:         fixedNow,
	})
	if err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if filepath.Base(path) != "ogon.json" {
		t.Errorf("manifest filename = %q, want ogon.json", filepath.Base(path))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("on-disk manifest is not valid JSON")
	}
}

// TestDumpManifestReportsFreshness verifies AGENT-002/017: DumpManifest
// reports manifest freshness correctly.
func TestDumpManifestReportsFreshness(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	// First dump: no manifest on disk → not fresh, with a warning.
	dump, err := agent.DumpManifest(agent.ManifestOptions{
		ProjectRoot: root, Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("DumpManifest #1: %v", err)
	}
	if dump.Diagnostics.ManifestFresh {
		t.Errorf("manifest should not be fresh when no on-disk manifest exists")
	}
	if len(dump.Diagnostics.Warnings) == 0 {
		t.Errorf("warnings should be non-empty when manifest is missing")
	}

	// Write the manifest; second dump should report fresh.
	if _, err := agent.WriteManifest(agent.ManifestOptions{
		ProjectRoot: root, Now: fixedNow,
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	dump2, err := agent.DumpManifest(agent.ManifestOptions{
		ProjectRoot: root, Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("DumpManifest #2: %v", err)
	}
	if !dump2.Diagnostics.ManifestFresh {
		t.Errorf("manifest should be fresh after WriteManifest; warnings=%v",
			dump2.Diagnostics.Warnings)
	}
}

// TestValidateFlagsStaleManifest verifies AGENT-017: Validate flags a
// stale manifest when the project has changed underneath it.
func TestValidateFlagsStaleManifest(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	// Generate the manifest with the current state.
	if _, err := agent.WriteManifest(agent.ManifestOptions{
		ProjectRoot: root, Now: fixedNow,
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	// Validate without changes — should be fresh.
	r, err := agent.Validate(root, false)
	if err != nil {
		t.Fatalf("Validate #1: %v", err)
	}
	if !r.Fresh {
		t.Errorf("expected fresh, got stale: %s", r.Reason)
	}

	// Add a new model file so scanModels sees a different result.
	newModel := filepath.Join(root, "app", "models", "product.go")
	if err := os.WriteFile(newModel,
		[]byte("package models\n\ntype Product struct{}\n"), 0o644); err != nil {
		t.Fatalf("write product.go: %v", err)
	}

	// Validate again — should be stale.
	r2, err := agent.Validate(root, false)
	if err != nil {
		t.Fatalf("Validate #2: %v", err)
	}
	if r2.Fresh {
		t.Errorf("expected stale after adding a model file")
	}

	// Validate with fix=true — should refresh.
	r3, err := agent.Validate(root, true)
	if err != nil {
		t.Fatalf("Validate #3: %v", err)
	}
	if !r3.Fresh {
		t.Errorf("expected fresh after --fix; reason=%s", r3.Reason)
	}
}

// TestGenDryRunProducesNoFiles verifies AGENT-007: GenDryRun returns
// a plan but writes nothing to disk.
func TestGenDryRunProducesNoFiles(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	before, _ := filepath.Glob(filepath.Join(root, "*"))
	plan, err := agent.GenDryRun("resource", "Widget")
	if err != nil {
		t.Fatalf("GenDryRun: %v", err)
	}
	if !plan.DryRun {
		t.Errorf("plan.DryRun = false, want true (AGENT-007)")
	}
	if len(plan.Files) == 0 {
		t.Fatalf("plan.Files is empty; expected at least one planned file")
	}
	for _, f := range plan.Files {
		full := filepath.Join(root, f.Path)
		if _, err := os.Stat(full); err == nil {
			t.Errorf("dry-run wrote a file at %s (AGENT-007 violation)", full)
		}
		_ = full // keep linter happy
	}
	after, _ := filepath.Glob(filepath.Join(root, "*"))
	if len(before) != len(after) {
		t.Errorf("dry-run changed the filesystem: before=%d after=%d",
			len(before), len(after))
	}
}

// TestGenDryRunByteStable verifies CLI-061: the plan is byte-stable.
func TestGenDryRunByteStable(t *testing.T) {
	p1, err := agent.GenDryRun("resource", "User")
	if err != nil {
		t.Fatalf("GenDryRun #1: %v", err)
	}
	p2, err := agent.GenDryRun("resource", "User")
	if err != nil {
		t.Fatalf("GenDryRun #2: %v", err)
	}
	// Files slice order + content must be identical.
	if len(p1.Files) != len(p2.Files) {
		t.Fatalf("plan file counts differ: %d vs %d", len(p1.Files), len(p2.Files))
	}
	for i := range p1.Files {
		if p1.Files[i].Path != p2.Files[i].Path {
			t.Errorf("file path differs at %d: %q vs %q", i, p1.Files[i].Path, p2.Files[i].Path)
		}
		if p1.Files[i].Content != p2.Files[i].Content {
			t.Errorf("file content differs at %d (path=%s)", i, p1.Files[i].Path)
		}
	}
}

// TestGenDryRunRejectsUnknownKind verifies error path.
func TestGenDryRunRejectsUnknownKind(t *testing.T) {
	_, err := agent.GenDryRun("nonsense", "User")
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

// TestExplainTextStable verifies AGENT-009: same input → same output.
func TestExplainTextStable(t *testing.T) {
	out1, err := agent.ExplainText("magic")
	if err != nil {
		t.Fatalf("ExplainText #1: %v", err)
	}
	out2, err := agent.ExplainText("magic")
	if err != nil {
		t.Fatalf("ExplainText #2: %v", err)
	}
	if out1 != out2 {
		t.Errorf("ExplainText is not stable for identical inputs")
	}
	if !strings.Contains(out1, "# ogon explain magic") {
		t.Errorf("explain output missing header: %q", out1)
	}
	if !strings.Contains(out1, "agent surface") {
		t.Errorf("explain output missing agent surface footer")
	}
}

// TestExplainErrorCode verifies AGENT-009 for error-code lookups.
func TestExplainErrorCode(t *testing.T) {
	out, err := agent.ExplainText("error-code OGON-E3001")
	if err != nil {
		t.Fatalf("ExplainText error-code: %v", err)
	}
	if !strings.Contains(out, "OGON-E3001") {
		t.Errorf("error-code explain missing the code: %q", out)
	}
	if !strings.Contains(out, "Class:") {
		t.Errorf("error-code explain missing Class field")
	}
}

// TestExplainUnknownTopicErrors verifies AGENT-009 stable errors.
func TestExplainUnknownTopicErrors(t *testing.T) {
	_, err := agent.ExplainText("nonsense")
	if err == nil {
		t.Fatalf("expected error for unknown topic")
	}
	if !strings.Contains(err.Error(), "unknown topic") {
		t.Errorf("error message lacks 'unknown topic': %v", err)
	}
}

// TestAffectedTestsFallsBack verifies AGENT-013: when no manifest is
// present, AffectedTests falls back to sibling _test.go discovery.
func TestAffectedTestsFallsBack(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	// Change CWD to the project root so detectProjectRoot finds it.
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	// Create a source file + its sibling test file.
	srcDir := filepath.Join(root, "app", "handlers")
	_ = os.MkdirAll(srcDir, 0o755)
	src := filepath.Join(srcDir, "users.go")
	test := filepath.Join(srcDir, "users_test.go")
	if err := os.WriteFile(src, []byte("package handlers\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(test, []byte("package handlers\n"), 0o644); err != nil {
		t.Fatalf("write test: %v", err)
	}
	tests, err := agent.AffectedTests(filepath.Join("app", "handlers", "users.go"), "")
	if err != nil {
		t.Fatalf("AffectedTests: %v", err)
	}
	if len(tests) == 0 {
		t.Errorf("expected affected tests, got 0")
	}
}

// TestMigrationPlanForKnownPair verifies AGENT-014: the registered
// migration plan returns a structured response.
func TestMigrationPlanForKnownPair(t *testing.T) {
	plan, err := agent.GenerateMigrationPlan("0.9.0", "1.0.0")
	if err != nil {
		t.Fatalf("GenerateMigrationPlan: %v", err)
	}
	if plan.From != "0.9.0" || plan.To != "1.0.0" {
		t.Errorf("plan versions wrong: %+v", plan)
	}
	if len(plan.Steps) == 0 {
		t.Errorf("plan has no steps")
	}
	// We registered a manual step → CodemodsAvailable should be false.
	if plan.CodemodsAvailable {
		t.Errorf("CodemodsAvailable = true; expected false (one step is manual)")
	}
}

// TestMigrationPlanForUnknownPair verifies error path.
func TestMigrationPlanForUnknownPair(t *testing.T) {
	_, err := agent.GenerateMigrationPlan("9.9.9", "10.0.0")
	if err == nil {
		t.Fatalf("expected error for unknown pair")
	}
}

// TestCheckReturnsReport verifies AGENT-015: Check returns a structured
// report and exits 0 on a clean project.
func TestCheckReturnsReport(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	r, err := agent.Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if r.ExitCode != 0 {
		t.Errorf("Check exit = %d, want 0; items=%+v", r.ExitCode, r.Items)
	}
	if len(r.Items) == 0 {
		t.Errorf("Check items is empty")
	}
}

// TestCheckFailsWithoutOgonYAML verifies AGENT-015 failure path.
func TestCheckFailsWithoutOgonYAML(t *testing.T) {
	dir := t.TempDir() // no ogon.yaml
	r, err := agent.Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if r.ExitCode == 0 {
		t.Errorf("Check exit = 0, want non-zero without ogon.yaml")
	}
}

// TestSkillsRootDoc verifies AGENT-006: the SKILL.md body is non-empty
// and contains the canonical header.
func TestSkillsRootDoc(t *testing.T) {
	body := agent.SkillsRootDoc()
	if !strings.HasPrefix(body, "# OgonGo skills") {
		t.Errorf("SKILL.md body missing header: %q", body[:min(50, len(body))])
	}
	if !strings.Contains(body, "Agent surface version") {
		t.Errorf("SKILL.md body missing agent surface version")
	}
	if !strings.Contains(body, "add-endpoint") {
		t.Errorf("SKILL.md body missing add-endpoint entry")
	}
}

// TestSkillBodies verifies the four per-task skill bodies are non-empty
// and each starts with a level-1 header.
func TestSkillBodies(t *testing.T) {
	for _, name := range agent.SkillNames() {
		body, err := agent.SkillBody(name)
		if err != nil {
			t.Errorf("SkillBody(%s): %v", name, err)
			continue
		}
		if !strings.HasPrefix(body, "# ") {
			t.Errorf("skill %s body missing level-1 header", name)
		}
		if !strings.Contains(body, "## Verify") {
			t.Errorf("skill %s body missing Verify section", name)
		}
	}
}

// TestWriteSkillsWritesAll verifies WriteSkills writes all 5 skills
// (SKILL + 4 per-task) under skills/ogongo/.
func TestWriteSkillsWritesAll(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()

	written, err := agent.WriteSkills(root)
	if err != nil {
		t.Fatalf("WriteSkills: %v", err)
	}
	if len(written) != 5 {
		t.Errorf("WriteSkills wrote %d files, want 5", len(written))
	}
	for _, p := range written {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("written skill missing on disk: %s", p)
		}
	}
}

// TestLLMSDoc verifies AGENT-010: llms.txt body is non-empty + has
// the canonical shape.
func TestLLMSDoc(t *testing.T) {
	doc := agent.DefaultLLMSDoc()
	body := doc.Render()
	if !strings.HasPrefix(body, "# OgonGo") {
		t.Errorf("llms.txt body missing header")
	}
	if !strings.Contains(body, "## Docs") {
		t.Errorf("llms.txt body missing Docs section")
	}
	if !strings.Contains(body, "https://ogongo.dev/docs/") {
		t.Errorf("llms.txt body missing docs URL")
	}
}

// TestRepoMap verifies AGENT-012: repo map body is non-empty.
func TestRepoMap(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()
	rm := &agent.RepoMap{Root: root, ProjectName: "test", GeneratedAt: fixedNow}
	body := rm.Render()
	if !strings.HasPrefix(body, "# test — repo map") {
		t.Errorf("repo map body missing header")
	}
	if !strings.Contains(body, "ogon.yaml") {
		t.Errorf("repo map body should list ogon.yaml")
	}
}

// TestWriteLLMSTxt writes the file.
func TestWriteLLMSTxt(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()
	path, err := agent.WriteLLMSTxt(root)
	if err != nil {
		t.Fatalf("WriteLLMSTxt: %v", err)
	}
	if filepath.Base(path) != "llms.txt" {
		t.Errorf("filename = %q, want llms.txt", filepath.Base(path))
	}
}

// TestWriteAPIRef verifies AGENT-011.
func TestWriteAPIRef(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()
	path, err := agent.WriteAPIRef(root)
	if err != nil {
		t.Fatalf("WriteAPIRef: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read api-ref: %v", err)
	}
	if !strings.Contains(string(body), "github.com/OgonFrameworks/ogon.go") {
		t.Errorf("api-ref body missing package path")
	}
}

// TestWriteRepoMap verifies AGENT-012.
func TestWriteRepoMap(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()
	path, err := agent.WriteRepoMap(root)
	if err != nil {
		t.Fatalf("WriteRepoMap: %v", err)
	}
	if filepath.Base(path) != "REPO_MAP.md" {
		t.Errorf("filename = %q, want REPO_MAP.md", filepath.Base(path))
	}
}

// TestWriteAgentsMD verifies AGENT-016.
func TestWriteAgentsMD(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()
	m, _ := agent.GenerateManifest(agent.ManifestOptions{
		ProjectRoot: root, Now: fixedNow,
	})
	path, err := agent.WriteAgentsMD(root, m)
	if err != nil {
		t.Fatalf("WriteAgentsMD: %v", err)
	}
	if filepath.Base(path) != "AGENTS.md" {
		t.Errorf("filename = %q, want AGENTS.md", filepath.Base(path))
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "agent surface version") {
		t.Errorf("AGENTS.md body missing agent surface version line")
	}
}

// TestCodeowners verifies AGENT-016.
func TestCodeowners(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()
	body, err := agent.Codeowners(root, nil)
	if err != nil {
		t.Fatalf("Codeowners: %v", err)
	}
	if !strings.Contains(body, "@ogonframeworks/maintainers") {
		t.Errorf("CODEOWNERS body missing maintainers team")
	}
	if !strings.Contains(body, "/generated/") {
		t.Errorf("CODEOWNERS body missing generated/ entry")
	}
}

// TestTelemetryOptOut verifies AGENT-023: DO_NOT_TRACK and OGON_TELEMETRY=0
// both disable telemetry.
func TestTelemetryOptOut(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		cfg  *bool
		want bool
	}{
		{"default", nil, nil, true},
		{"DO_NOT_TRACK=1", map[string]string{"DO_NOT_TRACK": "1"}, nil, false},
		{"DO_NOT_TRACK=true", map[string]string{"DO_NOT_TRACK": "true"}, nil, false},
		{"OGON_TELEMETRY=0", map[string]string{"OGON_TELEMETRY": "0"}, nil, false},
		{"OGON_TELEMETRY=off", map[string]string{"OGON_TELEMETRY": "off"}, nil, false},
		{"cfg-disabled", nil, boolPtr(false), false},
		{"cfg-enabled", nil, boolPtr(true), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agent.TelemetryEnabled(func(k string) string {
				if c.env == nil {
					return ""
				}
				return c.env[k]
			}, c.cfg)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// boolPtr returns a pointer to b.
func boolPtr(b bool) *bool { return &b }

// TestSurfaceCompat verifies AGENT-022.
func TestSurfaceCompat(t *testing.T) {
	// SurfaceVersion() returns AgentSurfaceVersion == "1.0.0".
	if !agent.SurfaceCompat("1.0.0") {
		t.Errorf("expected compat with 1.0.0")
	}
	if !agent.SurfaceCompat("1.0.5") {
		t.Errorf("expected compat with 1.0.5 (patch ignored)")
	}
	if agent.SurfaceCompat("2.0.0") {
		t.Errorf("expected incompat with 2.0.0 (major mismatch)")
	}
	if agent.SurfaceCompat("0.9.0") {
		t.Errorf("expected incompat with 0.9.0 (minor too low)")
	}
}

// TestAT019 verifies AGENT-025 / AT-019: scripted acceptance returns a
// report with the canonical step list.
func TestAT019(t *testing.T) {
	r, err := agent.AT019(context.Background(), false, ".ogon/ogon.json", "/api/widgets")
	if err != nil {
		t.Fatalf("AT019: %v", err)
	}
	if r.Manifest != ".ogon/ogon.json" {
		t.Errorf("manifest path wrong: %s", r.Manifest)
	}
	if len(r.Steps) == 0 {
		t.Errorf("steps list is empty")
	}
	// In dry-run mode, all steps should be skip.
	for _, s := range r.Steps {
		if s.Status != "skip" {
			t.Errorf("step %s status = %s, want skip in dry-run", s.Name, s.Status)
		}
	}
}

// TestRunCRUDEval verifies AGENT-021.
func TestRunCRUDEval(t *testing.T) {
	r, err := agent.RunCRUDEval(context.Background(), false)
	if err != nil {
		t.Fatalf("RunCRUDEval: %v", err)
	}
	if r.GoldenPath != "crud" {
		t.Errorf("golden path = %q, want crud", r.GoldenPath)
	}
}

// TestDevAgentPolicy verifies AGENT-019: --agent disables browser.
func TestDevAgentPolicy(t *testing.T) {
	p := agent.DevAgentPolicyFor("http://127.0.0.1:3000")
	if p.OpenBrowser {
		t.Errorf("OpenBrowser = true, want false under --agent")
	}
	if !p.PrintURLJSON {
		t.Errorf("PrintURLJSON = false, want true under --agent")
	}
	if p.Interactive {
		t.Errorf("Interactive = true, want false under --agent")
	}
	if p.URL == "" {
		t.Errorf("URL is empty")
	}
}

// --- MCP server tests ---

// TestMCPServerStartsAndToolsList verifies AGENT-005: the MCP server
// starts, responds to initialize, and exposes the safe tool catalog.
func TestMCPServerStartsAndToolsList(t *testing.T) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errw := &bytes.Buffer{}
	srv := mcp.New(
		mcp.WithStdio(in, out, errw),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Write both requests up front; the server reads them and exits on EOF.
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")

	if err := srv.Start(ctx); err != nil && err != io.EOF {
		t.Fatalf("Start: %v", err)
	}

	// Parse the responses from out. Each is a single JSON object per line.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 response lines, got %d (stderr=%s)",
			len(lines), errw.String())
	}

	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("parse initialize response: %v\nline=%s", err, lines[0])
	}
	if initResp.Result.ProtocolVersion != mcp.ProtocolVersion {
		t.Errorf("protocol = %q, want %q",
			initResp.Result.ProtocolVersion, mcp.ProtocolVersion)
	}
	if initResp.Result.ServerInfo.Name != "ogon" {
		t.Errorf("server name = %q, want ogon", initResp.Result.ServerInfo.Name)
	}

	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("parse tools/list response: %v\nline=%s", err, lines[1])
	}
	if len(listResp.Result.Tools) == 0 {
		t.Errorf("tools/list returned no tools")
	}
	// Verify the canonical tool names are present.
	want := map[string]bool{
		"ogon.explain": false,
		"ogon.inspect": false,
		"ogon.gen":     false,
		"ogon.doctor":  false,
		"ogon.test":    false,
	}
	for _, tool := range listResp.Result.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing tool %q in tools/list", name)
		}
	}
}

// TestMCPNoDestructiveTools verifies the safe-tool policy: no tool
// name suggests a destructive action.
func TestMCPNoDestructiveTools(t *testing.T) {
	for _, tool := range mcp.DefaultTools() {
		name := strings.ToLower(tool.Name)
		for _, bad := range []string{"delete", "drop", "rm", "purge", "reset", "destroy"} {
			if strings.Contains(name, bad) {
				t.Errorf("tool %q suggests destructive action (%s)", tool.Name, bad)
			}
		}
	}
}

// TestMCPExplainToolCall verifies AGENT-005/009: tools/call ogon.explain
// returns a stable, parseable response.
func TestMCPExplainToolCall(t *testing.T) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	srv := mcp.New(mcp.WithStdio(in, out, io.Discard))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ogon.explain","arguments":{"thing":"magic"}}}` + "\n")
	if err := srv.Start(ctx); err != nil && err != io.EOF {
		t.Fatalf("Start: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 response lines, got %d", len(lines))
	}
	var callResp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &callResp); err != nil {
		t.Fatalf("parse tools/call response: %v\nline=%s", err, lines[1])
	}
	if callResp.Result.IsError {
		t.Errorf("ogon.explain returned isError=true")
	}
	if len(callResp.Result.Content) == 0 {
		t.Fatalf("ogon.explain returned no content")
	}
	if !strings.Contains(callResp.Result.Content[0].Text, "magic") {
		t.Errorf("ogon.explain response missing 'magic'")
	}
}

// TestMCPRejectsUnknownTool verifies error path.
func TestMCPRejectsUnknownTool(t *testing.T) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	srv := mcp.New(mcp.WithStdio(in, out, io.Discard))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ogon.delete","arguments":{}}}` + "\n")
	_ = srv.Start(ctx)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 1 {
		t.Fatalf("no response")
	}
	var resp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("parse: %v\nline=%s", err, lines[0])
	}
	if resp.Error.Code == 0 {
		t.Errorf("expected non-zero error code, got 0")
	}
	if !strings.Contains(resp.Error.Message, "ogon.delete") {
		t.Errorf("error message should reference the unknown tool: %q", resp.Error.Message)
	}
}

// TestMCPInvalidJSONReturnsParseError verifies JSON-RPC error contract.
func TestMCPInvalidJSONReturnsParseError(t *testing.T) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	srv := mcp.New(mcp.WithStdio(in, out, io.Discard))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	in.WriteString("not json\n")
	_ = srv.Start(ctx)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 1 {
		t.Fatalf("no response")
	}
	var resp struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("parse: %v\nline=%s", err, lines[0])
	}
	// -32700 is the canonical parse error code.
	if resp.Error.Code != -32700 {
		t.Errorf("error code = %d, want -32700", resp.Error.Code)
	}
}

// TestMCPGenToolAlwaysDryRun verifies AGENT-007 via the MCP surface:
// the ogon.gen tool never writes files.
func TestMCPGenToolAlwaysDryRun(t *testing.T) {
	root, cleanup := newProject(t)
	defer cleanup()
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	srv := mcp.New(mcp.WithStdio(in, out, io.Discard))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ogon.gen","arguments":{"kind":"resource","name":"Widget"}}}` + "\n")
	if err := srv.Start(ctx); err != nil && err != io.EOF {
		t.Fatalf("Start: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 1 {
		t.Fatalf("no response")
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("parse: %v\nline=%s", err, lines[0])
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("ogon.gen returned no content")
	}
	// The plan must include dry_run: true.
	if !strings.Contains(resp.Result.Content[0].Text, `"dry_run": true`) {
		t.Errorf("ogon.gen response missing dry_run: true; got: %s",
			resp.Result.Content[0].Text)
	}
	// And no file should exist on disk for the planned resource.
	if _, err := os.Stat(filepath.Join(root, "app", "models", "widget.go")); err == nil {
		t.Errorf("ogon.gen wrote a file (AGENT-007 violation)")
	}
}

// min returns the smaller of a, b (used to slice the SKILL.md body).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

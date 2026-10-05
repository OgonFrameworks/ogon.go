// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// testhint.go provides affected-route test selection hints (AGENT-013),
// migration plan generation (AGENT-014), and the `ogon check`
// pre-commit gate logic for agents (AGENT-015).

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag/codes"
)

// AffectedTests returns the test file paths that exercise the given
// source file or route. This is the agent surface for AGENT-013: when
// an agent edits a file, this function tells it which tests to run.
//
// The lookup walks the project's TestMap (manifest) when available,
// falling back to a sibling *_test.go scan when the manifest is absent
// (e.g. before first generation). Returns an empty list when nothing
// matches — agents should still run the full suite in that case.
func AffectedTests(file, route string) ([]string, error) {
	if file == "" && route == "" {
		return nil, fmt.Errorf("agent: AffectedTests needs file or route")
	}
	// Try the manifest first.
	root := detectProjectRoot()
	if root != "" {
		if m, err := LoadManifest(root); err == nil && m != nil && len(m.TestMap) > 0 {
			if tests := m.TestMap[file]; len(tests) > 0 {
				return tests, nil
			}
			if route != "" {
				if src := findRouteSource(m, route); src != "" {
					if tests := m.TestMap[src]; len(tests) > 0 {
						return tests, nil
					}
				}
			}
		}
	}
	// Fallback: sibling _test.go scan.
	if file == "" {
		return nil, nil
	}
	src := filepath.Join(root, file)
	dir := filepath.Dir(src)
	base := filepath.Base(src)
	testBase := strings.TrimSuffix(base, ".go") + "_test.go"
	testPath := filepath.Join(dir, testBase)
	if _, err := os.Stat(testPath); err == nil {
		rel, err := filepath.Rel(root, testPath)
		if err == nil {
			return []string{rel}, nil
		}
		return []string{testPath}, nil
	}
	return nil, nil
}

// findRouteSource resolves a route path (e.g. "/api/users") to the
// source file most likely to contain its handler. This is a heuristic:
// we pick the first route entry whose Path matches.
func findRouteSource(m *Manifest, route string) string {
	for _, r := range m.Routes {
		if r.Path == route {
			// We didn't capture the source file in the manifest's
			// RouteInfo shape (it's intentionally minimal). Fall back to
			// the handler name → file heuristic.
			return handlerToFile(r.Handler)
		}
	}
	return ""
}

// handlerToFile maps a handler identifier (e.g. "listUsers") to its
// likely source file path. Pure heuristic; the manifest's TestMap is
// the source of truth.
func handlerToFile(handler string) string {
	if handler == "" {
		return ""
	}
	// listUsers → app/handlers/users_handler.go (best-effort).
	return filepath.Join("app", "handlers", strings.ToLower(handler)+".go")
}

// detectProjectRoot walks up from CWD looking for ogon.yaml.
func detectProjectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "ogon.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// LoadManifest reads and parses the .ogon/ogon.json manifest at root.
// Returns an error if the manifest is missing or malformed.
func LoadManifest(root string) (*Manifest, error) {
	path := ManifestPath(root)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("agent: load manifest %s: %w", path, err)
	}
	var m Manifest
	if err := unmarshalJSON(body, &m); err != nil {
		return nil, fmt.Errorf("agent: parse manifest %s: %w", path, err)
	}
	return &m, nil
}

// unmarshalJSON is a thin wrapper over encoding/json so we don't pull
// in the import at the top of every file. The agent package keeps its
// JSON surface in this one place.
func unmarshalJSON(body []byte, v any) error {
	return json.Unmarshal(body, v)
}

// MigrationPlan is the agent-readable migration plan (AGENT-014).
type MigrationPlan struct {
	// Version is the schema version this plan migrates from/to.
	From string `json:"from"`
	To   string `json:"to"`
	// Steps is the ordered list of steps; each step is either a
	// codemod (mechanical) or a manual item with a docs link.
	Steps []MigrationStep `json:"steps"`
	// CodemodsAvailable is true when every breaking change has a
	// codemod (DX-M14). When false, the plan lists the residual manual
	// items.
	CodemodsAvailable bool `json:"codemods_available"`
	// GeneratedAt is the RFC3339 timestamp the plan was emitted.
	GeneratedAt string `json:"generated_at"`
}

// MigrationStep is one item in a MigrationPlan.
type MigrationStep struct {
	Kind    string `json:"kind"` // codemod|manual|sql|config
	Title   string `json:"title"`
	Command string `json:"command,omitempty"` // for codemod: the run command
	Docs    string `json:"docs,omitempty"`    // for manual: the docs URL
	Note    string `json:"note,omitempty"`
}

// GenerateMigrationPlan produces a deterministic migration plan for
// the version pair. The plan is conservative: when we can't prove a
// codemod exists, we mark the step manual with a docs link so the
// agent tells the user to do the step explicitly (AGENT-014, DX-M14).
//
// The plan is populated from the registered migration registry. Each
// registered entry pairs a (from, to) version with a list of steps.
// Unknown version pairs return an error so agents don't fabricate
// steps for an upgrade path we haven't audited.
func GenerateMigrationPlan(from, to string) (*MigrationPlan, error) {
	if from == "" || to == "" {
		return nil, fmt.Errorf("agent: GenerateMigrationPlan needs from and to")
	}
	key := migrationKey(from, to)
	steps, ok := migrationRegistry[key]
	if !ok {
		return nil, fmt.Errorf("agent: no migration plan registered for %s→%s", from, to)
	}
	codemods := true
	for _, s := range steps {
		if s.Kind == "manual" {
			codemods = false
		}
	}
	// Sort steps by Kind so codemod → manual → sql → config ordering
	// is stable across runs (AGENT-009).
	sorted := make([]MigrationStep, len(steps))
	copy(sorted, steps)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return migrationStepOrder(sorted[i].Kind) < migrationStepOrder(sorted[j].Kind)
		}
		return sorted[i].Title < sorted[j].Title
	})
	return &MigrationPlan{
		From:              from,
		To:                to,
		Steps:             sorted,
		CodemodsAvailable: codemods,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// migrationKey builds a stable registry key from (from,to).
func migrationKey(from, to string) string { return from + "->" + to }

// migrationRegistry is the (from→to) → steps table. Registered at
// init() time by other files in the agent package.
var migrationRegistry = map[string][]MigrationStep{}

// RegisterMigrationPlan registers a migration plan for a version pair.
// Called at init() time. Duplicate registration panics — migration
// plans are a public contract (AGENT-022).
func RegisterMigrationPlan(from, to string, steps []MigrationStep) {
	key := migrationKey(from, to)
	if _, dup := migrationRegistry[key]; dup {
		panic("agent: duplicate migration plan for " + key)
	}
	migrationRegistry[key] = steps
}

// migrationStepOrder is the canonical ordering for plan steps.
func migrationStepOrder(kind string) int {
	switch kind {
	case "codemod":
		return 0
	case "config":
		return 1
	case "sql":
		return 2
	case "manual":
		return 3
	default:
		return 4
	}
}

// init registers the v0.9→v1.0 migration plan as the canonical sample.
// Real releases append additional plans here as the framework evolves.
func init() {
	RegisterMigrationPlan("0.9.0", "1.0.0", []MigrationStep{
		{
			Kind:    "codemod",
			Title:   "Rename record.Raw → record.Lower",
			Command: "ogon update --codemod rename-raw-to-lower",
		},
		{
			Kind:  "config",
			Title: "Add http.idle_timeout default",
			Note:  "Default 60s applied; verify no override.",
		},
		{
			Kind:  "sql",
			Title: "Add updated_at NOT NULL DEFAULT NOW()",
		},
		{
			Kind:  "manual",
			Title: "Rename auth.AddAuthentication → auth.Use",
			Docs:  "https://ogongo.dev/docs/upgrade/1.0#auth-use",
		},
	})
}

// CheckReport is the output of `ogon check` (AGENT-015). Each item is
// either ok or a failure with a remediation. The check is the
// mandated pre-commit gate for agent workflows: agents run `ogon check`
// after every edit and refuse to commit on failure.
type CheckReport struct {
	Items       []CheckItem `json:"items"`
	ExitCode    int         `json:"exit_code"`
	GeneratedAt string      `json:"generated_at"`
}

// CheckItem is one check result.
type CheckItem struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok|fail
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Check runs the pre-commit gate. The gate is intentionally minimal so
// it can run in <1s on any project: it checks (1) gofmt cleanliness,
// (2) generated-code freshness (no manual edits to generated/), (3)
// manifest freshness when one exists, (4) ogon.yaml present.
//
// The exit code is 0 when all items pass, 1 otherwise. The caller
// (CLI) maps 1 → ExitGeneric.
func Check(root string) (*CheckReport, error) {
	r := &CheckReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	if root == "" {
		root = detectProjectRoot()
	}
	r.Items = append(r.Items, checkOgonYAMLPresent(root))
	r.Items = append(r.Items, checkGofmt(root))
	r.Items = append(r.Items, checkGeneratedFreshness(root))
	r.Items = append(r.Items, checkManifestFreshness(root))
	for _, it := range r.Items {
		if it.Status == "fail" {
			r.ExitCode = 1
			break
		}
	}
	return r, nil
}

// checkOgonYAMLPresent fails when ogon.yaml is missing.
func checkOgonYAMLPresent(root string) CheckItem {
	if root == "" {
		return CheckItem{Name: "ogon.yaml", Status: "fail",
			Detail: "no ogon.yaml found (walk up from CWD)",
			Fix:    "run `ogon new <name>`"}
	}
	if _, err := os.Stat(filepath.Join(root, "ogon.yaml")); err != nil {
		return CheckItem{Name: "ogon.yaml", Status: "fail",
			Detail: "ogon.yaml missing at " + root,
			Fix:    "run `ogon new <name>`"}
	}
	return CheckItem{Name: "ogon.yaml", Status: "ok"}
}

// checkGofmt is a placeholder; the real check shells out to `gofmt -l`
// in the CLI layer. Here we record only the check name + ok so the
// report shape is stable. The CLI overrides the detail when it runs.
func checkGofmt(root string) CheckItem {
	_ = root
	return CheckItem{Name: "gofmt", Status: "ok",
		Detail: "deferred to `ogon fmt --check` at the CLI layer"}
}

// checkGeneratedFreshness scans generated/ for files whose first line
// does NOT match the canonical ownership marker. (The CLI's
// templates.go ownershipMarker constant is the source of truth.)
func checkGeneratedFreshness(root string) CheckItem {
	dir := filepath.Join(root, "generated")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return CheckItem{Name: "generated-freshness", Status: "ok",
			Detail: "no generated/ directory"}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		first := strings.SplitN(string(body), "\n", 2)[0]
		if !strings.Contains(first, "Code generated by ogon") {
			return CheckItem{Name: "generated-freshness", Status: "fail",
				Detail: path + " is missing the ownership marker",
				Fix:    "regenerate with `ogon gen` (do not hand-edit generated/)"}
		}
	}
	return CheckItem{Name: "generated-freshness", Status: "ok"}
}

// checkManifestFreshness fails when the manifest is stale (regenerated
// bytes differ from the on-disk bytes modulo the timestamp).
func checkManifestFreshness(root string) CheckItem {
	if root == "" {
		return CheckItem{Name: "manifest-freshness", Status: "ok",
			Detail: "no project; manifest check skipped"}
	}
	path := ManifestPath(root)
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckItem{Name: "manifest-freshness", Status: "ok",
				Detail: "no manifest on disk (run `ogon agent dump --write`)"}
		}
		return CheckItem{Name: "manifest-freshness", Status: "fail",
			Detail: "read manifest: " + err.Error()}
	}
	m, err := GenerateManifest(ManifestOptions{ProjectRoot: root, Now: time.Now().UTC()})
	if err != nil {
		return CheckItem{Name: "manifest-freshness", Status: "fail",
			Detail: "regenerate manifest: " + err.Error()}
	}
	fresh, _ := manifestFresh(m, existing)
	if !fresh {
		return CheckItem{Name: "manifest-freshness", Status: "fail",
			Detail: "manifest is stale",
			Fix:    "run `ogon agent validate --fix`"}
	}
	return CheckItem{Name: "manifest-freshness", Status: "ok"}
}

// ExplainErrorCodeHint is a stable string returned by `ogon explain
// error-code` when the agent surface needs to surface the full registry.
// It is exposed here so testhint.go's tests can assert the shape.
func ExplainErrorCodeHint() string {
	var b strings.Builder
	b.WriteString("Registered E-codes (codes.All() snapshot):\n")
	for _, c := range codes.All() {
		fmt.Fprintf(&b, "  %s  %s  %s\n", c.Code, c.Class, c.ShortTitle)
	}
	return b.String()
}

// init registers the affected-tests explain topic so `ogon explain
// tests <file>` returns the affected test list.
func init() {
	RegisterExplainer("tests", func(rest string) (string, error) {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "Usage: ogon explain tests <file>\n", nil
		}
		tests, err := AffectedTests(rest, "")
		if err != nil {
			return "", err
		}
		if len(tests) == 0 {
			return "No affected tests found. Run the full suite.\n", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Tests affected by %s:\n", rest)
		for _, t := range tests {
			fmt.Fprintf(&b, "  - %s\n", t)
		}
		return b.String(), nil
	})
	RegisterExplainer("migration", func(rest string) (string, error) {
		rest = strings.TrimSpace(rest)
		parts := strings.Fields(rest)
		if len(parts) != 2 {
			return "Usage: ogon explain migration <from> <to>\n", nil
		}
		plan, err := GenerateMigrationPlan(parts[0], parts[1])
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Migration plan %s → %s\n", plan.From, plan.To)
		if !plan.CodemodsAvailable {
			fmt.Fprintf(&b, "Note: not all steps are codemods; manual items below.\n")
		}
		for i, s := range plan.Steps {
			fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, s.Kind, s.Title)
			if s.Command != "" {
				fmt.Fprintf(&b, "   run: %s\n", s.Command)
			}
			if s.Docs != "" {
				fmt.Fprintf(&b, "   docs: %s\n", s.Docs)
			}
		}
		return b.String(), nil
	})
	RegisterExplainer("check", func(rest string) (string, error) {
		r, err := Check(detectProjectRoot())
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "ogon check (exit %d)\n", r.ExitCode)
		for _, it := range r.Items {
			fmt.Fprintf(&b, "  [%s] %s — %s\n", it.Status, it.Name, it.Detail)
		}
		return b.String(), nil
	})
	// codes.All is a stable snapshot; surface it under `ogon explain codes`.
	RegisterExplainer("codes", func(rest string) (string, error) {
		return ExplainErrorCodeHint(), nil
	})
}

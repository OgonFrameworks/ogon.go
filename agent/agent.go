// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// agent.go provides the machine-manifest generator and dump emitter
// for OgonGo's agent surface (AGENT-001..004, AGENT-017). The manifest
// is a single .ogon/ogon.json file that captures everything an AI agent
// needs to understand a project: routes, models + fields, config schema,
// modules, framework versions, the diag-code index, and the test map.
// `ogon agent dump` emits the same data + diagnostics in one call.

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/OgonFrameworks/ogon.go"
	"github.com/OgonFrameworks/ogon.go/diag/codes"
)

// ManifestVersion is the schema version of the .ogon/ogon.json manifest.
// Bumped only on breaking shape change to the manifest. The manifest is
// a public contract per spec Part XIX.1 (independently versioned from
// the framework).
const ManifestVersion = "1.0.0"

// AgentSurfaceVersion is the version of the agent *interface* — the
// stable contract for `ogon agent dump`, `ogon mcp`, the manifest schema,
// and `ogon explain` output. Bumped per spec AGENT-022 when any agent-
// facing surface changes shape.
const AgentSurfaceVersion = "1.0.0"

// ManifestDir is the canonical directory for the manifest file. Lives
// under .ogon/ so it is gitignored by default; CI regenerates it.
const ManifestDir = ".ogon"

// ManifestFile is the canonical manifest filename.
const ManifestFile = "ogon.json"

// ManifestPath returns the absolute path to the manifest file under the
// given project root. The root must be the directory containing ogon.yaml.
func ManifestPath(root string) string {
	return filepath.Join(root, ManifestDir, ManifestFile)
}

// Manifest is the in-memory shape of .ogon/ogon.json. Fields are ordered
// alphabetically inside nested blocks for stable diffability.
type Manifest struct {
	// SchemaVersion is the manifest schema version (ManifestVersion).
	// Agents MUST verify this before consuming the manifest.
	SchemaVersion string `json:"schema_version"`

	// AgentSurface is the version of the agent interface contract
	// (AGENT-022). Agents MUST verify compatibility against this.
	AgentSurface string `json:"agent_surface"`

	// GeneratedAt is the RFC3339 timestamp the manifest was emitted.
	// Useful for freshness checks (AGENT-017).
	GeneratedAt string `json:"generated_at"`

	// Framework is the OgonGo framework version (ogon.Version).
	Framework string `json:"framework"`

	// GoVersion is the runtime.GoVersion() of the generating binary.
	GoVersion string `json:"go_version"`

	// OS is runtime.GOOS.
	OS string `json:"os"`

	// Arch is runtime.GOARCH.
	Arch string `json:"arch"`

	// Project is the project name (app.name from ogon.yaml) and root.
	Project ProjectInfo `json:"project"`

	// Routes is the list of HTTP routes registered in the app.
	Routes []RouteInfo `json:"routes,omitempty"`

	// Models is the list of OgonRecord models with their fields,
	// indexes, and relations.
	Models []ModelInfo `json:"models,omitempty"`

	// ConfigSchema is a flattened list of config keys with their
	// expected types and defaults.
	ConfigSchema []ConfigKeyInfo `json:"config_schema,omitempty"`

	// Modules is the list of installed OgonGo modules.
	Modules []ModuleInfo `json:"modules,omitempty"`

	// DiagCodes is the list of registered E-codes (codes.All). Each
	// entry has Code/Class/ShortTitle/DocURL.
	DiagCodes []codes.CodeInfo `json:"diag_codes,omitempty"`

	// TestMap maps a source file path to the list of test files that
	// exercise it (AGENT-013 affected-route test selection).
	TestMap map[string][]string `json:"test_map,omitempty"`

	// Skills is the list of agent skills available under skills/.
	Skills []SkillInfo `json:"skills,omitempty"`
}

// ProjectInfo captures the project identity.
type ProjectInfo struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

// RouteInfo describes one HTTP route.
type RouteInfo struct {
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Handler    string   `json:"handler"`
	Middleware []string `json:"middleware,omitempty"`
	OpenAPIID  string   `json:"openapi_id,omitempty"`
	Auth       string   `json:"auth,omitempty"` // none|session|jwt|passkey
}

// ModelInfo describes one OgonRecord model.
type ModelInfo struct {
	Name      string         `json:"name"`
	Table     string         `json:"table"`
	Fields    []FieldInfo    `json:"fields"`
	Indexes   []IndexInfo    `json:"indexes,omitempty"`
	Relations []RelationInfo `json:"relations,omitempty"`
	Endpoint  string         `json:"endpoint,omitempty"` // REST path
}

// FieldInfo describes one model field.
type FieldInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Tag      string `json:"tag,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`
	Primary  bool   `json:"primary,omitempty"`
	Unique   bool   `json:"unique,omitempty"`
	Default  string `json:"default,omitempty"`
}

// IndexInfo describes one database index.
type IndexInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}

// RelationInfo describes one model relation.
type RelationInfo struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // has_one|has_many|belongs_to|many_to_many
	Model   string `json:"model"`
	Foreign string `json:"foreign,omitempty"`
}

// ConfigKeyInfo describes one config key in the merged config tree.
type ConfigKeyInfo struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
	Source  string `json:"source,omitempty"` // file|env|flag|inline|default
}

// ModuleInfo describes one installed OgonGo module.
type ModuleInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path,omitempty"`
}

// SkillInfo describes one agent skill.
type SkillInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

// ManifestOptions configures the generator.
type ManifestOptions struct {
	// ProjectRoot is the path to the project root (where ogon.yaml lives).
	// Required.
	ProjectRoot string

	// ProjectName overrides the project name (default: derive from root
	// dir basename or ogon.yaml app.name).
	ProjectName string

	// Routes allows the caller to inject route info. When nil the
	// generator scans app/routes.go for a minimal heuristic.
	Routes []RouteInfo

	// Models allows the caller to inject model info. When nil the
	// generator scans app/models/ for *.go files and parses the
	// minimum-viable struct shape.
	Models []ModelInfo

	// ConfigSchema allows the caller to inject config keys.
	ConfigSchema []ConfigKeyInfo

	// Modules allows the caller to inject module info.
	Modules []ModuleInfo

	// TestMap allows the caller to inject the test map. When nil the
	// generator scans for *_test.go files paired with their source files.
	TestMap map[string][]string

	// Skills allows the caller to inject skill info.
	Skills []SkillInfo

	// Now overrides the timestamp used for GeneratedAt. Tests pass a
	// fixed value so the manifest is byte-stable for identical inputs.
	Now time.Time
}

// GenerateManifest builds a Manifest from the given options without
// writing it to disk. Use WriteManifest to persist.
//
// The output is byte-stable for identical inputs (AGENT-009 / CLI-061):
// maps are sorted by key, slices by Name/Path, and the timestamp is
// overridable for tests.
func GenerateManifest(opts ManifestOptions) (*Manifest, error) {
	if opts.ProjectRoot == "" {
		return nil, fmt.Errorf("agent: ProjectRoot is required")
	}
	root, err := filepath.Abs(opts.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("agent: resolve root: %w", err)
	}
	name := opts.ProjectName
	if name == "" {
		name = deriveProjectName(root)
	}

	routes := opts.Routes
	if routes == nil {
		routes = scanRoutes(root)
	}
	models := opts.Models
	if models == nil {
		models = scanModels(root)
	}
	configSchema := opts.ConfigSchema
	if configSchema == nil {
		configSchema = scanConfigSchema(root)
	}
	modules := opts.Modules
	if modules == nil {
		modules = scanModules(root)
	}
	testMap := opts.TestMap
	if testMap == nil {
		testMap = scanTestMap(root)
	}
	skills := opts.Skills
	if skills == nil {
		skills = scanSkills(root)
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	m := &Manifest{
		SchemaVersion: ManifestVersion,
		AgentSurface:  AgentSurfaceVersion,
		GeneratedAt:   now.Format(time.RFC3339),
		Framework:     ogon.Version,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Project: ProjectInfo{
			Name: name,
			Root: root,
		},
		Routes:       sortRoutes(routes),
		Models:       sortModels(models),
		ConfigSchema: sortConfig(configSchema),
		Modules:      sortModules(modules),
		DiagCodes:    codes.All(),
		TestMap:      sortedTestMap(testMap),
		Skills:       sortSkills(skills),
	}
	return m, nil
}

// WriteManifest generates the manifest for the project root and writes
// it to .ogon/ogon.json. The directory is created if missing. The
// returned path is the absolute path to the manifest file.
//
// On byte-stable inputs (Now fixed), repeated calls produce identical
// file contents (CLI-061 / AGENT-009).
func WriteManifest(opts ManifestOptions) (string, error) {
	m, err := GenerateManifest(opts)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(m.Project.Root, ManifestDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("agent: mkdir %s: %w", dir, err)
	}
	body, err := MarshalManifest(m)
	if err != nil {
		return "", err
	}
	path := ManifestPath(m.Project.Root)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("agent: write %s: %w", path, err)
	}
	return path, nil
}

// MarshalManifest serializes a Manifest to canonical JSON: 2-space
// indent, stable key order (encoding/json sorts struct fields by
// declaration order; slice entries by their natural ordering), no HTML
// escaping (so URLs render verbatim), trailing newline preserved by
// Encoder.Encode. The output is byte-stable for identical inputs.
func MarshalManifest(m *Manifest) ([]byte, error) {
	if m == nil {
		return []byte("null\n"), nil
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("agent: marshal manifest: %w", err)
	}
	return []byte(buf.String()), nil
}

// MarshalIndent is an alias for MarshalManifest kept for naming
// symmetry with encoding/json.MarshalIndent. They share the same
// canonical formatter.
func MarshalIndent(m *Manifest) ([]byte, error) {
	return MarshalManifest(m)
}

// Dump is the full machine-context emitter (AGENT-002). It returns the
// manifest plus a diagnostics block. The diagnostics block summarizes
// the local environment for an agent: project detected, manifest fresh,
// routes count, models count, generation warnings.
type Dump struct {
	Manifest    *Manifest           `json:"manifest"`
	Diagnostics *MachineDiagnostics `json:"diagnostics"`
}

// MachineDiagnostics is the AGENT-004 machine-diagnostics format. Every
// field has a stable shape so agents can parse with confidence.
type MachineDiagnostics struct {
	// ProjectDetected is true when ogon.yaml was found at root.
	ProjectDetected bool `json:"project_detected"`
	// ManifestFresh is true when .ogon/ogon.json exists and matches the
	// regenerated manifest (byte-stable comparison; AGENT-017).
	ManifestFresh bool `json:"manifest_fresh"`
	// ManifestPath is the absolute path to the on-disk manifest, or "".
	ManifestPath string `json:"manifest_path,omitempty"`
	// RouteCount, ModelCount, ModuleCount are summaries.
	RouteCount  int `json:"route_count"`
	ModelCount  int `json:"model_count"`
	ModuleCount int `json:"module_count"`
	// Warnings is a list of human-readable warnings (e.g. "stale manifest,
	// run `ogon agent validate` to refresh").
	Warnings []string `json:"warnings,omitempty"`
	// Framework, GoVersion, OS, Arch mirror the manifest for convenience.
	Framework string `json:"framework"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// DumpManifest generates a manifest for the given root, compares it
// against the on-disk manifest (if present), and returns the Dump
// payload with diagnostics populated. The on-disk manifest is NOT
// modified; callers use WriteManifest or `ogon agent validate --fix`
// to refresh.
func DumpManifest(opts ManifestOptions) (*Dump, error) {
	m, err := GenerateManifest(opts)
	if err != nil {
		return nil, err
	}
	diag := &MachineDiagnostics{
		ProjectDetected: fileExists(filepath.Join(opts.ProjectRoot, "ogon.yaml")),
		ManifestPath:    ManifestPath(opts.ProjectRoot),
		RouteCount:      len(m.Routes),
		ModelCount:      len(m.Models),
		ModuleCount:     len(m.Modules),
		Framework:       m.Framework,
		GoVersion:       m.GoVersion,
		OS:              m.OS,
		Arch:            m.Arch,
	}
	// Freshness: byte-compare regenerated manifest against on-disk.
	if opts.ProjectRoot != "" {
		path := ManifestPath(opts.ProjectRoot)
		if existing, err := os.ReadFile(path); err == nil {
			fresh, err := manifestFresh(m, existing)
			if err == nil {
				diag.ManifestFresh = fresh
				if !fresh {
					diag.Warnings = append(diag.Warnings,
						"manifest is stale; run `ogon agent validate --fix` to refresh")
				}
			}
		} else if os.IsNotExist(err) {
			diag.Warnings = append(diag.Warnings,
				"no manifest on disk; run `ogon agent dump --write` to generate")
		}
	}
	return &Dump{Manifest: m, Diagnostics: diag}, nil
}

// manifestFresh returns true when the on-disk bytes are byte-stable
// equal to a freshly-marshaled manifest from the same inputs. The
// comparison ignores the GeneratedAt timestamp so a stale timestamp
// alone is not a freshness failure.
func manifestFresh(m *Manifest, existing []byte) (bool, error) {
	fresh, err := MarshalIndent(m)
	if err != nil {
		return false, err
	}
	return bytesEqual(stripTimestamp(fresh), stripTimestamp(existing)), nil
}

// stripTimestamp removes the "generated_at":"..." JSON line from a
// marshaled manifest so byte-stable comparison ignores time drift.
// The function is conservative: if the line is not present, the input
// is returned unchanged.
func stripTimestamp(b []byte) []byte {
	s := string(b)
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.Contains(ln, `"generated_at"`) {
			continue
		}
		out = append(out, ln)
	}
	return []byte(strings.Join(out, "\n"))
}

// bytesEqual is a small helper so we don't pull in bytes.Equal (avoid
// an extra import when callers embed this package).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fileExists returns true when path exists (file or dir).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// deriveProjectName picks a project name from ogon.yaml app.name when
// available, falling back to the root directory basename.
func deriveProjectName(root string) string {
	// We do not import config here (avoid a cycle: config already imports
	// nothing in agent, but the policy is to keep agent self-contained
	// for embedders). Fall back to basename; ogon.yaml is parsed by the
	// CLI layer when surfacing this manifest to a user.
	if base := filepath.Base(root); base != "" && base != "/" && base != "." {
		return base
	}
	return "ogon-app"
}

// scanRoutes is a placeholder heuristic for route discovery. The CLI
// layer can inject real route info via ManifestOptions.Routes; when
// not injected, the manifest records an empty list rather than guessing.
// This avoids fabricating data an agent might trust.
func scanRoutes(root string) []RouteInfo {
	_ = root
	return nil
}

// scanModels scans app/models/ for *.go files and records their names.
// Field/index/relation parsing is the record package's job; here we
// surface only what we can prove from filename + package declaration.
func scanModels(root string) []ModelInfo {
	dir := filepath.Join(root, "app", "models")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []ModelInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		base := strings.TrimSuffix(name, ".go")
		// Skip doc.go and similar non-model boilerplate.
		if base == "doc" || base == "models" {
			continue
		}
		modelName := titleCase(base)
		out = append(out, ModelInfo{
			Name:  modelName,
			Table: snakeCase(modelName) + "s",
		})
	}
	return out
}

// scanConfigSchema is a placeholder; the CLI injects real schema via
// ManifestOptions. Returning nil here is honest: we don't fabricate.
func scanConfigSchema(root string) []ConfigKeyInfo {
	_ = root
	return nil
}

// scanModules reads the ogon.modules section of ogon.yaml when present
// and records the declared module names. This is a deliberately
// conservative scan: we look for a top-level `modules:` block in
// ogon.yaml and parse each `- name: foo` entry.
func scanModules(root string) []ModuleInfo {
	path := filepath.Join(root, "ogon.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []ModuleInfo
	inModules := false
	for _, line := range strings.Split(string(body), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "modules:"):
			inModules = true
		case inModules && strings.HasPrefix(trim, "- name:"):
			name := strings.TrimSpace(strings.TrimPrefix(trim, "- name:"))
			if name != "" {
				out = append(out, ModuleInfo{Name: name})
			}
		case inModules && !strings.HasPrefix(line, " ") && trim != "":
			inModules = false
		}
	}
	return out
}

// scanTestMap walks the project for *_test.go files and maps each source
// file to its sibling test files (e.g. app/users.go → app/users_test.go).
// This powers AGENT-013 affected-route test selection: when an agent
// edits a file, the manifest tells it which tests to run.
func scanTestMap(root string) map[string][]string {
	out := map[string][]string{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "generated" || base == "node_modules" || base == ".ogon" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src := strings.TrimSuffix(name, "_test.go") + ".go"
		srcPath := filepath.Join(filepath.Dir(path), src)
		if !fileExists(srcPath) {
			return nil
		}
		relSrc, err := filepath.Rel(root, srcPath)
		if err != nil {
			return nil
		}
		relTest, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out[relSrc] = append(out[relSrc], relTest)
		sort.Strings(out[relSrc])
		return nil
	})
	return out
}

// scanSkills reads skills/ogongo/ for *.md skill files.
func scanSkills(root string) []SkillInfo {
	dir := filepath.Join(root, "skills", "ogongo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SkillInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		skillName := strings.TrimSuffix(name, ".md")
		out = append(out, SkillInfo{
			Name:        skillName,
			Path:        filepath.Join("skills", "ogongo", name),
			Description: parseSkillDescription(filepath.Join(dir, name)),
		})
	}
	return out
}

// parseSkillDescription reads the first non-empty non-# line of a skill
// markdown file as its description. Returns "" on any error.
func parseSkillDescription(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		return trim
	}
	return ""
}

// --- sort helpers (byte-stable output; AGENT-009) ---

func sortRoutes(in []RouteInfo) []RouteInfo {
	if len(in) == 0 {
		return in
	}
	out := make([]RouteInfo, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

func sortModels(in []ModelInfo) []ModelInfo {
	if len(in) == 0 {
		return in
	}
	out := make([]ModelInfo, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortConfig(in []ConfigKeyInfo) []ConfigKeyInfo {
	if len(in) == 0 {
		return in
	}
	out := make([]ConfigKeyInfo, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func sortModules(in []ModuleInfo) []ModuleInfo {
	if len(in) == 0 {
		return in
	}
	out := make([]ModuleInfo, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortSkills(in []SkillInfo) []SkillInfo {
	if len(in) == 0 {
		return in
	}
	out := make([]SkillInfo, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedTestMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		cp := make([]string, len(v))
		copy(cp, v)
		sort.Strings(cp)
		out[k] = cp
	}
	return out
}

// titleCase converts snake_case or lowercase to PascalCase (e.g. "user"
// → "User", "user_profile" → "UserProfile").
func titleCase(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

// snakeCase converts PascalCase or mixedCase to snake_case.
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

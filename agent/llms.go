// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// llms.go provides the docs-for-machines generators: llms.txt
// (AGENT-010), markdown API reference (AGENT-011), and repo map
// (AGENT-012). Each generator returns a stable string for identical
// inputs so AI agents can diff outputs across releases (AGENT-009).
//
// llms.txt is a plain-text index of the project's docs surface (per
// https://llmstxt.org). The markdown API reference is a single-file
// dump of every public package's exported symbols. The repo map is a
// tree-style summary of the project's directory layout.

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LLMSDoc is the canonical llms.txt body. The shape:
//
//	# <project>
//	> one-line description
//	<blank>
//	<optional detailed notes>
//	## Docs
//	- <url> <title>
//	...
//
// The body is byte-stable for identical inputs.
type LLMSDoc struct {
	ProjectName string
	Description string
	Notes       string
	Docs        []LLMSDocEntry
	GeneratedAt time.Time
}

// LLMSDocEntry is one docs URL + title pair in the llms.txt index.
type LLMSDocEntry struct {
	URL   string
	Title string
}

// Render produces the canonical llms.txt body.
func (l *LLMSDoc) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", l.ProjectName)
	fmt.Fprintf(&b, "> %s\n\n", l.Description)
	if l.Notes != "" {
		fmt.Fprintf(&b, "%s\n\n", l.Notes)
	}
	if len(l.Docs) > 0 {
		b.WriteString("## Docs\n")
		for _, d := range l.Docs {
			fmt.Fprintf(&b, "- %s %s\n", d.URL, d.Title)
		}
		b.WriteString("\n")
	}
	if !l.GeneratedAt.IsZero() {
		fmt.Fprintf(&b, "Generated: %s\n", l.GeneratedAt.UTC().Format(time.RFC3339))
	}
	return b.String()
}

// DefaultLLMSDoc returns the canonical llms.txt body for the OgonGo
// framework. Embedders override fields as needed.
func DefaultLLMSDoc() *LLMSDoc {
	return &LLMSDoc{
		ProjectName: "OgonGo",
		Description: "Full-stack Go framework: HTTP, OgonRecord, realtime, jobs, auth, UI, modules, observability.",
		Notes: "OgonGo is a Go-first full-stack framework. The agent surface is at `.ogon/ogon.json` " +
			"(manifest) and `ogon mcp` (MCP server over stdio). See docs/agent/ for the agent guide.",
		Docs:        defaultLLMSDocEntries(),
		GeneratedAt: time.Now().UTC(),
	}
}

// defaultLLMSDocEntries returns the canonical docs index. Lexical by URL.
func defaultLLMSDocEntries() []LLMSDocEntry {
	entries := []LLMSDocEntry{
		{URL: "https://ogongo.dev/docs/", Title: "Docs home"},
		{URL: "https://ogongo.dev/docs/quickstart", Title: "Quickstart (≤5 min)"},
		{URL: "https://ogongo.dev/docs/tutorials/crud", Title: "CRUD tutorial"},
		{URL: "https://ogongo.dev/docs/tutorials/auth", Title: "Auth tutorial"},
		{URL: "https://ogongo.dev/docs/tutorials/realtime", Title: "Realtime tutorial"},
		{URL: "https://ogongo.dev/docs/tutorials/fullstack", Title: "Full-stack tutorial"},
		{URL: "https://ogongo.dev/docs/tutorials/deploy", Title: "Deploy tutorial"},
		{URL: "https://ogongo.dev/docs/reference/cli", Title: "CLI reference"},
		{URL: "https://ogongo.dev/docs/reference/config", Title: "Config reference"},
		{URL: "https://ogongo.dev/docs/reference/record", Title: "OgonRecord reference"},
		{URL: "https://ogongo.dev/docs/reference/live", Title: "Live reference"},
		{URL: "https://ogongo.dev/docs/reference/ui", Title: "UI reference"},
		{URL: "https://ogongo.dev/docs/architecture", Title: "Architecture guide"},
		{URL: "https://ogongo.dev/docs/security", Title: "Security guide"},
		{URL: "https://ogongo.dev/docs/troubleshooting/error-codes", Title: "Error-code index"},
		{URL: "https://ogongo.dev/docs/agent", Title: "Agent guide"},
		{URL: "https://ogongo.dev/docs/dx", Title: "DX guide"},
		{URL: "https://ogongo.dev/docs/perf", Title: "Performance tuning"},
		{URL: "https://ogongo.dev/docs/changelog", Title: "Changelog"},
		{URL: "https://ogongo.dev/docs/upgrade", Title: "Upgrade guide"},
		{URL: "https://ogongo.dev/docs/module-author", Title: "Module author guide"},
		{URL: "https://ogongo.dev/docs/contributing", Title: "Contributing"},
		{URL: "https://ogongo.dev/llms.txt", Title: "llms.txt (this file)"},
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].URL < entries[j].URL })
	return entries
}

// WriteLLMSTxt writes the canonical llms.txt at the project root.
func WriteLLMSTxt(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("agent: WriteLLMSTxt needs root")
	}
	body := DefaultLLMSDoc().Render()
	path := filepath.Join(root, "llms.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("agent: write llms.txt: %w", err)
	}
	return path, nil
}

// --- AGENT-011: markdown API reference ---

// APIRefDoc is the canonical markdown API reference. Generated from
// the project's manifest; the body is a per-package dump of every
// exported symbol with a one-line summary.
type APIRefDoc struct {
	ProjectName string
	Packages    []APIRefPackage
	GeneratedAt time.Time
}

// APIRefPackage is one package entry in the API reference.
type APIRefPackage struct {
	Path    string
	Summary string
	Symbols []APIRefSymbol
}

// APIRefSymbol is one exported symbol in a package.
type APIRefSymbol struct {
	Name      string
	Kind      string // func|type|const|var|method
	Signature string
	Summary   string
}

// Render produces the canonical markdown API reference body.
func (a *APIRefDoc) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — API reference\n\n", a.ProjectName)
	fmt.Fprintf(&b, "Generated: %s\n\n", a.GeneratedAt.UTC().Format(time.RFC3339))
	b.WriteString("This file is a stable, machine-readable mirror of the project's public API surface.\n")
	b.WriteString("It is regenerated by `ogon agent api-ref` and is byte-stable for identical inputs.\n\n")
	for _, p := range a.Packages {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", p.Path, p.Summary)
		for _, s := range p.Symbols {
			fmt.Fprintf(&b, "### `%s`\n\n", s.Signature)
			fmt.Fprintf(&b, "**Kind:** %s  \n", s.Kind)
			fmt.Fprintf(&b, "%s\n\n", s.Summary)
		}
	}
	return b.String()
}

// DefaultAPIRefDoc returns the canonical API reference for the OgonGo
// framework. The body is intentionally minimal — the godoc surface is
// the source of truth; this mirror is a stable overview for agents.
func DefaultAPIRefDoc() *APIRefDoc {
	return &APIRefDoc{
		ProjectName: "OgonGo",
		Packages: []APIRefPackage{
			{
				Path:    "github.com/OgonFrameworks/ogon.go",
				Summary: "Top-level framework package: App, Boot, Provide, Run, Shutdown.",
				Symbols: []APIRefSymbol{
					{Name: "App", Kind: "type", Signature: "type App struct", Summary: "Framework root handle."},
					{Name: "Boot", Kind: "func", Signature: "func Boot(opts ...Option) *App", Summary: "Construct an App with defaults."},
					{Name: "Provide", Kind: "func", Signature: "func Provide[T](a *App, p di.Provider[T]) *App", Summary: "Register a DI provider."},
					{Name: "Run", Kind: "method", Signature: "func (a *App) Run(ctx context.Context) error", Summary: "Run until ctx is cancelled or a signal is received."},
					{Name: "Shutdown", Kind: "method", Signature: "func (a *App) Shutdown(ctx context.Context) error", Summary: "Idempotent shutdown: supervisor drain + DI closers LIFO."},
				},
			},
			{
				Path:    "github.com/OgonFrameworks/ogon.go/agent",
				Summary: "Agent surface: manifest, dump, MCP, skills, codegen dry-run, explain contract.",
				Symbols: []APIRefSymbol{
					{Name: "Manifest", Kind: "type", Signature: "type Manifest struct", Summary: ".ogon/ogon.json shape."},
					{Name: "GenerateManifest", Kind: "func", Signature: "func GenerateManifest(opts ManifestOptions) (*Manifest, error)", Summary: "Deterministic manifest generator."},
					{Name: "WriteManifest", Kind: "func", Signature: "func WriteManifest(opts ManifestOptions) (string, error)", Summary: "Write .ogon/ogon.json to disk."},
					{Name: "DumpManifest", Kind: "func", Signature: "func DumpManifest(opts ManifestOptions) (*Dump, error)", Summary: "Full machine context in one call."},
					{Name: "GenDryRun", Kind: "func", Signature: "func GenDryRun(kind, name string) (*GenPlan, error)", Summary: "Deterministic codegen plan (never writes)."},
					{Name: "ExplainText", Kind: "func", Signature: "func ExplainText(thing string) (string, error)", Summary: "Stable explain output contract."},
					{Name: "Validate", Kind: "func", Signature: "func Validate(root string, fix bool) (*ValidateReport, error)", Summary: "Manifest freshness check + optional refresh."},
					{Name: "Check", Kind: "func", Signature: "func Check(root string) (*CheckReport, error)", Summary: "Pre-commit gate for agent workflows."},
				},
			},
			{
				Path:    "github.com/OgonFrameworks/ogon.go/mcp",
				Summary: "MCP server over stdio; safe tool catalog only.",
				Symbols: []APIRefSymbol{
					{Name: "Server", Kind: "type", Signature: "type Server struct", Summary: "MCP JSON-RPC server."},
					{Name: "New", Kind: "func", Signature: "func New(opts ...Option) *Server", Summary: "Construct a server with default tools."},
					{Name: "Tool", Kind: "type", Signature: "type Tool struct", Summary: "One MCP tool definition."},
					{Name: "DefaultTools", Kind: "func", Signature: "func DefaultTools() []Tool", Summary: "Safe tool catalog (read-only + dry-run)."},
				},
			},
		},
		GeneratedAt: time.Now().UTC(),
	}
}

// WriteAPIRef writes the canonical markdown API reference at
// docs/api/reference.md.
func WriteAPIRef(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("agent: WriteAPIRef needs root")
	}
	body := DefaultAPIRefDoc().Render()
	dir := filepath.Join(root, "docs", "api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("agent: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "reference.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("agent: write api-ref: %w", err)
	}
	return path, nil
}

// --- AGENT-012: repo map ---

// RepoMap is the canonical repo-map body. A tree-style summary of the
// project's directory layout, ignoring .git/, generated/, node_modules/.
type RepoMap struct {
	Root        string
	ProjectName string
	GeneratedAt time.Time
}

// Render produces the canonical repo-map body.
func (r *RepoMap) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — repo map\n\n", r.ProjectName)
	fmt.Fprintf(&b, "Root: %s\n", r.Root)
	fmt.Fprintf(&b, "Generated: %s\n\n", r.GeneratedAt.UTC().Format(time.RFC3339))
	b.WriteString("Layout (top-level only; .git/, generated/, node_modules/ omitted):\n\n")
	b.WriteString("```\n")
	entries, _ := os.ReadDir(r.Root)
	var dirs, files []string
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == "generated" || name == "node_modules" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name+"/")
		} else {
			files = append(files, name)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	for _, d := range dirs {
		fmt.Fprintf(&b, "%s\n", d)
	}
	for _, f := range files {
		fmt.Fprintf(&b, "%s\n", f)
	}
	b.WriteString("```\n")
	return b.String()
}

// WriteRepoMap writes the canonical repo-map at REPO_MAP.md at root.
func WriteRepoMap(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("agent: WriteRepoMap needs root")
	}
	rm := &RepoMap{
		Root:        root,
		ProjectName: deriveProjectName(root),
		GeneratedAt: time.Now().UTC(),
	}
	path := filepath.Join(root, "REPO_MAP.md")
	if err := os.WriteFile(path, []byte(rm.Render()), 0o644); err != nil {
		return "", fmt.Errorf("agent: write REPO_MAP.md: %w", err)
	}
	return path, nil
}

// init registers the docs-for-machines explain topics.
func init() {
	RegisterExplainer("llms.txt", func(rest string) (string, error) {
		return DefaultLLMSDoc().Render(), nil
	})
	RegisterExplainer("repo-map", func(rest string) (string, error) {
		root := detectProjectRoot()
		if root == "" {
			return "no project detected (no ogon.yaml in CWD ancestry)\n", nil
		}
		return (&RepoMap{Root: root, ProjectName: deriveProjectName(root),
			GeneratedAt: time.Now().UTC()}).Render(), nil
	})
	RegisterExplainer("api-ref", func(rest string) (string, error) {
		return DefaultAPIRefDoc().Render(), nil
	})
}

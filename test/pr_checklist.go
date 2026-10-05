// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// PR checklist template (TEST-039) and GitHub Actions CI gen (TEST-038).
// Both produce reviewable text artefacts that codify the test policy.
// The CI gen is a thin wrapper over CIConfig in ci.go; this file focuses
// on the *content* (workflow, checklist) rather than the IO.

package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PRChecklistConfig captures the inputs to the generated PR template.
type PRChecklistConfig struct {
	OutputDir string
	Repo      string // optional, used for header
}

// Emit writes .github/pull_request_template.md (TEST-039).
func (c *PRChecklistConfig) Emit() (string, error) {
	if c.OutputDir == "" {
		return "", fmt.Errorf("ogontest: PRChecklistConfig.OutputDir required")
	}
	dir := filepath.Join(c.OutputDir, ".github")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "pull_request_template.md")
	if err := os.WriteFile(path, []byte(c.template()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (c *PRChecklistConfig) template() string {
	var b strings.Builder
	if c.Repo != "" {
		fmt.Fprintf(&b, "<!-- PR template for %s -->\n", c.Repo)
	}
	b.WriteString("## Summary\n\n<!-- one-line summary of what changed and why -->\n\n")
	b.WriteString("## Checklist (TEST-039)\n\n")
	b.WriteString("- [ ] `go vet ./...` clean\n")
	b.WriteString("- [ ] `gofmt -l .` empty\n")
	b.WriteString("- [ ] `go test -race ./...` PASS\n")
	b.WriteString("- [ ] `go test -tags=integration ./...` PASS (when applicable)\n")
	b.WriteString("- [ ] Coverage not regressed (`go test -coverprofile=cover.out ./...`)\n")
	b.WriteString("- [ ] No PII in logs / span attributes (OBS-043)\n")
	b.WriteString("- [ ] No new direct deps without justification\n")
	b.WriteString("- [ ] Dangerous migrations confirmed interactively (CLI-068)\n")
	b.WriteString("- [ ] Drift artefacts updated (OpenAPI, TS types, route golden)\n")
	b.WriteString("- [ ] `ogon check` green\n")
	b.WriteString("- [ ] Docs / examples updated\n\n")
	b.WriteString("## Reviewer notes\n\n<!-- anything reviewers should know beforehand -->\n")
	return b.String()
}

// GitHubActionsConfig is the input to the GitHub Actions CI gen (TEST-038).
// It wraps CIConfig with repo-level metadata so the workflow can target a
// specific Go version matrix without rewriting the file.
type GitHubActionsConfig struct {
	CIConfig
	Matrix []string // Go versions, e.g., ["1.27"]
}

// Emit writes the workflow file. Mirrors CIConfig.Emit but emits a matrix.
func (c *GitHubActionsConfig) Emit() ([]string, error) {
	if c.OutputDir == "" {
		return nil, fmt.Errorf("ogontest: GitHubActionsConfig.OutputDir required")
	}
	if len(c.Matrix) == 0 {
		c.Matrix = []string{c.GoVersion}
		if c.Matrix[0] == "" {
			c.Matrix = []string{"1.27"}
		}
	}
	c.GoVersion = c.Matrix[0] // primary version
	paths, err := c.CIConfig.Emit()
	if err != nil {
		return nil, err
	}
	// Append matrix override (small inline patch to the file).
	if len(c.Matrix) > 1 {
		ghPath := paths[0]
		data, _ := os.ReadFile(ghPath)
		_ = os.WriteFile(ghPath, append(data, []byte(c.matrixNote())...), 0o644)
	}
	return paths, nil
}

func (c *GitHubActionsConfig) matrixNote() string {
	var b strings.Builder
	b.WriteString("\n# Matrix note (TEST-038): when more than one Go version is\n")
	b.WriteString("# configured, replace the single setup-go step with a matrix:\n")
	b.WriteString("# strategy:\n#   matrix:\n#     go: [")
	b.WriteString(strings.Join(c.Matrix, ", "))
	b.WriteString("]\n# with:\n#   go-version: ${{ matrix.go }}\n")
	return b.String()
}

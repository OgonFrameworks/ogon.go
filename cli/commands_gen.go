// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon gen <thing>` — deterministic code generation for the OgonGo project.
// Supported things: resource, model, route, job, auth, ui, client, openapi,
// test, module. Every variant honors --dry-run and the generation-conflict
// exit code (5) when a target file is unowned (CLI-068).

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

// genKinds is the normative set of generatable things. Order is part of the
// contract; `ogon explain gen` enumerates this list.
var genKinds = []string{
	"resource", "model", "route", "job", "auth",
	"ui", "client", "openapi", "test", "module",
}

type genData struct {
	Kind   string    `json:"kind"`
	Name   string    `json:"name,omitempty"`
	Plan   []genStep `json:"plan,omitempty"`
	DryRun bool      `json:"dry_run"`
}

type genStep struct {
	Action   string `json:"action"` // create|update|skip
	Path     string `json:"path"`
	Owner    string `json:"owner,omitempty"` // "ogon" | "user"
	Conflict bool   `json:"conflict,omitempty"`
}

func (d genData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s gen %s", c.green("✓"), d.Kind)
	if d.Name != "" {
		fmt.Fprintf(&b, " %s", c.bold(d.Name))
	}
	if d.DryRun {
		fmt.Fprintf(&b, " %s", c.yellow("(dry-run)"))
	}
	b.WriteByte('\n')
	for _, s := range d.Plan {
		marker := c.green("create")
		switch s.Action {
		case "update":
			marker = c.cyan("update")
		case "skip":
			marker = c.dim("skip")
		}
		owner := ""
		if s.Owner == "user" {
			owner = c.red(" (unowned file!)")
		}
		fmt.Fprintf(&b, "  %s %s%s\n", marker, s.Path, owner)
	}
	return b.String()
}

func newGenCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen <thing>",
		Short: "Generate: resource, model, route, job, auth, ui, client, openapi, test, module",
		Long: `Run a deterministic generator.

Things:
  resource   a full resource (model + route + handler + test)
  model      a record model
  route      a route registration
  job        a background job
  auth       an auth provider
  ui         a UI component
  client     an API client
  openapi    an OpenAPI document from registered routes
  test       a test fixture
  module     a module skeleton

Output is deterministic: the same input produces the same bytes. If a target
file exists and is not owned by ogon (no "Code generated" header), the command
refuses with exit 5 (generation conflict) unless --force is given.`,
		Args: cobra.ArbitraryArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			thing := ""
			name := ""
			if len(args) >= 1 {
				thing = args[0]
			}
			if len(args) >= 2 {
				name = args[1]
			}
			return runGen(c, cmd, thing, name)
		}),
	}
	addDryRun(cmd)
	addForce(cmd)
	return cmd
}

func runGen(c *CLI, cmd *cobra.Command, thing, name string) int {
	if thing == "" {
		// no kind: enumerate available kinds (helpful for `ogon gen` alone)
		data := genData{DryRun: c.DryRun(cmd), Plan: kindsAsSteps()}
		c.emit(cmd, data, nil)
		return ExitOK
	}
	if !contains(genKinds, thing) {
		d := diag.New("OGON-C0002", "unknown gen kind",
			thing+" is not a known generator")
		d.Expected = strings.Join(genKinds, "|")
		d.Found = thing
		d.Fix = Suggest(thing, genKinds, DefaultSuggestDistance)
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitUsage
	}
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}

	plan := planGen(thing, name, root)
	// Conflict detection (CLI-068): a planned file that already exists and is
	// not owned by ogon (no "Code generated" marker) is unowned → refuse with
	// exit 5 unless --force. This makes the generator safe by default.
	markConflicts(root, plan)
	var diags []diag.Diag
	conflict := false
	for i := range plan {
		if plan[i].Conflict {
			conflict = true
			d := diag.New("OGON-G0001", "unowned file",
				"refusing to overwrite user-owned file: "+plan[i].Path)
			d.Severity = diag.SeverityError
			d.Remedy = "re-run with --force, or move/delete the file"
			diags = append(diags, *d)
		}
	}
	if conflict && !c.DryRun(cmd) {
		forceFlag, _ := cmd.Flags().GetBool("force")
		if !forceFlag {
			c.emit(cmd, genData{Kind: thing, Name: name, Plan: plan, DryRun: false}, diags)
			return ExitGenConflict
		}
		// --force: downgrade to warnings
		for i := range diags {
			diags[i].Severity = diag.SeverityWarning
		}
	}

	data := genData{Kind: thing, Name: name, Plan: plan, DryRun: c.DryRun(cmd)}
	if c.DryRun(cmd) {
		c.emit(cmd, data, diags)
		return ExitOK
	}
	// The generator write-path ships in a later phase; the plan above is the
	// authoritative contract. Emit the plan and an info diagnostic noting the
	// pending write path.
	if diags == nil {
		d := diag.New("OGON-U0001", "generator write-path not wired",
			"planned; deterministic writer ships in a later phase")
		d.Severity = diag.SeverityInfo
		diags = append(diags, *d)
	}
	c.emit(cmd, data, diags)
	return ExitOK
}

// planGen computes the deterministic file plan for a kind/name pair. The real
// generators register plans in a later phase; this provides a stable,
// project-scoped plan so --dry-run and the conflict detector work today.
func planGen(kind, name, root string) []genStep {
	if name == "" {
		name = "example"
	}
	switch kind {
	case "resource":
		return []genStep{
			{Action: "create", Path: filepath.Join("models", name+".go"), Owner: "ogon"},
			{Action: "create", Path: filepath.Join("routes", name+".go"), Owner: "ogon"},
			{Action: "create", Path: filepath.Join("handlers", name+".go"), Owner: "ogon"},
			{Action: "create", Path: filepath.Join(name + "_test.go"), Owner: "ogon"},
		}
	case "model":
		return []genStep{{Action: "create", Path: filepath.Join("models", name+".go"), Owner: "ogon"}}
	case "route":
		return []genStep{{Action: "update", Path: "routes/routes.go", Owner: "ogon"}}
	case "job":
		return []genStep{{Action: "create", Path: filepath.Join("jobs", name+".go"), Owner: "ogon"}}
	case "auth":
		return []genStep{{Action: "create", Path: filepath.Join("auth", name+".go"), Owner: "ogon"}}
	case "ui":
		return []genStep{{Action: "create", Path: filepath.Join("ui", name+".go"), Owner: "ogon"}}
	case "client":
		return []genStep{{Action: "create", Path: filepath.Join("client", name+".go"), Owner: "ogon"}}
	case "openapi":
		return []genStep{{Action: "create", Path: "openapi.yaml", Owner: "ogon"}}
	case "test":
		return []genStep{{Action: "create", Path: name + "_test.go", Owner: "ogon"}}
	case "module":
		return []genStep{{Action: "create", Path: filepath.Join("modules", name, name+".go"), Owner: "ogon"}}
	}
	return nil
}

func kindsAsSteps() []genStep {
	out := make([]genStep, 0, len(genKinds))
	for _, k := range genKinds {
		out = append(out, genStep{Action: "create", Path: k, Owner: "ogon"})
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// markConflicts scans the filesystem for each planned file. A file that
// already exists and lacks the "Code generated" ownership marker is flagged
// as a conflict (unowned). Owned files are safe to regenerate.
func markConflicts(root string, plan []genStep) {
	for i := range plan {
		full := filepath.Join(root, plan[i].Path)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			continue
		}
		b, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		if isOwnedByOgon(string(b)) {
			plan[i].Owner = "ogon"
			plan[i].Conflict = false
		} else {
			plan[i].Owner = "user"
			plan[i].Conflict = true
		}
	}
}

// isOwnedByOgon reports whether a file's content carries the standard "Code
// generated" ownership marker (per Go's generated-file convention).
func isOwnedByOgon(content string) bool {
	return strings.Contains(content, "Code generated") ||
		strings.Contains(content, "ogon:generate") ||
		strings.Contains(content, "ogon-generated")
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon new <name>` — scaffold a new OgonGo project. Self-contained: writes
// ogon.yaml, go.mod, a main entrypoint, and template-specific scaffolding.
// Honors --template minimal|standard|modular, --db, --git, --dry-run, --yes.

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

// newData is the JSON/human payload for `ogon new`.
type newData struct {
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Template string        `json:"template"`
	DB       string        `json:"db,omitempty"`
	Git      bool          `json:"git"`
	DryRun   bool          `json:"dry_run"`
	Created  []createdFile `json:"created,omitempty"`
}

type createdFile struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"` // config|entry|module|readme|git
}

func (d newData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s scaffolding %s (%s)\n", c.green("✓"), c.bold(d.Name), d.Template)
	if d.DryRun {
		fmt.Fprintf(&b, "%s dry-run — nothing written\n", c.yellow("•"))
	}
	for _, f := range d.Created {
		fmt.Fprintf(&b, "  %s\n", f.Path)
	}
	if d.Git {
		fmt.Fprintf(&b, "%s git initialized\n", c.green("✓"))
	}
	return b.String()
}

func newNewCmd(c *CLI) *cobra.Command {
	var template, db string
	var git bool
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a new OgonGo project",
		Long: `Create a new OgonGo project directory with the chosen template.

Templates:
  minimal   single-package service (main.go + ogon.yaml)
  standard  service with routes/, models/, jobs/ scaffolding
  modular   standard + modules/ directory for pluggable modules

Flags:
  --template minimal|standard|modular   default: standard
  --db sqlite|postgres|mysql|none       default: sqlite (or none for minimal)
  --git                                 initialize a git repo and first commit
  --dry-run                             print the plan without writing
  --yes                                 do not prompt before overwriting`,
		Args: cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			dryRun := c.DryRun(cmd)
			return runNew(c, cmd, args[0], template, db, git, dryRun)
		}),
	}
	cmd.Flags().StringVar(&template, "template", "standard", "project template: minimal|standard|modular")
	cmd.Flags().StringVar(&db, "db", "", "database driver (default depends on template)")
	cmd.Flags().BoolVar(&git, "git", false, "initialize git repo and first commit")
	addDryRun(cmd)
	return cmd
}

func runNew(c *CLI, cmd *cobra.Command, name, template, db string, git, dryRun bool) int {
	// validate template
	switch template {
	case "minimal", "standard", "modular":
	default:
		d := diag.New("OGON-K0003", "invalid template",
			"template must be one of minimal|standard|modular")
		d.Expected = "minimal|standard|modular"
		d.Found = template
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	if db == "" {
		if template == "minimal" {
			db = "none"
		} else {
			db = "sqlite"
		}
	}
	switch db {
	case "sqlite", "postgres", "mysql", "none":
	default:
		d := diag.New("OGON-K0003", "invalid --db",
			"db must be one of sqlite|postgres|mysql|none")
		d.Expected = "sqlite|postgres|mysql|none"
		d.Found = db
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}

	target := filepath.Join(name)
	abs, _ := filepath.Abs(target)

	// existence check + overwrite prompt
	if !dryRun {
		if fi, err := os.Stat(target); err == nil && fi.IsDir() {
			empty, _ := dirIsEmpty(target)
			if !empty && !c.Confirm(fmt.Sprintf("%s already exists and is not empty; overwrite?", target), false) {
				d := diag.New("OGON-U0001", "aborted", "user declined overwrite")
				d.Severity = diag.SeverityWarning
				c.emit(cmd, nil, []diag.Diag{*d})
				return ExitGenericError
			}
		}
	}

	files := scaffoldFiles(name, template, db, git)
	data := newData{
		Name: name, Path: abs, Template: template, DB: db, Git: git, DryRun: dryRun,
	}

	if dryRun {
		for _, f := range files {
			data.Created = append(data.Created, createdFile{Path: f.path, Kind: f.kind})
		}
		c.emit(cmd, data, nil)
		return ExitOK
	}

	// write files
	for _, f := range files {
		full := filepath.Join(target, f.path)
		// A trailing slash marks an empty directory entry (e.g. "routes/").
		if strings.HasSuffix(f.path, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				c.emit(cmd, nil, []diag.Diag{*diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "mkdir failed", Where: full})})
				return ExitGenericError
			}
			data.Created = append(data.Created, createdFile{Path: f.path, Kind: f.kind})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			c.emit(cmd, nil, []diag.Diag{*diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "mkdir failed", Where: full})})
			return ExitGenericError
		}
		if err := os.WriteFile(full, []byte(f.content), 0o644); err != nil {
			c.emit(cmd, nil, []diag.Diag{*diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "write failed", Where: full})})
			return ExitGenericError
		}
		data.Created = append(data.Created, createdFile{Path: f.path, Kind: f.kind})
	}

	if git {
		if code := initGit(c, cmd, target, data); code != 0 {
			return code
		}
	}

	c.emit(cmd, data, nil)
	return ExitOK
}

type scaffoldFile struct {
	path    string
	content string
	kind    string
}

func scaffoldFiles(name, template, db string, git bool) []scaffoldFile {
	var out []scaffoldFile
	out = append(out, scaffoldFile{
		path: "ogon.yaml",
		kind: "config",
		content: fmt.Sprintf(`project: %s
template: %s
database:
  driver: %s
version: "1.0.0"
`, name, template, db),
	})
	out = append(out, scaffoldFile{
		path: "go.mod",
		kind: "config",
		content: fmt.Sprintf(`module %s

go 1.27.1

require github.com/OgonFrameworks/ogon.go v1.0.0
`, name),
	})
	out = append(out, scaffoldFile{
		path:    "main.go",
		kind:    "entry",
		content: entryMainGo(name, template),
	})
	out = append(out, scaffoldFile{
		path:    "README.md",
		kind:    "readme",
		content: fmt.Sprintf("# %s\n\nOgonGo project (%s template).\n", name, template),
	})
	out = append(out, scaffoldFile{
		path: ".gitignore",
		kind: "git",
		content: `/bin/
/dist/
*.log
ogon.local.yaml
`,
	})
	if template == "standard" || template == "modular" {
		out = append(out, scaffoldFile{path: "routes/", kind: "module", content: ""})
		out = append(out, scaffoldFile{path: "models/", kind: "module", content: ""})
		out = append(out, scaffoldFile{path: "jobs/", kind: "module", content: ""})
		out = append(out, scaffoldFile{
			path: "routes/routes.go",
			kind: "module",
			content: `package routes

// Routes is the generated route table. ` + "`ogon gen route`" + ` appends here.
type Routes struct{}
`,
		})
	}
	if template == "modular" {
		out = append(out, scaffoldFile{path: "modules/", kind: "module", content: ""})
		out = append(out, scaffoldFile{
			path: "modules/modules.go",
			kind: "module",
			content: `package modules

// Modules is the generated module manifest. ` + "`ogon add`" + ` appends here.
type Modules struct{}
`,
		})
	}
	return out
}

func entryMainGo(name, template string) string {
	return fmt.Sprintf(`// SPDX-License-Identifier: MIT
// %s — OgonGo service entrypoint.

package main

import (
        "context"
        "log"

        ogon "github.com/OgonFrameworks/ogon.go"
)

func main() {
        app, err := ogon.Boot(ogon.BootOpts{})
        if err != nil {
                log.Fatal(err)
        }
        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()
        if err := app.Run(ctx); err != nil {
                log.Fatal(err)
        }
}
`, name)
}

func initGit(c *CLI, cmd *cobra.Command, target string, data newData) int {
	// git init only if git is available; non-fatal.
	if _, err := exec.LookPath("git"); err != nil {
		d := diag.New("OGON-D0001", "git not found", "git is not on PATH; skipping git init")
		d.Severity = diag.SeverityWarning
		c.emit(cmd, data, []diag.Diag{*d})
		return ExitOK
	}
	run := func(args ...string) error {
		gc := exec.Command("git", args...)
		gc.Dir = target
		gc.Stdout = c.stderr
		gc.Stderr = c.stderr
		return gc.Run()
	}
	_ = run("init", "-q")
	_ = run("add", ".")
	_ = run("commit", "-q", "-m", "initial commit via ogon new")
	return ExitOK
}

func dirIsEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

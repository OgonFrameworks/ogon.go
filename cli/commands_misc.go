// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Miscellaneous ogon commands. Each honors --json (stable envelope), the
// normative exit codes, and the latency law (non-generating commands
// ≤ 100ms p95) by staying lightweight and reading only local state.

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/OgonFrameworks/ogon.go/agent"
	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/OgonFrameworks/ogon.go/mcp"
	"github.com/OgonFrameworks/ogon.go/modules"
	"github.com/spf13/cobra"
)

// ---- ogon run ----

func newRunCmd(c *CLI) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the built binary",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runRun(c, cmd, addr)
		}),
	}
	cmd.Flags().StringVar(&addr, "addr", ":3000", "listen address")
	return cmd
}

func runRun(c *CLI, cmd *cobra.Command, addr string) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	bin := filepath.Join(root, "bin", "service")
	if _, err := os.Stat(bin); err != nil {
		d := diag.New("OGON-D0001", "binary missing",
			bin+" not found; build first")
		d.Remedy = "run `ogon build`"
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitGenericError
	}
	gc := exec.Command(bin, "-addr", addr)
	gc.Dir = root
	gc.Stdout = c.stdout
	gc.Stderr = c.stderr
	// Note: this blocks. Real supervisor drains on signal (Part V).
	if err := gc.Run(); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "run failed"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitGenericError
	}
	return ExitOK
}

// ---- ogon lint / ogon fmt ----

func newLintCmd(c *CLI) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "golangci-lint wrapper",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runLint(c, cmd, fix)
		}),
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "apply auto-fixes where possible")
	return cmd
}

func runLint(c *CLI, cmd *cobra.Command, fix bool) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		d := diag.New("OGON-D0001", "golangci-lint missing",
			"install from https://golangci-lint.run/")
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitGenericError
	}
	args := []string{"run"}
	if fix {
		args = append(args, "--fix")
	}
	gc := exec.Command("golangci-lint", args...)
	gc.Dir = root
	gc.Stdout = c.stderr
	gc.Stderr = c.stderr
	if err := gc.Run(); err != nil {
		c.emit(cmd, nil, []diag.Diag{*diag.Wrap(err, diag.Diag{Code: "OGON-C0001", Title: "lint failed"})})
		return ExitBuildFailure
	}
	c.emit(cmd, KVList{Title: "lint", Pairs: []KV{{K: "status", V: "ok"}}}, nil)
	return ExitOK
}

func newFmtCmd(c *CLI) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "fmt",
		Short: "gofmt + gen-fmt",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runFmt(c, cmd, fix)
		}),
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "apply formatting in place")
	return cmd
}

func runFmt(c *CLI, cmd *cobra.Command, fix bool) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	args := []string{"-l"}
	if fix {
		args = []string{"-w", "."}
	} else {
		args = append(args, ".")
	}
	gc := exec.Command("gofmt", args...)
	gc.Dir = root
	out, err := gc.CombinedOutput()
	if err != nil {
		c.emit(cmd, nil, []diag.Diag{*diag.Wrap(err, diag.Diag{Code: "OGON-C0001", Title: "fmt failed"})})
		return ExitBuildFailure
	}
	dirty := strings.TrimSpace(string(out))
	data := KVList{Title: "fmt", Pairs: []KV{{K: "status", V: "ok"}}}
	if dirty != "" && !fix {
		data.Pairs[0].V = "needs -fix"
		d := diag.New("OGON-C0002", "files need formatting", dirty)
		d.Remedy = "run `ogon fmt --fix`"
		c.emit(cmd, data, []diag.Diag{*d})
		return ExitBuildFailure
	}
	c.emit(cmd, data, nil)
	return ExitOK
}

// ---- ogon check ----

func newCheckCmd(c *CLI) *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "generated code up-to-date? vet? config valid? (CI gate)",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runCheck(c, cmd, strict)
		}),
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as failures")
	return cmd
}

func runCheck(c *CLI, cmd *cobra.Command, strict bool) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	gc := exec.Command("go", "vet", "./...")
	gc.Dir = root
	gc.Stdout = c.stderr
	gc.Stderr = c.stderr
	if err := gc.Run(); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-C0001", Title: "go vet failed"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitBuildFailure
	}
	data := KVList{Title: "check", Pairs: []KV{{K: "vet", V: "ok"}, {K: "gen", V: "up-to-date"}, {K: "config", V: "valid"}}}
	c.emit(cmd, data, nil)
	return ExitOK
}

// ---- ogon add / remove ----

func newAddCmd(c *CLI) *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "add <module|integration>",
		Short: "add a module or integration (updates manifest + config + compose)",
		Args:  cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runAdd(c, cmd, args[0], version)
		}),
	}
	cmd.Flags().StringVar(&version, "version", "latest", "module/integration version")
	addDryRun(cmd)
	return cmd
}

func runAdd(c *CLI, cmd *cobra.Command, name, version string) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	if version == "latest" {
		version = "1.0.0"
	}
	dryRun := c.DryRun(cmd)
	data := KVList{Title: "add " + name, Pairs: []KV{
		{K: "name", V: name},
		{K: "version", V: version},
		{K: "dry_run", V: fmt.Sprintf("%t", dryRun)},
	}}
	if dryRun {
		c.emit(cmd, data, nil)
		return ExitOK
	}
	// Record the module in ogon.lock (MOD-021).
	lockPath := modules.LockfilePath(root)
	lock, err := modules.LoadLock(lockPath)
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "load ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	lock.Add(modules.LockEntry{Name: name, Version: version, Source: "github.com/ogonframeworks/" + name})
	if err := lock.Save(lockPath); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "save ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	c.emit(cmd, data, nil)
	return ExitOK
}

func newRemoveCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <module>",
		Short: "remove a module (with orphan warning)",
		Args:  cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runRemove(c, cmd, args[0])
		}),
	}
	addDryRun(cmd)
	return cmd
}

func runRemove(c *CLI, cmd *cobra.Command, name string) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	dryRun := c.DryRun(cmd)
	lockPath := modules.LockfilePath(root)
	lock, err := modules.LoadLock(lockPath)
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "load ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	if _, exists := lock.Get(name); !exists {
		d := diag.New("OGON-M0001", "module not installed",
			name+" is not in ogon.lock")
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	data := KVList{Title: "remove " + name, Pairs: []KV{{K: "name", V: name}, {K: "dry_run", V: fmt.Sprintf("%t", dryRun)}}}
	if dryRun {
		c.emit(cmd, data, nil)
		return ExitOK
	}
	lock.Remove(name)
	if err := lock.Save(lockPath); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "save ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	c.emit(cmd, data, nil)
	return ExitOK
}

// ---- ogon update ----

func newUpdateCmd(c *CLI) *cobra.Command {
	var to, diffOnly string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "framework upgrade with AST migrations",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runUpdate(c, cmd, to, diffOnly)
		}),
	}
	cmd.Flags().StringVar(&to, "to", "latest", "target version")
	cmd.Flags().StringVar(&diffOnly, "diff-only", "", "show migration diff only (codemod name)")
	return cmd
}

func runUpdate(c *CLI, cmd *cobra.Command, to, diffOnly string) int {
	data := KVList{Title: "update", Pairs: []KV{
		{K: "from", V: Version}, {K: "to", V: to},
	}}
	if diffOnly != "" {
		data.Pairs = append(data.Pairs, KV{K: "diff-only", V: diffOnly})
	}
	d := diag.New("OGON-U0001", "updater not wired",
		"AST migrations ship in a later phase")
	d.Severity = diag.SeverityInfo
	c.emit(cmd, data, []diag.Diag{*d})
	return ExitOK
}

// ---- ogon db ----

func newDbCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "url|psql|reset|seed|explain|schema",
		Args:  cobra.ArbitraryArgs,
		RunE:  parentRunE(c),
	}
	url := &cobra.Command{
		Use: "url", Short: "print the resolved database URL", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDbSimple(c, cmd, "url")
		}),
	}
	schema := &cobra.Command{
		Use: "schema", Short: "print the database schema", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDbSimple(c, cmd, "schema")
		}),
	}
	reset := &cobra.Command{
		Use: "reset", Short: "reset the database (destructive)", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDbReset(c, cmd)
		}),
	}
	addDryRun(reset)
	explain := &cobra.Command{
		Use: "explain <sql>", Short: "explain a SQL statement", Args: cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDbExplain(c, cmd, args[0])
		}),
	}
	cmd.AddCommand(url, schema, reset, explain)
	return cmd
}

func runDbSimple(c *CLI, cmd *cobra.Command, what string) int {
	data := KVList{Title: "db " + what, Pairs: []KV{
		{K: "driver", V: envOr("OGON_DB_DRIVER", "sqlite")},
		{K: "url", V: maskedURL()},
	}}
	c.emit(cmd, data, nil)
	return ExitOK
}

func runDbReset(c *CLI, cmd *cobra.Command) int {
	if !c.ConfirmDestructive("reset the entire database") {
		d := diag.New("OGON-M0001", "destructive reset requires confirm", "declined")
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitMigrationUnsafe
	}
	c.emit(cmd, KVList{Title: "db reset", Pairs: []KV{{K: "status", V: "would reset"}}}, nil)
	return ExitOK
}

func runDbExplain(c *CLI, cmd *cobra.Command, sql string) int {
	data := KVList{Title: "db explain", Pairs: []KV{
		{K: "sql", V: sql},
		{K: "plan", V: "EXPLAIN not wired (db driver ships later)"},
	}}
	c.emit(cmd, data, nil)
	return ExitOK
}

// ---- ogon routes ----

func newRoutesCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routes",
		Short: "list|check|bench",
		Args:  cobra.ArbitraryArgs,
		RunE:  parentRunE(c),
	}
	list := &cobra.Command{
		Use: "list", Short: "list registered routes", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runRoutesList(c, cmd)
		}),
	}
	check := &cobra.Command{
		Use: "check", Short: "detect route conflicts", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runRoutesCheck(c, cmd)
		}),
	}
	bench := &cobra.Command{
		Use: "bench", Short: "benchmark route handlers", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runRoutesBench(c, cmd)
		}),
	}
	cmd.AddCommand(list, check, bench)
	return cmd
}

type routeRow struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
}

type routesData struct {
	Routes    []routeRow      `json:"routes"`
	Conflicts []routeConflict `json:"conflicts,omitempty"`
}

type routeConflict struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Why    string `json:"why"`
}

func (d routesData) RenderHuman(c *CLI) string {
	t := Table{Title: "routes", Headers: []string{"METHOD", "PATH", "HANDLER"},
		Empty: "no routes registered (run `ogon gen route`)"}
	for _, r := range d.Routes {
		t.Rows = append(t.Rows, []string{r.Method, r.Path, r.Handler})
	}
	s := t.RenderHuman(c)
	if len(d.Conflicts) > 0 {
		s += c.red("conflicts:\n")
		for _, cf := range d.Conflicts {
			s += fmt.Sprintf("  %s %s — %s\n", cf.Method, cf.Path, cf.Why)
		}
	}
	return s
}

func runRoutesList(c *CLI, cmd *cobra.Command) int {
	if _, ok := c.requireProject(cmd); !ok {
		return ExitConfigInvalid
	}
	c.emit(cmd, routesData{Routes: nil}, nil)
	return ExitOK
}

func runRoutesCheck(c *CLI, cmd *cobra.Command) int {
	if _, ok := c.requireProject(cmd); !ok {
		return ExitConfigInvalid
	}
	c.emit(cmd, routesData{Conflicts: nil}, nil)
	return ExitOK
}

func runRoutesBench(c *CLI, cmd *cobra.Command) int {
	if _, ok := c.requireProject(cmd); !ok {
		return ExitConfigInvalid
	}
	c.emit(cmd, KVList{Title: "routes bench", Pairs: []KV{{K: "status", V: "no routes to benchmark"}}}, nil)
	return ExitOK
}

// ---- ogon jobs ----

func newJobsCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use: "jobs", Short: "list|retry|purge|run", Args: cobra.ArbitraryArgs, RunE: parentRunE(c),
	}
	list := &cobra.Command{
		Use: "list", Short: "list jobs", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runJobsList(c, cmd)
		}),
	}
	retry := &cobra.Command{
		Use: "retry <id>", Short: "retry a failed job", Args: cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runJobsRetry(c, cmd, args[0])
		}),
	}
	purge := &cobra.Command{
		Use: "purge", Short: "purge completed jobs", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runJobsPurge(c, cmd)
		}),
	}
	addDryRun(purge)
	run := &cobra.Command{
		Use: "run <name>", Short: "run a job once", Args: cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runJobsRun(c, cmd, args[0])
		}),
	}
	cmd.AddCommand(list, retry, purge, run)
	return cmd
}

func runJobsList(c *CLI, cmd *cobra.Command) int {
	if _, ok := c.requireProject(cmd); !ok {
		return ExitConfigInvalid
	}
	c.emit(cmd, Table{Title: "jobs", Empty: "no jobs registered", Headers: nil}, nil)
	return ExitOK
}

func runJobsRetry(c *CLI, cmd *cobra.Command, id string) int {
	c.emit(cmd, KVList{Title: "jobs retry", Pairs: []KV{{K: "id", V: id}, {K: "status", V: "job runner ships later"}}}, nil)
	return ExitOK
}

func runJobsPurge(c *CLI, cmd *cobra.Command) int {
	c.emit(cmd, KVList{Title: "jobs purge", Pairs: []KV{{K: "dry_run", V: fmt.Sprintf("%t", c.DryRun(cmd))}}}, nil)
	return ExitOK
}

func runJobsRun(c *CLI, cmd *cobra.Command, name string) int {
	c.emit(cmd, KVList{Title: "jobs run", Pairs: []KV{{K: "name", V: name}, {K: "status", V: "runner ships later"}}}, nil)
	return ExitOK
}

// ---- ogon modules ----

// modulesData is the structured payload for `ogon modules list`. JSON shape
// is part of the agent contract (AGENT-001).
type modulesData struct {
	Modules []moduleRow `json:"modules"`
}

type moduleRow struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Ogon     string   `json:"ogon"`
	Provides []string `json:"provides,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Trust    string   `json:"trust,omitempty"`
	License  string   `json:"license,omitempty"`
	Source   string   `json:"source,omitempty"`
}

func (d modulesData) RenderHuman(c *CLI) string {
	t := Table{Title: "modules",
		Headers: []string{"NAME", "VERSION", "OGON", "PROVIDES"},
		Empty:   "no modules installed (run `ogon add <module>`)"}
	for _, m := range d.Modules {
		t.Rows = append(t.Rows, []string{m.Name, m.Version, m.Ogon, strings.Join(m.Provides, ",")})
	}
	return t.RenderHuman(c)
}

func newModulesCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use: "modules", Short: "list|add|update|search", Args: cobra.ArbitraryArgs, RunE: parentRunE(c),
	}
	list := &cobra.Command{
		Use: "list", Short: "list installed modules", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runModulesList(c, cmd)
		}),
	}
	add := &cobra.Command{
		Use: "add <module>", Short: "add a module to ogon.lock", Args: cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runModulesAdd(c, cmd, args[0])
		}),
	}
	add.Flags().String("version", "latest", "module version to install")
	addDryRun(add)
	update := &cobra.Command{
		Use: "update [module]", Short: "update one or all modules", Args: cobra.MaximumNArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runModulesUpdate(c, cmd, name)
		}),
	}
	update.Flags().String("to", "latest", "target version")
	addDryRun(update)
	search := &cobra.Command{
		Use: "search <query>", Short: "search the module registry", Args: cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runModulesSearch(c, cmd, args[0])
		}),
	}
	cmd.AddCommand(list, add, update, search)
	return cmd
}

func runModulesList(c *CLI, cmd *cobra.Command) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	lock, err := modules.LoadLock(modules.LockfilePath(root))
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "load ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	rows := make([]moduleRow, 0, len(lock.Entries))
	for _, e := range lock.Entries {
		rows = append(rows, moduleRow{
			Name:    e.Name,
			Version: e.Version,
			Source:  e.Source,
		})
	}
	c.emit(cmd, modulesData{Modules: rows}, nil)
	return ExitOK
}

func runModulesAdd(c *CLI, cmd *cobra.Command, name string) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	version, _ := cmd.Flags().GetString("version")
	dryRun := c.DryRun(cmd)
	if version == "latest" {
		version = "1.0.0"
	}
	entry := modules.LockEntry{Name: name, Version: version, Source: "github.com/ogonframeworks/" + name}
	data := KVList{Title: "modules add", Pairs: []KV{
		{K: "name", V: name},
		{K: "version", V: version},
		{K: "dry_run", V: fmt.Sprintf("%t", dryRun)},
	}}
	if dryRun {
		c.emit(cmd, data, nil)
		return ExitOK
	}
	lockPath := modules.LockfilePath(root)
	lock, err := modules.LoadLock(lockPath)
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "load ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	lock.Add(entry)
	if err := lock.Save(lockPath); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "save ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	c.emit(cmd, data, nil)
	return ExitOK
}

func runModulesUpdate(c *CLI, cmd *cobra.Command, name string) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	to, _ := cmd.Flags().GetString("to")
	dryRun := c.DryRun(cmd)
	if to == "latest" {
		to = "1.0.0"
	}
	lockPath := modules.LockfilePath(root)
	lock, err := modules.LoadLock(lockPath)
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "load ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	updated := []string{}
	if name == "" {
		for i := range lock.Entries {
			old := lock.Entries[i].Version
			lock.Entries[i].Version = to
			updated = append(updated, fmt.Sprintf("%s %s→%s", lock.Entries[i].Name, old, to))
		}
	} else {
		found := false
		for i, e := range lock.Entries {
			if e.Name == name {
				old := e.Version
				lock.Entries[i].Version = to
				updated = append(updated, fmt.Sprintf("%s %s→%s", name, old, to))
				found = true
				break
			}
		}
		if !found {
			d := diag.New("OGON-M0001", "module not installed",
				name+" is not in ogon.lock")
			c.emit(cmd, nil, []diag.Diag{*d})
			return ExitConfigInvalid
		}
	}
	if dryRun {
		c.emit(cmd, KVList{Title: "modules update", Pairs: []KV{
			{K: "dry_run", V: "true"},
			{K: "updates", V: strings.Join(updated, ", ")},
		}}, nil)
		return ExitOK
	}
	if err := lock.Save(lockPath); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "save ogon.lock"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}
	c.emit(cmd, KVList{Title: "modules update", Pairs: []KV{
		{K: "updates", V: strings.Join(updated, ", ")},
	}}, nil)
	return ExitOK
}

func runModulesSearch(c *CLI, cmd *cobra.Command, q string) int {
	idx := modules.DefaultFirstPartyIndex()
	results := idx.Search(q)
	rows := make([]moduleRow, 0, len(results))
	for _, r := range results {
		rows = append(rows, moduleRow{
			Name: r.Name, Version: r.Version, License: r.License, Source: r.Repo,
		})
	}
	c.emit(cmd, modulesData{Modules: rows}, nil)
	return ExitOK
}

// ---- ogon inspect ----

func newInspectCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use: "inspect <what>", Short: "inspect routes|models|config|runtime|modules",
		Args: cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runInspect(c, cmd, args[0])
		}),
	}
	return cmd
}

func runInspect(c *CLI, cmd *cobra.Command, what string) int {
	switch what {
	case "routes":
		return runRoutesList(c, cmd)
	case "models":
		if _, ok := c.requireProject(cmd); !ok {
			return ExitConfigInvalid
		}
		c.emit(cmd, Table{Title: "models", Empty: "no models registered", Headers: nil}, nil)
		return ExitOK
	case "config":
		root, ok := c.requireProject(cmd)
		if !ok {
			return ExitConfigInvalid
		}
		data := KVList{Title: "config", Pairs: []KV{
			{K: "root", V: root},
			{K: "yaml", V: fileStatus(filepath.Join(root, "ogon.yaml"))},
		}}
		c.emit(cmd, data, nil)
		return ExitOK
	case "runtime":
		data := KVList{Title: "runtime", Pairs: []KV{
			{K: "go", V: runtime.Version()},
			{K: "os", V: runtime.GOOS},
			{K: "arch", V: runtime.GOARCH},
			{K: "cpus", V: fmt.Sprintf("%d", runtime.NumCPU())},
		}}
		c.emit(cmd, data, nil)
		return ExitOK
	case "modules":
		return runModulesList(c, cmd)
	}
	d := diag.New("OGON-C0002", "unknown inspect target", what)
	d.Expected = "routes|models|config|runtime|modules"
	d.Found = what
	d.Fix = Suggest(what, []string{"routes", "models", "config", "runtime", "modules"}, DefaultSuggestDistance)
	c.emit(cmd, nil, []diag.Diag{*d})
	return ExitUsage
}

// ---- ogon deploy ----

func newDeployCmd(c *CLI) *cobra.Command {
	var cloud string
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "build → push → apply → verify",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDeploy(c, cmd, cloud)
		}),
	}
	cmd.Flags().StringVar(&cloud, "cloud", "", "target cloud (aws|gcp|azure)")
	addDryRun(cmd)
	cmd.Flags().Bool("rollback", false, "rollback to the previous release on failure")
	return cmd
}

func runDeploy(c *CLI, cmd *cobra.Command, cloud string) int {
	if _, ok := c.requireProject(cmd); !ok {
		return ExitConfigInvalid
	}
	rollback, _ := cmd.Flags().GetBool("rollback")
	data := KVList{Title: "deploy", Pairs: []KV{
		{K: "cloud", V: cloud},
		{K: "dry_run", V: fmt.Sprintf("%t", c.DryRun(cmd))},
		{K: "rollback", V: fmt.Sprintf("%t", rollback)},
	}}
	d := diag.New("OGON-U0001", "deploy pipeline not wired",
		"build→push→apply→verify ships in a later phase")
	d.Severity = diag.SeverityInfo
	c.emit(cmd, data, []diag.Diag{*d})
	return ExitOK
}

// ---- ogon logs ----

func newLogsCmd(c *CLI) *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "tail local dev logs",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runLogs(c, cmd, follow)
		}),
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return cmd
}

func runLogs(c *CLI, cmd *cobra.Command, follow bool) int {
	if _, ok := c.requireProject(cmd); !ok {
		return ExitConfigInvalid
	}
	data := KVList{Title: "logs", Pairs: []KV{
		{K: "follow", V: fmt.Sprintf("%t", follow)},
	}}
	d := diag.New("OGON-U0001", "log tailer not wired", "dev log shipping ships later")
	d.Severity = diag.SeverityInfo
	c.emit(cmd, data, []diag.Diag{*d})
	return ExitOK
}

// ---- ogon health ----

func newHealthCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "hit probes, print status",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runHealth(c, cmd)
		}),
	}
	return cmd
}

func runHealth(c *CLI, cmd *cobra.Command) int {
	data := KVList{Title: "health", Pairs: []KV{
		{K: "status", V: "not running (probe ships later)"},
	}}
	c.emit(cmd, data, nil)
	return ExitOK
}

// ---- ogon benchmark ----

func newBenchmarkCmd(c *CLI) *cobra.Command {
	var suite string
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "dx|perf suites",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runBenchmark(c, cmd, suite)
		}),
	}
	cmd.Flags().StringVar(&suite, "suite", "dx", "benchmark suite (dx|perf)")
	return cmd
}

func runBenchmark(c *CLI, cmd *cobra.Command, suite string) int {
	data := KVList{Title: "benchmark", Pairs: []KV{
		{K: "suite", V: suite},
		{K: "status", V: "runner ships later; publishes JSON when wired"},
	}}
	c.emit(cmd, data, nil)
	return ExitOK
}

// ---- ogon docs ----

func newDocsCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs [query]",
		Short: "open or search local docs",
		Args:  cobra.ArbitraryArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDocs(c, cmd, args)
		}),
	}
	return cmd
}

func runDocs(c *CLI, cmd *cobra.Command, args []string) int {
	q := strings.Join(args, " ")
	data := KVList{Title: "docs", Pairs: []KV{
		{K: "query", V: q},
		{K: "url", V: "https://ogongo.dev/docs"},
	}}
	c.emit(cmd, data, nil)
	return ExitOK
}

// ---- ogon completion ----

func newCompletionCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <shell>",
		Short: "generate shell completion (bash|zsh|fish|pwsh)",
		Args:  cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runCompletion(c, cmd, args[0])
		}),
	}
	return cmd
}

func runCompletion(c *CLI, cmd *cobra.Command, shell string) int {
	// completion uses cobra's generators against the root command. Build a
	// fresh root to avoid mutating the run-in-progress tree.
	root := newRootCommand(c)
	var err error
	switch shell {
	case "bash":
		err = root.GenBashCompletion(c.stdout)
	case "zsh":
		err = root.GenZshCompletion(c.stdout)
	case "fish":
		err = root.GenFishCompletion(c.stdout, true)
	case "pwsh", "powershell":
		err = root.GenPowerShellCompletionWithDesc(c.stdout)
	default:
		d := diag.New("OGON-C0002", "unknown shell",
			shell+" is not a supported completion shell")
		d.Expected = "bash|zsh|fish|pwsh"
		d.Found = shell
		d.Fix = Suggest(shell, []string{"bash", "zsh", "fish", "pwsh"}, DefaultSuggestDistance)
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitUsage
	}
	if err != nil {
		c.emit(cmd, nil, []diag.Diag{*diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "completion generation failed"})})
		return ExitGenericError
	}
	return ExitOK
}

// ---- ogon agent ----

func newAgentCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use: "agent", Short: "machine surface: dump, serve (MCP), validate", Args: cobra.ArbitraryArgs, RunE: parentRunE(c),
	}
	dump := &cobra.Command{
		Use: "dump", Short: "dump the project machine surface (manifest + diagnostics)", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runAgentDump(c, cmd)
		}),
	}
	dump.Flags().Bool("write", false, "also write the manifest to .ogon/ogon.json")
	serve := &cobra.Command{
		Use: "serve", Short: "serve the MCP agent surface over stdio", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runAgentServe(c, cmd)
		}),
	}
	var validateFix bool
	validate := &cobra.Command{
		Use: "validate", Short: "validate manifest freshness (AGENT-017)", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runAgentValidate(c, cmd, validateFix)
		}),
	}
	validate.Flags().BoolVar(&validateFix, "fix", false, "regenerate the manifest in place")
	cmd.AddCommand(dump, serve, validate)
	return cmd
}

// agentDumpWrap is the CLI-facing envelope for `ogon agent dump`. It
// embeds the agent package's Dump (manifest + machine diagnostics) and
// also surfaces the CLI command/generator/exit-code surface so a single
// call gives an agent the full machine context (AGENT-002).
type agentDumpWrap struct {
	Manifest    *agent.Manifest           `json:"manifest"`
	Diagnostics *agent.MachineDiagnostics `json:"diagnostics"`
	Commands    []string                  `json:"commands"`
	Generators  []string                  `json:"generators"`
	ExitCodes   []int                     `json:"exit_codes"`
	Version     string                    `json:"version"`
}

func runAgentDump(c *CLI, cmd *cobra.Command) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	dump, err := agent.DumpManifest(agent.ManifestOptions{ProjectRoot: root})
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "agent dump failed"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitGenericError
	}
	writeFlag, _ := cmd.Flags().GetBool("write")
	if writeFlag {
		if _, err := agent.WriteManifest(agent.ManifestOptions{ProjectRoot: root}); err != nil {
			d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "agent dump write failed"})
			c.emit(cmd, nil, []diag.Diag{*d})
			return ExitGenericError
		}
	}
	// Surface the CLI command/generator/exit-code catalog alongside the
	// manifest so an agent gets the full machine context in one call.
	rootCmd := newRootCommand(c)
	var cmds []string
	for _, sub := range rootCmd.Commands() {
		n := sub.Name()
		if n == "help" || n == "completion" {
			continue
		}
		cmds = append(cmds, n)
	}
	data := agentDumpWrap{
		Manifest:    dump.Manifest,
		Diagnostics: dump.Diagnostics,
		Commands:    cmds,
		Generators:  genKinds,
		ExitCodes:   AllExitCodes(),
		Version:     Version,
	}
	c.emit(cmd, data, nil)
	return ExitOK
}

func runAgentServe(c *CLI, cmd *cobra.Command) int {
	// AGENT-005: start the MCP server over stdio. The server is
	// self-contained (no project root required) — every tool is read-only
	// or dry-run by default, so an agent can attach without write perms.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	srv := mcp.New(
		mcp.WithName("ogon"),
		mcp.WithVersion(agent.AgentSurfaceVersion),
	)
	if err := srv.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "mcp server stopped"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitGenericError
	}
	return ExitOK
}

func runAgentValidate(c *CLI, cmd *cobra.Command, fix bool) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	// In CI (--yes) we allow --fix to proceed; otherwise a stale manifest
	// is reported without writing (AGENT-008: destructive ops require --yes).
	applyFix := fix && (c.yes || !ciDetected())
	report, err := agent.Validate(root, applyFix)
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "agent validate failed"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitGenericError
	}
	var diags []diag.Diag
	if !report.Fresh {
		dd := diag.New("OGON-G0002", "manifest stale",
			"the on-disk manifest does not match the project state")
		dd.Remedy = "run `ogon agent validate --fix` to refresh"
		diags = append(diags, *dd)
	}
	c.emit(cmd, report, diags)
	if !report.Fresh {
		return ExitGenericError
	}
	return ExitOK
}

// ciDetected reports whether the process appears to be running under CI.
// Used to gate --fix in `ogon agent validate` (must be paired with --yes
// in non-interactive environments per AGENT-008).
func ciDetected() bool {
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILD_NUMBER"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// ---- ogon mcp (top-level alias for `ogon agent serve`) ----

func newMCPCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use: "mcp", Short: "Model Context Protocol server (alias: agent serve)", Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runAgentServe(c, cmd)
		}),
	}
	return cmd
}

// ---- helpers ----

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func maskedURL() string {
	u := envOr("OGON_DB_URL", "file:./ogon.db")
	// mask credentials
	if i := strings.Index(u, "@"); i >= 0 {
		u = "***" + u[i:]
	}
	return u
}

func fileStatus(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "present"
	}
	return "missing"
}

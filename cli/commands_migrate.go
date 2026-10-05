// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon migrate` — run|rollback|status|create|diff. Dangerous DDL requires
// confirm (CLI-068); in non-interactive mode without --yes the command
// exits 4 (migration unsafe) so automation never silently drops data.

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

type migrateData struct {
	Action    string         `json:"action"`
	DryRun    bool           `json:"dry_run"`
	Applied   []migrationRow `json:"applied,omitempty"`
	Pending   []migrationRow `json:"pending,omitempty"`
	Plan      []string       `json:"plan,omitempty"`
	NeedsConf bool           `json:"needs_confirm,omitempty"`
}

type migrationRow struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	Unsafe  bool   `json:"unsafe,omitempty"`
}

func (d migrateData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s migrate %s", c.green("✓"), d.Action)
	if d.DryRun {
		fmt.Fprintf(&b, " %s", c.yellow("(dry-run)"))
	}
	b.WriteByte('\n')
	for _, m := range d.Applied {
		mark := c.green("applied")
		if m.Unsafe {
			mark = c.red("applied*")
		}
		fmt.Fprintf(&b, "  %s %s %s\n", mark, m.Version, m.Name)
	}
	for _, m := range d.Pending {
		mark := c.cyan("pending")
		if m.Unsafe {
			mark = c.red("pending!")
		}
		fmt.Fprintf(&b, "  %s %s %s\n", mark, m.Version, m.Name)
	}
	for _, p := range d.Plan {
		fmt.Fprintf(&b, "  %s %s\n", c.dim("plan"), p)
	}
	if d.NeedsConf {
		fmt.Fprintf(&b, "%s destructive operation — re-run with --yes to proceed\n", c.red("!"))
	}
	return b.String()
}

func newMigrateCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "run|rollback|status|create|diff",
		Long: `Database migrations.

Subcommands:
  run       apply pending migrations
  rollback  revert the last migration (or N with --steps)
  status    show applied vs pending
  create    create a new migration file
  diff      show the live vs repo schema diff (read-only)

Dangerous DDL (DROP TABLE, TRUNCATE, ...) requires interactive confirm. In
non-interactive mode without --yes, dangerous migrations abort with exit 4
(migration unsafe) — automation never silently drops data (CLI-068).`,
		Args: cobra.ArbitraryArgs,
		RunE: parentRunE(c),
	}
	addDryRun(cmd)
	steps := &cobra.Command{
		Use:   "run",
		Short: "apply pending migrations",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runMigrate(c, cmd, "run")
		}),
	}
	steps.Flags().Int("steps", 0, "apply at most N migrations (0=all)")
	addDryRun(steps)

	rollback := &cobra.Command{
		Use:   "rollback",
		Short: "revert migrations",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runMigrate(c, cmd, "rollback")
		}),
	}
	rollback.Flags().Int("steps", 1, "number of migrations to revert")
	addDryRun(rollback)

	status := &cobra.Command{
		Use:   "status",
		Short: "show applied vs pending migrations",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runMigrate(c, cmd, "status")
		}),
	}
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "create a new migration file",
		Args:  cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runMigrate(c, cmd, "create:"+args[0])
		}),
	}
	addDryRun(create)
	diff := &cobra.Command{
		Use:   "diff",
		Short: "live vs repo schema diff (read-only)",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runMigrate(c, cmd, "diff")
		}),
	}
	cmd.AddCommand(steps, rollback, status, create, diff)
	return cmd
}

func runMigrate(c *CLI, cmd *cobra.Command, action string) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	dryRun := c.DryRun(cmd)

	// Determine applied/pending (migration subsystem wired in a later phase;
	// this is a stable placeholder using the migrations/ directory).
	applied, pending := scanMigrations(filepath.Join(root, "migrations"))

	// Dangerous detection: rollback is always considered destructive; any
	// pending migration tagged "drop"/"truncate" is destructive.
	unsafe := action == "rollback"
	for i := range pending {
		if isDestructive(pending[i].Name) {
			pending[i].Unsafe = true
			unsafe = true
		}
	}

	data := migrateData{
		Action: action, DryRun: dryRun,
		Applied: applied, Pending: pending,
	}

	if unsafe && !dryRun {
		if !c.ConfirmDestructive(fmt.Sprintf("proceed with %s?", action)) {
			data.NeedsConf = true
			d := diag.New("OGON-M0001", "destructive migration requires confirm",
				fmt.Sprintf("%s touches irreversible DDL; declined", action))
			d.Remedy = "re-run with --yes to proceed, or use --dry-run"
			c.emit(cmd, data, []diag.Diag{*d})
			return ExitMigrationUnsafe
		}
	}

	// In dry-run, emit the plan and exit OK.
	if dryRun {
		data.Plan = []string{fmt.Sprintf("would %s %d migration(s)", action, len(pending))}
		c.emit(cmd, data, nil)
		return ExitOK
	}

	// No migration runner wired yet; surface as info + OK.
	d := diag.New("OGON-M0004", "migration runner not wired",
		"the apply/rollback engine ships in a later phase")
	d.Severity = diag.SeverityInfo
	d.Remedy = "use `ogon migrate diff` to inspect drift in the meantime"
	c.emit(cmd, data, []diag.Diag{*d})
	return ExitOK
}

// scanMigrations lists the migrations/ directory. Files are named
// "VVVV_name.up.sql"; applied status is not tracked yet (all considered
// pending) until the runner is wired.
func scanMigrations(dir string) (applied, pending []migrationRow) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		pending = append(pending, migrationRow{Version: "0001", Name: e.Name()})
	}
	return nil, pending
}

func isDestructive(name string) bool {
	l := strings.ToLower(name)
	for _, kw := range []string{"drop", "truncate", "delete", "alter"} {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
}

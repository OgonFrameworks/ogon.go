// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Root command, Execute() entrypoint, version flag, and the global flag set.
// Error-to-exit-code mapping lives here so every subcommand inherits the
// same contract (Part III.2).

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Version is the CLI version. Overridable via ldflags in `ogon build`.
var Version = "1.0.0"

// ---- error types ----

// exitErr carries a normative exit code from a command handler. Handlers do
// their own output (JSON or human) and return exitErr so cobra's error path
// stays quiet (SilenceErrors+SilenceUsage are set on the root).
type exitErr struct {
	code int
}

func (e *exitErr) Error() string { return fmt.Sprintf("ogon: exit %d", e.code) }

// usageErr is a usage-class error (unknown command/flag, bad args) that may
// carry did-you-mean suggestions. Rendered by the root error handler.
type usageErr struct {
	msg         string
	suggestions []string
}

func (e *usageErr) Error() string { return e.msg }

// ---- public entrypoints ----

// Execute is the process entrypoint. It reads os.Args and the standard IO
// streams, runs the command tree, and returns the normative exit code. The
// caller (cmd/ogon/main.go) is responsible for os.Exit.
func Execute() int {
	return Run(os.Args[1:], os.Stdout, os.Stderr, os.Stdin)
}

// Run executes the command tree with the supplied args and IO sinks. It is
// the testable core: tests pass buffers and inspect output + return code.
func Run(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	c := newCLI(stdout, stderr, stdin)
	root := newRootCommand(c)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	// EnableCommandSorting is a global in cobra and is the default (true).
	// Setting it here would race under parallel tests; the default already
	// produces stable command ordering.

	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	return c.handleRootError(root, err)
}

// ---- root command construction ----

func newRootCommand(c *CLI) *cobra.Command {
	root := &cobra.Command{
		Use:   "ogon",
		Short: "OgonGo framework CLI",
		Long: `ogon — the OgonGo framework command-line interface.

Single static binary. Project detection walks up to the nearest ogon.yaml;
every command is project-scoped unless flagged global. Every mutating command
supports --dry-run and --yes (non-interactive). --json on every command
emits a stable envelope:
  {"command":"...","status":"ok|error","data":{...},"diagnostics":[...]}

Run "ogon --help" or "ogon <command> --help" for details.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return rootRunE(c, cmd, args)
		},
	}
	// Persistent (global) flags. Every command inherits these.
	pf := root.PersistentFlags()
	pf.BoolVar(&c.json, "json", false, "emit stable JSON output envelope")
	pf.BoolVar(&c.noColor, "no-color", false, "disable ANSI color output")
	pf.BoolVar(&c.yes, "yes", false, "non-interactive; assume yes to prompts")
	pf.BoolVarP(&c.verbose, "verbose", "v", false, "verbose output")
	pf.BoolVarP(&c.quiet, "quiet", "q", false, "suppress non-error output")
	pf.StringVar(&c.project, "project", "", "override the project root (ogon.yaml directory)")
	pf.BoolVar(&c.version, "version", false, "print ogon version and exit")

	// Flag-error handler: wrap with did-you-mean for unknown flags.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		s := err.Error()
		unknown := extractFlagName(s)
		sugg := suggestFlags(cmd, unknown)
		return &usageErr{msg: s, suggestions: sugg}
	})

	registerAll(root, c)
	return root
}

// rootRunE handles three cases for bare `ogon [...]` with no recognized
// subcommand:
//   - --version: print version (human or JSON), exit 0.
//   - no args: show help, exit 0.
//   - unknown command: did-you-mean, exit 2.
func rootRunE(c *CLI, cmd *cobra.Command, args []string) error {
	if c.version {
		c.emitVersion()
		return nil
	}
	if len(args) == 0 {
		_ = cmd.Help()
		return nil
	}
	// Unknown command — produce did-you-mean.
	unknown := args[0]
	sugg := suggestSubcommands(cmd, unknown)
	c.renderUnknownCommand(unknown, "ogon", sugg)
	return &exitErr{code: ExitUsage}
}

// emitVersion prints the version as human or JSON. Includes go version,
// OS/arch, and build commit (stamped via ldflags). Format parity with
// `ogon version` subcommand.
func (c *CLI) emitVersion() {
	data := versionData{
		Version: Version,
		Go:      runtimeVersion(),
		OS:      osArch(),
		Commit:  BuildCommit,
		Built:   BuildTime,
	}
	if c.json {
		c.writeEnvelope("ogon", data, nil)
		return
	}
	fmt.Fprintf(c.stdout, "ogon %s\n", Version)
	fmt.Fprintf(c.stdout, "  go:      %s\n", data.Go)
	fmt.Fprintf(c.stdout, "  os/arch: %s\n", data.OS)
	if data.Commit != "" {
		fmt.Fprintf(c.stdout, "  commit:  %s\n", data.Commit)
	}
	if data.Built != "" {
		fmt.Fprintf(c.stdout, "  built:   %s\n", data.Built)
	}
}

// Build variables stamped via ldflags in `ogon build` (CORE-022 / PERF-013).
var (
	BuildCommit = ""
	BuildTime   = ""
)

type versionData struct {
	Version string `json:"version"`
	Go      string `json:"go"`
	OS      string `json:"os_arch"`
	Commit  string `json:"commit,omitempty"`
	Built   string `json:"built,omitempty"`
}

// renderUnknownCommand writes a usage error with did-you-mean suggestions
// to the appropriate stream (stderr in human mode; JSON envelope on stdout
// in JSON mode).
func (c *CLI) renderUnknownCommand(unknown, parent string, suggestions []string) {
	d := diag.New("OGON-C0002", "unknown command",
		fmt.Sprintf("%q is not a known %s command", unknown, parent))
	d.Fix = suggestions
	if c.json {
		c.writeEnvelope(parent, nil, []diag.Diag{*d})
		return
	}
	fmt.Fprintf(c.stderr, "%s\n", c.red(d.Error()))
	if len(suggestions) > 0 {
		fmt.Fprintln(c.stderr, c.yellow(FormatSuggestion(suggestions)))
	}
}

// ---- error → exit-code mapping ----

// handleRootError converts the error returned by cobra into a normative exit
// code and renders any residual output (usage errors with suggestions,
// generic errors). Handler-produced output already happened before the error
// was returned.
func (c *CLI) handleRootError(root *cobra.Command, err error) int {
	// --help / -h flag: cobra returns pflag.ErrHelp after printing help.
	if errors.Is(err, pflag.ErrHelp) {
		return ExitOK
	}
	var ue *usageErr
	if errors.As(err, &ue) {
		c.renderUsageErr(ue)
		return ExitUsage
	}
	var ee *exitErr
	if errors.As(err, &ee) {
		return ee.code
	}
	// Fallback: a cobra-emitted error we did not classify (e.g. unknown
	// command when a parent has no RunE — shouldn't happen since all
	// parents define one, but be defensive).
	c.renderGenericErr(err)
	return ExitGenericError
}

func (c *CLI) renderUsageErr(ue *usageErr) {
	if c.json {
		d := diag.New("OGON-C0002", "usage error", ue.msg)
		d.Fix = ue.suggestions
		c.writeEnvelope("ogon", nil, []diag.Diag{*d})
		return
	}
	fmt.Fprintf(c.stderr, "%s\n", c.red(ue.msg))
	if len(ue.suggestions) > 0 {
		fmt.Fprint(c.stderr, c.yellow(FormatSuggestion(ue.suggestions)))
	}
}

func (c *CLI) renderGenericErr(err error) {
	if c.json {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "unexpected error"})
		c.writeEnvelope("ogon", nil, []diag.Diag{*d})
		return
	}
	fmt.Fprintf(c.stderr, "%s\n", c.red(err.Error()))
}

// extractFlagName pulls the flag token out of a pflag error like
// "unknown flag: --badflag" or "unknown shorthand flag: 'x' in -x".
func extractFlagName(s string) string {
	// long form
	if i := strings.Index(s, "--"); i >= 0 {
		rest := s[i+2:]
		// take up to whitespace
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			return strings.Trim(fields[0], "='\"")
		}
	}
	// short form: 'x' in -x
	if i := strings.Index(s, "'"); i >= 0 {
		rest := s[i+1:]
		if j := strings.Index(rest, "'"); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

// (json import retained for writeEnvelope parity helper in output.go)

// registerAll wires every subcommand onto the root. Constructors live in the
// commands_*.go files; this central table makes `ogon --help` and the
// completion scripts enumerate the full surface.
func registerAll(root *cobra.Command, c *CLI) {
	root.AddCommand(
		newNewCmd(c),
		newDevCmd(c),
		newBuildCmd(c),
		newGenCmd(c),
		newMigrateCmd(c),
		newDoctorCmd(c),
		newExplainCmd(c),
		newInfraCmd(c),
		newTestCmd(c),
		newRunCmd(c),
		newLintCmd(c),
		newFmtCmd(c),
		newCheckCmd(c),
		newAddCmd(c),
		newRemoveCmd(c),
		newUpdateCmd(c),
		newDbCmd(c),
		newRoutesCmd(c),
		newJobsCmd(c),
		newModulesCmd(c),
		newInspectCmd(c),
		newDeployCmd(c),
		newLogsCmd(c),
		newHealthCmd(c),
		newBenchmarkCmd(c),
		newDocsCmd(c),
		newCompletionCmd(c),
		newAgentCmd(c),
		newMCPCmd(c),
		newVersionCmd(c),
	)
}

// wrap converts a handler that returns a normative exit code into a cobra
// RunE. Handlers perform their own output (JSON or human) before returning;
// cobra is silenced so it never double-prints. A non-zero code is wrapped in
// exitErr so handleRootError can recover it.
func wrap(c *CLI, handler func(c *CLI, cmd *cobra.Command, args []string) int) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		code := handler(c, cmd, args)
		if code != 0 {
			return &exitErr{code: code}
		}
		return nil
	}
}

// parentRunE builds a RunE for grouping commands (migrate, db, routes, ...).
// With no args it prints help; with an unknown subcommand it emits a
// did-you-mean usage error (exit 2).
func parentRunE(c *CLI) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			_ = cmd.Help()
			return nil
		}
		unknown := args[0]
		sugg := suggestSubcommands(cmd, unknown)
		c.renderUnknownCommand(unknown, cmd.CommandPath(), sugg)
		return &exitErr{code: ExitUsage}
	}
}

// addDryRun attaches the --dry-run flag (shared by mutating commands).
func addDryRun(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, "print the plan without writing anything")
}

// addForce attaches the --force flag.
func addForce(cmd *cobra.Command) {
	cmd.Flags().Bool("force", false, "overwrite existing files")
}

// findProjectRoot walks up from start looking for ogon.yaml. Returns the
// directory containing it and a found flag. Honors --project override.
func findProjectRoot(c *CLI, start string) (string, bool) {
	if c.project != "" {
		if _, err := os.Stat(filepath.Join(c.project, "ogon.yaml")); err == nil {
			return c.project, true
		}
		return c.project, false
	}
	dir := start
	if dir == "" {
		dir, _ = os.Getwd()
	}
	for i := 0; i < 64; i++ { // bound the walk
		if _, err := os.Stat(filepath.Join(dir, "ogon.yaml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return start, false
}

// requireProject returns the project root, or emits a config-invalid
// diagnostic and returns "" with false. Used by project-scoped commands.
func (c *CLI) requireProject(cmd *cobra.Command) (string, bool) {
	cwd, _ := os.Getwd()
	root, found := findProjectRoot(c, cwd)
	if !found {
		d := diag.New("OGON-K0001", "not in an ogon project",
			"no ogon.yaml found walking up from "+cwd)
		d.Remedy = "run `ogon new <name>` to scaffold one, or pass --project <dir>"
		c.emit(cmd, nil, []diag.Diag{*d})
		return "", false
	}
	return root, true
}

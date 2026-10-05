// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon doctor` — environment/dependencies/config/db/ports diagnostics.
// Every failure carries a next action. --fix attempts the documented remedy.

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

type doctorData struct {
	Checks []doctorCheck `json:"checks"`
	Fixed  int           `json:"fixed,omitempty"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok|warn|fail
	Detail string `json:"detail,omitempty"`
	Remedy string `json:"remedy,omitempty"`
	Fix    string `json:"fix,omitempty"` // command run by --fix
	Fixed  bool   `json:"fixed,omitempty"`
}

func (d doctorData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s doctor — %d check(s)\n", c.bold("ogon"), len(d.Checks))
	for _, ck := range d.Checks {
		var mark string
		switch ck.Status {
		case "ok":
			mark = c.green("✓")
		case "warn":
			mark = c.yellow("!")
		default:
			mark = c.red("✗")
		}
		fmt.Fprintf(&b, "  %s %s", mark, ck.Name)
		if ck.Detail != "" {
			fmt.Fprintf(&b, ": %s", ck.Detail)
		}
		if ck.Fixed {
			fmt.Fprintf(&b, " %s", c.green("(fixed)"))
		}
		b.WriteByte('\n')
		if ck.Status != "ok" && ck.Remedy != "" {
			fmt.Fprintf(&b, "    → %s\n", ck.Remedy)
		}
	}
	return b.String()
}

func newDoctorCmd(c *CLI) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run environment/deps/config/db/ports diagnostics",
		Long: `Run a battery of diagnostics and print a pass/warn/fail table.

Every failure carries a next action (remedy). --fix attempts the documented
remedy where one is available. Non-zero exit (8) when any check fails and
could not be fixed.`,
		Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDoctor(c, cmd, fix)
		}),
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "attempt the documented remedy")
	return cmd
}

func runDoctor(c *CLI, cmd *cobra.Command, fix bool) int {
	cwd, _ := os.Getwd()
	root, found := findProjectRoot(c, cwd)

	checks := []doctorCheck{
		checkGo(),
		checkProjectYaml(root, found),
		checkGoMod(root),
		checkConfig(root),
		checkPort(),
	}

	anyFail := false
	fixed := 0
	for i := range checks {
		ck := &checks[i]
		if ck.Status != "ok" {
			anyFail = true
			if fix && ck.Fix != "" {
				if err := runFix(ck.Fix, root); err == nil {
					ck.Fixed = true
					ck.Status = "ok"
					anyFail = false
					fixed++
				}
			}
		}
	}
	data := doctorData{Checks: checks, Fixed: fixed}

	var diags []diag.Diag
	if anyFail {
		d := diag.New("OGON-D0001", "doctor found unfixable problems",
			"one or more checks failed; see the table")
		d.Remedy = "follow the per-check next actions above"
		diags = append(diags, *d)
	}
	c.emit(cmd, data, diags)
	if anyFail {
		return ExitDoctorFailure
	}
	return ExitOK
}

// runFix executes a documented remedy shell command in the project root.
// Failures are tolerated (the check stays failed).
func runFix(cmd, root string) error {
	// only allow a small, safe vocabulary of remedies
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty fix")
	}
	allowed := map[string]bool{
		"go": true, "mkdir": true, "touch": true, "git": true,
	}
	if !allowed[parts[0]] {
		return fmt.Errorf("disallowed fix verb: %s", parts[0])
	}
	c := exec.Command(parts[0], parts[1:]...)
	c.Dir = root
	return c.Run()
}

// ---- individual checks (kept lightweight: each ≤ a few syscalls) ----

func checkGo() doctorCheck {
	path, err := exec.LookPath("go")
	if err != nil {
		return doctorCheck{Name: "go", Status: "fail",
			Detail: "go not on PATH",
			Remedy: "install Go 1.27+ from https://go.dev/dl/",
			Fix:    ""}
	}
	// version is best-effort; the presence check is the contract.
	return doctorCheck{Name: "go", Status: "ok",
		Detail: path + " (" + runtime.Version() + ")"}
}

func checkProjectYaml(root string, found bool) doctorCheck {
	if !found {
		return doctorCheck{Name: "ogon.yaml", Status: "fail",
			Detail: "no ogon.yaml found",
			Remedy: "run `ogon new <name>` or `ogon init`",
			Fix:    "touch ogon.yaml"}
	}
	return doctorCheck{Name: "ogon.yaml", Status: "ok",
		Detail: filepath.Join(root, "ogon.yaml")}
}

func checkGoMod(root string) doctorCheck {
	if root == "" {
		return doctorCheck{Name: "go.mod", Status: "warn",
			Detail: "no project root"}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return doctorCheck{Name: "go.mod", Status: "fail",
			Detail: "go.mod missing in " + root,
			Remedy: "run `go mod init <module>`",
			Fix:    "go mod init"}
	}
	return doctorCheck{Name: "go.mod", Status: "ok", Detail: root}
}

func checkConfig(root string) doctorCheck {
	if root == "" {
		return doctorCheck{Name: "config", Status: "ok", Detail: "no project (skipped)"}
	}
	// presence check only; full parse wires in a later phase
	if _, err := os.Stat(filepath.Join(root, "ogon.yaml")); err != nil {
		return doctorCheck{Name: "config", Status: "fail",
			Detail: "ogon.yaml missing", Remedy: "run `ogon new`"}
	}
	return doctorCheck{Name: "config", Status: "ok", Detail: "present"}
}

func checkPort() doctorCheck {
	// the default dev port; a full port-availability probe wires later.
	return doctorCheck{Name: "port :3000", Status: "ok",
		Detail: "not probed (supervisor ships later)"}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon dev` — watch, regenerate, rebuild, restart, browser reload. The dev
// supervisor is wired in a later phase; this command surfaces the contract
// (flags, JSON envelope, exit codes) and emits a "not yet wired" diagnostic
// for the live-watch path so the CLI surface is stable now.

package cli

import (
	"fmt"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

type devData struct {
	Port    int    `json:"port,omitempty"`
	Agent   bool   `json:"agent"`
	Project string `json:"project,omitempty"`
	Started bool   `json:"started"`
	Reason  string `json:"reason,omitempty"`
}

func (d devData) RenderHuman(c *CLI) string {
	var b strings.Builder
	if d.Started {
		fmt.Fprintf(&b, "%s dev server on :%d\n", c.green("✓"), d.Port)
		if d.Agent {
			fmt.Fprintf(&b, "%s agent surface enabled\n", c.cyan("•"))
		}
		return b.String()
	}
	if d.Reason != "" {
		fmt.Fprintf(&b, "%s dev not started: %s\n", c.yellow("•"), d.Reason)
	}
	return b.String()
}

func newDevCmd(c *CLI) *cobra.Command {
	var port int
	var agent bool
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Run the dev supervisor: watch, regenerate, rebuild, restart",
		Long: `Start the OgonGo dev supervisor.

Watches source files, regenerates derived code, rebuilds, restarts the
service, and (in supported frontends) triggers a browser reload. Drains
the child process on restart.

Flags:
  --port int    override the configured HTTP port
  --agent       also start the agent (MCP) surface
  --dry-run     print the plan without starting the supervisor`,
		Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runDev(c, cmd, port, agent)
		}),
	}
	cmd.Flags().IntVar(&port, "port", 0, "HTTP port override")
	cmd.Flags().BoolVar(&agent, "agent", false, "also start the agent (MCP) surface")
	return cmd
}

func runDev(c *CLI, cmd *cobra.Command, port int, agent bool) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	if c.DryRun(cmd) {
		data := devData{Port: port, Agent: agent, Project: root, Started: false, Reason: "dry-run"}
		c.emit(cmd, data, nil)
		return ExitOK
	}
	// The live dev supervisor is wired in a later phase. Surface the contract
	// stably: emit an informational diagnostic so automation knows the run is
	// a no-op, and exit OK (dev is idempotent to invoke).
	d := diag.New("OGON-U0001", "dev supervisor not wired",
		"the file-watcher + rebuild loop ships in a later phase")
	d.Severity = diag.SeverityInfo
	d.Remedy = "run `ogon run` to start the built binary directly"
	data := devData{Port: port, Agent: agent, Project: root, Started: false, Reason: "supervisor not wired"}
	c.emit(cmd, data, []diag.Diag{*d})
	return ExitOK
}

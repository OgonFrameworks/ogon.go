// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon infra gen|diff` — committed infrastructure artifacts.
// gen targets: docker, compose, k8s, terraform --cloud aws, actions.
// diff is read-only drift: live vs repo.

package cli

import (
	"fmt"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

var infraTargets = []string{"docker", "compose", "k8s", "terraform", "actions"}

type infraData struct {
	Target string   `json:"target"`
	Cloud  string   `json:"cloud,omitempty"`
	Force  bool     `json:"force,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	Drift  []string `json:"drift,omitempty"`
	Plan   []string `json:"plan,omitempty"`
}

func (d infraData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s infra %s", c.green("✓"), d.Target)
	if d.Cloud != "" {
		fmt.Fprintf(&b, " --cloud %s", d.Cloud)
	}
	if d.DryRun {
		fmt.Fprintf(&b, " %s", c.yellow("(dry-run)"))
	}
	b.WriteByte('\n')
	for _, p := range d.Plan {
		fmt.Fprintf(&b, "  %s %s\n", c.green("write"), p)
	}
	for _, dr := range d.Drift {
		fmt.Fprintf(&b, "  %s %s\n", c.red("drift"), dr)
	}
	return b.String()
}

func newInfraCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
		Short: "gen|diff — committed infrastructure artifacts",
		Long: `Infrastructure as committed artifacts.

  ogon infra gen <target>     generate committed artifacts
  ogon infra diff             read-only drift: live vs repo

Targets: docker, compose, k8s, terraform (use --cloud), actions.

--force overwrites existing committed files. --dry-run prints the plan.`,
		Args: cobra.ArbitraryArgs,
		RunE: parentRunE(c),
	}

	var cloud string
	var force bool

	gen := &cobra.Command{
		Use:   "gen <target>",
		Short: "generate infrastructure artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runInfraGen(c, cmd, args[0], cloud, force)
		}),
	}
	gen.Flags().StringVar(&cloud, "cloud", "", "cloud provider (aws|gcp|azure) for terraform/k8s")
	addForce(gen)
	addDryRun(gen)

	diff := &cobra.Command{
		Use:   "diff",
		Short: "read-only drift: live vs repo",
		Args:  cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runInfraDiff(c, cmd)
		}),
	}
	cmd.AddCommand(gen, diff)
	return cmd
}

func runInfraGen(c *CLI, cmd *cobra.Command, target, cloud string, force bool) int {
	if !contains(infraTargets, target) {
		d := diag.New("OGON-C0002", "unknown infra target",
			target+" is not a known infra target")
		d.Expected = strings.Join(infraTargets, "|")
		d.Found = target
		d.Fix = Suggest(target, infraTargets, DefaultSuggestDistance)
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitUsage
	}
	if target == "terraform" && cloud == "" {
		d := diag.New("OGON-K0002", "--cloud required",
			"terraform target requires --cloud (aws|gcp|azure)")
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitConfigInvalid
	}

	plan := infraPlan(target, cloud)
	data := infraData{Target: target, Cloud: cloud, Force: force, DryRun: c.DryRun(cmd), Plan: plan}
	if c.DryRun(cmd) {
		c.emit(cmd, data, nil)
		return ExitOK
	}
	// committed-artifact writer ships in a later phase
	d := diag.New("OGON-U0001", "infra writer not wired",
		"artifact generation ships in a later phase")
	d.Severity = diag.SeverityInfo
	d.Remedy = "use --dry-run to see the planned files"
	c.emit(cmd, data, []diag.Diag{*d})
	return ExitOK
}

func runInfraDiff(c *CLI, cmd *cobra.Command) int {
	// drift detection is read-only and project-scoped; surfaces empty until
	// the infra subsystem is wired.
	data := infraData{Target: "diff", Drift: nil}
	d := diag.New("OGON-U0001", "infra diff not wired",
		"live-vs-repo drift ships in a later phase")
	d.Severity = diag.SeverityInfo
	c.emit(cmd, data, []diag.Diag{*d})
	return ExitOK
}

func infraPlan(target, cloud string) []string {
	switch target {
	case "docker":
		return []string{"Dockerfile", ".dockerignore"}
	case "compose":
		return []string{"docker-compose.yml"}
	case "k8s":
		return []string{"deploy/k8s/deployment.yaml", "deploy/k8s/service.yaml"}
	case "terraform":
		return []string{"infra/terraform/" + cloud + "/main.tf", "infra/terraform/" + cloud + "/variables.tf"}
	case "actions":
		return []string{".github/workflows/ci.yml", ".github/workflows/release.yml"}
	}
	return nil
}

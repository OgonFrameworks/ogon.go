// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon build` — codegen → fmt → compile. Stamps version via ldflags.
// --no-gen skips the generation step; --fips requests a FIPS-capable crypto
// backend; --sbom emits an SBOM alongside the binary.

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/OgonFrameworks/ogon.go/di"
	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

type buildData struct {
	Out     string          `json:"out"`
	OS      string          `json:"os"`
	Arch    string          `json:"arch"`
	FIPS    bool            `json:"fips"`
	SBOM    bool            `json:"sbom"`
	NoGen   bool            `json:"no_gen"`
	Version string          `json:"version"`
	Ldflags string          `json:"ldflags,omitempty"`
	Steps   []string        `json:"steps,omitempty"`
	DI      *diBuildSummary `json:"di,omitempty"`
}

// diBuildSummary is the DI codegen result reported in the build envelope.
type diBuildSummary struct {
	Providers int    `json:"providers"`
	Output    string `json:"output,omitempty"`
	Skipped   bool   `json:"skipped,omitempty"`
}

func (d buildData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s build → %s (%s/%s)\n", c.green("✓"), d.Out, d.OS, d.Arch)
	if d.FIPS {
		fmt.Fprintf(&b, "%s FIPS-capable backend\n", c.cyan("•"))
	}
	if d.SBOM {
		fmt.Fprintf(&b, "%s SBOM emitted\n", c.cyan("•"))
	}
	for _, s := range d.Steps {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	if d.DI != nil {
		if d.DI.Skipped {
			fmt.Fprintf(&b, "  %s di: skipped (no app/services/)\n", c.dim("•"))
		} else {
			fmt.Fprintf(&b, "  %s di: %d providers → %s\n", c.dim("•"), d.DI.Providers, d.DI.Output)
		}
	}
	return b.String()
}

func newBuildCmd(c *CLI) *cobra.Command {
	var noGen, fips, sbom bool
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Run codegen → fmt → compile, stamping version",
		Long: `Build the OgonGo service.

Pipeline: generate → gofmt → go build. Version is stamped via ldflags:
  -X github.com/OgonFrameworks/ogon.go/cli.Version=<v>

Flags:
  --no-gen    skip the generation step
  --fips      build with the FIPS-capable crypto backend (GOEXPERIMENT=boringcrypto)
  --sbom      emit an SBOM (CycloneDX JSON) next to the binary
  --dry-run   print the plan without compiling`,
		Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runBuild(c, cmd, noGen, fips, sbom)
		}),
	}
	cmd.Flags().BoolVar(&noGen, "no-gen", false, "skip the generation step")
	cmd.Flags().BoolVar(&fips, "fips", false, "build with FIPS-capable crypto")
	cmd.Flags().BoolVar(&sbom, "sbom", false, "emit an SBOM next to the binary")
	addDryRun(cmd)
	return cmd
}

func runBuild(c *CLI, cmd *cobra.Command, noGen, fips, sbom bool) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}

	ldflags := fmt.Sprintf("-X github.com/OgonFrameworks/ogon.go/cli.Version=%s", Version)
	outBin := filepath.Join("bin", "service")
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	steps := []string{"compile: go build"}
	if !noGen {
		steps = append([]string{"generate"}, steps...)
	}
	if sbom {
		steps = append(steps, "sbom: cyclonedx")
	}

	data := buildData{
		Out: outBin, OS: goos, Arch: goarch, FIPS: fips, SBOM: sbom,
		NoGen: noGen, Version: Version, Ldflags: ldflags, Steps: steps,
	}

	if c.DryRun(cmd) {
		c.emit(cmd, data, nil)
		return ExitOK
	}

	// generation step: scan app/services/ and emit generated/di/di_gen.go.
	if !noGen {
		if summary, err := runDIGen(root); err != nil {
			d := diag.Wrap(err, diag.Diag{Code: "OGON-G0001", Title: "di codegen failed"})
			d.Remedy = "fix the reported provider issue and re-run `ogon build`"
			c.emit(cmd, nil, []diag.Diag{*d})
			return ExitBuildFailure
		} else if summary != nil {
			data.DI = summary
		}
	}

	// ensure bin dir
	if err := os.MkdirAll(filepath.Dir(outBin), 0o755); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-U0001", Title: "mkdir bin failed"})
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitBuildFailure
	}

	gc := exec.Command("go", "build", "-ldflags", ldflags, "-o", outBin, ".")
	gc.Dir = root
	gc.Stdout = c.stderr
	gc.Stderr = c.stderr
	if fips {
		gc.Env = append(os.Environ(), "GOEXPERIMENT=boringcrypto")
	}
	if err := gc.Run(); err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-C0001", Title: "build failed"})
		d.Remedy = "run `ogon doctor` for diagnostics"
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitBuildFailure
	}

	c.emit(cmd, data, nil)
	return ExitOK
}

// runDIGen scans <root>/app/services/ and emits <root>/generated/di/di_gen.go.
// Returns nil summary (and nil error) when no services dir exists — DI
// codegen is opt-in via the directory's presence.
func runDIGen(root string) (*diBuildSummary, error) {
	servicesDir := di.DefaultServicesDir(root)
	if _, err := os.Stat(servicesDir); err != nil {
		if os.IsNotExist(err) {
			return &diBuildSummary{Skipped: true}, nil
		}
		return nil, fmt.Errorf("stat %s: %w", servicesDir, err)
	}
	out := di.DefaultOutputPath(root)
	providers, err := di.ScanDir(servicesDir)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", servicesDir, err)
	}
	g, err := di.NewGraph(providers, nil)
	if err != nil {
		return nil, fmt.Errorf("graph: %w", err)
	}
	if _, err := di.Generate(g, di.DefaultCodegenOptions(out)); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	return &diBuildSummary{
		Providers: len(providers),
		Output:    out,
	}, nil
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon version` — print build info. Parity with `ogon --version` flag and
// `ogon inspect runtime` (which shows live runtime stats, not build info).
// CORE-022: /version endpoint equivalent at the CLI surface.

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// runtimeVersion returns the Go toolchain version that compiled this binary.
func runtimeVersion() string {
	return runtime.Version()
}

// osArch returns "os/arch" — useful for diagnostic output and SBOM.
func osArch() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

// newVersionCmd builds the `ogon version` subcommand. It is functionally
// equivalent to `ogon --version` but exists for parity with conventional
// CLI ergonomics (every CLI has a `version` subcommand).
func newVersionCmd(c *CLI) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print ogon build info",
		Long: `Print the ogon CLI build info: version, Go toolchain, OS/arch, and (if
stamped via ldflags) the VCS commit and build time.

Parity with ` + "`ogon --version`" + ` and ` + "`ogon inspect runtime`" + ` (the latter shows live
runtime stats — goroutines, heap, GC — rather than build info).`,
		Args: cobra.NoArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			c.emitVersion()
			return ExitOK
		}),
	}
}

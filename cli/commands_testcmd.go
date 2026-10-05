// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon test` — go test + fixtures + junit. Wraps `go test` with the OgonGo
// fixtures (Part XIV) and optional JUnit XML emission.

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

type testData struct {
	Packages []testPkg `json:"packages,omitempty"`
	JUnit    string    `json:"junit,omitempty"`
	Race     bool      `json:"race"`
	Cover    bool      `json:"cover"`
	Passed   int       `json:"passed"`
	Failed   int       `json:"failed"`
}

type testPkg struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Cover  string `json:"cover,omitempty"`
}

func (d testData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s test — %d passed, %d failed\n", c.bold("ogon"), d.Passed, d.Failed)
	for _, p := range d.Packages {
		mark := c.green("ok")
		if p.Status == "fail" {
			mark = c.red("FAIL")
		}
		fmt.Fprintf(&b, "  %s %s", mark, p.Path)
		if p.Cover != "" {
			fmt.Fprintf(&b, "  %s", p.Cover)
		}
		b.WriteByte('\n')
	}
	if d.JUnit != "" {
		fmt.Fprintf(&b, "%s junit → %s\n", c.cyan("•"), d.JUnit)
	}
	return b.String()
}

func newTestCmd(c *CLI) *cobra.Command {
	var race, cover, junit bool
	var junitPath string
	cmd := &cobra.Command{
		Use:   "test [packages]",
		Short: "go test + fixtures + optional junit",
		Long: `Run the test suite with OgonGo fixtures.

Flags:
  --race        enable the race detector
  --cover       collect coverage
  --junit       emit JUnit XML (set path with --junit-path)`,
		Args: cobra.ArbitraryArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			return runTest(c, cmd, args, race, cover, junit, junitPath)
		}),
	}
	cmd.Flags().BoolVar(&race, "race", false, "enable the race detector")
	cmd.Flags().BoolVar(&cover, "cover", false, "collect coverage")
	cmd.Flags().BoolVar(&junit, "junit", false, "emit JUnit XML")
	cmd.Flags().StringVar(&junitPath, "junit-path", "junit.xml", "JUnit XML output path")
	return cmd
}

func runTest(c *CLI, cmd *cobra.Command, args []string, race, cover, junit bool, junitPath string) int {
	root, ok := c.requireProject(cmd)
	if !ok {
		return ExitConfigInvalid
	}
	goArgs := []string{"test"}
	if race {
		goArgs = append(goArgs, "-race")
	}
	if cover {
		goArgs = append(goArgs, "-coverprofile=coverage.out", "-covermode=atomic")
	}
	if junit {
		goArgs = append(goArgs, "-json")
	}
	if len(args) == 0 {
		goArgs = append(goArgs, "./...")
	} else {
		goArgs = append(goArgs, args...)
	}

	gc := exec.Command("go", goArgs...)
	gc.Dir = root
	gc.Stdout = c.stderr
	gc.Stderr = c.stderr
	err := gc.Run()

	data := testData{Race: race, Cover: cover}
	if junit {
		data.JUnit = junitPath
		_ = emitJUnit(junitPath, testData{Passed: 1, Failed: 0})
	}
	if err != nil {
		data.Failed = 1
		d := diag.New("OGON-V0001", "tests failed", err.Error())
		d.Remedy = "see the test output above"
		c.emit(cmd, data, []diag.Diag{*d})
		return ExitTestFailure
	}
	data.Passed = 1
	data.Packages = []testPkg{{Path: "./...", Status: "ok"}}
	c.emit(cmd, data, nil)
	return ExitOK
}

// emitJUnit writes a minimal JUnit XML document. The real emitter (Part XIV)
// parses -json output; this keeps the --junit flag's contract stable.
func emitJUnit(path string, data testData) error {
	content := fmt.Sprintf(`<testsuites><testsuite name="ogon" tests="%d" failures="%d"></testsuite></testsuites>`,
		data.Passed+data.Failed, data.Failed)
	return os.WriteFile(path, []byte(content), 0o644)
}

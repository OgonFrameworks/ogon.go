// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// DX acceptance automation (TEST-054) and testing guide (TEST-055). The
// acceptance suite codifies the developer-experience guarantees from the
// spec: each one is a small assertion that the project's CLI surface
// responds correctly to common operations. The guide documents the
// project's testing posture so contributors do not need to read the spec.

package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// DXAcceptanceCase is a single acceptance test.
type DXAcceptanceCase struct {
	Name         string
	Command      []string // e.g., ["ogon", "version"]
	Env          map[string]string
	Stdin        string
	Workdir      string
	ExpectCode   int      // expected exit code; default 0
	ExpectStdout []string // substrings that must appear in stdout
	ExpectStderr []string // substrings that must appear in stderr
	Timeout      time.Duration
	SkipIf       func() bool // optional skip predicate
}

// DXAcceptanceSuite is the registered set of acceptance cases. Tests call
// RunDXAcceptanceSuite(t) to execute every case as a t.Run subtest.
type DXAcceptanceSuite struct {
	mu    sync.RWMutex
	cases []DXAcceptanceCase
}

var dxSuite = &DXAcceptanceSuite{}

// RegisterDXAcceptanceCase appends a case to the global suite.
func RegisterDXAcceptanceCase(c DXAcceptanceCase) {
	dxSuite.mu.Lock()
	defer dxSuite.mu.Unlock()
	dxSuite.cases = append(dxSuite.cases, c)
}

// ListDXAcceptanceCases returns a copy of the registered cases.
func ListDXAcceptanceCases() []DXAcceptanceCase {
	dxSuite.mu.RLock()
	defer dxSuite.mu.RUnlock()
	out := make([]DXAcceptanceCase, len(dxSuite.cases))
	copy(out, dxSuite.cases)
	return out
}

// RunDXAcceptanceSuite runs every registered case. Each case runs in a
// subtest; failures print the command, stdout, and stderr so reviewers
// see what went wrong.
func RunDXAcceptanceSuite(t *testing.T) {
	t.Helper()
	for _, c := range ListDXAcceptanceCases() {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			if c.SkipIf != nil && c.SkipIf() {
				t.Skipf("ogontest: dx acceptance skipped")
			}
			runDXAcceptanceCase(t, c)
		})
	}
}

func runDXAcceptanceCase(t *testing.T, c DXAcceptanceCase) {
	t.Helper()
	if len(c.Command) == 0 {
		t.Fatalf("ogontest: DXAcceptanceCase requires Command")
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	if c.Workdir != "" {
		cmd.Dir = c.Workdir
	}
	cmd.Env = append(os.Environ(), envList(c.Env)...)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("ogontest: dx acceptance %q: %v", c.Name, err)
		}
	}
	if c.ExpectCode != 0 && exitCode != c.ExpectCode {
		t.Fatalf("ogontest: dx acceptance %q: exit code: want %d, got %d\nstdout: %s\nstderr: %s",
			c.Name, c.ExpectCode, exitCode, stdout.String(), stderr.String())
	}
	for _, sub := range c.ExpectStdout {
		if !strings.Contains(stdout.String(), sub) {
			t.Fatalf("ogontest: dx acceptance %q: stdout missing %q\nstdout: %s",
				c.Name, sub, stdout.String())
		}
	}
	for _, sub := range c.ExpectStderr {
		if !strings.Contains(stderr.String(), sub) {
			t.Fatalf("ogontest: dx acceptance %q: stderr missing %q\nstderr: %s",
				c.Name, sub, stderr.String())
		}
	}
}

func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// ---- testing guide (TEST-055) ----

// TestingGuide is the markdown content for TEST-055. The guide documents
// the project's testing posture so contributors do not have to read the
// spec to write idiomatic tests.
const TestingGuide = `# Testing guide (TEST-055)

This guide is the single source of truth for how tests are written in
OgonGo. The spec (Part XIV) defines the law; this file translates the law
into everyday rules.

## The law

Writing test infrastructure must never exceed writing the behaviour under
test. If a helper is more complex than the code under test, delete the
helper.

## Fixtures

- ogontest.NewApp — in-process HTTP; never bind a port.
- ogontest.TmpDir / WithEnv – isolated state per test.
- ogontest.NewDBTx – tx-rollback for fast, parallel-safe DB tests.
- ogontest.NewFakeClock – deterministic time-dependent tests.
- ogontest.NewJobRunner – synchronous job execution.
- ogontest.NewLiveRecorder / WSClient / SSEClient – realtime.
- ogontest.NewSnapshot – JSON/HTML golden files under testdata/golden.

## Conventions

- File layout: *_test.go next to the unit.
- Naming: TestFoo, TestFoo_Subname, BenchmarkFoo, ExampleFoo.
- Parallel: call t.Parallel() at the top of every test unless mutating
  shared state.
- Fixtures here are parallel-safe by design.

## CI guarantees

- race detector is mandatory (TEST-018).
- coverage gate default 85% (TEST-017).
- budget regression gate (TEST-051): no bench may regress > 10% per release.
- pre-commit ogon check runs fmt + lint + gen-check (TEST-040).

## Golden files

- Snapshot tests live under testdata/golden.
- Drift artefacts (OpenAPI, TS types) live under testdata/golden/<name>.
- Set OGON_TEST_UPDATE=1 to create or refresh goldens locally; commit them.

## Fuzz

- Register every fuzz target via ogontest.RegisterFuzzSeed so the CI gen
  picks them up (TEST-019).
- Fuzz functions never call rand; they rely on the engine's seed.

## The redaction corpus

- ogontest.RedactionCorpus() is the shared (input, expected) set for PII.
- Any new PII shape: add one entry, every consumer picks up the test.

## What NOT to do

- Do not write assertions against time.Sleep — use FakeClock.Advance.
- Do not bind real ports in unit tests — use NewApp.
- Do not import production subsystems directly — go through the fixture.
- Do not add a test helper that is more complex than the code under test.
`

// EmitTestingGuide writes the guide to path. Default path is docs/testing.md
// relative to the supplied project root.
func EmitTestingGuide(rootDir string) (string, error) {
	if rootDir == "" {
		return "", fmt.Errorf("ogontest: EmitTestingGuide requires rootDir")
	}
	dir := filepath.Join(rootDir, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "testing.md")
	if err := os.WriteFile(path, []byte(TestingGuide), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

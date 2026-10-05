// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Supply-chain audit hook (SEC-032..035, SEC-072..074). Programmatic
// counterpart to the policy encoded in auth/supply_chain.go.
//
// VerifySupplyChainGate inspects the runtime environment for the four
// non-negotiable posture markers and returns a structured report. `ogon
// verify supply-chain` (the CLI subcommand) renders this report; CI
// fails the build when the report's Pass field is false.

package auth

import (
	"os"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// SupplyChainGateReport is the verdict rendered by VerifySupplyChainGate.
type SupplyChainGateReport struct {
	// GOSUMDBOn: true iff the build environment has GOSUMDB at its default
	// (sum.golang.org) or an explicit non-empty value. GOSUMDB=off is the
	// failure mode — operators who disable it bypass checksum verification.
	GOSUMDBOn bool
	// ModReadonly: true iff GOFLAGS contains -mod=readonly (the default
	// since Go 1.16; -mod=mod is the failure mode — it allows the toolchain
	// to silently rewrite go.mod).
	ModReadonly bool
	// GovulncheckInCI: true iff the supplied CI workflow YAML mentions
	// govulncheck. The audit cannot introspect CI itself, so it relies on
	// the caller passing the workflow file contents (typically read by the
	// CLI from .github/workflows/).
	GovulncheckInCI bool
	// GitleaksInCI: true iff the supplied CI workflow YAML mentions gitleaks.
	GitleaksInCI bool
	// Failures: structured diagnostics for each failed check.
	Failures []diag.Diag
	// Pass: true iff every required check is true.
	Pass bool
}

// VerifySupplyChainGate runs the supply-chain posture check. ciWorkflowYAML
// is the contents of .github/workflows/security.yml (or equivalent); an
// empty string skips the CI-mention checks (the gate then only inspects
// env vars).
func VerifySupplyChainGate(ciWorkflowYAML string) SupplyChainGateReport {
	r := SupplyChainGateReport{}

	// GOSUMDB: default is "sum.golang.org" — present and non-empty when on.
	// Explicit "off" disables verification.
	gosumdb := os.Getenv("GOSUMDB")
	if gosumdb == "" {
		// Go treats unset GOSUMDB as "use the default" which IS sum.golang.org.
		r.GOSUMDBOn = true
	} else if gosumdb == "off" || strings.EqualFold(gosumdb, "off") {
		r.GOSUMDBOn = false
	} else {
		r.GOSUMDBOn = true
	}

	// GOFLAGS: -mod=readonly or -mod=vendor is the safe posture. -mod=mod
	// silently rewrites go.mod. -mod=tidy is for tooling.
	goflags := os.Getenv("GOFLAGS")
	for _, tok := range strings.Fields(goflags) {
		if tok == "-mod=readonly" || tok == "-mod=vendor" {
			r.ModReadonly = true
		}
		if tok == "-mod=mod" {
			// explicit failure posture
			r.ModReadonly = false
		}
	}
	// If GOFLAGS is empty, the Go toolchain default is -mod=readonly
	// (since 1.16). Treat that as PASS.
	if goflags == "" {
		r.ModReadonly = true
	}

	// CI workflow mentions govulncheck/gitleaks?
	if ciWorkflowYAML != "" {
		lower := strings.ToLower(ciWorkflowYAML)
		r.GovulncheckInCI = strings.Contains(lower, "govulncheck")
		r.GitleaksInCI = strings.Contains(lower, "gitleaks")
	}

	// Build the failure list.
	if !r.GOSUMDBOn {
		r.Failures = append(r.Failures, diag.Diag{
			Code:     "OGON-SEC-032",
			Severity: diag.SeverityError,
			Title:    "GOSUMDB is disabled (off)",
			What:     "set GOSUMDB to the default or remove GOSUMDB=off from env",
		})
	}
	if !r.ModReadonly {
		r.Failures = append(r.Failures, diag.Diag{
			Code:     "OGON-SEC-032",
			Severity: diag.SeverityError,
			Title:    "GOFLAGS does not pin -mod=readonly",
			What:     "set GOFLAGS=-mod=readonly so go.mod is not silently rewritten",
		})
	}
	if ciWorkflowYAML != "" {
		if !r.GovulncheckInCI {
			r.Failures = append(r.Failures, diag.Diag{
				Code:     "OGON-SEC-033",
				Severity: diag.SeverityError,
				Title:    "CI workflow does not invoke govulncheck",
				What:     "add a govulncheck step to .github/workflows/security.yml",
			})
		}
		if !r.GitleaksInCI {
			r.Failures = append(r.Failures, diag.Diag{
				Code:     "OGON-SEC-034",
				Severity: diag.SeverityError,
				Title:    "CI workflow does not invoke gitleaks",
				What:     "add a gitleaks step to .github/workflows/security.yml",
			})
		}
	}

	r.Pass = len(r.Failures) == 0
	return r
}

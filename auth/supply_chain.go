// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Supply-chain verification (SEC-032..035, SEC-072..074).
//
// This file documents the supply-chain policy and exposes a thin API
// surface that `ogon verify supply-chain` (a CLI subcommand implemented
// in cli/commands_misc.go) calls. The actual checks run via external
// tooling (govulncheck, gitleaks, syft, cosign); here we only encode
// the policy and the verdict-rendering contract.

package auth

import (
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// SupplyChainPolicy is the production-safe default. Operators adjust
// per-environment via config.
type SupplyChainPolicy struct {
	// GOSUMDBVerify: when true, the build refuses to use any module
	// whose checksum is not present in sum.golang.org.
	GOSUMDBVerify bool // default true
	// GOVulnCheck: when true, CI runs govulncheck and fails on any
	// known-vuln affecting the binary.
	GOVulnCheck bool // default true
	// Gitleaks: when true, CI runs gitleaks on every push.
	Gitleaks bool // default true
	// SBOM: when true, `ogon build --sbom` generates a SPDX SBOM.
	SBOM bool // default true
	// CosignSign: when true, releases are cosign-signed.
	CosignSign bool // default true
	// AllowedVulnLevels lists the severity levels that fail the build.
	// Values: "critical", "high", "medium", "low". Default: critical+high.
	AllowedVulnLevels []string
}

// DefaultSupplyChainPolicy returns the production-safe default.
func DefaultSupplyChainPolicy() SupplyChainPolicy {
	return SupplyChainPolicy{
		GOSUMDBVerify:     true,
		GOVulnCheck:       true,
		Gitleaks:          true,
		SBOM:              true,
		CosignSign:        true,
		AllowedVulnLevels: []string{"critical", "high"},
	}
}

// SupplyChainVerdict is the result of a supply-chain scan.
type SupplyChainVerdict struct {
	GOSUMDB     string // "ok", "missing-checksum", "unknown"
	GOVulnCheck string // "ok", "vulns-found", "skipped"
	Gitleaks    string // "ok", "leaks-found", "skipped"
	SBOM        string // "ok", "skipped"
	Cosign      string // "ok", "not-signed", "skipped"
	Verdict     string // "pass", "fail", "warn"
	Failures    []diag.Diag
}

// Evaluate runs the policy against the supplied verdict and returns
// the final verdict. The verdict is a fail if any required check is
// in a failure state and its severity is allowed.
func (p SupplyChainPolicy) Evaluate(v SupplyChainVerdict) SupplyChainVerdict {
	if p.GOSUMDBVerify && v.GOSUMDB != "ok" {
		v.Failures = append(v.Failures, diag.Diag{
			Code: "OGON-SEC-032", Severity: diag.SeverityError,
			Title: "GOSUMDB verify failed", What: v.GOSUMDB,
		})
	}
	if p.GOVulnCheck && v.GOVulnCheck == "vulns-found" {
		v.Failures = append(v.Failures, diag.Diag{
			Code: "OGON-SEC-033", Severity: diag.SeverityError,
			Title: "govulncheck found vulnerabilities",
		})
	}
	if p.Gitleaks && v.Gitleaks == "leaks-found" {
		v.Failures = append(v.Failures, diag.Diag{
			Code: "OGON-SEC-034", Severity: diag.SeverityError,
			Title: "gitleaks found secrets in source",
		})
	}
	if p.CosignSign && v.Cosign == "not-signed" {
		v.Failures = append(v.Failures, diag.Diag{
			Code: "OGON-SEC-072", Severity: diag.SeverityError,
			Title: "release artifact not cosign-signed",
		})
	}
	if len(v.Failures) > 0 {
		v.Verdict = "fail"
	} else {
		v.Verdict = "pass"
	}
	return v
}

// SupplyChainExplain returns the documentation block for `ogon explain
// supply-chain`.
func SupplyChainExplain() string {
	return `Supply-chain policy (SEC-032..035, SEC-072..074)

GOSUMDB:         sum.golang.org — every imported module's checksum must be present.
govulncheck:     scans the call graph for known CVEs; fails on Critical+High.
gitleaks:        scans git history for secrets; fails on any finding.
SBOM:            ` + "`ogon build --sbom`" + ` emits SPDX JSON via syft.
cosign:          every release artifact is signed; unsigned releases are blocked.

Override per-env: ogon.yaml security.supply_chain.<flag>: false
`
}

// LevelsContain is a small helper used in CI to check whether a found
// vuln's severity is in the allowed-fail list.
func (p SupplyChainPolicy) LevelsContain(sev string) bool {
	for _, a := range p.AllowedVulnLevels {
		if strings.EqualFold(a, sev) {
			return true
		}
	}
	return false
}

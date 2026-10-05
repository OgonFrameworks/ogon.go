// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Supply-chain audit test (SEC-032..035). Verifies VerifySupplyChainGate
// renders the correct verdict under (a) the happy path and (b) each
// individual failure mode.

package auth

import (
	"os"
	"testing"
)

// withEnv sets env vars for the test, restoring on cleanup.
func withEnv(t *testing.T, kvs map[string]string) {
	t.Helper()
	for k, v := range kvs {
		old, hadOld := os.LookupEnv(k)
		_ = os.Setenv(k, v)
		t.Cleanup(func() {
			if hadOld {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
}

func TestSupplyChainGatePassesWhenAllOk(t *testing.T) {
	withEnv(t, map[string]string{
		"GOSUMDB": "",
		"GOFLAGS": "-mod=readonly",
	})
	ci := `name: security
on: [pull_request]
jobs:
  scan:
    steps:
      - run: govulncheck ./...
      - run: gitleaks --repo-path=.
`
	r := VerifySupplyChainGate(ci)
	if !r.Pass {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
	if !r.GOSUMDBOn {
		t.Fatal("GOSUMDB should be on when unset")
	}
	if !r.ModReadonly {
		t.Fatal("GOFLAGS=-mod=readonly should yield ModReadonly=true")
	}
	if !r.GovulncheckInCI {
		t.Fatal("govulncheck should be detected in CI yaml")
	}
	if !r.GitleaksInCI {
		t.Fatal("gitleaks should be detected in CI yaml")
	}
}

func TestSupplyChainGateFailsWhenGOSUMDBOff(t *testing.T) {
	withEnv(t, map[string]string{
		"GOSUMDB": "off",
		"GOFLAGS": "-mod=readonly",
	})
	r := VerifySupplyChainGate("")
	if r.Pass {
		t.Fatal("GOSUMDB=off must fail the gate")
	}
	if r.GOSUMDBOn {
		t.Fatal("GOSUMDB=off should yield GOSUMDBOn=false")
	}
	// Verify the specific failure code is present.
	found := false
	for _, f := range r.Failures {
		if f.Code == "OGON-SEC-032" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected OGON-SEC-032 failure, got %+v", r.Failures)
	}
}

func TestSupplyChainGateFailsWhenModMod(t *testing.T) {
	withEnv(t, map[string]string{
		"GOSUMDB": "",
		"GOFLAGS": "-mod=mod",
	})
	r := VerifySupplyChainGate("")
	if r.Pass {
		t.Fatal("GOFLAGS=-mod=mod must fail the gate")
	}
	if r.ModReadonly {
		t.Fatal("-mod=mod should not count as readonly")
	}
}

func TestSupplyChainGateFailsWhenCIEmptyAndGovulncheckMissing(t *testing.T) {
	withEnv(t, map[string]string{
		"GOSUMDB": "",
		"GOFLAGS": "-mod=readonly",
	})
	// CI yaml that omits both govulncheck and gitleaks.
	ci := `name: security
jobs:
  scan:
    steps:
      - run: echo nope
`
	r := VerifySupplyChainGate(ci)
	if r.Pass {
		t.Fatal("CI without govulncheck/gitleaks must fail the gate")
	}
	if r.GovulncheckInCI {
		t.Fatal("govulncheck should not be detected")
	}
	if r.GitleaksInCI {
		t.Fatal("gitleaks should not be detected")
	}
}

func TestSupplyChainGateAcceptsVendorMode(t *testing.T) {
	withEnv(t, map[string]string{
		"GOSUMDB": "",
		"GOFLAGS": "-mod=vendor",
	})
	r := VerifySupplyChainGate("")
	if !r.ModReadonly {
		t.Fatal("-mod=vendor is also safe (no silent go.mod rewrite)")
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Dockerfile generator tests. Asserts the generated Dockerfile honors the
// INFRA-001..005 contracts and the INFRA-059 hello image budget.

package docker

import (
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// TestGeneratorImplementsInterface verifies Generator satisfies infra.Generator.
func TestGeneratorImplementsInterface(t *testing.T) {
	var _ infra.Generator = Generator{}
}

// TestDockerfileMultiStage verifies INFRA-002 multi-stage shape.
func TestDockerfileMultiStage(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateDockerfile(cfg)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	c := spec.Stamped()
	if !strings.Contains(c, "FROM golang:") {
		t.Error("expected builder stage FROM golang: (INFRA-002 multi-stage)")
	}
	if !strings.Contains(c, "AS builder") {
		t.Error("expected AS builder stage name")
	}
	if !strings.Contains(c, "distroless/static") {
		t.Error("expected distroless/static runtime (INFRA-002)")
	}
}

// TestDockerfileNonRoot verifies INFRA-002 non-root user.
func TestDockerfileNonRoot(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDockerfile(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "nonroot") {
		t.Error("expected distroless :nonroot variant (INFRA-002)")
	}
	if !strings.Contains(c, "USER 65532:65532") {
		t.Error("expected USER 65532:65532 (INFRA-002 non-root)")
	}
}

// TestDockerfileHealthcheck verifies INFRA-003.
func TestDockerfileHealthcheck(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDockerfile(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "HEALTHCHECK") {
		t.Error("expected HEALTHCHECK (INFRA-003)")
	}
	if !strings.Contains(c, cfg.Probes.LivenessPath) {
		t.Errorf("expected healthcheck to use %s", cfg.Probes.LivenessPath)
	}
}

// TestDockerfileBuildArgs verifies INFRA-004 build args (version, fips).
func TestDockerfileBuildArgs(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDockerfile(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "ARG APP_VERSION") {
		t.Error("expected ARG APP_VERSION (INFRA-004)")
	}
	if !strings.Contains(c, "ARG FIPS") {
		t.Error("expected ARG FIPS (INFRA-004)")
	}
	if !strings.Contains(c, "GODEBUG=auth") {
		t.Error("expected GODEBUG=auth env (FIPS-mode runtime marker)")
	}
}

// TestDockerignoreExcludesLock verifies INFRA-005 excludes the lock file.
func TestDockerignoreExcludesLock(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateDockerignore(cfg)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	c := spec.Stamped()
	if !strings.Contains(c, ".ogon-infra.lock") {
		t.Error("expected .ogon-infra.lock excluded (INFRA-005)")
	}
	if !strings.Contains(c, "*_test.go") {
		t.Error("expected *_test.go excluded (image budget INFRA-059)")
	}
}

// TestGeneratorPlan verifies stable-order plan (INFRA-039).
func TestGeneratorPlan(t *testing.T) {
	cfg := infra.DefaultConfig()
	plan := Generator{}.Plan(cfg)
	if len(plan) != 2 {
		t.Fatalf("plan len = %d, want 2", len(plan))
	}
	if plan[0] != "Dockerfile" || plan[1] != ".dockerignore" {
		t.Errorf("plan = %v, want [Dockerfile .dockerignore]", plan)
	}
}

// TestGeneratorGenerateIdempotent verifies Generate is deterministic.
func TestGeneratorGenerateIdempotent(t *testing.T) {
	cfg := infra.DefaultConfig()
	a, _ := Generator{}.Generate(cfg)
	b, _ := Generator{}.Generate(cfg)
	if len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Stamped() != b[i].Stamped() {
			t.Errorf("spec[%d] not idempotent (INFRA-039)", i)
		}
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// compose generator tests. Asserts the dev stack honors INFRA-006/007/055.

package compose

import (
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// TestGeneratorImplementsInterface verifies Generator satisfies infra.Generator.
func TestGeneratorImplementsInterface(t *testing.T) {
	var _ infra.Generator = Generator{}
}

// TestComposeDevStack verifies INFRA-006: app + postgres + redis + mailpit.
func TestComposeDevStack(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateCompose(cfg)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	c := spec.Stamped()
	for _, want := range []string{"app:", "postgres:", "redis:", "mailpit:"} {
		if !strings.Contains(c, want) {
			t.Errorf("expected %q in compose stack (INFRA-006)", want)
		}
	}
}

// TestComposeHealthchecks verifies INFRA-007: every service has a healthcheck.
func TestComposeHealthchecks(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateCompose(cfg)
	c := spec.Stamped()
	// Each service has healthcheck: line.
	count := strings.Count(c, "healthcheck:")
	if count < 4 {
		t.Errorf("expected >=4 healthcheck blocks (app/pg/redis/mailpit), got %d (INFRA-007)", count)
	}
}

// TestComposeDependsOn verifies INFRA-007: app depends_on postgres + redis.
func TestComposeDependsOn(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateCompose(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "depends_on:") {
		t.Error("expected depends_on (INFRA-007)")
	}
	if !strings.Contains(c, "condition: service_healthy") {
		t.Error("expected condition: service_healthy (INFRA-007)")
	}
}

// TestComposePreviewProfile verifies INFRA-055 PR preview env.
func TestComposePreviewProfile(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateCompose(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "profiles:") {
		t.Error("expected profiles: section (INFRA-055)")
	}
	if !strings.Contains(c, cfg.Compose.PreviewProfile) {
		t.Errorf("expected preview profile %q (INFRA-055)", cfg.Compose.PreviewProfile)
	}
}

// TestComposeIdempotent verifies INFRA-039.
func TestComposeIdempotent(t *testing.T) {
	cfg := infra.DefaultConfig()
	a, _ := GenerateCompose(cfg)
	b, _ := GenerateCompose(cfg)
	if a.Stamped() != b.Stamped() {
		t.Fatal("compose not idempotent (INFRA-039)")
	}
}

// TestComposeAdminerOpt verifies the adminer opt is gated by cfg.Compose.Adminer.
func TestComposeAdminerOpt(t *testing.T) {
	cfg := infra.DefaultConfig()
	cfg.Compose.Adminer = true
	spec, _ := GenerateCompose(cfg)
	if !strings.Contains(spec.Stamped(), "adminer:") {
		t.Error("expected adminer service when Compose.Adminer=true")
	}
	cfg.Compose.Adminer = false
	spec2, _ := GenerateCompose(cfg)
	// With Adminer=false the template skips the adminer block; verify there
	// are no "adminer:" service entries (the literal string "adminer:" appears
	// only inside the conditional).
	if strings.Contains(spec2.Stamped(), "container_name: "+cfg.App.Name+"-adminer") {
		t.Error("did not expect adminer when Compose.Adminer=false")
	}
}

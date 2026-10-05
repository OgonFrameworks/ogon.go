// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Terraform AWS generator tests. Asserts INFRA-020/063/064/050.

package terraform

import (
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// TestGeneratorImplementsInterface verifies Generator satisfies infra.Generator.
func TestGeneratorImplementsInterface(t *testing.T) {
	var _ infra.Generator = Generator{}
}

// TestAWSMainContainsAllResources verifies INFRA-020 ships every resource.
func TestAWSMainContainsAllResources(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateAWSMain(cfg)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	c := spec.Stamped()
	for _, want := range []string{
		`resource "aws_vpc" "main"`,
		`resource "aws_ecs_cluster" "main"`,
		`resource "aws_ecs_task_definition" "app"`,
		`resource "aws_ecs_service" "app"`,
		`resource "aws_db_instance" "main"`,
		`resource "aws_elasticache_cluster" "main"`,
		`resource "aws_lb" "main"`,
		`resource "aws_lb_target_group" "app"`,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("expected %q in main.tf (INFRA-020)", want)
		}
	}
}

// TestAWSMainPrivateByDefault verifies INFRA-064.
func TestAWSMainPrivateByDefault(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateAWSMain(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "publicly_accessible    = false") {
		t.Error("expected publicly_accessible=false on RDS (INFRA-064)")
	}
	if !strings.Contains(c, "assign_public_ip = false") {
		t.Error("expected assign_public_ip=false on Fargate (INFRA-064)")
	}
	if !strings.Contains(c, "Private route tables have NO default route to the IGW") {
		t.Error("expected private route table comment (INFRA-064)")
	}
}

// TestAWSMainIAMDBAuth verifies INFRA-063 (opt-in).
func TestAWSMainIAMDBAuth(t *testing.T) {
	// Disabled by default.
	cfg := infra.DefaultConfig()
	spec, _ := GenerateAWSMain(cfg)
	if !strings.Contains(spec.Stamped(), "iam_database_authentication_enabled = var.iam_db_auth") {
		t.Error("expected iam_database_authentication_enabled var (INFRA-063)")
	}
	// When enabled, the policy block is present.
	cfg.Terraform.IAMDBAuth = true
	spec, _ = GenerateAWSMain(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "rds-db:connect") {
		t.Error("expected rds-db:connect IAM policy when IAMDBAuth=true (INFRA-063)")
	}
}

// TestAWSMainMultiRegion verifies INFRA-050.
func TestAWSMainMultiRegion(t *testing.T) {
	cfg := infra.DefaultConfig()
	cfg.Terraform.MultiRegion = true
	spec, _ := GenerateAWSMain(cfg)
	c := spec.Stamped()
	// The replica provider alias lives in providers.tf; verify the replica
	// RDS instance is generated in main.tf.
	if !strings.Contains(c, `resource "aws_db_instance" "replica"`) {
		t.Error("expected replica RDS instance (INFRA-050)")
	}
	// Provider alias is in providers.tf.
	specProv, _ := GenerateAWSProviders(cfg)
	cProv := specProv.Stamped()
	if !strings.Contains(cProv, `provider "aws"`) || !strings.Contains(cProv, `alias  = "replica"`) {
		t.Error("expected replica provider alias in providers.tf (INFRA-050)")
	}
}

// TestAWSVariables verifies variables.tf has the canonical set.
func TestAWSVariables(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateAWSVariables(cfg)
	c := spec.Stamped()
	for _, want := range []string{"aws_region", "app_name", "image_repo", "db_name", "multi_region", "iam_db_auth", "vpc_private"} {
		if !strings.Contains(c, `variable "`+want+`"`) {
			t.Errorf("expected variable %q in variables.tf", want)
		}
	}
}

// TestAWSOutputs verifies outputs.tf.
func TestAWSOutputs(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateAWSOutputs(cfg)
	c := spec.Stamped()
	for _, want := range []string{"vpc_id", "alb_dns_name", "ecs_cluster_arn", "rds_endpoint", "redis_endpoint"} {
		if !strings.Contains(c, `output "`+want+`"`) {
			t.Errorf("expected output %q (INFRA-020)", want)
		}
	}
}

// TestAWSProviders verifies the providers.tf has aws + multi-region alias.
func TestAWSProviders(t *testing.T) {
	cfg := infra.DefaultConfig()
	cfg.Terraform.MultiRegion = true
	spec, _ := GenerateAWSProviders(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, `source = "hashicorp/aws"`) {
		t.Error("expected aws provider")
	}
	if !strings.Contains(c, `alias  = "replica"`) {
		t.Error("expected replica provider alias when multi-region (INFRA-050)")
	}
}

// TestTFValidateCI verifies INFRA-041.
func TestTFValidateCI(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateTFValidateCI(cfg)
	c := spec.Stamped()
	for _, want := range []string{"terraform fmt", "terraform init", "terraform validate"} {
		if !strings.Contains(c, want) {
			t.Errorf("expected %q in tf CI snippet (INFRA-041)", want)
		}
	}
}

// TestQuotaDoc verifies INFRA-054.
func TestQuotaDoc(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateQuotaDoc(cfg)
	c := spec.Stamped()
	for _, want := range []string{"VPCs per region", "RDS instances per region", "ElastiCache nodes per cluster", "Cloud Run services"} {
		if !strings.Contains(c, want) {
			t.Errorf("expected %q in quota doc (INFRA-054)", want)
		}
	}
}

// TestGeneratorPlan verifies stable-order plan (INFRA-039).
func TestGeneratorPlan(t *testing.T) {
	cfg := infra.DefaultConfig()
	plan := Generator{}.Plan(cfg)
	if len(plan) != 4 {
		t.Errorf("plan len = %d, want 4", len(plan))
	}
	for i := 1; i < len(plan); i++ {
		if plan[i-1] > plan[i] {
			t.Errorf("plan not sorted (INFRA-039): %q before %q", plan[i-1], plan[i])
		}
	}
}

// TestAWSMainIdempotent verifies INFRA-039.
func TestAWSMainIdempotent(t *testing.T) {
	cfg := infra.DefaultConfig()
	a, _ := GenerateAWSMain(cfg)
	b, _ := GenerateAWSMain(cfg)
	if a.Stamped() != b.Stamped() {
		t.Fatal("terraform main not idempotent (INFRA-039)")
	}
}

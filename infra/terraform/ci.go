// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Terraform CI helpers (INFRA-041/054). Emits a partial ci.yml snippet
// (terraform fmt/validate in CI) and a quota/limits doc.

package terraform

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateTFValidateCI emits a partial ci.yml step block for tf fmt/validate.
// The full ci.yml is owned by infra/actions; this returns a reusable snippet
// for per-cloud jobs.
func GenerateTFValidateCI(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("tfci").Parse(tfCITemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("tf ci template: %w", err)
	}
	return infra.FileSpec{
		Path: ".github/workflows/_terraform-validate.yml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Reusable terraform fmt + validate job (INFRA-041)",
	}, nil
}

const tfCITemplate = `# Reusable workflow snippet: terraform fmt/validate (INFRA-041).
# Include via workflow_call or copy-paste into a per-cloud job.
on:
  workflow_call:
jobs:
  terraform-validate:
    runs-on: {{.GitHub.Runner}}
    defaults:
      run:
        working-directory: infra/terraform/aws
    steps:
      - uses: actions/checkout@v4
      - uses: hashicorp/setup-terraform@v3
        with:
          terraform_version: "1.7.0"
          terraform_wrapper: false
      - name: terraform fmt
        run: terraform fmt -check -recursive -diff
      - name: terraform init
        run: terraform init -backend=false
      - name: terraform validate
        run: terraform validate -no-color
      - name: terraform plan (dry-run, no credentials)
        run: terraform plan -input=false -no-color || true
`

// GenerateQuotaDoc emits a per-provider quota/limits reference (INFRA-054).
func GenerateQuotaDoc(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	const doc = `# Provider Quotas and Limits (INFRA-054)

A consolidated reference for the default-service limits that bite first when
scaling up. Numbers are conservative defaults — verify against the cloud
provider's current documentation at deploy time.

## AWS

| Resource | Default limit | Where to request |
|---|---|---|
| VPCs per region | 5 | Service Quotas |
| Elastic IPs per region | 5 | Service Quotas |
| RDS instances per region | 40 (soft) | Service Quotas |
| RDS storage (gp3) |  | Account-level |
| ElastiCache nodes per cluster | 1–500 | Service Quotas |
| ALBs per region | 50 | Service Quotas |
| ECS Fargate tasks (On-Demand) | varies by family | Service Quotas |
| IAM roles | 1000 | IAM limits |
| S3 buckets | 100 (soft) | Account-level |
| NAT Gateways per AZ | 5 | Service Quotas |
| ECR repositories | unlimited | n/a |
| ACM certificates | 2500 | Account-level |

## GCP

| Resource | Default limit |
|---|---|
| VPC networks | 15 |
| Cloud SQL instances | 100 |
| GKE clusters per region | 8 (soft) |
| Cloud Run services | 1000 |
| Memorystore nodes | 50 |
| External IPs | varies |

## Azure

| Resource | Default limit |
|---|---|
| Resource Groups per subscription | 980 |
| AKS clusters per subscription | 250 |
| Postgres servers | 200 |
| Public IPs | 10 (default, soft) |
| Load balancers | 1000 |

## Notes

- Quota increases are account-scoped; share the link to Service Quotas in the
  project runbook (INFRA-047..049).
- The infra generator (INFRA-001) defaults to small shapes
  (db.t4g.micro, cache.t4g.micro, 256m/512m task) precisely so the first
  deploy is below all soft limits.
`
	return infra.FileSpec{
		Path: "infra/terraform/QUOTAS.md", Content: doc,
		Marker: infra.MarkSeeded, CommentPrefix: "#",
		Description: "Per-provider quota/limits reference (INFRA-054)",
	}, nil
}

var _ = strings.TrimSpace

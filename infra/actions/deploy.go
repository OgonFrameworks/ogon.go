// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// GitHub Actions deploy.yml per-cloud generator (INFRA-034). Emits a
// per-cloud deploy workflow that runs the registry push, k8s apply (or
// ECS Fargate update), and rollout poll (ogon deploy status — INFRA-068).

package actions

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateDeployWorkflow emits deploy.yml per-cloud. Cloud is selected from
// cfg.GitHub.Cloud. Unknown clouds produce a generic stub.
func GenerateDeployWorkflow(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("deploy").Funcs(template.FuncMap{
		"is": func(a, b string) bool { return a == b },
	}).Parse(deployTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("deploy template: %w", err)
	}
	return infra.FileSpec{
		Path: ".github/workflows/deploy.yml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "GitHub Actions deploy.yml per-cloud (INFRA-034): apply + rollout poll (INFRA-068)",
	}, nil
}

const deployTemplate = `name: deploy
on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      rollback:
        description: 'Roll back to last revision (INFRA-069)'
        required: false
        default: 'false'
        type: choice
        options: ['false', 'true']
permissions:
  contents: read
  id-token: write   # OIDC for cloud role assumption
concurrency:
  group: deploy-${{ github.ref }}
  cancel-in-progress: false
jobs:
  deploy:
    runs-on: {{.GitHub.Runner}}
    environment:
      name: production
    steps:
      - uses: actions/checkout@v4
{{if is .GitHub.Cloud "aws"}}
      # ----- AWS: assume role via OIDC, update ECS service, poll rollout
      - name: Configure AWS credentials (OIDC)
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::{{.Terraform.AccountID}}:role/ogon-deploy
          aws-region: {{.Terraform.Region}}
      - name: Install ogon
        run: go install ./cmd/ogon
      - name: terraform apply (infra/terraform/aws)
        run: |
          cd infra/terraform/aws
          terraform init -input=false
          terraform apply -input=false -auto-approve
      - name: ECS deploy (update service with new image)
        run: |
          aws ecs update-service \
            --cluster {{.App.Name}} \
            --service {{.App.Name}} \
            --force-new-deployment
      - name: ogon deploy status (rollout poll — INFRA-068)
        run: ogon deploy status --timeout 5m
      - name: ogon deploy --rollback (if requested — INFRA-069)
        if: github.event.inputs.rollback == 'true'
        run: ogon deploy --rollback
{{else if is .GitHub.Cloud "gcp"}}
      # ----- GCP: WIF auth, deploy to Cloud Run, poll
      - uses: google-github-actions/auth@v2
        with:
          workload_identity_provider: projects/000/locations/global/workloadIdentityPools/ogon/providers/gh
          service_account: deploy@{{.Terraform.AccountID}}.iam.gserviceaccount.com
      - uses: google-github-actions/setup-gcloud@v2
      - name: deploy to Cloud Run
        run: |
          gcloud run deploy {{.App.Name}} \
            --image {{.Registry.Host}}/{{.Registry.Account}}/{{.App.Name}}:sha-${{ github.sha }} \
            --region {{.Terraform.Region}} \
            --platform managed
      - name: ogon deploy status
        run: ogon deploy status --timeout 5m
{{else if is .GitHub.Cloud "azure"}}
      # ----- Azure: federated auth, deploy to AKS
      - uses: azure/login@v2
        with:
          client-id: ${{ secrets.AZURE_CLIENT_ID }}
          tenant-id: ${{ secrets.AZURE_TENANT_ID }}
          subscription-id: ${{ secrets.AZURE_SUBSCRIPTION_ID }}
      - uses: azure/setup-kubectl@v4
      - name: apply k8s manifests
        run: kubectl apply -f deploy/k8s/
      - name: rollout status (INFRA-068)
        run: kubectl rollout status deployment/{{.App.Name}} -n {{.K8s.Namespace}} --timeout=5m
      - name: rollback (INFRA-069)
        if: github.event.inputs.rollback == 'true'
        run: kubectl rollout undo deployment/{{.App.Name}} -n {{.K8s.Namespace}}
{{else}}
      # ----- generic / unknown cloud: render a stub
      - name: deploy (stub)
        run: |
          echo "::notice::no deploy target for cloud={{.GitHub.Cloud}}"
          echo "configure cfg.GitHub.Cloud to one of: aws|gcp|azure"
{{end}}
`

var _ = strings.TrimSpace

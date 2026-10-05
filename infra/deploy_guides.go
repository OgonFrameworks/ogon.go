// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Per-provider deploy guides (INFRA-070). One markdown per cloud; each
// walks the deploy from zero to a running, observable, signed image.

package infra

import (
	"fmt"
	"strings"
	"text/template"
)

// GenerateDeployGuide emits a per-provider deploy guide. provider is one of:
// aws, gcp, azure, k8s-generic. Unknown providers return an error.
func GenerateDeployGuide(cfg *InfraConfig, provider string) (FileSpec, error) {
	tpl, ok := deployGuideTemplates[provider]
	if !ok {
		return FileSpec{}, fmt.Errorf("infra: no deploy guide for provider %q (INFRA-070)", provider)
	}
	t := template.Must(template.New("guide").Parse(tpl))
	var b strings.Builder
	if err := t.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("deploy guide template: %w", err)
	}
	return FileSpec{
		Path:    fmt.Sprintf("infra/runbooks/deploy-%s.md", provider),
		Content: b.String(),
		Marker:  MarkSeeded, CommentPrefix: "<!--",
		Description: fmt.Sprintf("Deploy guide for %s (INFRA-070)", provider),
	}, nil
}

var deployGuideTemplates = map[string]string{
	"aws": `# AWS Deploy Guide — {{.App.Name}} (INFRA-070)

> Seeded by ogon infra gen. Edit by hand. --force required to regenerate.

## Prerequisites

- AWS account with billing enabled.
- ` + "`aws`" + ` CLI configured (` + "`aws configure`" + `) or GitHub OIDC role.
- ` + "`terraform`" + ` >= 1.7 installed.
- ` + "`docker`" + ` installed for image builds.

## Steps

1. **Generate infra artifacts** (if not already done):
   ` + "```" + `
   ogon infra gen terraform --cloud aws
   ogon infra gen k8s
   ogon infra gen actions
   ` + "```" + `

2. **Apply Terraform** (creates VPC, ECS, RDS, ElastiCache, ALB, IAM):
   ` + "```" + `
   cd infra/terraform/aws
   terraform init
   terraform plan -out=tfplan
   terraform apply tfplan
   ` + "```" + `

3. **Create the ECR repository** (one-time):
   ` + "```" + `
   aws ecr create-repository --repository-name {{.App.Name}} --region {{.Terraform.Region}}
   ` + "```" + `

4. **Build and push the image**:
   ` + "```" + `
   ogon build --tag $(git rev-parse --short HEAD)
   infra/scripts/push.sh
   ` + "```" + `

5. **Deploy the service**:
   ` + "```" + `
   aws ecs update-service --cluster {{.App.Name}} --service {{.App.Name}} --force-new-deployment
   ogon deploy status --timeout 5m
   ` + "```" + `

6. **Verify**:
   ` + "```" + `
   aws elbv2 describe-load-balancers --names {{.App.Name}}
   curl -s https://app.example.com/healthz
   ` + "```" + `

7. **Roll back if needed** (INFRA-069):
   ` + "```" + `
   aws ecs describe-services --cluster {{.App.Name}} --services {{.App.Name}} | jq '.services[0].deployments'
   aws ecs update-service --cluster {{.App.Name}} --service {{.App.Name}} --task-definition <OLD_ARN>
   ` + "```" + `
`,

	"gcp": `# GCP Deploy Guide — {{.App.Name}} (INFRA-070)

> Seeded by ogon infra gen. Edit by hand. --force required to regenerate.

## Prerequisites

- GCP project with billing enabled.
- ` + "`gcloud`" + ` CLI authenticated.
- ` + "`docker`" + ` installed.
- Workload Identity Federation configured for CI (recommended over SA keys).

## Steps

1. **Generate infra artifacts**:
   ` + "```" + `
   ogon infra gen terraform --cloud gcp
   ogon infra gen actions
   ` + "```" + `

2. **Create the Artifact Registry repo** (one-time):
   ` + "```" + `
   gcloud artifacts repositories create {{.App.Name}} \
     --repository-format=docker --location={{.Terraform.Region}}
   ` + "```" + `

3. **Build and push the image**:
   ` + "```" + `
   ogon build --tag $(git rev-parse --short HEAD)
   infra/scripts/push.sh
   ` + "```" + `

4. **Deploy to Cloud Run**:
   ` + "```" + `
   gcloud run deploy {{.App.Name}} \
     --image {{.Registry.Host}}/{{.Registry.Account}}/{{.App.Name}}:latest \
     --region {{.Terraform.Region}} \
     --platform managed \
     --min-instances 2 \
     --max-instances 10 \
     --set-env-vars APP_ENV=prod,HTTP_PORT=8080 \
     --set-secrets DATABASE_URL={{.App.Name}}-db-url:latest
   ` + "```" + `

5. **Verify**:
   ` + "```" + `
   gcloud run services describe {{.App.Name}} --region {{.Terraform.Region}}
   curl -s https://{{.App.Name}}.run.app/healthz
   ` + "```" + `
`,

	"azure": `# Azure Deploy Guide — {{.App.Name}} (INFRA-070)

> Seeded by ogon infra gen. Edit by hand. --force required to regenerate.

## Prerequisites

- Azure subscription.
- ` + "`az`" + ` CLI authenticated.
- ` + "`docker`" + ` installed.
- Federated identity credentials configured for GitHub Actions.

## Steps

1. **Generate infra artifacts**:
   ` + "```" + `
   ogon infra gen terraform --cloud azure
   ogon infra gen k8s
   ogon infra gen actions
   ` + "```" + `

2. **Create the AKS cluster** (one-time, via Terraform):
   ` + "```" + `
   cd infra/terraform/azure
   terraform init && terraform apply
   az aks get-credentials --resource-group {{.App.Name}} --name {{.App.Name}}
   ` + "```" + `

3. **Build and push the image** to Azure Container Registry:
   ` + "```" + `
   az acr create --resource-group {{.App.Name}} --name {{.App.Name}} --sku Basic
   ogon build --tag $(git rev-parse --short HEAD)
   infra/scripts/push.sh
   ` + "```" + `

4. **Apply k8s manifests**:
   ` + "```" + `
   kubectl apply -f deploy/k8s/
   kubectl rollout status deployment/{{.App.Name}} -n {{.K8s.Namespace}} --timeout=5m
   ` + "```" + `

5. **Verify**:
   ` + "```" + `
   kubectl get ingress -n {{.K8s.Namespace}}
   curl -s https://{{.K8s.IngressHost}}/healthz
   ` + "```" + `
`,

	"k8s-generic": `# Generic Kubernetes Deploy Guide — {{.App.Name}} (INFRA-070)

> Seeded by ogon infra gen. Edit by hand. --force required to regenerate.

For clusters that are not tied to a specific cloud (self-hosted, bare-metal,
or managed k8s without cloud-specific providers).

## Prerequisites

- ` + "`kubectl`" + ` configured to talk to your cluster.
- A container registry reachable from the cluster.
- An Ingress controller (nginx, traefik, etc.).
- cert-manager + an Issuer (letsencrypt-prod is the default).

## Steps

1. **Generate infra artifacts**:
   ` + "```" + `
   ogon infra gen k8s
   ogon infra gen docker
   ogon infra gen actions
   ` + "```" + `

2. **Build and push the image** to your registry of choice.

3. **Apply the manifests**:
   ` + "```" + `
   kubectl apply -f deploy/k8s/namespace.yaml
   kubectl apply -f deploy/k8s/configmap.yaml
   kubectl apply -f deploy/k8s/secret.yaml    # hydrate values first!
   kubectl apply -f deploy/k8s/migration-job.yaml
   kubectl apply -f deploy/k8s/deployment.yaml
   kubectl apply -f deploy/k8s/service.yaml
   kubectl apply -f deploy/k8s/ingress.yaml
   kubectl apply -f deploy/k8s/hpa.yaml
   kubectl apply -f deploy/k8s/pdb.yaml
   kubectl apply -f deploy/k8s/network-policy.yaml
   ` + "```" + `

4. **Poll the rollout** (INFRA-068):
   ` + "```" + `
   kubectl rollout status deployment/{{.App.Name}} -n {{.K8s.Namespace}} --timeout=5m
   ` + "```" + `

5. **Verify**:
   ` + "```" + `
   curl -s https://{{.K8s.IngressHost}}/healthz
   ` + "```" + `

6. **Roll back if needed** (INFRA-069):
   ` + "```" + `
   kubectl rollout undo deployment/{{.App.Name}} -n {{.K8s.Namespace}}
   ` + "```" + `)
`,
}

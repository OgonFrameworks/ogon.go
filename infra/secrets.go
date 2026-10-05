// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Secrets mapping (INFRA-027). Translates ogon.yaml secret references into
// k8s Secret keys + env var bindings. The mapping table is itself a
// committed artifact so it is auditable and diffable.

package infra

import (
	"fmt"
	"strings"
	"text/template"
)

// GenerateSecretsManifest emits a manifest that documents the yaml→k8s
// secrets mapping. The k8s/k8s Secret manifest itself is emitted by
// k8s/configmap.go; this file is the documentation + audit surface.
func GenerateSecretsManifest(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("secrets").Parse(secretsTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("secrets template: %w", err)
	}
	return FileSpec{
		Path: "infra/secrets.yaml", Content: b.String(),
		Marker: MarkOwned, CommentPrefix: "#",
		Description: "Secrets mapping: ogon.yaml → k8s Secret keys + env vars (INFRA-027)",
	}, nil
}

const secretsTemplate = `# Secret mapping (INFRA-027). This file is the source of truth for which
# ogon.yaml secret keys map to which k8s Secret entries and container env
# vars. The k8s Secret manifest (deploy/k8s/secret.yaml) is generated from
# this mapping; values are hydrated by the deploy pipeline from the
# configured secrets backend (Vault, AWS SSM, GCP Secret Manager, Azure KV).
#
# Convention:
#   ogon.yaml key      → k8s Secret key      → container env var
#   database.url       → DATABASE_URL        → DATABASE_URL
#   cache.url          → REDIS_URL           → REDIS_URL
#   mail.smtp_url      → SMTP_URL            → SMTP_URL
#   auth.oidc.secret   → OIDC_CLIENT_SECRET  → OIDC_CLIENT_SECRET
#   auth.session.key   → SESSION_SECRET      → SESSION_SECRET
#
# NEVER commit real secret material. This manifest carries only keys.
secrets:
{{range .Secrets}}
  - ogon_yaml: {{.Name}}
    secret: {{.Secret}}
    key: {{.Key}}
    env_var: {{.EnvVar}}
{{else}}
  - ogon_yaml: database.url
    secret: app-secret
    key: DATABASE_URL
    env_var: DATABASE_URL
  - ogon_yaml: cache.url
    secret: app-secret
    key: REDIS_URL
    env_var: REDIS_URL
  - ogon_yaml: mail.smtp_url
    secret: app-secret
    key: SMTP_URL
    env_var: SMTP_URL
  - ogon_yaml: auth.oidc.secret
    secret: app-secret
    key: OIDC_CLIENT_SECRET
    env_var: OIDC_CLIENT_SECRET
  - ogon_yaml: auth.session.key
    secret: app-secret
    key: SESSION_SECRET
    env_var: SESSION_SECRET
{{end}}
backends:
  - provider: vault
    path: secret/{{.App.Name}}
    note: "HashiCorp Vault. Configure via VAULT_ADDR + VAULT_TOKEN/JWT."
  - provider: aws_secretsmanager
    path: {{.App.Name}}/{{.Env}}
    note: "AWS Secrets Manager. Use IAM roles for service account (IRSA)."
  - provider: gcp_secret_manager
    path: projects/{{.Terraform.AccountID}}/secrets/{{.App.Name}}
    note: "GCP Secret Manager. Use Workload Identity."
  - provider: azure_keyvault
    path: https://{{.App.Name}}.vault.azure.net/
    note: "Azure Key Vault. Use federated identity credentials."
rotation:
  cadence: 90d
  on_rotate: "regenerate Secret manifest via 'ogon infra gen k8s --force'"
`

var _ = strings.TrimSpace

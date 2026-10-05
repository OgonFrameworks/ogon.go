// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s ConfigMap + Secret refs from ogon.yaml (INFRA-014/027). The ConfigMap
// holds non-secret config (env-tiered); the Secret holds secret keys mapped
// from ogon.yaml. See infra/secrets.go for the mapping table.

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateConfigMap emits the app ConfigMap (INFRA-014).
func GenerateConfigMap(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("cm").Parse(cmTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("cm template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/configmap.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "ConfigMap from ogon.yaml (INFRA-014); node affinity (INFRA-066) + spot tolerations (INFRA-067) are in deployment.yaml",
	}, nil
}

const cmTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.App.Name}}-config
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
data:
  APP_NAME: {{.App.Name}}
  APP_ENV: {{.Env}}
  APP_VERSION: {{.App.Version}}
  HTTP_PORT: "{{.App.Port}}"
  LOG_FORMAT: json
  LOG_LEVEL: info
  DB_ENGINE: {{.Database.Engine}}
  DB_NAME: {{.Database.Name}}
  CACHE_ENGINE: {{.Cache.Engine}}
  MAIL_ENGINE: {{.Mail.Engine}}
`

// GenerateSecret emits an empty/placeholder Secret manifest. The keys are
// mapped from ogon.yaml per INFRA-027 (see infra/secrets.go). Values are
// deliberately left as ${SECRET_KEY} placeholders — the deploy pipeline
// hydrates them from the configured secrets backend (Vault, AWS SSM, etc.).
func GenerateSecret(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("secret").Parse(secretTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("secret template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/secret.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Secret manifest with key placeholders (INFRA-027); values hydrated by deploy pipeline",
	}, nil
}

const secretTemplate = `# INFRA-027: secret keys mapped from ogon.yaml. Placeholders below are
# hydrated at deploy time from the configured secrets backend. Never commit
# real secret material to git.
apiVersion: v1
kind: Secret
metadata:
  name: {{.App.Name}}-secret
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
type: Opaque
stringData:
  DATABASE_URL: ${DATABASE_URL}
  REDIS_URL: ${REDIS_URL}
  SMTP_URL: ${SMTP_URL}
  OIDC_CLIENT_SECRET: ${OIDC_CLIENT_SECRET}
  SESSION_SECRET: ${SESSION_SECRET}
`

var _ = strings.TrimSpace

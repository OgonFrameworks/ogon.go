// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s NetworkPolicy generator (INFRA-013/064/065). Default-deny ingress;
// egress scoped to db (postgres) + redis (cache) + DNS + HTTPS egress.

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateNetworkPolicy emits the default-deny ingress + scoped egress
// NetworkPolicy. When cfg.K8s.NetworkPolicy == "none", emits a no-op stub.
func GenerateNetworkPolicy(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	if cfg.K8s.NetworkPolicy == "none" {
		return infra.FileSpec{
			Path:    "deploy/k8s/network-policy.yaml",
			Content: "# network-policy disabled by config (cfg.K8s.NetworkPolicy == \"none\")\n",
			Marker:  infra.MarkOwned, CommentPrefix: "#",
			Description: "NetworkPolicy disabled (INFRA-013 opt-out)",
		}, nil
	}
	tpl := template.Must(template.New("np").Parse(npTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("np template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/network-policy.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "NetworkPolicy default-deny ingress (INFRA-065) + scoped egress to db/redis (INFRA-013/064)",
	}, nil
}

const npTemplate = `# INFRA-065: default-deny ingress. No traffic reaches these pods unless
# explicitly allowed by another policy below.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.App.Name}}-default-deny-ingress
  namespace: {{.K8s.Namespace}}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: {{.App.Name}}
  policyTypes:
    - Ingress
  ingress: []
---
# INFRA-013/064: scoped egress. Allow only DNS, the database (postgres),
# the cache (redis), and HTTPS to the public internet (for OIDC/webhook
# callbacks). All other egress is denied.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.App.Name}}-egress-scoped
  namespace: {{.K8s.Namespace}}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: {{.App.Name}}
  policyTypes:
    - Egress
  egress:
    # DNS (kube-dns in kube-system).
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
    # Postgres (typically a Service "postgres" or RDS via ExternalName).
    - to:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: postgres
      ports:
        - protocol: TCP
          port: 5432
    # Redis.
    - to:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: redis
      ports:
        - protocol: TCP
          port: 6379
    # HTTPS egress (OIDC, webhooks, container registry).
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: 443
    # Sidecar OTLP exporter (obs stack).
    - to:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: otel-collector
      ports:
        - protocol: TCP
          port: 4317
`

var _ = strings.TrimSpace

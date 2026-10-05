// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s Ingress generator (INFRA-010). Emits an Ingress with cert-manager TLS
// sample (INFRA-045) and external-dns annotations sample (INFRA-046).

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateIngress emits the Ingress with cert-manager TLS + external-dns.
func GenerateIngress(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("ingress").Parse(ingressTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("ingress template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/ingress.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Ingress with cert-manager TLS (INFRA-045) + external-dns (INFRA-046)",
	}, nil
}

const ingressTemplate = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{.App.Name}}
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
  annotations:
    {{if .K8s.TLSEnabled}}
    # cert-manager TLS sample (INFRA-045).
    cert-manager.io/cluster-issuer: {{.K8s.CertIssuer}}
    cert-manager.io/acme-http01-ingress-class: nginx
    {{end}}
    {{if .K8s.ExternalDNS}}
    # external-dns sample (INFRA-046): creates the DNS record on merge.
    external-dns.alpha.kubernetes.io/hostname: {{.K8s.IngressHost}}
    external-dns.alpha.kubernetes.io/ttl: "60"
    {{end}}
    nginx.ingress.kubernetes.io/proxy-body-size: "10m"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  ingressClassName: nginx
  {{if .K8s.TLSEnabled}}
  tls:
    - hosts:
        - {{.K8s.IngressHost}}
      secretName: {{.App.Name}}-tls
  {{end}}
  rules:
    - host: {{.K8s.IngressHost}}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{.App.Name}}
                port:
                  name: http
`

var _ = strings.TrimSpace

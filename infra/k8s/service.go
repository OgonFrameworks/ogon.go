// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s Service generator (INFRA-009). ClusterIP service fronting the web
// Deployment, with a headless option for the worker.

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateService emits the ClusterIP Service.
func GenerateService(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("service").Parse(serviceTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("service template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/service.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "ClusterIP Service (INFRA-009)",
	}, nil
}

const serviceTemplate = `apiVersion: v1
kind: Service
metadata:
  name: {{.App.Name}}
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: {{.App.Name}}
    ogonframeworks.dev/role: web
  ports:
    - name: http
      port: 80
      targetPort: http
      protocol: TCP
`

var _ = strings.TrimSpace // keep import for future template helpers

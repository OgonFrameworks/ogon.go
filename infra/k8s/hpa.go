// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s HPA (cpu+mem, INFRA-011) with autoscale-on-p95 policy (INFRA-044),
// and PDB (INFRA-012).

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateHPA emits an HPA targeting cpu+mem utilization, with a p95-based
// autoscale policy comment (INFRA-044).
func GenerateHPA(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("hpa").Parse(hpaTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("hpa template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/hpa.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "HPA (cpu+mem, INFRA-011); autoscale-on-p95 policy (INFRA-044)",
	}, nil
}

const hpaTemplate = `apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{.App.Name}}
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
  annotations:
    # INFRA-044: autoscale on p95 latency policy. The actual p95 metric is
    # served by the obs stack (OGON-OBS) via a custom metric adapter; the
    # rule below is the bootstrap shape (cpu/mem) used until the metric is
    # wired. Replace the cpu rule with a p95 rule when obs is live.
    ogonframeworks.dev/autoscale-policy: "p95"
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{.App.Name}}
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{.K8s.HPACPUTarget}}
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: {{.K8s.HPAMemTarget}}
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 30
      policies:
        - type: Percent
          value: 100
          periodSeconds: 30
    scaleDown:
      stabilizationWindowSeconds: 300
      policies:
        - type: Percent
          value: 25
          periodSeconds: 60
`

// GeneratePDB emits a PodDisruptionBudget guaranteeing min-available.
func GeneratePDB(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("pdb").Parse(pdbTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("pdb template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/pdb.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "PDB (INFRA-012): min-available = 1",
	}, nil
}

const pdbTemplate = `apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{.App.Name}}
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
spec:
  minAvailable: {{.K8s.PDBMinAvailable}}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{.App.Name}}
      ogonframeworks.dev/role: web
`

var _ = strings.TrimSpace

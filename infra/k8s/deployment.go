// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s Deployment generator (INFRA-008/017/043). Emits a Deployment with:
//   - liveness/readiness/startup probes (INFRA-008)
//   - resources (250m/512Mi baseline — INFRA-043)
//   - securityContext: nonRoot, readOnlyRootFilesystem, drop ALL caps
//   - rolling strategy (maxSurge 25%, maxUnavailable 0 — INFRA-017)
//   - terminationGracePeriodSeconds 30 (SIGTERM drain — INFRA-061)

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// Generator implements infra.Generator for the k8s target.
type Generator struct{}

func (Generator) Name() string { return "k8s" }

func (Generator) Plan(cfg *infra.InfraConfig) []string {
	return []string{
		"deploy/k8s/configmap.yaml",
		"deploy/k8s/cronjobs.yaml",
		"deploy/k8s/deployment.yaml",
		"deploy/k8s/hpa.yaml",
		"deploy/k8s/ingress.yaml",
		"deploy/k8s/migration-job.yaml",
		"deploy/k8s/namespace.yaml",
		"deploy/k8s/network-policy.yaml",
		"deploy/k8s/pdb.yaml",
		"deploy/k8s/secret.yaml",
		"deploy/k8s/service.yaml",
		"deploy/k8s/worker.yaml",
	}
}

func (Generator) Generate(cfg *infra.InfraConfig) ([]infra.FileSpec, error) {
	var out []infra.FileSpec
	for _, fn := range []func(*infra.InfraConfig) (infra.FileSpec, error){
		GenerateDeployment, GenerateService, GenerateIngress, GenerateHPA,
		GeneratePDB, GenerateNetworkPolicy, GenerateConfigMap, GenerateSecret,
		GenerateWorkerDeployment, GenerateCronJobs, GenerateMigrationJob, GenerateNamespace,
	} {
		spec, err := fn(cfg)
		if err != nil {
			return nil, fmt.Errorf("k8s: %w", err)
		}
		out = append(out, spec)
	}
	return out, nil
}

// GenerateNamespace emits a Namespace manifest (always owned + regenerable).
func GenerateNamespace(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	content := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    ogonframeworks.dev/managed: "true"
`, cfg.K8s.Namespace)
	return infra.FileSpec{
		Path: "deploy/k8s/namespace.yaml", Content: content,
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Kubernetes Namespace",
	}, nil
}

// GenerateDeployment emits the web Deployment (probes/resources/securityContext/rolling strategy).
func GenerateDeployment(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("deployment").Parse(deploymentTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("deployment template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/deployment.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Web Deployment: probes, resources (250m/512Mi baseline INFRA-043), securityContext non-root, rolling strategy (INFRA-017)",
	}, nil
}

const deploymentTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.App.Name}}
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
    app.kubernetes.io/part-of: ogon
spec:
  replicas: {{.Replicas}}
  revisionHistoryLimit: 10
  progressDeadlineSeconds: 600
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 25%
      maxUnavailable: 0          # INFRA-017: zero-downtime by construction
  selector:
    matchLabels:
      app.kubernetes.io/name: {{.App.Name}}
      ogonframeworks.dev/role: web
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{.App.Name}}
        ogonframeworks.dev/role: web
    spec:
      automountServiceAccountToken: false
      terminationGracePeriodSeconds: 30   # INFRA-061: SIGTERM drain
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      {{if .K8s.NodeAffinity}}
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: {{.K8s.NodeAffinity}}
                    operator: In
                    values: ["true"]
      {{end}}
      {{if .K8s.SpotToleration}}
      tolerations:
        - key: kubernetes.io/spot
          operator: Exists
          effect: NoSchedule
      {{end}}
      initContainers:
        - name: migrate
          image: {{.Image.Repository}}:{{.Release.SemVer}}
          command: ["/app", "migrate", "up", "--non-interactive"]
          envFrom:
            - configMapRef:
                name: {{.App.Name}}-config
            - secretRef:
                name: {{.App.Name}}-secret
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
      containers:
        - name: app
          image: {{.Image.Repository}}:{{.Release.SemVer}}
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: {{.App.Port}}
              protocol: TCP
          envFrom:
            - configMapRef:
                name: {{.App.Name}}-config
            - secretRef:
                name: {{.App.Name}}-secret
          resources:
            requests:
              cpu: "{{.Resources.CPURequest}}"   # INFRA-043 baseline
              memory: "{{.Resources.MemRequest}}"
            limits:
              cpu: "{{.Resources.CPULimit}}"
              memory: "{{.Resources.MemLimit}}"
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            capabilities:
              drop: ["ALL"]
          startupProbe:
            httpGet:
              path: {{.Probes.StartupPath}}
              port: http
            failureThreshold: 30
            periodSeconds: 10     # INFRA-060: ~5 min budget for slow boot
          livenessProbe:
            httpGet:
              path: {{.Probes.LivenessPath}}
              port: http
            periodSeconds: 10
            timeoutSeconds: 1
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: {{.Probes.ReadinessPath}}
              port: http
            periodSeconds: 5
            timeoutSeconds: 1
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["/app", "drain"]    # INFRA-061: graceful drain
            postStart:
              exec:
                command: ["/app", "health", "--post-start"]
`

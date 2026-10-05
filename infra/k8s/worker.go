// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s Worker Deployment (INFRA-015) — separate from the web Deployment. The
// worker runs background jobs and shares the same image, probes, and
// securityContext shape but no HTTP port, no Ingress, and a longer
// terminationGracePeriod for in-flight job draining (INFRA-061).

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateWorkerDeployment emits the worker Deployment.
func GenerateWorkerDeployment(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	if !cfg.K8s.Worker.Enabled {
		return infra.FileSpec{
			Path:    "deploy/k8s/worker.yaml",
			Content: "# worker Deployment disabled by config (cfg.K8s.Worker.Enabled == false)\n",
			Marker:  infra.MarkOwned, CommentPrefix: "#",
			Description: "Worker Deployment disabled (INFRA-015 opt-out)",
		}, nil
	}
	tpl := template.Must(template.New("worker").Parse(workerTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("worker template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/worker.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Worker Deployment (INFRA-015); separate from web; long drain (INFRA-061)",
	}, nil
}

const workerTemplate = `# INFRA-015: separate worker Deployment. Shares image with web but runs
# background jobs (queue consumers, scheduled tasks). No HTTP port, no
# Ingress. terminationGracePeriodSeconds is 300s to allow in-flight jobs
# to drain (INFRA-061).
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.App.Name}}-worker
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
    ogonframeworks.dev/role: worker
spec:
  replicas: {{.K8s.Worker.Replicas}}
  revisionHistoryLimit: 10
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app.kubernetes.io/name: {{.App.Name}}
      ogonframeworks.dev/role: worker
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{.App.Name}}
        ogonframeworks.dev/role: worker
    spec:
      automountServiceAccountToken: false
      terminationGracePeriodSeconds: 300
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: worker
          image: {{.Image.Repository}}:{{.Release.SemVer}}
          imagePullPolicy: IfNotPresent
          command:
{{range .K8s.Worker.Command}}
            - {{.}}
{{end}}
          envFrom:
            - configMapRef:
                name: {{.App.Name}}-config
            - secretRef:
                name: {{.App.Name}}-secret
          resources:
            requests:
              cpu: "{{.Resources.CPURequest}}"
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
          # Workers don't serve HTTP; use an exec probe on a /healthz
          # subcommand to detect liveness without a port.
          livenessProbe:
            exec:
              command: ["/app", "health", "--worker"]
            periodSeconds: 30
            timeoutSeconds: 5
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["/app", "drain", "--timeout", "300s"]
`

// GenerateCronJobs emits CronJobs for schedules (INFRA-016).
func GenerateCronJobs(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	if len(cfg.K8s.CronSchedules) == 0 {
		return infra.FileSpec{
			Path:    "deploy/k8s/cronjobs.yaml",
			Content: "# no cron schedules configured (cfg.K8s.CronSchedules == [])\n",
			Marker:  infra.MarkOwned, CommentPrefix: "#",
			Description: "CronJobs: none configured (INFRA-016 opt-out)",
		}, nil
	}
	tpl := template.Must(template.New("crons").Parse(cronsTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("crons template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/cronjobs.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "CronJobs for schedules (INFRA-016)",
	}, nil
}

const cronsTemplate = `# INFRA-016: CronJobs for scheduled tasks. Each entry is one schedule
# from ogon.yaml (cfg.K8s.CronSchedules).
{{range .K8s.CronSchedules}}
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: {{$.App.Name}}-cron-{{.Name}}
  namespace: {{$.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{$.App.Name}}
    ogonframeworks.dev/role: cron
spec:
  schedule: "{{.Schedule}}"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      backoffLimit: 2
      template:
        spec:
          automountServiceAccountToken: false
          restartPolicy: OnFailure
          terminationGracePeriodSeconds: 60
          securityContext:
            runAsNonRoot: true
            runAsUser: 65532
          containers:
            - name: job
              image: {{$.Image.Repository}}:{{$.Release.SemVer}}
              imagePullPolicy: IfNotPresent
              command:
{{range .Command}}
                - {{.}}
{{end}}
              envFrom:
                - configMapRef:
                    name: {{$.App.Name}}-config
                - secretRef:
                    name: {{$.App.Name}}-secret
              resources:
                requests:
                  cpu: "{{$.Resources.CPURequest}}"
                  memory: "{{$.Resources.MemRequest}}"
                limits:
                  cpu: "{{$.Resources.CPULimit}}"
                  memory: "{{$.Resources.MemLimit}}"
              securityContext:
                allowPrivilegeEscalation: false
                readOnlyRootFilesystem: true
                runAsNonRoot: true
                capabilities:
                  drop: ["ALL"]
{{end}}
`

var _ = strings.TrimSpace

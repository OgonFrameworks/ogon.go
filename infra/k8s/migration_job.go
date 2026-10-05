// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Pre-deploy migration Job (INFRA-028). A one-shot Job that runs
// `ogon migrate up --non-interactive` before the web Deployment's new
// pods accept traffic. Helm hook annotations included for helm-based deploys.

package k8s

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// GenerateMigrationJob emits the pre-deploy migration Job.
func GenerateMigrationJob(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("migrate").Parse(migrateTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("migrate template: %w", err)
	}
	return infra.FileSpec{
		Path: "deploy/k8s/migration-job.yaml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Pre-deploy migration Job (INFRA-028): runs `ogon migrate up` before new pods",
	}, nil
}

const migrateTemplate = `# INFRA-028: pre-deploy migration Job. Runs to completion before the
# new ReplicaSet accepts traffic. Helm hook annotations allow this to be
# wired as a pre-upgrade hook when deploying via Helm.
apiVersion: batch/v1
kind: Job
metadata:
  name: {{.App.Name}}-migrate
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
    ogonframeworks.dev/role: migrate
  annotations:
    # Helm: run before any Deployment upgrade.
    "helm.sh/hook": pre-upgrade,pre-install
    "helm.sh/hook-weight": "-5"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 600
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{.App.Name}}
        ogonframeworks.dev/role: migrate
    spec:
      automountServiceAccountToken: false
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: migrate
          image: {{.Image.Repository}}:{{.Release.SemVer}}
          imagePullPolicy: IfNotPresent
          command: ["/app", "migrate", "up", "--non-interactive", "--strict"]
          envFrom:
            - configMapRef:
                name: {{.App.Name}}-config
            - secretRef:
                name: {{.App.Name}}-secret
          resources:
            requests:
              cpu: "500m"
              memory: "256Mi"
            limits:
              cpu: "1"
              memory: "512Mi"
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            capabilities:
              drop: ["ALL"]
`

var _ = strings.TrimSpace

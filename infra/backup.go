// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Backup + restore runbook + DR checklist (INFRA-047/048/049). Emits:
//   - k8s CronJob for pg_dump backups (INFRA-047)
//   - restore runbook markdown (INFRA-048)
//   - DR checklist markdown (INFRA-049)

package infra

import (
	"fmt"
	"strings"
	"text/template"
)

// GenerateBackupCron emits a k8s CronJob that runs pg_dump nightly to S3.
// (INFRA-047.)
func GenerateBackupCron(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("backup").Parse(backupTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("backup template: %w", err)
	}
	return FileSpec{
		Path: "deploy/k8s/backup-cron.yaml", Content: b.String(),
		Marker: MarkOwned, CommentPrefix: "#",
		Description: "pg_dump backup CronJob (INFRA-047): nightly, S3, 30d retention",
	}, nil
}

const backupTemplate = `# INFRA-047: pg_dump backup CronJob. Runs nightly at 02:00 UTC.
# Stores compressed dumps in S3 with 30-day retention (lifecycle-managed
# by the bucket). Restore via infra/runbooks/restore.md (INFRA-048).
apiVersion: batch/v1
kind: CronJob
metadata:
  name: {{.App.Name}}-backup
  namespace: {{.K8s.Namespace}}
  labels:
    app.kubernetes.io/name: {{.App.Name}}
    ogonframeworks.dev/role: backup
spec:
  schedule: "0 2 * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 7
  failedJobsHistoryLimit: 7
  jobTemplate:
    spec:
      backoffLimit: 2
      template:
        spec:
          restartPolicy: OnFailure
          automountServiceAccountToken: false
          securityContext:
            runAsNonRoot: true
            runAsUser: 65532
          containers:
            - name: pgdump
              image: postgres:{{.Database.Version}}-alpine
              env:
                - name: PGPASSWORD
                  valueFrom:
                    secretKeyRef:
                      name: {{.App.Name}}-secret
                      key: DATABASE_URL
                - name: AWS_REGION
                  value: {{.Terraform.Region}}
              envFrom:
                - secretRef:
                    name: {{.App.Name}}-secret
              command:
                - /bin/sh
                - -c
                - |
                  set -eu
                  TS=$(date -u +%Y%m%dT%H%M%SZ)
                  DUMP=/tmp/{{.App.Name}}-$TS.dump
                  pg_dump --format=custom --no-owner --no-privileges \
                    "$DATABASE_URL" -f "$DUMP"
                  aws s3 cp "$DUMP" \
                    "s3://{{.App.Name}}-backups/{{.App.Name}}-$TS.dump" \
                    --sse AES256
                  # Retention: 30d via S3 lifecycle; 7d of "warm" copies
                  # retained in this bucket prefix.
              resources:
                requests:
                  cpu: "500m"
                  memory: "512Mi"
                limits:
                  cpu: "1"
                  memory: "1Gi"
              securityContext:
                allowPrivilegeEscalation: false
                readOnlyRootFilesystem: false
                runAsNonRoot: true
                capabilities:
                  drop: ["ALL"]
`

// GenerateRestoreRunbook emits the restore runbook (INFRA-048).
func GenerateRestoreRunbook(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("restore").Parse(restoreTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("restore runbook template: %w", err)
	}
	return FileSpec{
		Path: "infra/runbooks/restore.md", Content: b.String(),
		Marker: MarkSeeded, CommentPrefix: "<!--",
		Description: "Restore runbook (INFRA-048): step-by-step pg_restore",
	}, nil
}

const restoreTemplate = `# Restore Runbook — {{.App.Name}} (INFRA-048)

> Seeded by ogon infra gen. Edit by hand. --force required to regenerate.

This runbook restores the {{.Database.Engine}} database from a backup taken
by the {{.App.Name}}-backup CronJob (INFRA-047).

## Prerequisites

- kubectl access to the target cluster ({{.K8s.Namespace}} namespace).
- AWS credentials with read access to the {{.App.Name}}-backups S3 bucket.
- A maintenance window (the app should be drained during restore).

## Steps

1. **Quiesce traffic.** Scale the web Deployment to 0:
   ` + "```" + `
   kubectl -n {{.K8s.Namespace}} scale deploy {{.App.Name}} --replicas=0
   kubectl -n {{.K8s.Namespace}} scale deploy {{.App.Name}}-worker --replicas=0
   ` + "```" + `

2. **List recent backups.**
   ` + "```" + `
   aws s3 ls s3://{{.App.Name}}-backups/ | tail -10
   ` + "```" + `

3. **Pick a backup.** Prefer the most recent successful dump. Note the
   timestamp; you will need it for verification.

4. **Run the restore Job** (one-shot, manual):
   ` + "```" + `
   cat <<EOF | kubectl -n {{.K8s.Namespace}} apply -f -
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: {{.App.Name}}-restore-manual
   spec:
     backoffLimit: 0
     template:
       spec:
         restartPolicy: Never
         containers:
           - name: restore
             image: postgres:{{.Database.Version}}-alpine
             envFrom:
               - secretRef:
                   name: {{.App.Name}}-secret
             command:
               - /bin/sh
               - -c
               - |
                 set -eu
                 aws s3 cp s3://{{.App.Name}}-backups/<CHOSEN_DUMP> /tmp/dump.dump
                 # Drop + recreate to avoid stale rows
                 psql "$DATABASE_URL" -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
                 pg_restore --no-owner --no-privileges --clean --if-exists \
                   -d "$DATABASE_URL" /tmp/dump.dump
         securityContext:
           runAsNonRoot: true
           runAsUser: 65532
   EOF
   ` + "```" + `

5. **Run migrations** to bring the schema to the current app version:
   ` + "```" + `
   kubectl -n {{.K8s.Namespace}} create job --from=job/{{.App.Name}}-migrate \
     {{.App.Name}}-migrate-post-restore
   ` + "```" + `

6. **Verify.** Run smoke tests against the canary pod:
   ` + "```" + `
   kubectl -n {{.K8s.Namespace}} run smoke --rm -i --tty \
     --image={{.Image.Repository}}:{{.Release.SemVer}} --restart=Never \
     -- /app health --path /readyz --port {{.App.Port}}
   ` + "```" + `

7. **Restore traffic.** Scale the web Deployment back:
   ` + "```" + `
   kubectl -n {{.K8s.Namespace}} scale deploy {{.App.Name}} --replicas={{.Replicas}}
   kubectl -n {{.K8s.Namespace}} scale deploy {{.App.Name}}-worker --replicas={{.K8s.Worker.Replicas}}
   ` + "```" + `

8. **Post-incident.** File a post-mortem. Update the runbook if any step
   surprised you.
`

// GenerateDRChecklist emits the DR checklist (INFRA-049).
func GenerateDRChecklist(cfg *InfraConfig) (FileSpec, error) {
	const doc = `# Disaster Recovery Checklist (INFRA-049)

> Seeded by ogon infra gen. Edit by hand. --force required to regenerate.

Pre-flight (quarterly drill):

- [ ] Backup CronJob has succeeded in the last 24h (kubectl get cj app-backup).
- [ ] Most recent backup object exists in S3 (aws s3 ls).
- [ ] Restore runbook (infra/runbooks/restore.md) has been walked end-to-end
      in a non-prod namespace within the last 90 days.
- [ ] Multi-region replica (if enabled — cfg.Terraform.MultiRegion) is
      reachable and within replication lag SLA (<5s).
- [ ] DNS failover plan documented (external-dns or Route53 health checks).
- [ ] Secrets backend (Vault/SSM/SM) has a tested failover or replica.
- [ ] Image registry has a secondary pull source (ECR cross-region replicate,
      or a GAR multi-region).
- [ ] On-call rotation updated; runbook linked from alerting rules.

During an incident:

1. Acknowledge the alert. Page secondary if no ack in 5m.
2. Post a status update in #incidents within 10m.
3. Decide: roll back (ogon deploy --rollback — INFRA-069) OR restore (runbook
   INFRA-048) OR failover to replica region (INFRA-050).
4. Communicate every 30m until resolved.
5. File a post-mortem within 5 business days.
`
	return FileSpec{
		Path: "infra/runbooks/dr-checklist.md", Content: doc,
		Marker: MarkSeeded, CommentPrefix: "<!--",
		Description: "Disaster recovery checklist (INFRA-049)",
	}, nil
}

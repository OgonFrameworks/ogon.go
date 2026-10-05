# P11 — Generative Infrastructure (OGON-INFRA)

## Files written (31 source + 6 test = 37)

### Top-level package `infra` (15 files)

| File | Role | Spec |
|------|------|------|
| `infra/infra.go` | InfraConfig schema, Generator interface, FileSpec, markers, DefaultConfig, Validate | INFRA-001 |
| `infra/marker.go` | `ogon:generated` / `ogon:owned` / `ogon:seeded` markers, Stamp, IsOwned/Seeded/Generated/UserOwned | INFRA-038 |
| `infra/idempotent.go` | LockFile ledger, SHA256, IdempotentWrite (created/updated/skipped-identical/skipped-unowned/skipped-edited/forced-overwrite), --force semantics | INFRA-039/040 |
| `infra/diff.go` | `ogon infra diff` drift report (unchanged/drift/missing/extra), scans infra/ for extras | INFRA-037 |
| `infra/env_matrix.go` | per-env config files (local/dev/prod) + parity checklist (dev→prod) | INFRA-026/056 |
| `infra/secrets.go` | secrets mapping (ogon.yaml → k8s Secret keys → env vars) + backend inventory | INFRA-027 |
| `infra/release.go` | Release stamping, image tag (sha+semver), registry push helper, GHCR/ECR/GAR auth docs | INFRA-029/030/031/032 |
| `infra/security.go` | SBOM (syft SPDX), trivy scan, cosign keyless signing snippets | INFRA-057/058/SEC-074 |
| `infra/backup.go` | pg_dump backup CronJob, restore runbook, DR checklist | INFRA-047/048/049 |
| `infra/cost.go` | cost/rightsize hints doc, 250m/512Mi baseline reference | INFRA-042/043 |
| `infra/deploy_guides.go` | per-provider deploy guides (aws/gcp/azure/k8s-generic) | INFRA-070 |
| `infra/tests_test.go` | startup probe timing (INFRA-060), SIGTERM drain (INFRA-061), zero-downtime rolling (INFRA-062), image budget (INFRA-059), resource defaults (INFRA-043), VPC private (INFRA-064), default-deny ingress (INFRA-065) — 7 tests |
| `infra/marker_test.go` | 11 marker tests |
| `infra/diff_test.go` | 15 idempotent + drift tests |

### Subdir packages

| Package | Files | Spec coverage |
|---------|-------|---------------|
| `infra/docker` | `dockerfile.go`, `dockerfile_test.go`, `test_test.go` | INFRA-001..005, 059 (Dockerfile multi-stage distroless/static non-root healthcheck build-args + .dockerignore + hello budget) |
| `infra/compose` | `compose.go`, `compose_test.go` | INFRA-006/007/055 (dev stack app+pg+redis+mailpit healthchecks depends_on, PR preview profile) |
| `infra/k8s` | `deployment.go` (008/017/043), `service.go` (009), `ingress.go` (010/045/046), `hpa.go` (011/044/012), `network_policy.go` (013/064/065), `configmap.go` (014/027), `worker.go` (015/016), `migration_job.go` (028), `deploy.go` (068/069 — rollout poll + rollback), `deployment_test.go` (21 tests) |
| `infra/terraform` | `aws.go` (020/050/063/064), `ci.go` (041/054), `aws_test.go` (12 tests) |
| `infra/actions` | `ci.go` (034 + TEST-040 pre-commit), `deploy.go` (034 per-cloud deploy) |
| `infra/cloudflare` | `cloudflare.go` (DNS + R2 + Workers + WAF rate-limit stubs) |
| `infra/helm` | `helm.go` (Chart.yaml + values.yaml + templates/_all.yaml + _helpers.tpl + .helmignore) |

## Test count: 85 (all PASS)

- `infra` package: 33 (15 diff + 11 marker + 7 tests_test)
- `infra/compose`: 7
- `infra/docker`: 12 (8 dockerfile_test + 4 test_test)
- `infra/k8s`: 21 (deployment_test)
- `infra/terraform`: 12 (aws_test)

## Verification

- `go build ./infra/...` — clean
- `go test -count=1 ./infra/...` — all 85 tests PASS
- `go vet ./infra/...` — clean
- `gofmt -l infra/` — empty

## Critical-rule adherence

- ✅ Everything generated lands in `infra/`, is committed, is editable
- ✅ Generators write only marker-unowned files; edited files require `--force` (IdempotentWrite matrix)
- ✅ `ogon infra diff` reports drift (Diff() + DriftReport with 4 statuses)
- ✅ Image budget < 30 MB hello (Dockerfile uses distroless/static + CGO_ENABLED=0 + no apk/apt in runtime stage)
- ✅ Pre-deploy migration Job (k8s/migration_job.go + helm pre-upgrade hook annotation)
- ✅ Worker Deployment separate from web Deployment (k8s/worker.go)
- ✅ LICENSE header (SPDX MIT) on every file
- ✅ No external deps added (text/template + stdlib only)

## Naming-deviation note

Two deliverable-named files were renamed to satisfy Go's test-discovery rule:

- `infra/tests.go` → `infra/tests_test.go` (Test* functions must live in `*_test.go` to be picked up by `go test`)
- `infra/docker/test.go` → `infra/docker/test_test.go` (same reason)

The OGON-CLI workaround (`commands_testcmd.go`) is for shipping non-test code in the binary — irrelevant here since these files contain only Test functions, which need `_test.go` to be discovered. The worklog's `_testcmd.go` pattern only applies to files that need to ship in the binary AND don't contain Test functions.

## CLI wiring deferred

Per the task scope ("write ONLY to /home/z/my-project/ogongo/OgonGo/infra/"), the existing `cli/commands_infra.go` "ships in a later phase" diagnostic is left untouched. The cli command surface is stable; a future task wires `runInfraGen` / `runInfraDiff` to call into `infra` package generators.

## Blockers / Future work

- None for this phase. The `infra` package surface is stable and the CLI wiring is a separate scoped task.
- Live cluster tests (kubectl rollout, actual pg_dump) are out of scope; the test harnesses assert the static generated artifact shape (the same approach OGON-TEST uses for golden tests).

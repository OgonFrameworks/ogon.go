# Deploy guide — `ogon infra gen`, `ogon deploy --cloud aws`

> **Goal**: ship a containerized OgonGo service to AWS, GCP, or Azure,
> with infrastructure-as-code, secrets management, rollback, and
> verification.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

The `infra` package and `ogon deploy` / `ogon infra` commands own the
post-build lifecycle:

- `infra/docker` — Dockerfile generation (multi-stage, distroless,
  non-root, HEALTHCHECK).
- `infra/compose` — docker-compose generation (dev + prod).
- `infra/k8s` — k8s manifests (Deployment, Service, HPA, PDB,
  ConfigMap, Secret refs, Probes).
- `infra/helm` — helm chart generation.
- `infra/terraform` — terraform modules (VPC, ECS, RDS, ElastiCache,
  S3, CloudFront, IAM).
- `infra/cloudflare` — Cloudflare bindings (R2, KV, D1, Workers).
- `infra/secrets` — secret store integration (AWS Secrets Manager,
  GCP Secret Manager, Azure Key Vault).
- `infra/cost` — cost estimation per environment.
- `infra/env_matrix` — per-env diff (dev / staging / prod).
- `infra/idempotent` — `ogon deploy` is idempotent; running it twice
  produces no diff if nothing changed.
- `infra/release` — release notes + git tag automation.
- `infra/backup` — DB + asset backup definitions.
- `infra/security` — security context defaults (non-root, readOnlyRootFS).
- `infra/marker` — ownership marker for committed artifacts
  (`.ogon-infra`); `ogon infra diff` detects drift.
- `infra/diff` — drift detection between the generated artifacts and
  the committed ones.

`ogon deploy` runs `build → push → apply → verify` in that order;
`--rollback` restores the previous release automatically if a probe
fails.

## When

Use `ogon infra gen` + `ogon deploy` when:

- you want committed infrastructure artifacts (the repo is the source of
  truth, not a CloudFormation console);
- you want `ogon deploy` to be the only deploy command (no
  terraform/cloudformation/kubectl you also have to learn);
- you want idempotent deploys and automatic rollback on probe failure.

For a single-binary deploy to a single VM (no orchestrator), use
`infra/docker` only; skip the k8s / terraform modules.

## Quickstart

```bash
ogon new saas --template standard --git
cd saas

ogon infra gen docker --dry-run
ogon infra gen docker            # writes Dockerfile + .dockerignore

ogon infra gen k8s --dry-run
ogon infra gen k8s               # writes k8s/*.yaml

ogon infra gen terraform --target aws --dry-run
ogon infra gen terraform --target aws
```

Verify the artifacts are committed:

```bash
ogon infra diff                  # empty = no drift
git add Dockerfile k8s/ terraform/
git commit -m "chore(infra): ship initial infra"
```

Deploy:

```bash
ogon deploy --cloud aws --dry-run   # plan only
ogon deploy --cloud aws             # build → push → apply → verify
ogon deploy --cloud aws --rollback  # rollback on probe failure
```

## Config

`ogon.yaml`:

```yaml
infra:
  target: aws                     # aws|gcp|azure|local
  registry:
    url_env: OGON_REGISTRY_URL
  container:
    base: gcr.io/distroless/go-debian12
    non_root: true
    read_only_root_fs: true
  k8s:
    namespace: ogon
    replicas: 3
    hpa:
      min: 3
      max: 20
      cpu_pct: 75
    probes:
      liveness: /healthz
      readiness: /readyz
    resources:
      requests: { cpu: 250m, memory: 256Mi }
      limits:   { cpu: 1,    memory: 1Gi }
  secrets:
    backend: aws-secrets-manager   # aws-sm|gcp-sm|azure-kv|file
    prefix: /ogon/prod
  backup:
    db: hourly
    retention: 30d
  cost:
    alert_monthly_usd: 500
```

Environment matrix:

```bash
ogon infra env-matrix
# dev        staging     prod
# replicas: 1            3           6
# hpa.max:  3            10          20
# secrets:  file         aws-sm      aws-sm
```

## Test

`ogon infra diff` is the CI gate:

```bash
ogon infra gen docker --force
ogon infra diff --strict            # non-empty diff = CI failure
```

The `test` package ships a drift detector:

```go
test.OpenAPIDriftTest{
    Baseline: "testdata/openapi.yaml",
}.Run(t, http.Handler())
```

And an idempotency test:

```go
func TestDeployIdempotent(t *testing.T) {
    first := runDeploy(t, "--dry-run")
    second := runDeploy(t, "--dry-run")
    require.Equal(t, first, second, "deploy plan must be idempotent")
}
```

`ogon test` runs these alongside unit tests; CI runs `ogon check`
(which includes `ogon infra diff`).

## Prod

```bash
# 1. ensure secrets exist
ogon infra secrets set OGON_SESSION_SECRET --generate 32
ogon infra secrets set OGON_DB_URL         --from-literal "$DB_URL"

# 2. plan
ogon deploy --cloud aws --dry-run --verbose

# 3. apply
ogon deploy --cloud aws

# 4. verify (probes + smoke)
ogon health
ogon deploy --verify-only
```

The pipeline:

1. **Build** — `ogon build` (codegen → fmt → compile, stamps version).
2. **Push** — pushes the OCI image to `$OGON_REGISTRY_URL` with a SHA
   tag.
3. **Apply** — applies the k8s manifests (or terraform, depending on
   target).
4. **Verify** — hits the readiness probe, then runs a smoke test
   (HTTP 200 on `/`, signed-jwt round-trip on `/auth/whoami`).

If verify fails and `--rollback` was passed, the previous release is
restored automatically. The pipeline emits structured diagnostics
(`OGON-U0020` deploy-verify-failed, `OGON-U0021` rollback-done) so CI
can route the failure to the right team.

## Escape

- **Bring your own Dockerfile**: write `Dfile` by hand and set
  `infra.docker.path: ./Dfile` in `ogon.yaml`. The CLI stops generating
  it.
- **Plain kubectl / helm**: `ogon infra gen k8s --stdout` writes the
  manifests to stdout; pipe them to `kubectl apply -f -` or `helm
  template`.
- **Skip `ogon deploy`**: `ogon build && docker push ... && kubectl
  apply -f k8s/` is a valid escape hatch. You lose rollback + verify.
- **Terraform only**: `ogon infra gen terraform --target aws` produces
  a standalone terraform module; you can run `terraform apply` directly.
- **Local prod**: `ogon deploy --cloud local` runs the binary on the
  local machine; useful for smoke tests in CI.

## Troubleshoot

| Symptom                                                 | Fix                                                              |
|---------------------------------------------------------|------------------------------------------------------------------|
| `ogon infra diff` reports drift                        | You edited a generated file by hand; re-run `ogon infra gen`.    |
| `OGON-U0020: deploy verify failed`                     | The readiness probe did not return 200 within the timeout.       |
| `OGON-U0021: rollback done`                            | The deploy failed; the previous release is restored. Read the diagnostic to find the original failure. |
| `OGON-S0010: secret not found`                         | `ogon infra secrets set` the missing key.                         |
| `OGON-K0010: registry URL missing`                     | `OGON_REGISTRY_URL` env is unset; export it or set in `ogon.prod.yaml`. |
| Image pushed but pod not pulling                        | Check image pull secrets; `infra.k8s.image_pull_secret_env`.     |
| HPA never scales                                        | CPU `requests` too low (HPA uses request-based CPU); raise it.  |
| Cost alert fires daily                                  | Check `infra.cost.alert_monthly_usd` and the actual cost in the cloud console. |

---

Next: [Quickstart](../quickstart.md), [CRUD guide](./crud.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

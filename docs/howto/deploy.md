# How-to — Deploy an OgonGo service

> **Goal**: take a working OgonGo project from `ogon dev` to a
> production endpoint, with deterministic artifacts, zero-downtime
> rolling updates, and rollback.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo's deploy pipeline is `ogon deploy`. It runs, in order:

1. `ogon build` — codegen -> fmt -> compile, stamps version.
2. `ogon infra gen <target>` — Dockerfile, k8s manifests, Terraform.
3. `docker build` + `docker push` — to the configured registry.
4. `kubectl apply` (or `terraform apply` for AWS) — the manifests are
   idempotent and the markers refuse to apply on top of a manually
   edited file unless `--force` is passed.
5. `kubectl rollout status` — wait for the rollout to go green.
6. `ogon health` — hit the readiness probe on the new pods.

`--rollback` reverts the deployment if the rollout fails or the probe
does not go green within the timeout.

## When

Use `ogon deploy` when:

- you have a CI pipeline that needs deterministic deploy steps;
- you want zero-downtime rolling updates with auto-rollback;
- you want the generated infra to be committed, inspectable, and
  editable.

For one-off manual deploys, the underlying `ogon build` + `docker push`
+ `kubectl apply` steps are all exposed as separate commands.

## Quickstart

```bash
# 1. configure the cloud (one-time)
ogon infra gen docker                 # Dockerfile + compose.yaml
ogon infra gen k8s                    # k8s manifests
ogon infra gen aws                    # Terraform for EKS + RDS + ElastiCache

# 2. dry-run the deploy plan
ogon deploy --cloud aws --dry-run

# 3. ship it
ogon deploy --cloud aws --rollback    # rollback on failure
```

The plan output (truncated):

```
deploy --cloud aws
  build    -> bin/myservice (1.0.0)
  docker   -> myservice:1.0.0 -> 123456.dkr.ecr.us-east-1.amazonaws.com/myservice:1.0.0
  k8s      -> Deployment/myservice (rolling, maxSurge=1, maxUnavailable=0)
  rollout  -> waiting for 3/3 ready (timeout 5m)
  health   -> GET https://myservice.example.com/healthz -> 200
```

## Config

`ogon.yaml`:

```yaml
deploy:
  cloud: aws                      # aws | gcp | azure | k8s | docker
  registry: 123456.dkr.ecr.us-east-1.amazonaws.com
  image: myservice
  rollback_on_probe_failure: true
  rollout_timeout: 5m
  probe:
    path: /healthz
    initial_delay: 5s
    period: 5s
```

Per-environment overlay:

```yaml
# ogon.prod.yaml
deploy:
  cloud: aws
  registry: ${ECR_REGISTRY}
  replica_count: 3
  resources:
    cpu: 500m
    memory: 256Mi
```

Secrets come from the configured secret manager — never inline. The
deploy pipeline injects them at apply time.

## Test

`ogon deploy --dry-run` is the test. It does not mutate anything; it
just prints the plan and exits 0 if everything resolves. CI runs:

```bash
ogon deploy --cloud aws --dry-run
```

For end-to-end deploy tests, the `test.DeployHarness` (in the `test`
package) stands up a kind cluster in CI and runs the actual deploy
against it. AT-010 ("rolling update with zero dropped in-flight
requests") uses this harness.

## Prod

```bash
ogon deploy --cloud aws                 # ship
ogon deploy --cloud aws --rollback      # ship + auto-rollback on probe failure
ogon deploy --cloud gcp --dry-run       # plan only
```

Monitor:

```bash
ogon health                             # hit probes
ogon logs --follow                      # tail logs from the running pods
```

The generated Terraform and k8s manifests are committed to the repo
under `infra/`. They carry the `ogon:owned` marker; `ogon infra diff`
shows drift and refuses to apply on top of manual edits unless you
pass `--force`.

## Escape

- **Skip the pipeline**: `ogon build` produces a static binary; you can
  ship it however you want (`scp`, `lambda zip`, etc.).
- **Custom k8s manifests**: write your own under `infra/k8s/` and the
  deploy pipeline will apply them alongside the generated ones.
- **Custom Terraform**: drop `.tf` files under `infra/terraform/` —
  the pipeline runs `terraform apply` on the whole directory.
- **Bring your own registry**: set `deploy.registry` to any OCI
  registry; the pipeline uses `docker buildx` so multi-arch works.

## Troubleshoot

| Symptom                                                 | Fix                                                                |
|---------------------------------------------------------|--------------------------------------------------------------------|
| `OGON-K0005: missing env var ECR_REGISTRY`              | The overlay references a secret env var; populate it.              |
| `ogon deploy` fails at `docker build`                   | Read the build log; `ogon build --no-cache` reproduces locally.    |
| `rollout` stuck at 2/3 ready                            | `kubectl describe pod <name>`; check the readiness probe.          |
| `ogon deploy --rollback` did not roll back              | Rollback only triggers on probe failure within `rollout_timeout`. |
| `ogon infra diff` reports drift                         | Someone edited a generated file; `--force` to overwrite or revert the edit. |

---

Next: [Migrate how-to](./migrate.md), [Test how-to](./test.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

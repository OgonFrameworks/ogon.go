# Deploy guide — GCP

> **Goal**: ship an OgonGo service to GCP. OgonGo's first-party
> Terraform generator targets AWS; GCP is an external module (Part XX).
> This page documents the community path.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

Per Part XX, OgonGo does **not** ship a first-party GCP generator. The
GCP path is an external module: `ogon-gcp` (community-maintained,
hosted at `github.com/ogonframeworks-community/ogon-gcp`). It
provides:

- `ogon infra gen gcp` — writes Terraform for GKE + Cloud SQL +
  Memorystore + GCS.
- `ogon deploy --cloud gcp` — wraps `terraform apply` + `kubectl apply`.
- The same `ogon:owned` marker convention as the first-party
  generators.

The module's `ogon.module.json` declares a framework constraint
(`>=1.0.0 <2.0.0`) and is signed.

## When

- Your org is on GCP.
- You want GKE + Cloud SQL + Memorystore managed for you.
- You are OK using a community-maintained module.

## Quickstart

```bash
# 1. install the module
ogon add ogon-gcp@latest

# 2. generate the Terraform
ogon infra gen gcp

# 3. plan
ogon deploy --cloud gcp --dry-run

# 4. ship
ogon deploy --cloud gcp --rollback
```

## Config

`ogon.yaml#deploy.gcp` (added by the module):

```yaml
deploy:
  cloud: gcp
  gcp:
    project: my-gcp-project
    region: us-central1
    cluster_name: myservice-cluster
    db:
      tier: db-custom-2-7680
      high_availability: false
    redis:
      tier: BASIC_HA_CACHE_T1
    gcs:
      bucket_name: myservice-assets
    secrets:
      store: secretmanager      # secretmanager | krm
```

## Test

`ogon deploy --cloud gcp --dry-run` runs `terraform plan` and
`kubectl apply --dry-run=server`. The community module's CI runs
`terraform validate` on every PR.

## Prod

```bash
ogon deploy --cloud gcp --rollback
```

The flow is identical to AWS:

1. `ogon build` + `docker push` to Artifact Registry.
2. `terraform apply`.
3. `kubectl apply -f infra/k8s/` (the k8s manifests are first-party,
   see [k8s.md](./k8s.md)).
4. `kubectl rollout status deploy/myservice`.
5. `ogon health`.
6. `--rollback` reverts on probe failure.

## Escape

- **Custom Terraform**: drop `.tf` files in `infra/terraform/gcp/`.
- **Cloud Run**: not generated; write a `cloudrun.yaml` and use `gcloud
  run deploy` directly (the OgonGo binary is a static container, so
  Cloud Run works fine).
- **Bring your own VPC**: set `deploy.gcp.vpc.network` and
  `deploy.gcp.vpc.subnetwork`.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon add ogon-gcp` fails              | The module is unsigned in your org; pin a signed version.      |
| `terraform plan` shows drift           | `terraform plan -detailed-exitcode`; review before `apply`.    |
| GKE cluster creation is slow           | Normal; GKE is faster than EKS but still 10+ minutes.          |
| `ogon deploy --cloud gcp` is not found  | The `ogon-gcp` module is not installed; `ogon add ogon-gcp`.   |

---

See also: [AWS deploy guide](./aws.md), [k8s deploy guide](./k8s.md),
[Module author guide](../module-author.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

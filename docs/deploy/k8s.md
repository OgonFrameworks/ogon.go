# Deploy guide — Kubernetes

> **Goal**: ship an OgonGo service to Kubernetes with a Deployment,
> Service, Ingress, HPA, NetworkPolicy, and a migration Job — all
> generated, committed, and editable.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

`ogon infra gen k8s` writes, under `infra/k8s/`:

| File                  | What                                          |
|-----------------------|-----------------------------------------------|
| `deployment.yaml`     | Deployment with readiness + liveness probes.  |
| `service.yaml`        | ClusterIP Service.                            |
| `ingress.yaml`        | Ingress with TLS.                             |
| `hpa.yaml`            | HorizontalPodAutoscaler (CPU + memory).       |
| `networkpolicy.yaml`  | Deny-all + allow app<->db + allow ingress.    |
| `configmap.yaml`      | Non-secret config from `ogon.yaml`.           |
| `migration-job.yaml`  | One-shot Job that runs `ogon migrate run`.    |

All files carry the `ogon:owned` marker; `ogon infra diff` reports
drift if you edit them.

## When

- You want zero-downtime rolling updates.
- You want HPA + NetworkPolicy out of the box.
- You want the migration to run as a Job, not at boot.

## Quickstart

```bash
ogon infra gen k8s
kubectl apply -f infra/k8s/
kubectl rollout status deploy/myservice
kubectl get pods
```

The Deployment (truncated):

```yaml
# ogon:owned
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myservice
  labels:
    app: myservice
    ogon-framework/version: "1.0.0"
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    spec:
      containers:
        - name: app
          image: 123456.dkr.ecr.us-east-1.amazonaws.com/myservice:1.0.0
          ports: [{containerPort: 3000}]
          readinessProbe:
            httpGet: {path: /readyz, port: 3000}
            initialDelaySeconds: 5
            periodSeconds: 5
          livenessProbe:
            httpGet: {path: /healthz, port: 3000}
            initialDelaySeconds: 15
            periodSeconds: 10
          resources:
            requests: {cpu: 250m, memory: 128Mi}
            limits:   {cpu: 500m, memory: 256Mi}
```

## Config

`ogon.yaml#deploy.k8s`:

```yaml
deploy:
  k8s:
    namespace: prod
    replicas: 3
    ingress:
      host: myservice.example.com
      tls_issuer: letsencrypt
    hpa:
      min: 3
      max: 30
      cpu: 70
    network_policy:
      deny_all_by_default: true
```

## Test

`ogon deploy --dry-run` validates the manifests with `kubectl apply
--dry-run=server`. The `test.DeployHarness` (in CI) stands up a kind
cluster and runs the actual deploy; AT-010 ("rolling update with zero
dropped in-flight requests") uses it.

## Prod

```bash
ogon deploy --cloud k8s --rollback
```

`ogon deploy` runs:

1. `ogon build` + `docker push`.
2. `kubectl apply -f infra/k8s/` (idempotent).
3. `kubectl rollout status deploy/myservice` (waits for green).
4. `ogon health` (hits `/readyz` on the new pods).
5. `--rollback` reverts the Deployment if the rollout fails or the
   probe does not go green.

The migration Job runs before the main Deployment rolls. The
Deployment waits for the Job to complete.

## Escape

- **Custom manifests**: drop `.yaml` files in `infra/k8s/`; the deploy
  pipeline applies them alongside the generated ones.
- **Helm chart**: `ogon infra gen helm` writes a Helm chart under
  `infra/helm/`; use `helm install` instead of `kubectl apply`.
- **Bring your own Ingress**: delete `infra/k8s/ingress.yaml` and
  write your own.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| Rollout stuck at 2/3 ready              | `kubectl describe pod <name>`; check the readiness probe.      |
| Migration Job fails                    | `kubectl logs job/migration-<version>`; the down migration may be missing. |
| HPA does not scale                     | Metrics Server not installed; `kubectl top pod` should work.   |
| `ogon infra diff` reports drift        | Someone edited a generated file; `--force` to overwrite or revert. |

---

See also: [AWS deploy guide](./aws.md), [Docker deploy guide](./docker.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

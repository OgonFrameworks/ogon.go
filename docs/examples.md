# Examples

> **Goal**: a curated set of working OgonGo projects, one per golden
> path. Each example is the smallest possible project that
> demonstrates one capability end-to-end.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

The `examples/` directory at the repo root holds one project per
golden path:

| Example                       | What it demonstrates                                 |
|-------------------------------|------------------------------------------------------|
| [`examples/hello-world`](../../examples/hello-world) | Minimal route, dev supervisor, healthz. |
| [`examples/crud`](../../examples/crud)             | Full CRUD resource: model + route + handler + test + migration. |
| [`examples/auth`](../../examples/auth)             | Session + passkey login, role-gated route, CSRF. |
| [`examples/jobs`](../../examples/jobs)             | Enqueue, retry, DLQ, cron, transactional outbox. |
| [`examples/realtime`](../../examples/realtime)     | Chat room over WS + SSE; reconnect; presence. |
| [`examples/deploy`](../../examples/deploy)         | Full deploy: Dockerfile, k8s, Terraform (AWS). |

Each example has a `README.md` with a single `ogon dev` (or `ogon
deploy`) command that proves it works.

## When

- You learn best by reading working code.
- You want a known-good baseline before extending.
- You are writing an agent and want a deterministic target.

## Quickstart

```bash
# clone the repo (or copy the example dir)
cd examples/hello-world
ogon dev
# -> listening on http://localhost:3000

curl :3000/hello
# {"hello":"world"}
```

Each example's `README.md` has the exact commands for that example.

## Config

Examples use the `minimal` or `standard` template; the `ogon.yaml` is
intentionally tiny. Override per-env with `ogon.<env>.yaml`.

## Test

Every example ships with `ogon test` green. Run:

```bash
cd examples/<name>
ogon test
```

The `test.NewApp` fixture is used everywhere; no port allocation, no
external services (except `examples/deploy` which uses
`testcontainers` for the deploy harness).

## Prod

`examples/deploy` is the prod reference. It ships:

- `infra/docker/Dockerfile` — multi-stage, distroless final image.
- `infra/k8s/` — Deployment, Service, Ingress, HPA, NetworkPolicy,
  migration Job.
- `infra/terraform/aws/` — EKS, RDS Postgres, ElastiCache Redis,
  S3, IAM.

`ogon deploy --cloud aws --rollback` is the documented prod path.

## Escape

Examples are plain OgonGo projects; you can copy them and start
editing. They are not generated — they are committed and curated.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon dev` in an example fails         | Run `ogon doctor`; the example may need a dep the doctor flags.|
| An example's tests fail                | File a bug; examples must stay green (CI enforces this).       |
| `examples/deploy` is slow              | It uses testcontainers for real Postgres + Redis; that's normal.|

---

See also: [Tutorials](./tutorials/hello-world.md), [Cookbook](./cookbook.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

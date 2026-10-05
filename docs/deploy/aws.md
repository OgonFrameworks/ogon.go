# Deploy guide — AWS

> **Goal**: ship an OgonGo service to AWS with Terraform-managed EKS,
> RDS Postgres, ElastiCache Redis, S3, and IAM — all generated,
> committed, and editable.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

`ogon infra gen aws` writes, under `infra/terraform/aws/`:

| File                  | What                                                |
|-----------------------|-----------------------------------------------------|
| `main.tf`             | Provider config + locals.                           |
| `eks.tf`              | EKS cluster + node group.                           |
| `rds.tf`              | RDS Postgres instance + subnet group + security group. |
| `elasticache.tf`      | ElastiCache Redis cluster.                          |
| `s3.tf`               | S3 bucket for assets / backups.                     |
| `iam.tf`              | IAM role + policy for the app pod (IRSA).           |
| `secrets.tf`          | SSM Parameter Store entries for secrets.            |
| `outputs.tf`          | Cluster endpoint, DB endpoint, Redis endpoint.      |

All files carry the `ogon:owned` marker.

## When

- You want a fully managed AWS stack (EKS + RDS + ElastiCache).
- You want the infra to be committed, inspectable, and editable.
- You want `ogon deploy --cloud aws` to do the whole thing.

## Quickstart

```bash
ogon infra gen aws
cd infra/terraform/aws
terraform init
terraform plan
terraform apply

# update kubeconfig
aws eks update-kubeconfig --name myservice-cluster

# deploy the app
ogon deploy --cloud aws --rollback
```

## Config

`ogon.yaml#deploy.aws`:

```yaml
deploy:
  cloud: aws
  aws:
    region: us-east-1
    cluster_name: myservice-cluster
    db:
      instance_class: db.t4g.small
      allocated_storage: 20
      multi_az: false
    redis:
      node_type: cache.t4g.micro
      num_nodes: 1
    s3:
      bucket_name: myservice-assets
    secrets:
      store: ssm              # ssm | secretsmanager
```

Secrets come from SSM Parameter Store (or Secrets Manager if you set
`deploy.aws.secrets.store: secretsmanager`). The deploy pipeline
injects them as env vars at apply time.

## Test

`ogon deploy --cloud aws --dry-run` runs `terraform plan` and
`kubectl apply --dry-run=server` and prints the combined plan. CI
runs this on every PR.

`terraform validate` is run by `ogon check` on every PR; AT-009
asserts the generated Terraform passes `terraform validate`.

## Prod

```bash
ogon deploy --cloud aws --rollback
```

`ogon deploy` runs:

1. `ogon build` + `docker push` to ECR.
2. `terraform apply` (idempotent — only changes if the spec changed).
3. `kubectl apply -f infra/k8s/` (the k8s manifests are also
   generated, see [k8s.md](./k8s.md)).
4. `kubectl rollout status deploy/myservice`.
5. `ogon health` (hits `/readyz`).
6. `--rollback` reverts the Deployment on probe failure.

## Escape

- **Custom Terraform**: drop `.tf` files in `infra/terraform/aws/`;
  the pipeline runs `terraform apply` on the whole directory.
- **Skip the pipeline**: `terraform apply` + `kubectl apply` + `ogon
  health` are all separate commands you can run manually.
- **Bring your own VPC**: set `deploy.aws.vpc.id` in `ogon.yaml`; the
  generated Terraform will use it instead of creating one.
- **GCP / Azure**: out of scope for first-party (Part XX); use a
  community module.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `terraform plan` shows 50 changes      | Drift; run `terraform plan -detailed-exitcode` and review.     |
| EKS cluster creation takes 20 minutes  | Normal; EKS is slow to create. Use `terraform apply -target`.  |
| RDS is in `modifying` state            | Storage autoscaling; wait for it to finish.                    |
| `ogon deploy` fails at `terraform apply`| Read the plan; do not `--yes` blindly.                         |
| `IRSA` annotation missing              | `deploy.aws.iam.role_arn` is not set; `ogon doctor` flags it.  |

---

See also: [GCP deploy guide](./gcp.md), [k8s deploy guide](./k8s.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

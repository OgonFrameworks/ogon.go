# Deploy example

A full deploy reference: Dockerfile, k8s, Terraform (AWS).

## Run (dry-run)

```bash
ogon build
ogon infra gen docker
ogon infra gen k8s
ogon infra gen aws

ogon deploy --cloud aws --dry-run
```

## Run (real)

```bash
ogon deploy --cloud aws --rollback
```

## Files

```
deploy/
├── ogon.yaml
├── main.go
├── Dockerfile               # generated (ogon:owned)
├── compose.yaml             # generated (ogon:owned)
├── infra/
│   ├── k8s/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   ├── ingress.yaml
│   │   ├── hpa.yaml
│   │   ├── networkpolicy.yaml
│   │   ├── configmap.yaml
│   │   └── migration-job.yaml
│   └── terraform/aws/
│       ├── main.tf
│       ├── eks.tf
│       ├── rds.tf
│       ├── elasticache.tf
│       ├── s3.tf
│       ├── iam.tf
│       ├── secrets.tf
│       └── outputs.tf
```

## Test

```bash
ogon deploy --cloud aws --dry-run   # plan only
ogon test                            # app tests
```

The `test.DeployHarness` (in CI) stands up a kind cluster and runs the
actual deploy; AT-010 ("rolling update with zero dropped in-flight
requests") uses it.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

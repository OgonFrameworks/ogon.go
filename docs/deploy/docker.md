# Deploy guide — Docker

> **Goal**: ship an OgonGo service as a Docker image, with a
> multi-stage build, distroless final image, and a compose file for
> local dev.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

`ogon infra gen docker` writes:

- `Dockerfile` — multi-stage: builder (Go 1.27.1) + final
  (distroless static).
- `compose.yaml` — app + Postgres + Redis, for local dev.
- `.dockerignore` — keeps the build context small.

The Dockerfile is committed, inspectable, and editable. The
`ogon:owned` marker on line 1 means `ogon infra diff` will report
drift if you edit it; pass `--force` to overwrite.

## When

- You want a deterministic build artifact.
- You want a local dev environment with real Postgres/Redis.
- You want to ship to any container registry.

## Quickstart

```bash
ogon infra gen docker
docker build -t myservice:1.0.0 .
docker run --rm -p 3000:3000 \
  -e OGON_SESSION_SECRET=$(openssl rand -hex 32) \
  myservice:1.0.0

# or use compose for the full stack
docker compose up
```

The Dockerfile:

```dockerfile
# syntax=docker/dockerfile:1.7
# ogon:owned

FROM golang:1.27.1 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /bin/myservice ./cmd/myservice

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /bin/myservice /bin/myservice
USER nonroot:nonroot
EXPOSE 3000
ENTRYPOINT ["/bin/myservice"]
```

The compose file:

```yaml
# ogon:owned
services:
  app:
    build: .
    ports: ["3000:3000"]
    environment:
      OGON_DB_URL: postgres://ogon:ogon@db:5432/ogon?sslmode=disable
      OGON_REDIS_URL: redis://redis:6379
      OGON_SESSION_SECRET: ${OGON_SESSION_SECRET}
    depends_on: [db, redis]

  db:
    image: postgres:16
    environment:
      POSTGRES_USER: ogon
      POSTGRES_PASSWORD: ogon
      POSTGRES_DB: ogon
    volumes: [pgdata:/var/lib/postgresql/data]

  redis:
    image: redis:7

volumes:
  pgdata:
```

## Config

`ogon.yaml#deploy`:

```yaml
deploy:
  registry: 123456.dkr.ecr.us-east-1.amazonaws.com
  image: myservice
  docker:
    base_image: gcr.io/distroless/static-debian12:nonroot
    target_stage: final
```

## Test

```bash
ogon infra gen docker
docker build -t myservice:test .
docker run --rm -d -p 3000:3000 --name myservice-test myservice:test
curl localhost:3000/healthz
docker stop myservice-test
```

The `test.DeployHarness` runs this in CI.

## Prod

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t 123456.dkr.ecr.us-east-1.amazonaws.com/myservice:1.0.0 \
  --push .
```

Or via `ogon deploy --cloud aws` (which does this + k8s apply + health
check).

## Escape

- **Custom base image**: set `deploy.docker.base_image` in `ogon.yaml`.
- **Skip the distroless final**: edit the Dockerfile; the `ogon:owned`
  marker means `ogon infra diff` will report drift (you can ignore it).
- **Bring your own Dockerfile**: do not run `ogon infra gen docker`;
  the deploy pipeline uses whatever `Dockerfile` is in the repo root.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `docker build` is slow                  | The build context is too big; check `.dockerignore`.           |
| Image is 50 MiB (budget is 30)          | You added a CGO dep; remove it or set `CGO_ENABLED=0`.         |
| Container exits 1 on boot               | `OGON_SESSION_SECRET` is missing; check `docker run -e`.       |
| `docker compose up` fails to connect to db | Postgres is still booting; `depends_on` does not wait for readiness. Use a healthcheck. |

---

See also: [k8s deploy guide](./k8s.md), [AWS deploy guide](./aws.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

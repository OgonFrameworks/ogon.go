# OgonGo documentation

> The canonical handoff for humans and agents. The
> [`llms.txt`](../llms.txt) at the repo root is the machine-readable
> mirror of this index.

## Start here

- [Quickstart](./quickstart.md) — 5 minutes from zero to a running service.
- [Tutorials](./tutorials/hello-world.md) — hello-world, CRUD, auth.
- [How-to guides](./howto/deploy.md) — deploy, migrate, test, debug, optimize.
- [Architecture](./architecture.md) — how the pieces fit together.
- [Cookbook](./cookbook.md) — common patterns.

## Reference

- [API reference](./reference/index.md) — package + type index.
- [Error codes](./error-codes.md) — every `OGON-<class><nnnn>` code.
- [CLI reference](https://ogongo.dev/cli) — every `ogon` subcommand.
- [Config reference](https://ogongo.dev/config) — every `ogon.yaml` key.

## Going to production

- [Security guide](./security.md) — defaults, prod checks, vulnerability reporting.
- [Performance guide](./performance.md) — budgets, hot paths, tuning.
- [Deploy: Docker](./deploy/docker.md), [k8s](./deploy/k8s.md),
  [AWS](./deploy/aws.md), [GCP](./deploy/gcp.md).

## Staying current

- [Changelog](./changelog.md) — what changed.
- [Upgrade guide](./upgrade.md) — how to `ogon update`.
- [Migration: 0.x to 1.0](./migration/0.x-to-1.0.md).

## Contributing

- [Contributing](./contributing.md) — branch, commit, review.
- [Style guide](./style-guide.md) — Go + docs style rules.
- [Docs style guide](./reference/docs-style-guide.md) — markdown rules.
- [Accessibility](./accessibility.md) — WCAG 2.1 AA.
- [Module author guide](./module-author.md) — write an OgonGo module.
- [Examples](./examples.md) — one project per golden path.

## Deep dives

- [Guides](./guides/crud.md) — CRUD, auth, realtime, fullstack, deploy.
- [ADRs](./adr/0001-state-machine.md) — architecture decisions.
- [Errors](./errors/OGON-C.md) — one page per error class.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

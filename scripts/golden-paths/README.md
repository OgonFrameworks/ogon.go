# Golden path scripts (DX-001)

Each script verifies one golden path end-to-end. They are wired into CI
as the `ogon benchmark --suite dx` step.

| Script         | Verifies                                                          | Spec    |
|----------------|-------------------------------------------------------------------|---------|
| hello-world.sh | install -> scaffold -> dev -> first route -> 200 -> shutdown.     | AT-001  |
| crud.sh        | gen resource -> migrate run -> CRUD answers; <= 1 dev file.       | AT-002  |
| auth.sh        | gen auth -> login/logout/passkey -> role-gated route -> 403.      | AT-007  |
| jobs.sh        | enqueue -> process -> retry -> DLQ with <= 5 lines of test.       | AT-012  |
| realtime.sh    | live.Handle -> two subscribers -> broadcast -> reconnect.         | AT-013  |
| deploy.sh      | build -> docker build -> k8s dry-run -> health.                   | AT-009  |

Run them all:

```bash
for script in scripts/golden-paths/*.sh; do
    bash "$script"
done
```

Each script uses `mktemp -d` and cleans up via `trap`, so they are
safe to run in CI.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

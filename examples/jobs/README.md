# Jobs example

Enqueue, process, retry, DLQ, cron, transactional outbox.

## Run

```bash
ogon migrate run
ogon dev

# enqueue a welcome email
curl :3000/signup -d '{"email":"a@b.c"}'

# watch the worker process it
ogon logs --follow
```

## Files

```
jobs/
├── ogon.yaml
├── handlers/SendEmail.go
├── jobs/SendEmail.go          # the handler
├── migrations/0001_jobs.up.sql
└── SendEmail_test.go
```

## Test

```bash
ogon test -run TestJob
```

The test enqueues, runs the worker inline, and asserts retry + DLQ
behavior in <= 5 lines (AT-012).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

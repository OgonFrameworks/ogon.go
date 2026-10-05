# Error class `OGON-U` — Runtime errors

> Diagnostics that fire at runtime when something the framework cannot
> classify elsewhere goes wrong — panics, supervisor failures, deploy
> verify failures, log tailer not wired, MCP not wired.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-U` is the catch-all for runtime-class issues that are not config
(K), validation (V), security (S), or generation (G). Severity ranges
from `info` (a feature ships in a later phase) to `error` (the app
cannot continue).

| Code         | Title                                | HTTP / Exit | When                                                                  |
|--------------|--------------------------------------|-------------|------------------------------------------------------------------------|
| OGON-U0001   | Unexpected error                     | 1           | Generic, unclassified runtime failure.                                  |
| OGON-U0002   | Panic recovered                       | 500         | A handler panicked; the supervisor recovered it.                       |
| OGON-U0010   | Live transport unavailable            | 500         | WS upgrade failed and SSE fallback also failed.                        |
| OGON-U0020   | Deploy verify failed                  | 1           | The readiness probe did not return 200 in the verify window.            |
| OGON-U0021   | Rollback done                         | 1           | The deploy failed; the previous release was restored.                  |
| OGON-U0030   | Log tailer not wired                  | 0 (info)    | `ogon logs --follow` ships in a later phase; emitted as info.          |
| OGON-U0031   | MCP server not wired                  | 0 (info)    | `ogon agent serve` ships in a later phase; emitted as info.            |
| OGON-U0040   | Generator write-path not wired        | 0 (info)    | The plan was emitted but the writer ships in a later phase.            |

---

## When

`OGON-U` fires:

- at runtime in a handler (panic, IO error, unexpected state);
- on `ogon deploy` (verify / rollback);
- on `ogon logs --follow` and `ogon agent serve` (info-severity
  placeholders);
- on `ogon gen <thing>` without `--dry-run` (U0040 placeholder).

---

## Examples

### OGON-U0002 — Panic recovered

```bash
$ curl /orders/42
HTTP/1.1 500 Internal Server Error
Content-Type: application/problem+json
{"type":"OGON-U0002","title":"panic recovered","status":500,
 "detail":"nil pointer dereference","trace_id":"abc..."}
```

The supervisor logs the stack to stderr; the client sees only the
trace id (no stack in the response).

### OGON-U0020 — Deploy verify failed

```bash
$ ogon deploy --cloud aws
[OGON-U0020] deploy verify failed
  what:    readiness probe did not return 200 within 60s
  why:     the new revision did not become ready
  fix:     check `ogon logs --follow`; the pipeline will now rollback if --rollback was passed
```

### OGON-U0021 — Rollback done

```bash
[OGON-U0021] rollback done
  what:    the previous release (v1.2.3) is restored
  why:     OGON-U0020 fired with --rollback
  docs:    docs/errors/OGON-U.md
```

### OGON-U0030 — Log tailer not wired

```bash
$ ogon logs --follow
{"command":"ogon logs","status":"ok","data":{"follow":true},
 "diagnostics":[{"code":"OGON-U0030","title":"log tailer not wired","severity":"info",
   "what":"dev log shipping ships later"}]}
```

### OGON-U0040 — Generator write-path not wired

```bash
$ ogon gen resource User
✓ gen resource User
  create models/User.go
  ...
{"diagnostics":[{"code":"OGON-U0040","title":"generator write-path not wired",
  "severity":"info","what":"planned; deterministic writer ships in a later phase"}]}
```

This is a non-blocking info: the plan was emitted; the actual writer is
on the roadmap.

---

## Remedy

1. **For U0001 / U0002** — inspect the trace id in `ogon logs --follow`
   (when wired); check the structured log line that includes the
   stack.
2. **For U0010** — see [docs/guides/realtime.md](../guides/realtime.md).
3. **For U0020** — check the new revision's pod logs; the new
   revision did not become ready in the verify window.
4. **For U0021** — automatic rollback; read the original U0020 to
   find the failure cause.
5. **For U0030 / U0031 / U0040** — `info` severity; non-blocking.
   The feature ships in a later phase.

---

## Escape

- **Custom panic handler**: `http.Server.SetPanicHandler(fn)` lets you
  render a custom 500 page; the supervisor still logs the stack.
- **Bigger verify window**: `infra.deploy.verify_timeout` in
  `ogon.yaml` raises the U0020 window.
- **Manual rollback**: `ogon deploy --rollback --to v1.2.3` rolls
  back to a specific version, not just the previous one.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| `OGON-U0002` on a route that worked yesterday          | A recent change introduced a nil deref; check the trace id in logs. |
| `OGON-U0020` only in staging                          | Staging has stricter probes / fewer resources; widen `infra.deploy.verify_timeout` or check the probe path. |
| `OGON-U0030` on `ogon logs`                           | Expected; the tailer ships later. Pipe `docker logs` for now. |
| `OGON-U0040` on `ogon gen resource`                   | Expected; the writer ships later. The plan is authoritative.   |

---

Next: [OGON-G generation conflict](./OGON-G.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

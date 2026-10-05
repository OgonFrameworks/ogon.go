# Error class `OGON-S` — Security errors

> Diagnostics that fire when an authz check fails, a CSRF token is
> missing, MFA is required, or a PII redaction rule is violated.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-S` covers the `auth` and `authz` subsystems at runtime. They are
HTTP-status diagnostics — `401`, `403`, `429`, `451` — surfaced via
RFC 9457 ProblemDetails. The `auth/pii` linter also raises `OGON-S` at
authoring time (a developer mistake, not a runtime attack).

| Code         | Title                                | HTTP | When                                                                  |
|--------------|--------------------------------------|------|------------------------------------------------------------------------|
| OGON-S0001   | Session expired                      | 401  | Idle or absolute timeout fired.                                         |
| OGON-S0002   | Invalid credentials                  | 401  | Email/password mismatch, OAuth state mismatch, etc.                    |
| OGON-S0003   | CSRF token missing / invalid         | 403  | The form / JSON did not include the csrf token returned by login.       |
| OGON-S0004   | MFA required                          | 401  | The route is in `auth.mfa.required_for`; the session has not stepped up.|
| OGON-S0005   | Unsanitized content                  | 500  | The UI renderer received user content that escaped sanitization.       |
| OGON-S0006   | PII in span attr / log field         | 500  | The redaction lint caught a forbidden attr / field name at build time.  |
| OGON-S0007   | Forbidden (RBAC)                      | 403  | `authz.Require(...)` denied the route.                                  |
| OGON-S0008   | Rate limited                          | 429  | `auth.ratelimit` tripped.                                              |
| OGON-S0009   | Account locked                         | 423  | `auth.lockout` tripped (N failures in window).                          |
| OGON-S0010   | Secret not found                      | 500  | The configured secret backend could not resolve the key.                 |

---

## When

`OGON-S` fires:

- at runtime on protected routes (auth / CSRF / MFA / RBAC);
- at runtime on rate-limited / lockout paths;
- at authoring time when the PII lint runs (`ogon lint`).

---

## Examples

### OGON-S0002 — Invalid credentials

```bash
$ curl -X POST /login -d '{"email":"a@b.c","password":"x"}'
HTTP/1.1 401 Unauthorized
Content-Type: application/problem+json
{"type":"OGON-S0002","title":"invalid credentials","status":401,
 "detail":"email or password is wrong"}
```

### OGON-S0003 — CSRF token missing

```bash
$ curl -X POST /users -d '{"email":"a@b.c"}'
HTTP/1.1 403 Forbidden
{"type":"OGON-S0003","title":"CSRF token missing","status":403,
 "detail":"include the csrf token returned by /login in the X-CSRF-Token header"}
```

### OGON-S0007 — Forbidden (RBAC)

```bash
$ curl -X DELETE /users/42 -H "Authorization: Bearer $USER_TOKEN"
HTTP/1.1 403 Forbidden
{"type":"OGON-S0007","title":"forbidden","status":403,
 "detail":"role user lacks users:delete"}
```

### OGON-S0009 — Account locked

```bash
$ curl -X POST /login -d '{"email":"a@b.c","password":"x"}'   # 6th attempt
HTTP/1.1 423 Locked
{"type":"OGON-S0009","title":"account locked","status":423,
 "detail":"5 failed attempts in 15m; locked for 15m","retry_after":900}
```

### OGON-S0006 — PII in span attr (authoring time)

```bash
$ ogon lint
[OGON-S0006] PII in span attr
  what:    span.SetAttributes("user.email", user.Email)
  why:     attr name contains denylist substring "email"
  where:   routes/orders.go:42
  fix:     use a safe attr name (e.g. user.id, user.role); the value will be redacted at runtime
```

---

## Remedy

- **For S0001 / S0002** — re-authenticate. The fix is client-side.
- **For S0003** — include `X-CSRF-Token: <token>` on every mutating
  request; the token is returned by `/login`.
- **For S0004** — call `/mfa/step-up` first; the session then carries
  the elevated claim.
- **For S0005 / S0006** — these are developer mistakes. Fix the code;
  re-run `ogon lint` / `ogon check`.
- **For S0007** — grant the role the required permission in
  `authz/policy/*.policy`, or use a different account.
- **For S0008** — back off; respect `Retry-After`.
- **For S0009** — wait `Retry-After` seconds; the lockout auto-expires.
- **For S0010** — `ogon infra secrets set <NAME> ...`.

---

## Escape

- **Disable CSRF on a specific route**: `http.Register(...,
  http.SkipCSRF())`. Rare; document the reason in the route's doc.
- **Custom RBAC**: implement `authz.Decider`; `authz.Require` uses
  the configured decider.
- **Skip MFA**: remove the route from `auth.mfa.required_for`. The
  diagnostic then never fires.
- **Loosen PII lint**: there is no flag. If a name is wrongly on the
  denylist, file an issue; the denylist is a contract.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| `OGON-S0001` immediately after login                 | Session idle timeout is set too low; raise `auth.session.idle_timeout`. |
| `OGON-S0003` on JSON POST                             | The CSRF token must be in `X-CSRF-Token`, not the body.       |
| `OGON-S0004` after step-up                            | The step-up claim has a TTL; check `auth.mfa.step_up_ttl`.    |
| `OGON-S0006` on a safe attr name                      | The name contains a denylist substring; rename.               |
| Rate limit on localhost                               | `auth.rate_limit.per_ip` is too low for dev; override in `ogon.dev.yaml`. |

---

Next: [OGON-U runtime errors](./OGON-U.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

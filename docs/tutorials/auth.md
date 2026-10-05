# Tutorial — Wire authentication (session + passkey)

> **Goal**: with one command (`ogon gen auth`), ship working login,
> logout, and passkey enrollment on top of an existing CRUD resource.
> You will write **zero** security plumbing.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

`ogon gen auth --flows session,passkey` writes:

- `models/User.go` and `models/Credential.go` (WebAuthn passkey storage);
- `routes/auth.go` — `/login`, `/logout`, `/passkey/register`,
  `/passkey/begin`, `/passkey/finish`;
- `handlers/auth.go` — handlers wired to `auth/session` and
  `auth/passkey`;
- migrations for both tables;
- the CSRF middleware (already in the chain — just confirmed);
- a test that proves a role-gated route returns 403 ProblemDetails.

You then add a single line — `auth.RequireRole("admin")` — to gate any
route. The middleware chain (CSRF, rate-limit, lockout, PII redaction)
is already in the default order; see `ogon explain route` for the
deterministic chain.

## When

Use `ogon gen auth` when:

- you need login/logout on day one;
- you want passkeys (WebAuthn) without reading the WebAuthn spec;
- you want CSRF, rate-limit, lockout, PII redaction, and session
  rotation prewired with safe defaults;
- you want `OGON-S0001` style diagnostics for security failures
  (locked-out account, weak password, expired token).

If you only need API keys (machine-to-machine), use `ogon gen auth
--flows apikey` instead. If you need OAuth/OIDC, layer it on with
`ogon add oauth` after.

## Quickstart

```bash
ogon new app --template standard --git
cd app
ogon gen resource User --dry-run   # the model the auth flows attach to
ogon gen resource User
ogon gen auth --flows session,passkey --dry-run
ogon gen auth --flows session,passkey
ogon migrate run
ogon test
```

The plan output:

```
gen auth --flows session,passkey
  create models/User.go            (extends the existing User)
  create models/Credential.go
  create routes/auth.go
  create handlers/auth.go
  create migrations/0002_auth.up.sql
  create migrations/0002_auth.down.sql
  create auth_test.go              (login + passkey enrollment + 403 matrix)
  patch  ogon.yaml                 (auth.session.secret_env, auth.passkey.rp_id)
```

The `patch` line means `ogon gen auth` updated `ogon.yaml` to add
`auth.session.secret_env: OGON_SESSION_SECRET` and
`auth.passkey.rp_id: localhost` (dev). The CLI never writes the secret
itself — that's an env var.

### Generate a session secret

```bash
openssl rand -hex 32 | tee .env.local
# OGON_SESSION_SECRET=...
```

`.env.local` is loaded by the dev supervisor automatically.

### Add a role-gated route

```go
// routes/admin.go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Admin route registration with role gate.

package routes

import (
    "net/http"

    "github.com/OgonFrameworks/ogon.go/auth"
    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

func init() {
    ogonhttp.Register("GET /admin", auth.RequireRole("admin")(admin))
}

func admin(c *ogonhttp.Ctx) error {
    return c.JSON(http.StatusOK, map[string]string{"hello": "admin"})
}
```

A request without the `admin` role returns:

```json
HTTP/1.1 403 Forbidden
Content-Type: application/problem+json

{
  "type": "https://ogongo.dev/errors/OGON-S0003",
  "title": "Forbidden",
  "status": 403,
  "detail": "role 'admin' required",
  "ogon-code": "OGON-S0003",
  "ogon-remedy": "log in as a user with role=admin; see /login"
}
```

### Enroll a passkey

The browser-side flow is the standard WebAuthn ceremony. The CLI
generated `/passkey/begin` and `/passkey/finish`; you call them from
your UI. A working example lives in
[`examples/auth`](../examples/auth) (see [Examples](../examples.md)).

## Config

`ogon.yaml` after `ogon gen auth`:

```yaml
auth:
  session:
    secret_env: OGON_SESSION_SECRET
    cookie_name: ogon_sess
    max_age: 720h            # 30 days
    secure: true             # prod only; dev auto-disables

  passkey:
    rp_id: localhost         # prod: your domain
    rp_name: "My App"
    timeout: 300s

  rate_limit:
    per_ip: 60/min
    per_user: 5/min          # /login

  lockout:
    threshold: 5
    window: 5m
    duration: 15m
```

Every key is documented in `ogon explain config`. Override per-env in
`ogon.<env>.yaml`.

## Test

The generated `auth_test.go` covers:

1. Login with correct password -> 200, `Set-Cookie`.
2. Login with wrong password -> 401, `OGON-S0002`, lockout counter
   increments.
3. 5 wrong attempts -> 423, `OGON-S0004` (locked out).
4. Login then `GET /admin` without role -> 403, `OGON-S0003`.
5. Login as admin then `GET /admin` -> 200.
6. Passkey enrollment end-to-end (in-process WebAuthn mock).

```bash
ogon test -run TestAuth
```

For matrix testing across roles x routes, use `test.NewAuthzMatrix` —
it generates a table from your `routes/*.go` `auth.RequireRole(...)`
calls.

## Prod

Before deploying:

1. Rotate `OGON_SESSION_SECRET` (32 hex bytes minimum).
2. Set `auth.passkey.rp_id` to your real domain (e.g. `app.example.com`).
3. Set `auth.session.secure: true` (it defaults to false in dev).
4. Run `ogon doctor` to confirm no missing env vars.
5. `ogon deploy --cloud aws` — the deploy pipeline injects the secret
   from the configured secret manager (AWS SSM, GCP Secret Manager).

## Escape

- **Custom session store**: implement `auth/session.Store` and set
  `auth.session.store: custom` in `ogon.yaml`.
- **JWT instead of sessions**: `ogon gen auth --flows jwt` writes token
  issue/verify handlers; you bring the JWKS endpoint.
- **Bypass the middleware chain**: `Server.Handler()` returns the raw
  `http.Handler` — useful for embedding OgonGo behind another gateway.
- **Bring your own RBAC**: implement `authz.Policy` and replace the
  default RBAC engine.

## Troubleshoot

| Symptom                                                | Fix                                                              |
|--------------------------------------------------------|------------------------------------------------------------------|
| `OGON-K0005: missing secret` on boot                   | `OGON_SESSION_SECRET` env var not set; `ogon doctor` shows it.   |
| `OGON-S0001: CSRF token missing`                       | The browser did not send the CSRF cookie; check `SameSite=Lax`.  |
| `OGON-S0002: invalid credentials`                      | Wrong password; check the lockout counter before retrying.       |
| `OGON-S0004: locked out`                               | Wait 15m (default `lockout.duration`), or use `ogon jobs run --kind unlock`. |
| Passkey enrollment: `SecurityError: RP ID mismatch`    | `auth.passkey.rp_id` does not match the browser's domain.        |
| `OGON-S0005: passkey challenge expired`                | User took >`passkey.timeout`; restart the ceremony.              |

---

Next: [Realtime guide](../guides/realtime.md), [Deploy guide](../guides/deploy.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

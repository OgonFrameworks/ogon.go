# Auth guide — `ogon gen auth`, sessions, RBAC, MFA

> **Goal**: ship login, logout, RBAC, and MFA end-to-end.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

The `auth` package is the security spine of an OgonGo service:

- `auth/session` — sessions with rotation, fixation defense, idle + absolute
  timeouts.
- `auth/token` — JWT / Opaque token issue + verify, JWKS, key rotation.
- `auth/password` — argon2id hashing with policy enforcement.
- `auth/passkey` — WebAuthn / passkey enrollment and authentication.
- `auth/oauth` — OAuth2 providers (Google, GitHub, ...).
- `auth/mfa` — TOTP, recovery codes, SMS step-up.
- `auth/fips` — FIPS-mode crypto entry points.
- `auth/ratelimit` and `auth/lockout` — per-IP and per-account attack
  mitigation.
- `auth/pii` — PII redaction for logs and traces.
- `auth/csrf`, `auth/csp` — CSRF token + Content-Security-Policy
  middleware.
- `authz` — RBAC + tenant scoping + policy DSL.
- `authz/policy` — declarative policy language.

`ogon gen auth <provider>` scaffolds the wiring for one provider.

## When

Use `ogon gen auth` when:

- you need login / logout (the most common case);
- you have multiple auth providers (email+password, OAuth, passkey);
- you want RBAC or tenant scoping from day one;
- you want MFA for elevated actions.

For a single hardcoded admin user with HTTP Basic, do not generate the
auth scaffold — `auth/session` + a manual middleware is enough.

## Quickstart

```bash
ogon new saas --template modular --git
cd saas

ogon gen auth email --dry-run
ogon gen auth email            # writes auth/email.go

ogon gen auth oauth --dry-run
ogon gen auth oauth            # writes auth/oauth/<provider>.go

ogon gen auth passkey --dry-run
ogon gen auth passkey
```

Wire a login route:

```go
package routes

import (
    "net/http"

    "github.com/OgonFrameworks/ogon.go/auth/session"
    "github.com/OgonFrameworks/ogon.go/http"
)

func init() {
    http.Register("POST /login",  login)
    http.Register("POST /logout", logout)
}

func login(c *http.Ctx) error {
    var body struct {
        Email    string `json:"email"`
        Password string `json:"password"`
    }
    if err := c.Bind(&body); err != nil {
        return err
    }

    user, ok, err := verifyCredentials(c, body.Email, body.Password)
    if err != nil {
        return err
    }
    if !ok {
        // ratelimit increments; lockout triggers after N fails
        return c.Problem(http.StatusUnauthorized, "OGON-S0002",
            "invalid credentials")
    }

    // issue session; rotation + fixation defense handled by auth/session
    sess, err := session.Issue(c, user.ID)
    if err != nil {
        return err
    }
    return c.JSON(http.StatusOK, map[string]string{
        "session": sess.Token,
        "csrf":    sess.CSRFToken,
    })
}

func logout(c *http.Ctx) error {
    return session.Revoke(c)
}
```

Protect a route with RBAC:

```go
http.Register("DELETE /users/{id}", authz.Require("users:delete", handlers.Delete))
```

## Config

`ogon.yaml`:

```yaml
auth:
  session:
    secret_env: OGON_SESSION_SECRET   # required; never inline
    idle_timeout: 30m
    absolute_timeout: 8h
    cookie:
      secure: true
      http_only: true
      same_site: lax
  password:
    algorithm: argon2id
    min_length: 12
    max_length: 128
  rate_limit:
    per_ip: 10/min
  lockout:
    threshold: 5
    duration: 15m
  oauth:
    providers:
      google:
        client_id_env:     GOOGLE_CLIENT_ID
        client_secret_env: GOOGLE_CLIENT_SECRET
      github:
        client_id_env:     GITHUB_CLIENT_ID
        client_secret_env: GITHUB_CLIENT_SECRET
  mfa:
    required_for:
      - "users:delete"
      - "billing:change_plan"
    totp:
      issuer: "My Saas"
  fips: false                    # set true for FIPS-mode crypto
```

Inspect: `ogon inspect config`. Validate: `ogon check`.

## Test

The `test` package ships auth + authz fixtures:

```go
func TestDeleteUserRequiresAdmin(t *testing.T) {
    app := test.NewApp(t, http.Handler())
    defer app.Close()

    // role fixtures
    app.RegisterRole("admin", "admin@example.com", map[string]any{"sub": "u_42"})

    // unauthenticated
    r := app.Recorder().Delete("/users/1")
    r.AssertStatus(t, http.StatusUnauthorized)

    // authenticated but not authorized
    r = app.Recorder().As("user").Delete("/users/1")
    r.AssertStatus(t, http.StatusForbidden)

    // admin: ok
    r = app.Recorder().As("admin").Delete("/users/1")
    r.AssertStatus(t, http.StatusNoContent)
}
```

RBAC matrix tests — generate a TSV matrix for PR review:

```go
mx := test.AuthzMatrix{}
mx.AddRole("admin",  []string{"/users","POST /users","DELETE /users/{id}"})
mx.Set("admin", "DELETE /users/{id}", test.AuthzAllow)
test.AssertMatrix(t, mx, http.Handler())
```

Lockout test — hammer the login endpoint and assert the threshold:

```go
for i := 0; i < 5; i++ {
    r := app.Recorder().Post("/login", test.JSON(`{"email":"a@b.c","password":"x"}`))
    r.AssertStatus(t, http.StatusUnauthorized)
}
r := app.Recorder().Post("/login", test.JSON(`{"email":"a@b.c","password":"x"}`))
r.AssertStatus(t, http.StatusTooManyRequests)   // lockout triggered
```

Run: `ogon test --race`.

## Prod

- Set `OGON_SESSION_SECRET` (32+ random bytes) via the platform secret
  store. `ogon doctor` checks this.
- Set the OAuth provider envs via `ogon infra secrets set`.
- Enable FIPS mode if regulated: `auth.fips: true` (selects the
  FIPS-validated crypto primitives).
- Configure MFA: TOTP with recovery codes, optional SMS step-up for
  elevated actions.
- Set `cookie.secure: true` (default) — the dev server allows insecure
  cookies for localhost only.

```bash
ogon infra secrets set OGON_SESSION_SECRET --generate 32
ogon infra secrets set GOOGLE_CLIENT_ID     --from-literal "$GID"
ogon deploy --cloud aws
```

## Escape

- **Bring your own session store**: implement `auth/session.Store`; the
  default is in-process for dev and `record`-backed for prod.
- **Custom password hasher**: implement `auth/password.Hasher`; the
  default is argon2id.
- **Policy DSL**: write `.policy` files under `authz/policy/` for
  attribute-based access control; `ogon check` compiles them.
- **Manual middleware**: bypass `authz.Require` and write your own
  `http.Middleware` if your authz is exotic.
- **FIPS escape**: `auth/fips` exposes primitives; the non-FIPS path
  remains the default unless the env requires otherwise.

## Troubleshoot

| Symptom                                            | Fix                                                            |
|----------------------------------------------------|----------------------------------------------------------------|
| `OGON-S0002: invalid credentials`                  | Ratelimit increments; lockout triggers after `auth.lockout.threshold`. |
| `OGON-S0001: session expired`                       | Idle timeout fired; re-authenticate.                            |
| `OGON-S0003: CSRF token missing`                   | The form / JSON must include the `csrf` token returned by login. |
| `OGON-S0004: MFA required`                          | The route is in `auth.mfa.required_for`; step up first.         |
| `OGON-K0005: secret missing`                        | `*_env` key referenced an unset env; `ogon doctor` shows which. |
| `ogon gen auth email` wrote to an unowned file      | The file existed without the "Code generated" header; `--force`.|
| Login works locally but fails in prod              | `cookie.secure: true` requires HTTPS; check the LB termination.|

---

Next: [Realtime guide](./realtime.md), [Full-stack guide](./fullstack.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

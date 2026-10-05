# Security guide

> **Goal**: the security posture of an OgonGo project — what is on by
> default, what to turn on for prod, how to report a vulnerability.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo ships secure-by-default (Part VIII.2). The defaults:

- **HTTPS-only cookies** in prod (dev auto-disables for `localhost`).
- **CSRF** middleware in the chain (double-submit cookie + SameSite=Lax).
- **CSP** with `nonce-` for scripts and styles; fail closed on rand error.
- **HSTS**, `X-Content-Type-Options: nosniff`, `X-Frame-Options:
  DENY`, `Referrer-Policy: strict-origin-when-cross-origin`.
- **Rate limiting** per-IP (60/min default) and per-user (5/min on
  `/login`).
- **Lockout** after 5 failed logins in 5 minutes (15-minute lockout).
- **Argon2id** for password hashing (memory=64 MiB, iterations=3,
  parallelism=2).
- **PII redaction** in logs, traces, and metrics (email, SSN, CC,
  JWT, UUID, phone, IPv4, API key).
- **FIPS mode** opt-in (`GOEXPERIMENT=boringcrypto` build).
- **Supply-chain audit** — every dep is on the allowlist (Part 0-A);
  `ogon doctor` checks for advisories.

## When

Read this page when:

- you are taking a project to prod;
- you need to know if a behavior is a vulnerability (file a report,
  do not open a public issue);
- you want to tune the defaults (rate limit, lockout, CSP).

## Quickstart

For dev, the defaults are already on. For prod:

```bash
ogon doctor --prod
# -> check: session secret       ok
# -> check: passkey rp_id        ok
# -> check: TLS                  ok
# -> check: rate limit           ok
# -> check: PII redaction        ok
```

Any failing check prints a remedy.

## Config

```yaml
auth:
  session:
    secret_env: OGON_SESSION_SECRET
    cookie_name: ogon_sess
    max_age: 720h
    secure: true              # prod only
    same_site: lax

  passkey:
    rp_id: app.example.com    # prod: your real domain
    rp_name: "My App"
    timeout: 300s

  rate_limit:
    per_ip: 60/min
    per_user: 5/min

  lockout:
    threshold: 5
    window: 5m
    duration: 15m

  csp:
    script_src: ["'self'", "'nonce-{nonce}'"]
    style_src:  ["'self'", "'nonce-{nonce}'"]
    img_src:    ["'self'", "data:"]
    frame_ancestors: ["'none'"]

obs:
  redaction:
    enabled: true
    shapes: [email, ssn, cc, jwt, uuid, phone, ipv4, apikey]

  pprof:
    enabled: false           # default; enable only with a bearer token
```

## Test

The auth package ships with security tests:

- `auth_security_test.go` — lockout, rate limit, CSRF, session
  fixation, open redirect, SSRF, XSS, path traversal, SQL injection,
  deserialization, upload.
- `obs/pii_lint_test.go` — authoring-time PII lint (catches new PII
  shapes before they ship).
- `test.RedactionCorpus()` — runtime redaction corpus; every PII shape
  must be redacted in logs, traces, metrics.

Run:

```bash
ogon test -run TestSecurity
ogon test -run TestPII
```

## Prod

Before deploying:

1. Rotate `OGON_SESSION_SECRET` (32 hex bytes minimum).
2. Set `auth.passkey.rp_id` to your real domain.
3. Set `auth.session.secure: true`.
4. Set `obs.pprof.enabled: false` (default) or true with a bearer
   token.
5. Run `ogon doctor --prod`.
6. `ogon deploy --cloud aws`.

## Escape

- **Disable CSRF** (not recommended): `auth.csrf.enabled: false`.
- **Custom CSP**: edit `auth.csp.*` in `ogon.yaml`.
- **Bring your own rate limiter**: implement `auth.RateLimiter` and
  set `auth.rate_limit.driver: custom`.
- **FIPS build**: `GOEXPERIMENT=boringcrypto go build` (AT-023).

## Reporting a vulnerability

**Do not open a public issue.** Email **security@ogongo.dev** with:

1. A description of the issue and its impact.
2. A minimal repro (a single `.go` file + the `ogon` command that
   triggers the issue is ideal).
3. Theorised affected versions (`ogon --version`).
4. Your preferred disclosure timeline.

Acknowledgement within 48 hours. See
[`SECURITY.md`](../SECURITY.md) for the full policy.

## Troubleshoot

| Symptom                                                | Fix                                                              |
|--------------------------------------------------------|------------------------------------------------------------------|
| `OGON-S0001: CSRF token missing`                       | Browser did not send the CSRF cookie; check SameSite=Lax.        |
| `OGON-S0002: invalid credentials`                      | Wrong password; lockout counter increments.                      |
| `OGON-S0004: locked out`                               | Wait 15m or use `ogon jobs run --kind unlock`.                   |
| Passkey: `SecurityError: RP ID mismatch`               | `auth.passkey.rp_id` does not match the browser's domain.        |
| `ogon doctor --prod` fails                             | Read the per-check remedy; rotate the secret, set the rp_id.     |

---

See also: [`SECURITY.md`](../SECURITY.md), [`BUG-BOUNTY.md`](../BUG-BOUNTY.md),
[Auth tutorial](./tutorials/auth.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

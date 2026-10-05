# Security Policy

## Supported versions

OgonGo ships one stable line at a time. Security fixes are backported to the
current `1.x` line; we do not patch `0.x` previews after the first stable
release.

| Version | Supported          | Until              |
|---------|--------------------|--------------------|
| 1.0.x   | :white_check_mark: | end of 1.x line    |
| < 1.0   | :x:                | — (previews)       |

A new minor release does **not** deprecate the previous minor; both receive
security fixes until the next minor ships. Major releases (e.g. `2.0.0`)
deprecate the previous major after a 6-month overlap.

## Reporting a vulnerability

**Do not open a public issue for a suspected security vulnerability.**

Email **security@ogongo.dev** with:

1. A description of the issue and its impact.
2. A minimal repro (a single `.go` file + the `ogon` command that triggers
   the issue is ideal).
3. Theorised affected versions (`ogon --version`).
4. Your preferred disclosure timeline.

You will receive an acknowledgement within **48 hours**. If you do not,
please escalate by contacting the maintainers via the
[community channels listed in the README](./README.md#documentation).

### What we treat as a vulnerability

- Auth bypass (session fixation, JWT confusion, OAuth redirect
  hijacking, passkey replay).
- SQL injection (any `record` driver path that escapes sanitization).
- XSS / template injection in the `ui` subsystem.
- Path traversal in `ogon new` / `ogon gen` / `ogon deploy`.
- PII leakage through metrics, traces, or logs (the `obs` redaction
  corpus is the contract; bypassing it is a vulnerability).
- Migration-data loss (`ogon migrate run` destroys data without `--yes`).
- Supply-chain compromise in any module shipped under
  `github.com/OgonFrameworks/ogon.go/<subsystem>/*`.

### What we do **not** treat as a vulnerability

- A `ogon` command that writes files when run with the documented
  mutating flag (e.g. `ogon gen route` without `--dry-run`) — that is
  documented behavior.
- A panic from a clearly malformed input where the contract requires
  validation upstream (file a bug instead).
- Performance regressions (file an issue with a benchmark).
- Standard `go vet` / `golangci-lint` warnings.

## Response timeline

| Step                              | Target                    |
|-----------------------------------|---------------------------|
| Acknowledge receipt               | 48 hours                  |
| Triage & confirm vulnerability    | 5 business days          |
| Coordinate fix & embargo          | up to 14 days             |
| Patch release                     | 30 days (severity-permitting) |
| Public disclosure (with credit)   | 24 hours after release    |

Critical (RCE / auth bypass) issues trigger an emergency patch within
7 days. Lower-severity issues can ride the next minor release.

## Disclosure

We credit reporters by name (or handle) in `CHANGELOG.md` unless they
request anonymity. Coordinated disclosure follows the standard
[disclosure.io](https://www.disclosure.io/) practice: embargo lifts
24 hours after the patched release ships.

## Security review

Every release is reviewed against:

- The `obs` PII redaction corpus (every PII shape the corpus knows must
  be redacted in logs, traces, and metrics).
- The `auth` subsystem's lockout, rate-limit, and FIPS surface.
- The `record` migration safety gate (`OGON-M0001` for destructive ops).
- The `jobs` payload-key encryption (`jobs.PayloadKey` AES-256-GCM).

Run `ogon doctor` to surface environment-side security drift (missing
Go version, missing golangci-lint, etc.).

## Contact

- **Vulnerability reports**: security@ogongo.dev (PGP key in
  [`SECURITY.md` GPG block below — to be published with the first
  stable release](#)).
- **Conduct reports**: conduct@ogongo.dev
- **General security questions**: open a discussion at
  <https://github.com/OgonFrameworks/ogon.go/discussions>.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

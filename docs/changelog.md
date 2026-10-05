# Changelog (docs mirror)

> The canonical changelog lives at
> [`/CHANGELOG.md`](../../CHANGELOG.md). This page is the docs-side
> mirror so the docs site search indexes it.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo's changelog follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Releases are credited per the security policy: reporters are named
unless they requested anonymity.

## When

- You want to know what changed between two versions.
- You are reviewing a PR that bumps the version.
- You are writing an upgrade plan (`ogon update`).

## Quickstart

```bash
# read the full changelog
cat CHANGELOG.md

# or browse it on the docs site
ogon docs changelog

# diff two versions
ogon update --from v0.9.0 --to v1.0.0 --diff-only
```

## Config

No config — the changelog is a markdown file at the repo root.

## Test

The release tooling (`ogon infra release`) generates the changelog
entry from the conventional-commit log between two tags. CI asserts
that the changelog has an entry for the current version before a
release ships (DX-022).

## Prod

Every release ships a changelog entry. Major releases also ship a
codemod (see [upgrade.md](./upgrade.md)).

## Escape

- **Skip the changelog**: not allowed for releases; allowed for
  internal previews.
- **Custom format**: the changelog format is fixed; do not deviate.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| Changelog is missing the current release | Run `ogon infra release --regen-changelog`.                  |
| Codemod link is broken                 | File a docs bug; the link is auto-generated.                   |

---

See also: [`/CHANGELOG.md`](../../CHANGELOG.md), [Upgrade guide](./upgrade.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

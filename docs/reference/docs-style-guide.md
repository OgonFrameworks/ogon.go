# Docs style guide

> **Goal**: the rules every OgonGo markdown file follows. Enforced by
> the docs lint (DX-032) and reviewers.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

Every OgonGo markdown file (`.md`):

- **Ends with the MIT license footer**:
  ```markdown
  <!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->
  ```
- **Follows the standard feature-page sections** (DOC-018), in order:
  1. **What** — one paragraph.
  2. **When** — when to use this; when not to.
  3. **Quickstart** — the smallest copy-pasteable snippet that works.
  4. **Config** — the `ogon.yaml` keys this feature reads.
  5. **Test** — how to test it.
  6. **Prod** — prod-specific concerns.
  7. **Escape** — escape hatches.
  8. **Troubleshoot** — a table of symptom → fix.
- **Uses Mermaid** for diagrams (DOC-019); no ASCII art for structure.
- **Uses tables** for tabular data; **code blocks** for code; **lists**
  for steps.
- **No marketing** (Part 0 rule 12).
- **Code blocks** are complete and copy-pasteable; never partial
  snippets that need context the reader does not have.
- **Links** are relative within the docs tree; absolute URLs only for
  external sites.
- **Headers** are sentence case; one H1 per page; H2 for the eight
  standard sections; H3 for sub-sections.
- **Tables** have a header row; columns are aligned with `|`.
- **No emojis** unless the user explicitly requests them.

## When

- Before writing a new docs page.
- Before reviewing a docs PR.
- When the docs lint (DX-032) fails.

## Quickstart

```bash
# lint the docs (links + headings + footer)
ogon docs lint
# -> check: links         ok  (all 142 links resolve)
# -> check: headings      ok  (all pages have 8 standard sections)
# -> check: footer        ok  (all pages end with the MIT footer)
# -> 3 ok, 0 fail

# auto-fix what's fixable
ogon docs lint --fix
```

## Config

No config — the style is fixed.

## Test

DX-032: docs lint (links / headings / footer) runs in CI on every PR.
A failing check fails the build.

DX-031: snippet compile tests extract every Go code block from the
docs and run `gofmt` + `go build` on them.

## Prod

The docs site (built by `ogon docs build`) renders every page with
Pagefind search (DOC-001, DOC-015).

## Escape

- **Skip a section**: not allowed for feature pages; allowed for
  changelog entries and ADRs.
- **Custom diagram tool**: only Mermaid is rendered; do not embed
  images of diagrams.

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon docs lint` reports broken link   | Fix the link; the lint is non-negotiable.                      |
| A page is missing a standard section   | Add it; the linter refuses to merge without all 8.             |
| A code block fails to compile (DX-031) | Fix the snippet; do not weaken the linter.                     |

---

See also: [Style guide](../style-guide.md), [Contributing](../contributing.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

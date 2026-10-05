# ADR 0003 — UI architecture: server-held state + tiny JS runtime

- **Status**: Accepted
- **Date**: 2026-09-29
- **Decision owner**: OgonFrameworks core team
- **Supersedes**: none
- **Superseded by**: none

## Context

OgonGo needs a UI subsystem that ships:

- server-side rendering (SSR) by default;
- progressive enhancement (works without JS, better with JS);
- form validation that does not duplicate the server contract;
- realtime updates (live data) without an SPA;
- the smallest JS payload on the web.

The dominant models today:

1. **SPA + hydration** (React, Vue, Svelte): the server ships JSON
   + a large JS bundle; the browser re-renders; hydration mismatch
   bugs are common; the JS payload grows over time.
2. **MPA + Turbo / HTMX**: the server ships full HTML; a small JS
   runtime swaps the body; no client-side component state. The
   payload stays small, but the UX can feel like full-page reloads.
3. **Server components (RSC)**: the server renders components;
   interactivity is split between server and client; the model is
   powerful but complex, and the runtime is non-trivial.

OgonGo's contract:

- "Single static binary" — the JS runtime must be tiny and shipped
  as a static asset, not a per-app build.
- "Latency law" — first render must be server-side; the client
  should not block on JS to render the page.
- "Server is the truth" — no client-side component state that the
  server cannot see; no optimistic UI that disagrees with the
  server's response.
- "No build step on the dev machine" — `ogon dev` must hot-reload
  `.ogon` files without esbuild / Vite / Webpack.

## Decision

OgonGo ships a **server-held state + tiny JS runtime** architecture:

1. **Components are Go functions** that return `ui.Node` (a
   virtual-DOM-like tree). They render server-side to HTML.
2. **`.ogon` files** are compiled to Go + a small per-component JS
   fragment by the `ui/compiler`. The compiler runs in `ogon dev`
   and `ogon build`, not in the developer's editor.
3. **The JS runtime** (`ui/runtime.js`) is ~5KB min+gzip. It does
   three things:
   - **event delegation**: subscribe to clicks / submits on
     `document`; route them to the server as intents via `fetch`
     (`POST` to the action URL with the form data).
   - **diff + swap**: the server responds with an HTML fragment;
     the runtime diffs the current DOM and swaps only the changed
     nodes; focus is preserved on the input that had the error.
   - **live subscriptions**: open a single SSE / WS connection to
     `/live`; the runtime updates any element that declared
     `data-live="<topic>"` when the server pushes.
4. **The server holds the truth**: every interactive element
   declares its action URL (`data-action="submit" data-event="..."`
   or `data-action="click" data-event="..."`). The runtime sends
   the intent; the server renders the response; the runtime
   applies the diff. There is **no client-side component state**.
5. **Sanitizer** (`ui/sanitize`) runs server-side on every render;
   the runtime does not inject HTML that has not been sanitized.

### The contract

- **No client-side component state.** The client only sends intents;
  the server renders the response.
- **First render is server-side.** The page is meaningful without
  JS; JS only enhances.
- **Focus preservation.** When a form re-renders with errors, the
  focus stays on the input that had the error.
- **Realtime via `data-live`.** Any element can subscribe to a
  topic; the runtime swaps its content when the server pushes.
- **Sanitize always.** No raw user content reaches the DOM
  without passing through `ui/sanitize`.

## Alternatives considered

### 1. SPA + React / Vue / Svelte

- **Pro**: rich client-side interactivity; large ecosystem.
- **Con**: large JS payload; hydration mismatch bugs; the server
  is a JSON API; first render blocked on JS; build step on the
  dev machine.

**Rejected** — violates "single static binary", "latency law",
"server is the truth", and "no build step".

### 2. HTMX + Go templates

- **Pro**: simple; tiny JS; MPA model.
- **Con**: no first-class component model; no isomorphic
  components; the form-validation contract is up to the developer;
  no realtime-with-presence first-class support.

**Rejected as the default** — too thin for the framework's
opinionated stance. Users who want HTMX can drop in the HTMX
runtime via the `ui.runtime` escape hatch; the OgonGo runtime is
HTMX-compatible.

### 3. Server components (RSC)

- **Pro**: powerful; the server can split work.
- **Con**: the runtime is non-trivial; the model is complex to
  teach; the JS payload is not tiny.

**Rejected** — overkill for the first release; revisit if the
use-case arises.

### 4. SvelteKit

- **Pro**: small bundles; SSR.
- **Con**: still an SPA at heart; build step on the dev machine;
  two languages (Svelte + Go) for one project.

**Rejected** — violates "no build step" and the single-binary
contract.

## Consequences

- **First render is HTML.** No flash of unstyled content; no
  hydration mismatch; no "loading..." spinner.
- **JS payload is ~5KB.** The runtime is shipped as a static
  asset; per-component fragments are concatenated + minified at
  `ogon build` time.
- **Forms are validated server-side.** The `ogon:` tags on the
  struct are the contract; the server re-renders with errors; the
  client swaps the form; focus is preserved.
- **Realtime is opt-in.** Add `data-live="<topic>"` to any element;
  the runtime subscribes via the `/live` endpoint.
- **The compiler runs in `ogon dev` and `ogon build`.** The
  developer's editor does not need a language server for `.ogon`
  files; the compiler reports errors via `OGON-C0001` on save.
- **The sanitizer is non-negotiable.** `ui.sanitize: false` in
  `ogon.yaml` requires a security review (see
  [SECURITY.md](../../SECURITY.md)).
- **Escape hatches are explicit.** `c.HTML` writes raw HTML; `ui.runtime:
  ""` ships zero JS; a hand-written Go component returns `ui.Node`
  directly.

## Compliance

- `ui/doc.go` documents the contract.
- `ui/compiler` emits `OGON-C0001` on bad `.ogon` syntax.
- `ui/sanitize` runs on every render unless explicitly disabled
  (security-reviewed).
- The runtime is `ui/runtime.js` (~5KB min+gzip).
- `test.NewSnapshot` and `test.A11yConfig` are the CI gates.
- The fullstack template (`ogon new --template fullstack`) ships
  the default component set.

## References

- [PROMPT.md Part IX — UI](../../PROMPT.md)
- [docs/guides/fullstack.md — full-stack guide](../guides/fullstack.md)
- [ADR 0001 — state machine](./0001-state-machine.md)
- [ADR 0002 — DI codegen](./0002-di-codegen.md)

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

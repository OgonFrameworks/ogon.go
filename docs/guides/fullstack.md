# Full-stack guide — `.ogon` components, SSR, hydration, forms

> **Goal**: render server-side, hydrate on the client, and ship forms
> with validation — without React, without a build step on the developer
> machine, and without 200KB of JS.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

OgonGo's UI is server-held state + a tiny JS runtime (see
[ADR 0003](../adr/0003-ui-architecture.md)). The pieces:

- `ui/` — isomorphic component runtime; components are Go functions that
  render server-side and hydrate client-side.
- `ui/compiler` — compiles `.ogon` component files into Go + a tiny JS
  payload at build time. No Webpack, no Vite, no esbuild needed on the
  dev machine.
- `ui/runtime` — the ~5KB JS runtime shipped to the client: event
  delegation, form submission, focus preservation.
- `ui/sanitize` — server-side HTML sanitizer; every render goes through
  it.
- `fullstack/` — the full-stack project template; ships with the
  default component set + a form helper + an SSR middleware.

The contract: **the server owns the truth; the client only sends
intents**. There is no client-side component state that the server
cannot see.

## When

Use the full-stack template when:

- you want server-side rendering without a SPA;
- you want progressive enhancement (works without JS, better with JS);
- you want forms with server-side validation, no client-side duplication;
- you want the smallest JS payload on the web — ~5KB min+gzip.

For a pure API service with no UI, do not pick the fullstack template —
use `standard`.

## Quickstart

```bash
ogon new shop --template fullstack --git
cd shop

ogon gen ui button --dry-run
ogon gen ui button
```

Edit a `.ogon` component:

```html
<!-- ui/components/button.ogon -->
<component name="button" props="label,kind">
  <button class="btn btn-{kind}" data-action="click" data-event="button:click">
    {label}
  </button>
</component>
```

The compiler generates `ui/components/button.go` and a tiny
`ui/components/button.js` fragment. The Go file is what you call from
your handler:

```go
package routes

import (
    "net/http"

    "github.com/OgonFrameworks/ogon.go/http"
    "shop/ui/components"
)

func init() {
    http.Register("GET /", index)
}

func index(c *http.Ctx) error {
    return c.Render(http.StatusOK, components.Button(c, components.ButtonProps{
        Label: "Buy now",
        Kind:  "primary",
    }))
}
```

### Forms

```html
<!-- ui/components/signup_form.ogon -->
<component name="signup_form" props="">
  <form data-action="submit" data-event="signup:submit" method="POST" action="/signup">
    <input name="email" type="email" required />
    <input name="password" type="password" required minlength="12" />
    <button type="submit">Sign up</button>
    {errors.email ? <span class="error">{errors.email}</span> : null}
  </form>
</component>
```

Server handler:

```go
func signup(c *http.Ctx) error {
    var body struct {
        Email    string `ogon:"email;required"`
        Password string `ogon:"required;min_length=12"`
    }
    if err := c.Bind(&body); err != nil {
        // re-render the form with errors — server holds the truth
        return c.Render(http.StatusBadRequest, components.SignupForm(c,
            components.SignupFormProps{Errors: err.Fields}))
    }
    // create user, set session, redirect
    return c.Redirect(http.StatusSeeOther, "/dashboard")
}
```

The tiny JS runtime intercepts the submit, posts as JSON, and re-renders
the form with errors — without losing focus on the input that had the
error. If JS is disabled, the form posts normally and the server renders
the full page.

### SSR

SSR is the default. `c.Render` writes HTML to the response. The dev
supervisor hot-reloads `.ogon` files: change a component, save, refresh,
done. The compiler runs in the dev server, not in your editor.

## Config

`ogon.yaml`:

```yaml
ui:
  components_dir: ui/components
  runtime: ui/runtime.js           # the ~5KB runtime; shipped as-is
  sanitize: true                    # default; never disable in prod
  hot_reload: true                  # dev only
  js_minified: true                 # prod only
  js_integrity: true                # SRI in prod
  features:
    - forms
    - focus_preservation
```

Inspect: `ogon inspect runtime` shows the live component count.

## Test

The `test` package ships an SSE client and HTML snapshot helpers:

```go
func TestSignupReRendersErrors(t *testing.T) {
    app := test.NewApp(t, http.Handler())
    defer app.Close()

    r := app.Recorder().Post("/signup", test.JSON(`{"email":"x","password":"short"}`))
    r.AssertStatus(t, http.StatusBadRequest)

    snap := test.NewSnapshot(t, "signup_error")
    snap.AssertHTML(t, r.Body(), `<span class="error">`)
}
```

A11y smoke test (axe-core / playwright):

```go
test.A11yConfig{
    Spec:  "tests/a11y/signup.spec.ts",
    Pages: []string{"/signup"},
}.Emit(t, ".")
```

Visual regression:

```go
test.VisualSmokeConfig{
    Page:  "/signup",
    Name:  "signup-empty",
}.Emit(t, ".")
```

Run: `ogon test --race`. CI: `ogon test --junit ./build/junit.xml`.

## Prod

```bash
ogon build                          # compiles .ogon → .go + .js (minified)
ogon deploy --cloud aws
```

The deploy pipeline:

1. compiles every `.ogon` file to Go;
2. minifies the per-component JS fragments;
3. concatenates with the runtime into a single `<sha>.js` with SRI;
4. inlines the critical-path CSS for the first render.

Total client payload for a typical form-and-table app: ~10–15KB gzip,
including the runtime.

## Escape

- **Plain HTML**: `c.HTML(http.StatusOK, "<html>...</html>", nil)` writes
  raw HTML — no component, no runtime.
- **Custom sanitizer**: implement `ui.Sanitizer`; the default is
  strict. Loosening it requires an explicit `ui.sanitize: false` in
  `ogon.yaml` (and a security review).
- **Skip the runtime**: set `ui.runtime: ""` to ship zero JS — the
  page works without progressive enhancement.
- **Bring your own JS**: add a `<script>` tag to your layout. The
  OgonGo runtime does not interfere with external JS.
- **Hand-written Go components**: skip `.ogon` and write Go that
  returns `ui.Node` directly. The compiler is a convenience, not a
  runtime dependency.

## Troubleshoot

| Symptom                                                | Fix                                                          |
|--------------------------------------------------------|--------------------------------------------------------------|
| `.ogon` file not picked up by dev server                | Check `ui.components_dir`; save the file; check `ogon dev` log.|
| Form posts without JS but re-render loses focus        | `ui.features.focus_preservation` is off; enable.            |
| `OGON-S0005: unsanitized content`                       | You wrote raw user input into a component; use the `Raw()` escape hatch deliberately. |
| Component renders but no event handlers                | The runtime script tag is missing from the layout; include `{ogon_runtime}`. |
| Hydration mismatch                                     | The server rendered one thing, the client expected another; this means a non-deterministic render. Remove `time.Now()` from the render path. |
| JS payload is 50KB instead of 5KB                      | `ui.js_minified: false` or you imported a heavy component; check `ogon build` output. |

---

Next: [Deploy guide](./deploy.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

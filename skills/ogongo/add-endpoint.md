# add-endpoint

Add a new HTTP endpoint to an OgonGo project that returns JSON and
verify that ogon test passes.

## When to use

- A new HTTP route is required (e.g. "/api/widgets").
- The endpoint returns JSON or HTML.
- The endpoint does NOT require a new model (use add-model for that).

## Preconditions

- .ogon/ogon.json exists. If not, run `ogon agent dump --write`.
- ogon.yaml present at project root.
- No uncommitted changes to generated/.

## Steps

1. Read the manifest:
   `ogon mcp tools/call ogon.inspect {"what":"routes"}`
2. Plan the resource:
   `ogon mcp tools/call ogon.gen {"kind":"resource","name":"Widget"}`
3. Review the plan. The plan is dry-run; no files are written.
4. Apply the plan via the CLI (CI requires --yes):
   `ogon gen resource Widget --yes`
5. Edit app/handlers/widget_handler.go to implement the handler body.
6. Run the pre-commit gate:
   `ogon check`
7. Run affected tests:
   `ogon test --filter ./app/handlers/...`

## Verify

- ogon check exits 0.
- ogon test passes.
- ogon explain route /api/widgets returns the route's handler +
  middleware + generated adapters.
- No file in generated/ is hand-edited.

## Escape hatches

- To register a route manually, add it to app/routes.go; the manifest
  picks it up on the next `ogon agent dump --write`.
- To override generated serialization, implement a custom handler in
  app/handlers/ and let the generated adapter delegate to it.

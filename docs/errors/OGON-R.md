# Error class `OGON-R` — Route errors

> Diagnostics that fire when routes conflict, when a handler signature is
> wrong, or when path / method declarations are malformed.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-R` diagnostics cover the typed router in the `http` package. They
fire at boot (after route registration) and in `ogon routes check`.

| Code         | Title                                | Exit | When                                                                  |
|--------------|--------------------------------------|------|------------------------------------------------------------------------|
| OGON-R0001   | Route conflict                       | 7    | Two routes registered the same method + path.                          |
| OGON-R0002   | Handler signature invalid            | 7    | Handler is not `func(*http.Ctx) error`.                                |
| OGON-R0003   | Path template malformed              | 2    | Path is not `/foo/{id}` form (e.g. `/foo/{id` missing `}`).             |
| OGON-R0004   | Path param mismatch                  | 7    | Path declares `{id}` but the handler does not extract it.               |
| OGON-R0005   | Method not allowed                   | 405  | Method + path registered for a different method; client used the wrong verb. |
| OGON-R0006   | Route not found                       | 404  | No route matched the request.                                          |
| OGON-R0007   | Middleware order invalid             | 7    | A middleware declared `Before: csrf` but `csrf` is not registered.      |

---

## When

`OGON-R` fires:

- at boot when `http.Register` detects a duplicate;
- in `ogon routes check` (CI gate);
- at request time for `405` / `404` (which are HTTP-status, not exit
  codes — they still carry the `OGON-R` code in the response).

It does **not** fire for handler panics — those are `OGON-U`.

---

## Examples

### OGON-R0001 — Route conflict

```go
http.Register("GET /users", list)
http.Register("GET /users", list2)  // ← OGON-R0001
```

```bash
$ ogon routes check
[OGON-R0001] route conflict
  what:    GET /users is registered twice
  where:   routes/users.go:12 and routes/users.go:13
  fix:     remove one registration, or change the path
```

### OGON-R0003 — Path template malformed

```go
http.Register("GET /users/{id", get)  // missing closing }
```

```
[OGON-R0003] path template malformed
  what:    /users/{id is not a valid path template
  why:     unclosed param {id
  fix:     close with }: /users/{id}
```

### OGON-R0005 — Method not allowed

```bash
$ curl -X POST /users/42
HTTP/1.1 405 Method Not Allowable
Content-Type: application/problem+json
{"type":"OGON-R0005","title":"method not allowed","status":405,"detail":"POST is not allowed for /users/42","instance":"/users/42","allowed":["GET","PUT","DELETE"]}
```

---

## Remedy

1. **For R0001 / R0007** — read the two registrations named in `where`,
   pick one to keep, delete the other.
2. **For R0002** — fix the handler signature to
   `func(*http.Ctx) error`. The CLI does not infer a different
   signature.
3. **For R0003 / R0004** — fix the path template; `ogon routes check`
   names the file and line.
4. **For R0005 / R0006** — these are runtime; the client should
   follow the `Allow` header or 404 to the canonical path.

---

## Escape

- **Bring your own mux**: `http.Server.Handler()` returns the
  `http.Handler`; you can mount it on a sub-tree of an existing mux.
- **Disable conflict detection**: there is no flag. If you have a
  legitimate reason for two routes to share a method+path (you do
  not), file an issue; the conflict is real.
- **Manual 404**: register a `GET /` fallback that returns your
  custom 404 page.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| `OGON-R0001` after a merge                            | Both branches registered the same route; delete one.          |
| `OGON-R0005` on `POST` you registered                 | The route is `PUT`, not `POST`; check the registration.       |
| `OGON-R0007` after adding a middleware                | `Before: csrf` but csrf is registered later; reorder.         |
| Allow header missing on 405                          | The router returned 405 via the framework's default handler; ensure `http.Server.Use(http.AllowHeader())`. |

---

Next: [OGON-V validation errors](./OGON-V.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

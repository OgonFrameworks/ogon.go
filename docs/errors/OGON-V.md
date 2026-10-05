# Error class `OGON-V` — Validation errors

> Diagnostics that fire when request data fails the `ogon:` tag validators
> on a model or a `Bind` target.

This page covers: **what**, **when**, **examples**, **remedy**, **escape**,
**troubleshoot** (DOC-018).

---

## What

`OGON-V` covers request-side validation. The `ogon:` tag on a struct
field declares a validator; `http.Ctx.Bind` runs them. Failures are
returned as `400 Bad Request` with an `application/problem+json` body
that includes a per-field breakdown.

| Code         | Title                                | HTTP | When                                                                  |
|--------------|--------------------------------------|------|------------------------------------------------------------------------|
| OGON-V0001   | Validation failed                    | 400  | One or more fields failed their declared validator.                     |
| OGON-V0002   | Required field missing               | 400  | The field has `ogon:"required"` and was absent.                         |
| OGON-V0003   | Type mismatch                        | 400  | The JSON value is not coercible to the Go type.                          |
| OGON-V0004   | Out of range                          | 400  | `min_length`, `max_length`, `min`, `max` violated.                      |
| OGON-V0005   | Format invalid                        | 400  | `email`, `url`, `phone`, `uuid`, `ipv4`, `ipv6` rejected the value.     |
| OGON-V0006   | Enum value not allowed               | 400  | `enum=a|b|c` and the value was `d`.                                    |
| OGON-V0007   | Custom validator rejected            | 400  | A user-supplied validator returned an error.                           |
| OGON-V0008   | Bind body too large                  | 413  | The request body exceeded `http.max_body_size`.                         |

---

## When

`OGON-V` fires at request time inside `http.Ctx.Bind` and
`http.Ctx.Query`. It is the most common error class end-users see.

---

## Examples

### OGON-V0001 — Validation failed

```go
type Body struct {
    Email    string `ogon:"email;required"`
    Password string `ogon:"required;min_length=12"`
}

func signup(c *http.Ctx) error {
    var b Body
    if err := c.Bind(&b); err != nil {
        return err   // already a *diag.Diag with code OGON-V0001
    }
    ...
}
```

```bash
$ curl -X POST /signup -d '{"email":"x","password":"short"}'
HTTP/1.1 400 Bad Request
Content-Type: application/problem+json
{
  "type": "OGON-V0001",
  "title": "validation failed",
  "status": 400,
  "fields": [
    {"field":"email","code":"OGON-V0005","detail":"must be a valid email"},
    {"field":"password","code":"OGON-V0004","detail":"min_length=12, got 5"}
  ]
}
```

### OGON-V0002 — Required field missing

```bash
$ curl -X POST /signup -d '{"email":"a@b.c"}'
{"type":"OGON-V0002","title":"required field missing","status":400,
 "fields":[{"field":"password","detail":"required"}]}
```

### OGON-V0006 — Enum value not allowed

```go
type Sort struct {
    Order string `ogon:"enum=asc|desc"`
}
```

```bash
$ curl '/items?order=sideways'
{"type":"OGON-V0006","title":"enum value not allowed","status":400,
 "fields":[{"field":"order","detail":"must be one of asc|desc"}]}
```

---

## Remedy

The fix is always client-side: send data that satisfies the declared
validators. The framework's job is to make the validators visible in
the response so the client can correct.

If the validator is wrong:

1. Update the `ogon:` tag on the struct field.
2. Re-run `ogon test` — the request fixture will surface the change.
3. Re-run `ogon check` — the OpenAPI drift detector will flag the
   contract change for review.

---

## Escape

- **Custom validator**: register `record.RegisterValidator("myrule",
  fn)` and use `ogon:"myrule"` on any field.
- **Skip Bind**: read the body manually with `c.Body()` and validate
  in plain Go.
- **Loosen a tag**: remove the validator from the field. There is no
  `ogon:"optional"` — absence of a validator means optional.

---

## Troubleshoot

| Symptom                                              | Fix                                                            |
|------------------------------------------------------|----------------------------------------------------------------|
| Validator not firing                                  | The tag is on the wrong field; `ogon:` tags live on the field, not the type. |
| `OGON-V0008` on every request                         | `http.max_body_size` is set too low; raise in `ogon.yaml`.    |
| Custom validator not registered                       | Register in an `init()` that runs before the route registration. |
| Format validator rejects valid email                 | The validator is strict (RFC 5321); use `ogon:"email_loose"` for lax validation. |

---

Next: [OGON-K config errors](./OGON-K.md), [back to error index](../../README.md#documentation).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

# CRUD guide — model, route, handler, migration, test

> **Goal**: ship a full CRUD resource end-to-end with the OgonGo CLI.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

A CRUD resource in OgonGo is one command — `ogon gen resource <Name>` —
that writes four files: a model (`models/<name>.go`), a route
registration (`routes/<name>.go`), a handler (`handlers/<name>.go`), and
a table test (`/<name>_test.go`). The migration engine then ships the
schema; the test fixture exercises the lifecycle; the build pipeline
wires it all into the generated DI container.

## When

Use `ogon gen resource` when:

- you have a noun with a stable schema (User, Order, Post, Comment);
- you want all four artifacts (model + route + handler + test) to stay
  in lockstep through refactors;
- you want destructive migrations to fail loud (`OGON-M0001`) instead
  of silently dropping data.

For read-only views or one-off endpoints, `ogon gen route` is lighter.

## Quickstart

```bash
ogon new blog --template standard --git
cd blog

ogon gen resource User --dry-run   # show the plan
ogon gen resource User             # write the four files
ogon migrate run                   # apply the schema
ogon test                           # green
```

The plan output is deterministic:

```
✓ gen resource User
  create models/User.go
  create routes/User.go
  create handlers/User.go
  create User_test.go
```

Edit the model:

```go
// models/User.go
package models

import (
    "time"

    "github.com/OgonFrameworks/ogon.go/record"
)

type User struct {
    record.BaseModel
    Email    string `ogon:"column=email;unique;nullable=false;email"`
    Name     string `ogon:"column=name;nullable=false"`
    Age      int    `ogon:"column=age;nullable=true"`
    LockedAt *time.Time `ogon:"column=locked_at;nullable=true"`
}
```

Edit the handler:

```go
// handlers/User.go
package handlers

import (
    "net/http"
    "strconv"

    "github.com/OgonFrameworks/ogon.go/http"
    "github.com/OgonFrameworks/ogon.go/record"
    "blog/models"
)

func List(c *http.Ctx) error {
    var users []models.User
    if err := record.All(c.Tx(), &users); err != nil {
        return err
    }
    return c.JSON(http.StatusOK, users)
}

func Create(c *http.Ctx) error {
    var u models.User
    if err := c.Bind(&u); err != nil {
        return err
    }
    if err := record.Insert(c.Tx(), &u); err != nil {
        return err
    }
    return c.JSON(http.StatusCreated, u)
}

func Get(c *http.Ctx) error {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    var u models.User
    if err := record.FindByID(c.Tx(), &u, id); err != nil {
        return err
    }
    return c.JSON(http.StatusOK, u)
}

func Update(c *http.Ctx) error {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    var u models.User
    if err := record.FindByID(c.Tx(), &u, id); err != nil {
        return err
    }
    if err := c.Bind(&u); err != nil {
        return err
    }
    if err := record.Update(c.Tx(), &u); err != nil {
        return err
    }
    return c.JSON(http.StatusOK, u)
}

func Delete(c *http.Ctx) error {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    if err := record.DeleteByID(c.Tx(), &models.User{}, id); err != nil {
        return err
    }
    return c.JSON(http.StatusNoContent, nil)
}
```

Register the routes (`routes/User.go` is generated; edit it):

```go
package routes

import (
    "blog/handlers"

    "github.com/OgonFrameworks/ogon.go/http"
)

func init() {
    http.Register("GET    /users",        handlers.List)
    http.Register("POST   /users",        handlers.Create)
    http.Register("GET    /users/{id}",   handlers.Get)
    http.Register("PUT    /users/{id}",   handlers.Update)
    http.Register("DELETE /users/{id}",   handlers.Delete)
}
```

## Config

`ogon.yaml` selects the driver:

```yaml
database:
  driver: sqlite
  url: file:./ogon.db
```

Switch to Postgres for prod:

```yaml
# ogon.prod.yaml
database:
  driver: postgres
  url_env: OGON_DB_URL
  max_open_conns: 25
  max_idle_conns: 5
  conn_max_lifetime: 5m
```

Inspect the resolved URL without leaking the secret:

```bash
ogon db url
# file:./ogon.db     (dev)   ***@host/db    (prod)
```

## Test

The generated `User_test.go` exercises the full lifecycle:

```go
func TestUserCRUD(t *testing.T) {
    app := test.NewApp(t, http.Handler())
    defer app.Close()

    db := app.DB()                            // transaction-rollback DB
    defer db.Rollback()

    // create
    r := app.Recorder().Post("/users", test.JSON(`{"email":"a@b.c","name":"A"}`))
    r.AssertStatus(t, 201)

    // list
    r = app.Recorder().Get("/users")
    r.AssertStatus(t, 200)
    r.AssertJSONContains(t, `"email":"a@b.c"`)
}
```

Helpers worth knowing:

- `test.NewDBTx(t)` — transaction-rollback DB (fast, parallel-safe).
- `test.NewTruncationDB(t)` — opt-in for DDL that cannot live in a tx.
- `test.QueryCounter` — wraps the DB and asserts N+1 absence.
- `test.RedactionCorpus()` — prove your model has no PII in span attrs.

Run:

```bash
ogon test --race
ogon test --junit ./build/junit.xml
```

## Prod

```bash
ogon migrate status                 # what's applied?
ogon migrate diff                   # what's pending?
ogon migrate run                    # apply pending
ogon migrate rollback               # one step back
ogon migrate rollback --to 0042     # back to a specific version
ogon deploy --cloud aws             # ship it
```

Destructive migrations (`DROP TABLE`, `ALTER ... DROP COLUMN` with data
loss) trigger `OGON-M0001` and require `--yes`. Migrations are
transactional where the driver supports DDL in a tx (Postgres: yes;
SQLite: partial).

## Escape

- **Raw SQL**: `record.Raw(c.Tx(), "SELECT ... WHERE id = $1", id).Scan(&u)`
  is the documented escape hatch. Same transaction, same driver, no
  builder.
- **Custom types**: implement `record.Scanner` and `record.Valuer` on
  any type to map column ↔ Go.
- **Skip generation**: write the model + route + handler by hand; the
  CLI does not require you to use `ogon gen`.
- **No migrations**: `ogon build` skips the migrate engine if
  `database.driver: none`.

## Troubleshoot

| Symptom                                              | Fix                                                        |
|------------------------------------------------------|------------------------------------------------------------|
| `OGON-G0001: unowned file` on `ogon gen resource`    | You edited `models/User.go`; pass `--force` to regenerate.  |
| `OGON-M0001: migration unsafe`                       | Read the destructive-op detection line; pass `--yes`.      |
| `OGON-R0001: route conflict`                         | Two routes registered the same method+path; `ogon routes check` shows both. |
| `OGON-V0001: validation failed`                     | The `ogon:` tag declared a validator (e.g. `email`) that the value failed. |
| `record: no table for type User`                      | `ogon gen resource User` writes the model; the migration creates the table; run `ogon migrate run`. |
| `ogon db url` returns `***@host/db`                  | The URL has credentials; the CLI masks them. That's expected. |

---

Next: [Auth guide](./auth.md), [Realtime guide](./realtime.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

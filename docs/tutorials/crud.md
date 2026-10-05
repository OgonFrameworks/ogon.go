# Tutorial — Build a CRUD resource end-to-end

> **Goal**: model a `Post` entity, generate the route + handler +
> migration + test from one command, apply the migration, and ship a
> full CRUD API. By the end you will have written **one developer file**
> and zero glue.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

A CRUD resource in OgonGo is `ogon gen resource <Name>`. The CLI writes
four files in lockstep:

| Artifact | Path                       | Owner        |
|----------|----------------------------|--------------|
| Model    | `models/<Name>.go`         | You + CLI    |
| Route    | `routes/<Name>.go`         | CLI          |
| Handler  | `handlers/<Name>.go`       | You + CLI    |
| Test     | `<Name>_test.go`           | CLI          |

The migration engine then ships the schema; the test fixture exercises
the lifecycle; the build pipeline wires it all into the generated DI
container. The four artifacts stay in lockstep through refactors.

## When

Use `ogon gen resource` when:

- you have a noun with a stable schema (`User`, `Order`, `Post`,
  `Comment`, `Invoice`);
- you want all four artifacts to stay in lockstep through refactors;
- you want destructive migrations to fail loud (`OGON-M0001`) instead
  of silently dropping data.

For read-only views or one-off endpoints, `ogon gen route` is lighter.
For nested/relational shapes, scaffold the resource then hand-edit the
model.

## Quickstart

```bash
ogon new blog --template standard --git
cd blog

ogon gen resource Post --dry-run   # show the plan
ogon gen resource Post             # write the four files
ogon migrate run                   # apply the schema
ogon test                          # green
```

The plan output is deterministic:

```
gen resource Post
  create models/Post.go
  create routes/Post.go
  create handlers/Post.go
  create Post_test.go
```

### Edit the model

```go
// models/Post.go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Blog post model.

package models

import (
    "time"

    "github.com/OgonFrameworks/ogon.go/record"
)

type Post struct {
    record.BaseModel
    Title    string    `ogon:"column=title;nullable=false;max=140"`
    Body     string    `ogon:"column=body;nullable=false;type=text"`
    AuthorID int64     `ogon:"column=author_id;fk=users.id;nullable=false;index"`
    PublishedAt *time.Time `ogon:"column=published_at;nullable=true;index"`
}
```

Tags are normative: `column`, `type`, `nullable`, `unique`, `index`,
`partial`, `fk`, `check`, `enum`, `jsonb`, `rls`, `audit`,
`tenant_id`, validators (`email`, `max`, `min`, `regex`). Validators
generate `OGON-V0001` 422 ProblemDetails responses automatically.

### Edit the handler

```go
// handlers/Post.go
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Blog post CRUD handlers.

package handlers

import (
    "net/http"
    "strconv"

    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
    "github.com/OgonFrameworks/ogon.go/record"
    "blog/models"
)

func List(c *ogonhttp.Ctx) error {
    var posts []models.Post
    if err := record.All(c.Tx(), &posts); err != nil {
        return err
    }
    return c.JSON(http.StatusOK, posts)
}

func Create(c *ogonhttp.Ctx) error {
    var p models.Post
    if err := c.Bind(&p); err != nil {
        return err
    }
    if err := record.Insert(c.Tx(), &p); err != nil {
        return err
    }
    return c.JSON(http.StatusCreated, p)
}

func Get(c *ogonhttp.Ctx) error {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    var p models.Post
    if err := record.FindByID(c.Tx(), &p, id); err != nil {
        return err
    }
    return c.JSON(http.StatusOK, p)
}

func Update(c *ogonhttp.Ctx) error {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    var p models.Post
    if err := record.FindByID(c.Tx(), &p, id); err != nil {
        return err
    }
    if err := c.Bind(&p); err != nil {
        return err
    }
    if err := record.Update(c.Tx(), &p); err != nil {
        return err
    }
    return c.JSON(http.StatusOK, p)
}

func Delete(c *ogonhttp.Ctx) error {
    id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
    if err := record.DeleteByID(c.Tx(), &models.Post{}, id); err != nil {
        return err
    }
    return c.JSON(http.StatusNoContent, nil)
}
```

### Routes are already generated

```go
// routes/Post.go (generated — owned by the CLI)
package routes

import (
    "blog/handlers"

    ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

func init() {
    ogonhttp.Register("GET    /posts",       handlers.List)
    ogonhttp.Register("POST   /posts",       handlers.Create)
    ogonhttp.Register("GET    /posts/{id}",  handlers.Get)
    ogonhttp.Register("PUT    /posts/{id}",  handlers.Update)
    ogonhttp.Register("DELETE /posts/{id}",  handlers.Delete)
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

The generated `Post_test.go` exercises the full lifecycle:

```go
func TestPostCRUD(t *testing.T) {
    app := test.NewApp(t, ogonhttp.Handler())
    defer app.Close()

    db := app.DB()                              // transaction-rollback DB
    defer db.Rollback()

    r := app.Recorder().Post("/posts", test.JSON(`{"title":"hi","body":"world","author_id":1}`))
    r.AssertStatus(t, 201)

    r = app.Recorder().Get("/posts")
    r.AssertStatus(t, 200)
    r.AssertJSONContains(t, `"title":"hi"`)
}
```

Helpers worth knowing:

- `test.NewDBTx(t)` — transaction-rollback DB (fast, parallel-safe).
- `test.NewTruncationDB(t)` — opt-in for DDL that cannot live in a tx.
- `test.QueryCounter` — wraps the DB and asserts N+1 absence.

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
ogon deploy --cloud aws             # ship it
```

Destructive migrations (`DROP TABLE`, `ALTER ... DROP COLUMN` with data
loss) trigger `OGON-M0001` and require `--yes`. Migrations are
transactional where the driver supports DDL in a tx (Postgres: yes;
SQLite: partial).

## Escape

- **Raw SQL**: `record.Raw(c.Tx(), "SELECT ... WHERE id = $1", id).Scan(&p)`
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
| `OGON-G0001: unowned file` on `ogon gen resource`    | You edited `models/Post.go`; pass `--force` to regenerate.  |
| `OGON-M0001: migration unsafe`                       | Read the destructive-op detection line; pass `--yes`.      |
| `OGON-R0001: route conflict`                         | Two routes registered the same method+path; `ogon routes check` shows both. |
| `OGON-V0001: validation failed`                      | The `ogon:` tag declared a validator (e.g. `max=140`) the value failed. |
| `record: no table for type Post`                     | `ogon migrate run` creates the table; you forgot to run it. |

---

Next: [Auth tutorial](./auth.md), [Realtime guide](../guides/realtime.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->

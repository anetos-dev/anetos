---
title: Define models and save data
since: v0.1.0
group: "Data"
weight: 201
---

# Define models and save data

Map structs to tables, and create, update and delete rows with
timestamps, soft deletes and hooks.

## Before you start

[Connect to a database](database.md) and create the tables with
[migrations](migrations.md).

## Steps

### 1. Define the models

```go
// Author writes posts.
type Author struct {
	db.Model        // id, created_at, updated_at
	Name     string `db:"name" json:"name"`
	Email    string `db:"email" json:"email"`

	Posts []Post `rel:"has_many" json:"posts,omitzero"` // posts.author_id; loaded with With or Load
}

// Post is a blog post. Deleting one only marks it deleted.
type Post struct {
	db.Model
	db.SoftDeletes            // deleted_at
	AuthorID       int64      `db:"author_id" json:"author_id"`
	Title          string     `db:"title" json:"title"`
	Body           string     `db:"body" json:"body"`
	Tags           []string   `db:"tags,json" json:"tags"` // stored as JSON text
	Views          int        `db:"views" json:"views"`
	PublishedAt    *time.Time `db:"published_at" json:"published_at"` // nullable

	Author *Author `rel:"belongs_to" json:"author,omitzero"` // by AuthorID
}

// AuthorCols and PostCols, the typed columns of the models, and
// AuthorRels and PostRels, their relations, are in models_gen.go, written
// by `go tool anetos generate` (or go generate).
//
//go:generate go tool anetos generate
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `models`.)

- `db.Model` adds an auto-increment `id` and the `created_at` and
  `updated_at` timestamps. Embed `db.Timestamps` alone if the key is
  something else.
- `db.SoftDeletes` adds `deleted_at`: deleting sets it, and queries skip
  those rows.
- Columns are the `db` tags; untagged fields use their snake_case names
  (`AuthorID` → `author_id`). Fields with a `rel` tag are
  [relations](relations.md), not columns; other struct, pointer-to-struct
  and slice-of-struct fields without a tag are skipped.
- The table is the snake_case plural of the type name (`Post` → `posts`,
  `Category` → `categories`). Add a `TableName() string` method to choose
  another.
- Use pointers (or `sql.Null[T]`) for nullable columns, and
  `db:"name,json"` to store a value as JSON.
- `go tool anetos generate` writes the typed columns (`PostCols.Title`) that
  queries use; see [Generate typed columns](code-generation.md).

All the tag options and naming rules are in the
[models reference](../reference/models.md).

### 2. Create, read, update, delete

```go
// illustrative
post := Post{AuthorID: 1, Title: "Hello"}
err := db.Create(ctx, &post)      // sets post.ID, CreatedAt, UpdatedAt

post, err = db.Find[Post](ctx, 1) // db.ErrNotFound if there is no row

post.Title = "Hello, world"
err = db.Update(ctx, &post)       // writes every column; sets UpdatedAt

err = db.Save(ctx, &post)         // Create if ID is zero, else Update

err = db.Delete(ctx, &post)       // soft delete: sets DeletedAt
err = db.Restore(ctx, &post)
err = db.ForceDelete(ctx, &post)  // really deletes the row
```

Handlers can return `db.ErrNotFound` as it is: it becomes a **404**.

### 3. Insert many rows, or upsert

```go
// illustrative
err := db.CreateMany(ctx, posts) // one INSERT per batch; IDs set on PostgreSQL and SQLite

// Insert, or update price for SKUs that already exist (sku needs a unique index).
err = db.Upsert(ctx, prices, []string{"sku"}, "price")
```

### 4. Run code around saves (optional)

Implement hook methods on the model's pointer type:

```go
// illustrative
func (p *Post) BeforeSave(ctx context.Context) error {
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		return validate.Fail("title", "The title can't be blank.")
	}
	return nil
}
```

Hooks run for `Create`, `Update`, `Save`, `Delete` and (create hooks)
`CreateMany`. A hook error stops the operation. Mass updates and deletes
on a query don't run hooks.

## Complete example

[`examples/database`](../../../examples/database/main.go) is a small blog
API using everything on this page.

## How it works

The first use of a model type reads its fields and tags once and caches
the result; later calls only copy values. Timestamps are set in UTC with
microsecond precision, so what you get back from the database equals what
you saved. See the [data layer concept](../concepts/data-layer.md).

> **Coming from Laravel?** `db.Model` and `db.SoftDeletes` play the role of
> Eloquent's base model and `SoftDeletes` trait. Models don't track dirty
> attributes: `Update` writes every column, and mass updates set exactly
> the columns you name.

## Testing it

Make rows with [factories](testing.md#4-make-rows-with-factories) in a
`anetostest` app, call your functions with `app.Context()`, and check the
table with `anetostest.AssertDatabaseHas[T]`.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `converting NULL to string is unsupported` | A nullable column mapped to a non-pointer field | Use `*string` or `sql.Null[string]` |
| `db: Post has a zero primary key; Create it first` | `Update` or `Delete` on a row without an ID | Load the row first, or use `Save` |
| Wrong table name (`persons` for `Person`… it's `people`) | The pluralizer's guess | Add a `TableName()` method |
| A field is never saved | It's a struct or slice of structs without a `db` tag | Tag it (`db:"meta,json"` for JSON) |
| IDs are zero after `CreateMany` on MySQL | MySQL can't report keys for multi-row inserts | Reload the rows, or use `Create` per row (tracked models get their IDs: the audit log inserts them one by one) |
| A new row can't reuse the email of a soft-deleted one | The unique index counts deleted rows | `UniqueWithoutTrashed` and `unique_without_trashed` on PostgreSQL and SQLite ([Keep an audit log](audit-log.md#soft-deletes-that-play-well-with-the-log)) |

## Next steps

- [Generate typed columns](code-generation.md)
- [Query data](queries.md)
- [Transactions](transactions.md)
- [Keep an audit log](audit-log.md): who changed what, and pruning
  soft-deleted rows
- [Models reference](../reference/models.md)

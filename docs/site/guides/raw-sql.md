---
title: Raw SQL
since: v0.1.0
---

# Raw SQL

Write SQL yourself and scan the results into structs, when the query
builder is in the way.

## Before you start

[Connect to a database](database.md).

## Steps

### 1. Scan rows into a struct

```go
// AuthorStats is one row of GET /stats.
type AuthorStats struct {
	Name  string `db:"name" json:"name"`
	Posts int64  `db:"posts" json:"posts"`
	Views int64  `db:"views" json:"views"`
}

func (Blog) Stats(c *web.Ctx, _ struct{}) ([]AuthorStats, error) {
	return db.Raw[AuthorStats](c, `
		SELECT a.name, COUNT(p.id) AS posts, COALESCE(SUM(p.views), 0) AS views
		FROM authors a LEFT JOIN posts p ON p.author_id = a.id AND p.deleted_at IS NULL
		GROUP BY a.id, a.name
		ORDER BY views DESC`)
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `raw`.)

Columns match the struct's `db` tags (or snake_case field names). Columns
without a field are ignored, and fields without a column keep their zero
values, so one struct can serve several queries.

### 2. Pass parameters

Write `?` on every database; it becomes `$1`, `$2`… on PostgreSQL:

```go
// illustrative
posts, err := db.Raw[Post](ctx, "SELECT * FROM posts WHERE author_id = ? AND views > ?", id, 100)
```

Or name them with `db.Named`:

```go
// illustrative
n, err := db.RawFirst[int64](ctx,
	"SELECT COUNT(*) FROM posts WHERE author_id = :author AND created_at > :since",
	db.Named{"author": id, "since": since})
```

`?` inside quotes and comments is left alone. SQL without arguments is
sent exactly as written. With arguments on PostgreSQL, write `??` for a
literal `?`, such as the JSON operators (`tags ??| array['go']`). A whole
query without `?` is also sent unchanged, so native `$1` placeholders
work; SQL fragments in the query builder (`db.SQL`, `WhereRaw`) must use
`?`.

### 3. Read one row or one value

`db.RawFirst[T]` returns the first row, or `db.ErrNotFound`. `T` can be a
single value such as `int64`, `string` or `time.Time` for one-column
results.

### 4. Run statements

```go
// illustrative
res, err := db.Exec(ctx, "UPDATE posts SET views = 0 WHERE author_id = ?", id)
n, _ := res.RowsAffected()
```

Raw queries and statements join the context's transaction like any other
query.

## Complete example

`Stats` in [`examples/database`](../../../examples/database/main.go).

## How it works

`db.Raw` rewrites placeholders for the dialect, runs the query, and scans
with a column-to-field plan cached per struct type and column list. SQL is
passed through otherwise: you choose the dialect-specific syntax.

## Testing it

Seed an in-memory SQLite database and compare the scanned structs.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `the SQL has 2 ? placeholders but 1 arguments were given` | Placeholder count mismatch, or a `?` operator | Pass every argument; write `??` for a literal `?` |
| `scanning into int64 needs exactly one column` | A scalar `T` with several columns | Select one column, or scan into a struct |
| `converting NULL to …` | A NULL (often from a `LEFT JOIN`) in a non-pointer field | Use a pointer field or `COALESCE` |
| A column is always zero | Its name doesn't match a tag | Alias it: `COUNT(*) AS posts` |

## Next steps

- [Query data](queries.md)
- [Transactions](transactions.md)

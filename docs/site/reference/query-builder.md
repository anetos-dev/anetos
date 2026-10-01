---
title: Query builder reference
since: v0.1.0
---

# Query builder reference

Everything available on `db.Q[T]`, the query started by
`db.Query[T](ctx)`. See [Query data](../guides/queries.md) for a walkthrough.

Each method returns a new query; the original is unchanged.

## Conditions and clauses

| Method | SQL |
|---|---|
| `Where(conds...)` | `WHERE … AND …`; repeated calls add more conditions |
| `WhereRaw(sql, args...)` | A condition in SQL with `?` placeholders |
| `Join(sql, args...)` | A join clause in SQL (`JOIN users ON …`); only `T`'s columns are selected |
| `OrderBy(orders...)` | `ORDER BY`, from `col.Asc()`, `col.Desc()` or `db.OrderRaw(sql)` |
| `Latest()`, `Oldest()` | `ORDER BY created_at DESC` / `ASC` |
| `Limit(n)`, `Offset(n)` | `LIMIT` / `OFFSET` |
| `GroupBy(cols...)`, `Having(conds...)` | `GROUP BY` / `HAVING` (read the groups with `db.Select`) |
| `Distinct()` | `SELECT DISTINCT` |
| `Scope(fns...)` | Applies `func(*db.Q[T]) *db.Q[T]` modifiers |
| `WithTrashed()`, `OnlyTrashed()` | Include / only soft-deleted rows |
| `WhereHas(rel, conds...)`, `WhereDoesntHave(rel, conds...)` | `EXISTS (…)` / `NOT EXISTS (…)` on a relation's rows ([relations](models.md#relations)) |
| `With(rels...)` | Loads relations of the rows, one query per relation ([relations](models.md#relations)) |
| `ForUpdate()`, `ForShare()` | Row locks until the transaction ends (nothing on SQLite) |

## Conditions

| Expression | SQL |
|---|---|
| `col.Eq(v)`, `Ne`, `Gt`, `Gte`, `Lt`, `Lte` | `=`, `<>`, `>`, `>=`, `<`, `<=`. `Eq(nil)` / `Ne(nil)` become `IS NULL` / `IS NOT NULL` |
| `col.In(vs...)`, `col.NotIn(vs...)` | `IN (…)`; an empty `In` matches nothing, an empty `NotIn` everything |
| `col.Between(lo, hi)` | `BETWEEN lo AND hi` |
| `col.Like(p)`, `col.NotLike(p)` | `LIKE` (case sensitivity depends on the database) |
| `col.IsNull()`, `col.NotNull()` | `IS NULL`, `IS NOT NULL` |
| `db.And(...)`, `db.Or(...)`, `db.Not(c)` | Grouped with parentheses; `And()` is true, `Or()` is false |
| `db.SQL(sql, args...)` | Any SQL, with `?` or `:name` placeholders |

## Columns

`go tool anetos gen` declares `PostCols` with a typed column per field of
model `Post` ([Generate typed columns](../guides/code-generation.md)). By
hand:

| Function or method | Makes |
|---|---|
| `db.Col[T]("name")` | A typed column; `"table.name"` qualifies it. `T` is the Go field's type (`*time.Time` for a nullable timestamp) |
| `db.JSONCol[T]("name")` | A column stored as JSON: values passed to its methods are encoded as JSON (nil stays `NULL`), and `db.Pluck` decodes them |
| `db.C("name")` | An untyped column (`Column[any]`) |
| `col.Of("posts")` | The same column qualified with a table (`posts.name`); `Of("")` removes the qualifier |
| `col.Name()` | The column name |
| `db.Columns[T]()` | Model `T`'s column names in field order, or an error if `T` isn't a model struct |

## Reading

| Method or function | Returns |
|---|---|
| `Get()` | `[]T`, empty (not nil) when nothing matches |
| `First()` | First row or `db.ErrNotFound` |
| `Find(id)`, `db.Find[T](ctx, id)` | Row by primary key or `db.ErrNotFound` |
| `All()` | `iter.Seq2[T, error]`, streaming rows |
| `Count()` | `int64` |
| `Exists()` | `bool` |
| `Paginate(page, perPage)` | `db.Page[T]`: `Data`, `CurrentPage`, `PerPage`, `Total`, `LastPage` (JSON: `data`, `current_page`, …); `HasPrev()`, `HasMore()`. A page below 1 is page 1; past the end, empty |
| `CursorPaginate(cursor, perPage)` | `db.CursorPage[T]`: `data`, `per_page`, `next_cursor`, `prev_cursor`; `db.ErrInvalidCursor` (400) for malformed cursors |
| `db.Pluck(q, col)` | `[]V`, one column (decoded from JSON for a `db.JSONCol`) |
| `db.Sum(q, col)`, `db.Min`, `db.Max` | `V`; zero if no rows match. `Limit` and `Offset` are respected, `Distinct` is ignored (write `SUM(DISTINCT x)` with `db.Select`); with `GroupBy`, use `db.Select` |
| `db.Avg(q, col)` | `float64` |
| `db.Select[R](q, terms...)` | `[]R`, for custom SELECT terms (`"COUNT(*) AS posts"`) |

`perPage` below 1 means `db.DefaultPerPage` (15). Cursor pagination orders
by the query's column orderings plus the primary key; it rejects
`OrderRaw`, and the ordering columns shouldn't contain NULLs. Cursors are
encoded, not signed: a client can craft one, which only moves where its
own page starts.

## Writing

| Method | Does |
|---|---|
| `Update(assignments...)` | `UPDATE … SET` from `col.Set(v)` or `col.SetRaw(sql, args...)`; adds `updated_at` for models with timestamps; returns the rows matched. Columns may be qualified with the model's own table |
| `Delete()` | Soft delete for `SoftDeletes` models (rows already deleted keep their `deleted_at`; with `OnlyTrashed` it does nothing), else `DELETE` |
| `ForceDelete()` | `DELETE` |
| `Restore()` | Clears `deleted_at` on matching trashed rows |

Mass writes don't run model hooks and accept only `Where` conditions:
`Join`, `OrderBy`, `Limit`, `Offset`, `GroupBy`, `Having`, `Distinct` and
locks are refused, because they don't work alike across databases. For
those, write the statement with `db.Exec`.

## Raw SQL

| Function | Does |
|---|---|
| `db.Raw[T](ctx, sql, args...)` | `[]T` from any query; `T` a struct or a single value |
| `db.RawFirst[T](ctx, sql, args...)` | First row or `db.ErrNotFound` |
| `db.Exec(ctx, sql, args...)` | `sql.Result` |

Placeholders: `?` everywhere (`??` for a literal `?` on PostgreSQL), or
`:name` with one `db.Named` argument. SQL without arguments is sent as
written. A whole `Raw` or `Exec` query
without `?` is sent as written, so native `$1` works there; fragments
(`db.SQL`, `WhereRaw`, `SetRaw`, `Join`) must use `?`.

## Transactions

| Function | Does |
|---|---|
| `db.Tx(ctx, fn)` | Runs `fn` in a transaction (a savepoint when nested) |
| `db.TxWith(ctx, opts, fn)` | With `*sql.TxOptions` |
| `db.AfterCommit(ctx, fn)` | Runs `fn` after the commit (or now, outside a transaction) |
| `db.InTx(ctx)` | Whether `ctx` has a transaction |
| `db.WithTx(ctx, tx)` | Queries on the returned context use a `*sql.Tx` you began (and commit) yourself; `AfterCommit` callbacks on it never run |
| `db.WithTestTx(ctx, tx)` | `WithTx` for a test's transaction, which is rolled back: `AfterCommit` callbacks run at once in it, or when a `db.Tx` directly inside it commits. `anetostest` uses it |
| `db.WithoutTx(ctx)` | Queries on the returned context leave the transaction: their writes stay after a rollback (SQLite: they wait for it) |
| `q.WithContext(ctx)` | The query with another context, e.g. a base query run inside `Tx` |

## Errors

| Error | Status in handlers | When |
|---|---|---|
| `db.ErrNotFound` | 404 | `First`, `Find`, `RawFirst`, `Delete` found no row |
| `db.ErrInvalidCursor` | 400 | `CursorPaginate` got a cursor it didn't produce |
| `db.ErrNoDB` | 500 | The context has no database |

---
title: Query data
since: v0.1.0
group: "Data"
weight: 203
---

# Query data

Find rows with typed conditions, sort and paginate them, aggregate, and
update or delete in bulk.

## Before you start

[Define your models](models.md).

## Steps

### 1. Build a query

`db.Query[T](ctx)` starts a query on `T`'s table. Conditions use the
typed columns that [`anetos gen`](code-generation.md) writes for each
model, so `PostCols.Views.Eq("ten")` doesn't compile:

```go
// illustrative
posts, err := db.Query[Post](ctx).
	Where(PostCols.Title.Like("%go%"), PostCols.Views.Gte(100)). // AND
	Where(db.Or(PostCols.AuthorID.Eq(1), PostCols.AuthorID.Eq(2))).
	OrderBy(PostCols.Views.Desc()).
	Limit(20).
	Get()
```

A column has its field's type: compare a nullable `*time.Time` column
with `new(t)`. For a quick query, `db.C("views")` is an untyped column,
and `db.Col[int]("views")` declares a typed one by hand.

Columns have `Eq`, `Ne`, `Gt`, `Gte`, `Lt`, `Lte`, `In`, `NotIn`,
`Between`, `Like`, `NotLike`, `IsNull` and `NotNull`. Combine conditions
with `db.And`, `db.Or` and `db.Not`, and write anything else in SQL with
`db.SQL("lower(email) = ?", email)` or `WhereRaw`.

Every method returns a new query, so a base query can be shared (a query
keeps the context it was started with; `WithContext` changes it):

```go
// illustrative
published := db.Query[Post](ctx).Where(PostCols.PublishedAt.NotNull())
latest, err := published.Latest().Limit(5).Get()
total, err := published.Count()
```

### 2. Get results

| Call | Returns |
|---|---|
| `Get()` | `[]T` (empty, not nil, when nothing matches) |
| `First()` | the first row, or `db.ErrNotFound` (add `OrderBy` to choose which) |
| `Find(id)` | the row with that primary key, or `db.ErrNotFound` |
| `All()` | an iterator: `for p, err := range q.All()`, for large results |
| `Count()`, `Exists()` | how many rows match; whether any does |
| `db.Pluck(q, col)` | one column's values |
| `db.Sum`, `db.Avg`, `db.Min`, `db.Max` | aggregates of a column |

### 3. Paginate

```go
// illustrative
page, err := db.Query[Post](ctx).Latest().Paginate(in.Page, 20)
```

Pages are 1-based; 0 or a negative page (a missing `?page=`) is the
first page, and a page past the end is empty. `perPage` often comes from
the request: bound it with `validate:"max:100"`. Order by a unique column
last (`PostCols.ID.Desc()` after `Latest()`), or rows created in the same
instant can move between pages.

A `db.Page[T]` has `Data`, `CurrentPage`, `PerPage`, `Total` and
`LastPage`, with `HasPrev()` and `HasMore()`. It encodes like Laravel's
paginator, so an API handler returns it as it is:
`{"data": […], "current_page": 2, "per_page": 20, "total": 57, "last_page": 3}`.

For an HTML list, pass the page to the view (`ListNotes` binds
``Page int `query:"page"` ``):

```go
func (Notes) Index(c *web.Ctx, in ListNotes) (web.Responder, error) {
	// Newest first; the ID breaks ties between notes created in the same
	// instant, so no note shows on two pages. With search words, the
	// best matches come first, then the newest. Search of no words
	// changes nothing.
	page, err := db.Query[Note](c).OrderBy(NoteCols.CreatedAt.Desc(), NoteCols.ID.Desc()).
		Search(in.Q).
		Paginate(in.Page, 10)
	if err != nil {
		return nil, err
	}
	return web.View(NotesPage(page, in.Q)), nil
}
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `index`.)

and link to the pages around it with `web.PageURL(ctx, n)`: a relative
link (`?q=go&page=2`) that keeps the other query parameters (a search, a
filter). Past the end, the "Newer" link goes back to the last page:

```templ
// NotesPage lists one page of notes, with links to the pages around it
// (they keep the search words, q) and a search box.
templ NotesPage(page db.Page[Note], q string) {
	@Layout("All notes") {
		<h1>Notes</h1>
		<p><a href={ web.URL(ctx, "notes.new") }>New note</a></p>
		<form method="get" action={ web.URL(ctx, "notes.index") } role="search">
			<input type="search" name="q" value={ q } aria-label="Search notes"/>
			<button type="submit">Search</button>
		</form>
		if page.Total == 0 && q != "" {
			<p>No notes match “{ q }”.</p>
		} else if page.Total == 0 {
			<p>No notes yet.</p>
		}
		<ul class="notes">
			for _, n := range page.Data {
				@noteItem(n)
			}
		</ul>
		<nav class="pages">
			if page.HasPrev() {
				<a href={ web.PageURL(ctx, min(page.CurrentPage-1, page.LastPage)) } rel="prev">Newer</a>
			}
			if page.LastPage > 1 {
				<span>Page { page.CurrentPage } of { page.LastPage }</span>
			}
			if page.HasMore() {
				<a href={ web.PageURL(ctx, page.CurrentPage+1) } rel="next">Older</a>
			}
		</nav>
	}
}
```

(Copied from [`examples/forms/notes.templ`](../../../examples/forms/notes.templ), region `list`.)

For long lists and infinite scroll, cursor pagination stays fast at any
depth and never repeats or skips rows as new ones arrive:

```go
// illustrative
page, err := db.Query[Post](ctx).OrderBy(PostCols.CreatedAt.Desc()).CursorPaginate(in.Cursor, 20)
// page.NextCursor and page.PrevCursor go back to the client
```

An invalid cursor returns `db.ErrInvalidCursor`, a **400**.

### 4. Group and aggregate

Scan grouped results into a struct of your own:

```go
// illustrative
type authorStats struct {
	AuthorID int64 `db:"author_id"`
	Posts    int64 `db:"posts"`
}
stats, err := db.Select[authorStats](
	db.Query[Post](ctx).GroupBy("author_id").Having(db.SQL("COUNT(*) > ?", 5)),
	"author_id", "COUNT(*) AS posts")
```

With a `Join`, qualify columns that both tables have:
`PostCols.ID.Of("posts")` is `posts.id`. For joins across several tables,
[raw SQL](raw-sql.md) is often clearer.

### 5. Update or delete many rows

```go
// illustrative
n, err := db.Query[Post](ctx).Where(PostCols.AuthorID.Eq(id)).Update(
	PostCols.Title.Set("[removed]"),
	PostCols.Views.SetRaw("views + ?", 1))

n, err = db.Query[Post](ctx).Where(PostCols.Views.Eq(0)).Delete() // soft delete for SoftDeletes models
```

Mass updates set `updated_at`; they don't run model hooks.

### 6. Reuse conditions as scopes

```go
// illustrative
func Popular(q *db.Q[Post]) *db.Q[Post] { return q.Where(PostCols.Views.Gte(1000)) }

posts, err := db.Query[Post](ctx).Scope(Popular).Get()
```

## Complete example

`ListPosts` in [`examples/database`](../../../examples/database/main.go)
combines filters from the query string with pagination.

## How it works

The query builder writes SQL for the database's dialect (placeholders,
quoting, `LIMIT` forms) and sends values as parameters, never inside the
SQL text. Soft-deleted rows are excluded unless you call `WithTrashed()`
or `OnlyTrashed()`. Every method is listed in the
[query builder reference](../reference/query-builder.md).

> **Coming from Laravel?** `Where(PostCols.Title.Like(…))` is `where('title',
> 'like', …)`, `Paginate` is `paginate`, and scopes are plain functions
> instead of `scopeX` methods. There is no lazy loading: load relations
> with `With` ([Relations and eager loading](relations.md)).

## Testing it

Create the rows a query should (and shouldn't) find with
[factories](testing.md#4-make-rows-with-factories), then run it on
`app.Context()` of an `anetostest` app.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `Update and Delete only support Where conditions` | `Join`, `OrderBy`, `Limit` and friends don't work the same on every database | Filter with `Where`, or write the statement for your database with `db.Exec` |
| `ambiguous column` after a `Join` | Both tables have the column | Qualify it: `db.C("posts.id")` |
| `First` returns a different row each time | No `OrderBy` | Add one |
| `LIKE` matches differently between databases | PostgreSQL is case-sensitive | Use `db.SQL("lower(title) LIKE ?", …)` or a database-specific operator |

## Next steps

- [Transactions](transactions.md)
- [Raw SQL](raw-sql.md)
- [Query builder reference](../reference/query-builder.md)

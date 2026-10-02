---
title: Find N+1 queries
since: v0.2.0
---

# Find N+1 queries

An N+1 is a query in a loop: one query for a list of posts, then one per
post for its author. It works, and it's fast with three posts in
development; with three hundred in production, it isn't. Anetos warns
when a request (or a job, a listener, a scheduled task) runs the same
query many times, and says where, so you can load the rows in one query
instead. The complete example is
[`examples/database`](../../../examples/database).

## Before you start

You have an app with a database (`db.Connect`). Detection is on in
development and in tests, and off in production: nothing to set up.

## Steps

### 1. Read the warning

Run the app (`go tool anetos dev`) and use it. When a request runs the
same SQL five times or more, with whatever arguments, its end is logged
as a warning:

```text
level=WARN msg="repeated query: an N+1? Load related rows with With or a single query" component=db unit="request GET /slow-posts" count=6 sql="SELECT \"authors\".\"id\", … FROM \"authors\" WHERE \"authors\".\"id\" = ? LIMIT 1" at=handlers/posts.go:42
```

`unit` is what ran the queries: `request GET /slow-posts`, `job
SendDigest`, `listener emailReceipt`, `message orders.created
(billing)`, `task prune-audit-log`, `tool find_order` (a model's call
of an [AI tool](ai.md)). `count` is how many times it ran
the query, and `at` is the line of your code that ran it (the fifth
time).

### 2. Fix it

Load the related rows with the query, in one query for all the rows:

```go
func (Blog) ListPosts(c *web.Ctx, in ListPosts) (db.Page[Post], error) {
	q := db.Query[Post](c).Where(PostCols.PublishedAt.NotNull())
	if in.Search != "" {
		q = q.Where(PostCols.Title.Like("%" + in.Search + "%"))
	}
	if in.Author != nil {
		q = q.Where(PostCols.AuthorID.Eq(*in.Author))
	}
	return q.With(PostRels.Author).Latest().Paginate(in.Page, in.PerPage) // one query for all the authors
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `list-posts`.)

Without a relation, collect the keys and query them once with
`WhereIn`, or write a join. Rows you have already loaded take their
relations with `db.Load` and `db.LoadMany` ([Relations](relations.md#3-or-load-them-afterwards)).

### 3. Keep it fixed with a test

`anetostest` records the repeated queries of the test's requests (they
are in the test's log too). `app.AssertNoRepeatedQueries()` fails the
test if there is one:

```go
// The posts page loads the authors with one query (With), so no request
// repeats a query. The handler below is the N+1 that avoids, a query per
// post: anetostest reports it, with the line it runs from.
func TestNoNPlusOne(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, Authors)
	now := time.Now().UTC()
	anetostest.CreateMany(app, Posts.With(func(p *Post) { p.AuthorID, p.PublishedAt = ada.ID, &now }), 6)

	app.GetJSON("/posts").AssertOK()
	app.AssertNoRepeatedQueries()

	app.Router().Get("/slow-posts", func(c *web.Ctx) error {
		posts, err := db.Query[Post](c).Get()
		if err != nil {
			return err
		}
		for i := range posts {
			author, err := db.Find[Author](c, posts[i].AuthorID) // one query per post
			if err != nil {
				return err
			}
			posts[i].Author = &author
		}
		return c.JSON(http.StatusOK, posts)
	})
	app.GetJSON("/slow-posts").AssertOK()
	if q := app.RepeatedQueries(); len(q) != 1 || q[0].Count != 6 || q[0].Unit.Name != "GET /slow-posts" ||
		!strings.HasPrefix(q[0].Caller, "database/main_test.go:") {
		t.Errorf("repeated queries: %v", q)
	}
}
```

(Copied from [`examples/database/main_test.go`](../../../examples/database/main_test.go), region `test-n-plus-one`.)

Create enough rows for a loop to cross the threshold: an N+1 over two
posts runs its query twice, under it.

### 4. Tune it

`DB_REPEATED_QUERIES` is the threshold: the number of runs of one query
in one unit of work that triggers the warning.

| Value | Then |
|---|---|
| unset | 5 in development and testing, off in staging and production |
| `0` | Off |
| `2` or more | That threshold, in any environment |

A loop that has to repeat a query (it is bounded and small, or each run
depends on the last) can opt out: queries made with
`db.Untracked(ctx)`'s context aren't counted.

```go
// illustrative
ctx := db.Untracked(ctx)
for _, id := range ids { … }
```

## How it works

Every unit of work of the app (a request, a queue job, an async or
queued event listener, a pub/sub message, a scheduled task, an AI tool
call) starts with
`app.StartUnit`, which runs the functions added with `app.AroundUnits`.
`db.Connect` adds one that gives the unit a counter in its context, when
detection is on. Each query made with that context counts its SQL text,
which the query builder writes the same way every time, placeholders
for values; when a count reaches the threshold, the call stack is read
once to find the first frame outside the framework. When the unit ends,
each query at or over the threshold is logged, and passed to the
functions added with `d.OnRepeatedQuery`. A unit inside another (a job
the sync queue driver runs in a request, a tool call in a request)
counts its own queries.

Queries of the framework's database stores (the database cache, queue
and session stores) aren't counted: a cache read per key or a job per
item is by design. Neither are the queries of validation rules
(`exists`, `unique`), which run once per element of a slice before the
handler. An operation the framework splits into chunks (`With` over
more than 1,000 keys, `CreateMany` of many rows) counts once. A unit
started inside `db.Untracked` (a job the sync driver runs) is tracked
again. Code outside a unit (a command, `migrate`, a seeder, a factory in
a test) isn't tracked; `d.Track(ctx, unit)` tracks it. The caller is
the first frame of the stack in neither the framework's modules nor the
standard library, told apart by the binary's build information; a
first-party plugin's code counts as the app's.

`db.Connect`'s database is tracked. Another one, opened with `db.Open`
(and `Config.RepeatedQueries` set), is tracked once the app has its
units tracked: `app.AroundUnits(analytics.Track)`.

Counting costs a map lookup per query while detection is on; with it
off, no unit function is added, so requests skip starting units, and
queries skip counting.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| A warning for queries that must repeat | A loop over a few items, by design | Use `db.Untracked(ctx)` for that loop, or raise `DB_REPEATED_QUERIES` |
| No warning for a loop over three rows | Under the threshold (5) | Test with more rows, or set `DB_REPEATED_QUERIES=2` in development |
| `at` is empty | Every frame of the stack is the framework's or the standard library's (a query from the framework itself) | Look at the unit and the SQL |
| A warning when a streaming handler (server-sent events, a WebSocket) ends | It polls the same query for as long as the client stays: by design | Poll with `db.Untracked(ctx)` |
| A goroutine's queries are missing from the report | It ran them after the request ended, which reports its queries | Expected: the unit was over |
| No warnings in production | Off there by default | Set `DB_REPEATED_QUERIES` in staging to try it with real data |

## Next steps

- [Relations](relations.md): `With`, `db.Load`, `WhereHas`.
- [Queries](queries.md): `WhereIn` and aggregates.
- [Configuration reference](../reference/configuration.md#database):
  `DB_REPEATED_QUERIES`.

> **Coming from Laravel?** `Model::preventLazyLoading()` throws when a
> relation is lazy-loaded; Anetos has no lazy loading, so an N+1 can
> only be a query you wrote in a loop. Detection is like Laravel
> Debugbar's count of duplicated queries, by the query's text, per
> request, job or task, and logged rather than shown in the page.

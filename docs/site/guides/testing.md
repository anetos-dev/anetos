---
title: Test your app
since: v0.1.0
---

# Test your app

Boot the app in a test, send it requests like a browser or an API
client, and check the responses, the session and the database, with
`anetostest`.

## Before you start

Give your app a `setup` function that connects the database and adds the
server and routes, and call it from `main`, as `anetos new` projects do:

```go
// illustrative
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	if _, err := migrate.ForApp(app, []*migrate.Set{migrations.All}); err != nil {
		return nil, err
	}
	srv, err := web.NewServer(app)
	…
	return srv, nil
}
```

Tests then build the same app as production, with test settings.

## Steps

### 1. Start the app in a test

```go
// illustrative
func TestHome(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Get("/").AssertOK().AssertSee("Welcome")
}
```

`anetostest.New` builds the app with `setup`, boots it, runs its
migrations (when `setup` called `migrate.ForApp`) and closes it when the
test ends. The settings, from highest priority:

1. `anetostest.Env(map[string]string{…})` options;
2. `APP_ENV=testing` and a random `APP_KEY`;
3. the process environment;
4. `.env.testing` next to `go.mod`, if there is one (for PostgreSQL or
   MySQL, all the `DB_*` settings: see
   [Switch a project to PostgreSQL or MySQL](database.md#5-switch-a-project-to-postgresql-or-mysql));
5. `HTTP_ACCESS_LOG=false`.

`.env` isn't read, and with SQLite and neither `DB_DATABASE` nor `DB_URL`
set (or set to `""`), the database is in memory: a test never touches
your development database.

The app's logs go to the test's log, shown when a test fails or with
`go test -v`. Make one app per test or subtest (it reports to the `t` it
was made with). Tests can call `t.Parallel()` with in-memory SQLite and
with PostgreSQL or MySQL, but not with a SQLite file, where each test's
transaction holds the database's only write lock.

### 2. Test a form

The app works like a browser: it keeps cookies (so the session), sends
the session's CSRF token with every `POST`, `PUT`, `PATCH` and `DELETE`,
and sends the last HTML page it loaded as the `Referer`, so a failed form
goes back to that page:

```go
func TestCreateNote(t *testing.T) {
	app := anetostest.New(t, setup)

	app.Get("/").AssertRedirect("/notes")
	app.Get("/notes/new").
		AssertOK().
		AssertSee("<title>New note · Notes</title>", "/assets/htmx.min.js?v=")

	// Invalid: back to the form, with the errors and the input kept.
	app.PostForm("/notes", url.Values{"title": {"<Groceries>"}, "body": {""}}).
		AssertRedirect("/notes/new").
		AssertValidationErrors("body").
		Follow().
		AssertSee(`value="&lt;Groceries&gt;"`, "The body field is required.", "Please fix the errors below.")

	// Valid: to the list, with a flash message shown once.
	app.PostForm("/notes", url.Values{"title": {"Groceries"}, "body": {"Milk"}}).
		AssertRedirectRoute("notes.index").
		AssertSessionHas("status", "Note created.").
		Follow().
		AssertSee("Note created.", "<strong>Groceries</strong>")
	app.Get("/notes").AssertDontSee("Note created.")
}
```

(Copied from [`examples/forms/main_test.go`](../../../examples/forms/main_test.go), region `test-forms`.)

Assertions report a failure with the request and the start of the body,
and return the response, so they chain. `Follow` loads the redirect's
target (on the test site only). `AssertSee` finds text as it is or
HTML-escaped, as templates write it.

Put things in the session before a request, such as a signed-in user,
with `WithSession`:

```go
// illustrative
app.WithSession(func(s *session.Session) { s.Put("user_id", user.ID) }).
	Get("/dashboard").
	AssertOK()
```

### 3. Test a JSON API

`GetJSON`, `PostJSON`, `PutJSON`, `PatchJSON` and `DeleteJSON` send and
accept JSON:

```go
func TestCreatePost(t *testing.T) {
	app := anetostest.New(t, setup) // a migrated database; the test leaves no rows behind
	author := anetostest.Create(app, Authors)

	app.PostJSON("/posts", map[string]any{"author_id": author.ID, "title": "Hello Go", "body": "a", "publish": true}).
		AssertCreated().
		AssertJSONPath("title", "Hello Go")
	anetostest.AssertDatabaseHas[Post](app, PostCols.Title.Eq("Hello Go"), PostCols.AuthorID.Eq(author.ID))

	app.PostJSON("/posts", map[string]any{"author_id": author.ID + 1, "title": "x", "body": "y"}).
		AssertUnprocessable().
		AssertValidationErrors("author_id")
	anetostest.AssertDatabaseCount[Post](app, 1)
}
```

(Copied from [`examples/database/main_test.go`](../../../examples/database/main_test.go), region `test-api`.)

`AssertJSONPath("data.0.title", "Hello")` checks one value (keys and
array indexes separated by dots); `AssertJSON(v)` the whole body; and
`res.JSON(&v)` decodes it. Values are compared as JSON, so `2` matches
`2.0` (exactly, however large) and a struct matches an object. `AssertValidationErrors` reads a 422
problem's `errors` for an API client, and the session's errors after a
form's redirect.

### 4. Make rows with factories

A factory makes valid model values, with a sequence number for values
that must differ:

```go
// Authors makes valid authors, each with its own email.
var Authors = factory.New(func(n int) Author {
	return Author{Name: fmt.Sprintf("Author %d", n), Email: fmt.Sprintf("author%d@example.com", n)}
})

// Posts makes drafts; set AuthorID with With.
var Posts = factory.New(func(n int) Post {
	return Post{Title: fmt.Sprintf("Post %d", n), Body: "Text.", Tags: []string{}}
})
```

(Copied from [`examples/database/factories_test.go`](../../../examples/database/factories_test.go), region `factories`.)

Create rows in the test's database, changing what the test cares about
with `With` (which returns a new factory, so the base stays shared):

```go
// illustrative
ada := anetostest.Create(app, Authors)
published := Posts.With(func(p *Post) { p.AuthorID, p.PublishedAt = ada.ID, &now })
anetostest.CreateMany(app, published, 3)
draft := Posts.Make() // a value, not saved
```

Put factories where both tests and seeders can import them: projects made
with `anetos new` have a `database/factories` package for them (the
examples keep theirs in package `main`). A seeder calls
`factories.Posts.CreateMany(ctx, 50)`; see [Seed the database](seeders.md).

The sequence number counts up for the whole test binary and never
resets, so a test's rows aren't numbered from 1: check the values
`Create` and `CreateMany` return, not "Post 1".

### 5. Check the database

```go
// illustrative
anetostest.AssertDatabaseHas[Post](app, PostCols.Title.Eq("Hello"))
anetostest.AssertDatabaseMissing[Post](app, PostCols.ID.Eq(id))
anetostest.AssertDatabaseCount[Post](app, 3, PostCols.AuthorID.Eq(ada.ID))
anetostest.AssertSoftDeleted[Post](app, PostCols.ID.Eq(id))
```

The conditions are the query builder's, and rows are counted as
`db.Query[T]` sees them: soft-deleted rows only count for
`AssertSoftDeleted`. To call your own code with the test's database, pass
`app.Context()`:

```go
// illustrative
post, err := posts.Publish(app.Context(), id)
```

## Complete example

- [`examples/forms/main_test.go`](../../../examples/forms/main_test.go):
  forms, validation, flash messages, htmx requests.
- [`examples/database/main_test.go`](../../../examples/database/main_test.go):
  a JSON API with factories and database assertions.

## How it works

Everything a test does runs on the test's context (`app.Context()`),
which carries the app's database. With SQLite and no `DB_DATABASE`, each
test gets its own in-memory database, migrated from scratch. With a
database server (or a SQLite file), migrations run once and each test runs
in a transaction that is rolled back when it ends, so tests leave nothing
behind and don't see each other's rows. Each request runs in a savepoint
inside it, so a failed statement (which aborts a PostgreSQL transaction)
doesn't break the rest of the test: the request's writes are undone
instead.

This differs from production in a few ways. `db.AfterCommit` callbacks
never run inside the test's transaction (they run at once with in-memory
SQLite, which has none). On PostgreSQL, a request that catches a failed
statement and goes on querying fails (production, without the
surrounding transaction, would carry on). A query canceled by a timeout
closes the connection holding the transaction on PostgreSQL and MySQL, and
MySQL commits it on any schema change. Test those paths with
`anetostest.WithoutTransaction()`.

Requests go straight to the router, without a network: `httptest`
requests to `http://example.test`, with a cookie jar. They carry no
`Origin` or `Sec-Fetch-Site` header, like a client that isn't a browser,
so `web.CSRF` checks only the token; its cross-origin check is covered by
the framework's own tests. `app.Do(req)`
sends a request you build, with extra headers:

```go
// illustrative
req := httptest.NewRequest(http.MethodDelete, "/notes/1", nil)
req.Header.Set("HX-Request", "true")
app.Do(req).AssertOK()
```

Every method is listed in the [anetostest reference](../reference/anetostest.md).

## Testing it

Run `go test ./...`, and `go test -race ./...` in CI. For PostgreSQL or
MySQL in CI, set `DB_*` (or `DB_URL`) in the job's environment: it wins
over `.env.testing`.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `connection refused` or `database "…_test" does not exist` | The test database from `.env.testing` isn't there | Create it, or set `DB_*` for the test run |
| `database is locked` | Two apps on one SQLite file: parallel tests, or an app made in a test and another in its subtest | Don't run those in parallel, or use in-memory SQLite |
| A request hangs until the test times out | Code opened its own connection (or `db.Tx` on a new context) and waits for the test's transaction's locks; or two parallel tests insert the same unique value | Use the request's context; `anetostest.WithoutTransaction()` and clean up yourself; unique values from factories |
| `current transaction is aborted` (PostgreSQL) | A handler went on after a failed statement | Test that path with `anetostest.WithoutTransaction()` |
| `The request left the test's transaction unusable` | A timeout cancelled a query, or a schema change on MySQL | `anetostest.WithoutTransaction()` for that test |
| `AfterCommit` callbacks don't run (or run on SQLite only) | The test's transaction never commits | `anetostest.WithoutTransaction()` for that test |
| 403 "The page has expired" | The form sent a `_token` field (it wins over the automatic token), or the route has no session middleware | Drop the field; add `sessions.Middleware` |
| `subtest may have called FailNow on a parent test` | An app made in a test used in its subtest | Make the app in the subtest |
| `setup returned no server` | `setup` returns a nil `*web.Server` | Return the server |
| A redirect back goes to `/` | No HTML page loaded first, so there's no `Referer` | `app.Get` the form's page before posting |

> **Coming from Laravel?** `anetostest.New` is the `TestCase` with
> `RefreshDatabase` (in-memory SQLite, or migrations plus a transaction per
> test). `app.PostForm(…).AssertRedirect(…)` and friends are `$this->post()`
> and `assertRedirect()`; factories are functions returning structs instead
> of classes, and `Posts.With(…)` is a state. Clock, mail and queue fakes
> come with those features.

## Next steps

- [Handle forms](forms.md)
- [Validate input](validation.md)
- [Seed the database](seeders.md): factories work in seeders too.

---
title: Test your app
since: v0.1.0
---

# Test your app

Boot the app in a test, send it requests like a browser or an API
client, and check the responses, the session, the database, the jobs,
events and email the app sent, and its files, with `anetostest`; freeze
or move the app's clock.

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
2. `APP_ENV=testing`, a random `APP_KEY`, and a `CACHE_PREFIX`,
   `SESSION_PREFIX`, `QUEUE_PREFIX` and `PUBSUB_PREFIX` of the app's own,
   so tests sharing a store don't see each other's items, sessions, Redis
   jobs or streams (they are removed when the app shuts down);
   `MAIL_DRIVER=memory`, so emails are kept, not sent (see
   [Send email](mail.md#6-test)); and `STORAGE_DRIVER=memory`, so files
   are kept in memory (see [Store files](storage.md#6-test));
3. the process environment;
4. `.env.testing` next to `go.mod`, if there is one (for PostgreSQL or
   MySQL, all the `DB_*` settings: see
   [Switch a project to PostgreSQL or MySQL](database.md#5-switch-a-project-to-postgresql-or-mysql));
5. `HTTP_ACCESS_LOG=false`, `APP_URL=http://example.test` (the test client's site) and
   `MAIL_FROM_ADDRESS=test@example.com`.

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

### 6. Check jobs, events, emails and messages

`anetostest` records what the app dispatches to the queue, emits as
events, sends or queues as email, and publishes to pub/sub, and checks
it with typed assertions. By default these still happen as usual (with
`QUEUE_DRIVER=sync`, jobs run at once); fake them to only record:

| Option | Then |
|---|---|
| `anetostest.FakeQueue()` | Dispatched jobs are recorded, not run or stored (queued email and queued listeners too) |
| `anetostest.FakeEvents(OrderPlaced{}, …)` | Those events (all, with no arguments) are recorded and don't reach their listeners |
| `anetostest.FakePubSub()` | Published messages are recorded, not sent to the broker |

Email is never sent in tests (`MAIL_DRIVER=memory`), so it needs no
fake. For sign-in with Google or GitHub, `anetostest.FakeSocial()` puts
a stand-in provider in their place and `app.SocialSignIn` signs in with
the account you give (see [Social login](social-login.md#3-test)).

```go
// With the queue and the OrderPlaced event faked, placing an order only
// records the charge job and the event: the test checks they were
// dispatched and emitted, and nothing ran.
func TestPlaceOrderFaked(t *testing.T) {
	g := &FakeGateway{}
	fakeGateway(t, g)
	app := anetostest.New(t, setup, anetostest.FakeQueue(), anetostest.FakeEvents(OrderPlaced{}))

	id := placeOrder(t, app, "Book", 1500)
	anetostest.AssertDispatched(app, func(j ChargeOrder) bool { return j.OrderID == id })
	anetostest.AssertEmitted(app, func(e OrderPlaced) bool { return e.OrderID == id && e.Cents == 1500 })
	anetostest.AssertMailNotSent[ReceiptMail](app, nil) // its listener didn't run
	if orderStatus(t, app, id) != "pending" || g.Charges() != 0 {
		t.Error("the charge ran")
	}
}
```

(Copied from [`examples/queue/main_test.go`](../../../examples/queue/main_test.go), region `test-fakes`.)

Each assertion takes the type and a `match` function (`nil` for any):

| Check | Assertions | Values |
|---|---|---|
| Jobs | `AssertDispatched[J]`, `AssertNotDispatched[J]`, `app.AssertNothingDispatched()` | `Jobs[J](app)`; `app.Dispatched()`, function jobs included |
| Events | `AssertEmitted[E]`, `AssertNotEmitted[E]`, `app.AssertNothingEmitted()` | `Events[E](app)`, `app.Emitted()` |
| Email | `AssertMailSent[M]` (`mailer.Send`), `AssertMailQueued[M]` (`mailer.Queue`), `AssertMailNotSent[M]`, `app.AssertNoMail()` | `Mailables[M](app)`, `app.Mail()` |
| Pub/sub | `AssertPublished[T](app, topic, match)`, `AssertNotPublished[T]` | `Messages[T](app, topic)`, `app.Published()` |

### 7. Check files

Disks keep their files in memory in tests (`STORAGE_DRIVER=memory`), so
each test starts with empty disks. `app.Disk()` (the default disk) and
`app.Disk("avatars")` check them; the clock ([step 8](#8-control-the-clock))
lets a test go past a link's expiry:

```go
// The upload is on the default disk, and its temporary URL stops working
// after 15 minutes: the test travels in time instead of waiting.
func TestDocumentLink(t *testing.T) {
	app := anetostest.New(t, setup, env)
	uploaded := app.Freeze(time.Time{})

	var doc Document
	app.PostMultipart("/documents", nil, anetostest.Upload{Field: "file", Filename: "q3.pdf", Content: pdf}).
		AssertStatus(http.StatusCreated).
		JSON(&doc)
	app.Disk().AssertContent("documents/"+doc.Name, string(pdf))
	app.Disk("avatars").AssertMissing("documents/" + doc.Name)
	if !doc.UploadedAt.Equal(uploaded) {
		t.Errorf("uploaded at %v, want %v", doc.UploadedAt, uploaded)
	}

	app.Travel(14 * time.Minute)
	app.Get(doc.URL).AssertOK()
	app.Travel(2 * time.Minute)
	app.Get(doc.URL).AssertStatus(http.StatusForbidden)
}
```

(Copied from [`examples/files/main_test.go`](../../../examples/files/main_test.go), region `test-disk`.)

`AssertExists`, `AssertMissing` and `AssertContent` chain; `Files(prefix)`
lists paths.

### 8. Control the clock

`app.Freeze(t)` stops the app's clock (at the current time, for the zero
time) and returns the time; `app.Travel(d)` moves it; `app.Unfreeze()`
returns to the real time. The framework reads the time from that clock:
timestamps, the expiry of sessions, cookies, tokens, signed URLs and
cached items, `after:now` rules. Read it in your code with
`anetos.Now(ctx)` instead of `time.Now()`, so tests control it too.

```go
// With the clock frozen, timestamps are known: created_at, published_at
// (set by the handler from anetos.Now), and deleted_at an hour later.
func TestTimestamps(t *testing.T) {
	app := anetostest.New(t, setup)
	now := app.Freeze(time.Time{})
	author := anetostest.Create(app, Authors)

	var p Post
	app.PostJSON("/posts", map[string]any{"author_id": author.ID, "title": "Hello", "body": "x", "publish": true}).
		AssertCreated().
		JSON(&p)
	anetostest.AssertDatabaseHas[Post](app, PostCols.ID.Eq(p.ID), PostCols.CreatedAt.Eq(now), PostCols.PublishedAt.Eq(&now))

	app.Travel(time.Hour)
	app.Delete("/posts/" + strconv.FormatInt(p.ID, 10)).AssertNoContent()
	later := now.Add(time.Hour)
	anetostest.AssertSoftDeleted[Post](app, PostCols.ID.Eq(p.ID), PostCols.DeletedAt.Eq(&later))
}
```

(Copied from [`examples/database/main_test.go`](../../../examples/database/main_test.go), region `test-clock`.)

Travel past an expiry instead of waiting for it:

```go
// The reset link works for AUTH_RESET_TTL (60 minutes): travel past it.
func TestResetLinkExpires(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/forgot-password")
	app.PostForm("/forgot-password", url.Values{"email": {"ada@example.com"}})
	q, _ := url.ParseQuery(emailedLink(t, app, "Reset your password")[len("/reset-password?"):])

	app.Travel(61 * time.Minute)
	app.Get("/reset-password?" + q.Encode())
	app.PostForm("/reset-password", url.Values{"token": {q.Get("token")}, "password": {"new password"}, "password_confirmation": {"new password"}}).
		AssertValidationErrors("password")
}
```

(Copied from [`examples/auth/main_test.go`](../../../examples/auth/main_test.go), region `test-clock`.)

The clock doesn't move time kept elsewhere: database and Redis servers
(items in the database and Redis cache stores expire, and those queues'
delayed jobs become due, on their server's clock), timeouts, and the
scheduler's and workers' loops. The [testing reference](../reference/anetostest.md#clock-anetostest-anetos)
lists what reads the app's clock.

## Complete example

- [`examples/forms/main_test.go`](../../../examples/forms/main_test.go):
  forms, validation, flash messages, htmx requests.
- [`examples/database/main_test.go`](../../../examples/database/main_test.go):
  a JSON API with factories, database assertions and a frozen clock.
- [`examples/queue/main_test.go`](../../../examples/queue/main_test.go):
  jobs, events and email, run or faked.
- [`examples/files/main_test.go`](../../../examples/files/main_test.go):
  uploads, disks and expiring links.

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

This differs from production in a few ways. Work done directly in the
test's transaction counts as committed: `db.AfterCommit` callbacks run
when a `db.Tx` inside it (a request's, say) commits, or at once outside
one. Code with its own connections (queue workers, `db.WithoutTx`)
doesn't see the test's rows. On PostgreSQL, a request that catches a failed
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
| A queue worker doesn't find the test's rows | Workers use their own connections, outside the test's transaction | `QUEUE_DRIVER=sync`, or `anetostest.WithoutTransaction()` for that test |
| 403 "The page has expired" | The form sent a `_token` field (it wins over the automatic token), or the route has no session middleware | Drop the field; add `sessions.Middleware` |
| `subtest may have called FailNow on a parent test` | An app made in a test used in its subtest | Make the app in the subtest |
| `setup returned no server` | `setup` returns a nil `*web.Server` | Return the server |
| A redirect back goes to `/` | No HTML page loaded first, so there's no `Referer` | `app.Get` the form's page before posting |
| `app.Freeze` doesn't change a time your code sets | The code reads `time.Now()` | Use `anetos.Now(ctx)` |
| An item in the database or Redis cache doesn't expire after `app.Travel` | Those stores use their server's clock | Use `CACHE_STORE=memory` in tests, or test expiry another way |
| `anetostest: FakeQueue: the app has no queue` | `setup` doesn't call `queue.ForApp` (or `events.ForApp`, `pubsub.ForApp` for the other fakes) | Drop the option, or set the service up |
| `job type … isn't registered` from `Jobs[J]` or `AssertDispatched[J]` | `J` isn't registered with `queue.Register` | Register it in `setup`; check function jobs with `app.Dispatched()` |
| `Search` finds nothing on MySQL or MariaDB in a test | Their full-text indexes only see committed rows, and the test runs in a transaction | `anetostest.WithoutTransaction()` for that test, or test search on SQLite or PostgreSQL |
| The app doesn't start: a search index doesn't match `SEARCH_LANGUAGE` | The test database was migrated with other search settings | Run `search:reindex` (or `migrate:fresh`) on it with the test settings |

> **Coming from Laravel?** `anetostest.New` is the `TestCase` with
> `RefreshDatabase` (in-memory SQLite, or migrations plus a transaction per
> test). `app.PostForm(…).AssertRedirect(…)` and friends are `$this->post()`
> and `assertRedirect()`; factories are functions returning structs instead
> of classes, and `Posts.With(…)` is a state. `anetostest.FakeQueue()`,
> `FakeEvents(…)` and `FakePubSub()` are `Queue::fake()`, `Event::fake()`
> and friends, with typed assertions (`AssertDispatched[ChargeOrder]` for
> `assertPushed(ChargeOrder::class, …)`); mail needs no `Mail::fake()`,
> since tests never send it. `app.Freeze`, `app.Travel` are
> `freezeTime()` and `travel()`, and `anetos.Now(ctx)` is `now()`.
> `app.Disk()` replaces `Storage::fake()`.

## Next steps

- [Handle forms](forms.md)
- [Validate input](validation.md)
- [Seed the database](seeders.md): factories work in seeders too.

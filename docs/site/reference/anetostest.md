---
title: Testing reference
since: v0.1.0
---

# Testing reference

The APIs of packages `anetostest` and `db/factory`, and the session
hooks they use. See [Test your app](../guides/testing.md) for a
walkthrough.

## Starting the app (`anetostest`)

| API | Does |
|---|---|
| `anetostest.New(t, setup, opts...)` | `*anetostest.App`: builds the app with `setup` (`func(*anetos.App) (*web.Server, error)`, or nil), boots it, runs its migrations, starts the test's transaction; closes the app when the test ends. Fails the test on any error. One per test or subtest: it reports to t |
| `anetostest.Env(map[string]string)` | Option: settings over everything else |
| `anetostest.WithoutMigrations()` | Option: don't run the migrations |
| `anetostest.WithoutTransaction()` | Option: don't wrap the test in a transaction; its writes are committed |
| `anetostest.LogLevel(level)` | Option: minimum level of the app's logs in the test's log. Default Info |
| `app.Context()` | The context of the test's requests: the app's services, the database and the test's transaction. Pass it to your own code. (It hides the embedded `anetos.App.Context(parent)`; call `app.App.Context` for that) |
| `app.Router()` | The app's `*web.Router`, or nil |
| `app.App` | The embedded `*anetos.App`: `app.Config()`, `anetos.Resolve[T](app.App)`, … |

### Settings

Highest priority first. `.env` is never read.

| Source | Holds |
|---|---|
| `anetostest.Env` | Whatever the test passes |
| Forced | `APP_ENV=testing`, a random `APP_KEY` |
| Process environment | `DB_*` in CI, … |
| `.env.testing` | Next to the test's `go.mod`; optional |
| Defaults | `HTTP_ACCESS_LOG=false` |

With `DB_CONNECTION` unset or `sqlite`, and `DB_DATABASE` and `DB_URL`
unset or empty in every source, `DB_DATABASE=:memory:` wins over all of
them: never the default `database/app.db`.

### Database

| Database | Each test gets |
|---|---|
| SQLite in memory (the default; checked with SQLite after connecting) | Its own database, migrated; no transaction, so `db.AfterCommit` callbacks run at once |
| SQLite file, PostgreSQL, MySQL | The migrated database, and a transaction rolled back when the test ends (`AfterCommit` callbacks never run). Each request runs in a savepoint (`anetostest_request`), rolled back if the request left the transaction failed. With `WithoutTransaction()`, neither |

## Requests (`anetostest`)

Every method returns a `*anetostest.Response`. Paths may have a query;
an URL on another site fails the test. Requests go to the router as
`http://example.test/…`, with:

- the jar's cookies (Path and expiry honored; Secure and Domain ignored);
- the headers set with `WithHeader`, which replace the method's `Accept`
  and `Content-Type`;
- as `Referer`, the last HTML page: a `GET` answered 200 with
  `text/html`, not an htmx fragment (`HX-Request` without `HX-Boosted`);
- for methods other than GET, HEAD, OPTIONS and TRACE, the session's
  CSRF token in `X-CSRF-Token`, unless the request has the header or
  (forms) a `_token` field.

| API | Sends | `Accept` |
|---|---|---|
| `app.Get(path)`, `app.Head(path)`, `app.Delete(path)` | No body | `text/html` |
| `app.PostForm(path, url.Values)`, `PutForm`, `PatchForm`, `DeleteForm` | URL-encoded form | `text/html` |
| `app.GetJSON(path)`, `app.DeleteJSON(path)` | No body | `application/json` |
| `app.PostJSON(path, v)`, `PutJSON`, `PatchJSON` | v encoded as JSON | `application/json` |
| `app.Do(req)` | req (from `httptest.NewRequest` with a path), adding the jar's cookies it doesn't have, headers it doesn't have, `Referer` and token | req's |
| `res.Follow()` | GET to the redirect's `Location`; fails the test for another site | the request's |
| `app.WithHeader(name, value)` | Sets a header on every later request; returns app | |
| `app.WithSession(func(*session.Session))` | Changes the session later requests carry; returns app. Needs `session.ForApp` in setup | |
| `app.Session()` | The `*session.Session` the next request will carry (flash values and errors from the last response included), to read | |

## Responses (`anetostest.Response`)

Fields: `StatusCode`, `Header`, `Body []byte`, `Request`. Assertions
report failures with `t.Errorf` (naming the request, with the start of the
body) and return the response.

| API | Checks |
|---|---|
| `AssertStatus(code)` | The status |
| `AssertOK()`, `AssertCreated()`, `AssertNoContent()` | 200, 201, 204 |
| `AssertNotFound()`, `AssertForbidden()`, `AssertUnprocessable()` | 404, 403, 422 |
| `AssertRedirect(path)` | A 3xx to path (`http://example.test` dropped from `Location`) |
| `AssertRedirectRoute(name, args...)` | A 3xx to the named route |
| `AssertHeader(name, value)` | One of the header's values is value |
| `AssertSee(texts...)`, `AssertDontSee(texts...)` | The body contains (none of) the texts, as they are or HTML-escaped (`html.EscapeString`, or html/template's, which also escapes `+`) |
| `AssertJSON(v)` | The body is JSON equal to v, compared as JSON (numbers exactly, by value) |
| `AssertJSONPath(path, v)` | The value at path (`"data.0.title"`; `""` for the whole body) equals v, compared as JSON |
| `AssertValidationErrors(fields...)` | A 422 (or 400) problem JSON with the fields in `errors`, or a redirect with the fields' errors flashed to the session. No fields: any error. Send JSON requests to check a 422 |
| `AssertNoValidationErrors()` | Not a 422, not a 400 with field errors, no errors flashed |
| `AssertSessionHas(key, value?)` | The next request's session has key (holding value, compared as JSON) |
| `AssertSessionMissing(key)` | It doesn't |
| `JSON(&v)` | Decodes the body; fails the test if it can't |
| `Text()` | The body as a string |

## Database (`anetostest`)

Generic functions, run on `app.Context()`. Conditions are
`db.Expr`s: `PostCols.Title.Eq("Hello")`.

| API | Does |
|---|---|
| `anetostest.AssertDatabaseHas[T](app, conds...)` | A T row matches (soft-deleted rows excluded) |
| `anetostest.AssertDatabaseMissing[T](app, conds...)` | No T row matches |
| `anetostest.AssertDatabaseCount[T](app, n, conds...)` | n T rows match |
| `anetostest.AssertSoftDeleted[T](app, conds...)` | A soft-deleted T row matches; T embeds `db.SoftDeletes` |
| `anetostest.Create(app, factory)` | Inserts a row made by the factory; returns it |
| `anetostest.CreateMany(app, factory, n)` | Inserts n rows |

## Factories (`db/factory`)

| API | Does |
|---|---|
| `factory.New(func(n int) T)` | `*factory.Factory[T]`; n is a sequence number from 1, unique per factory (shared by the factories `With` derives) |
| `f.With(fns...)` | A new factory that also applies `func(*T)`s to every value |
| `f.Make()`, `f.MakeMany(n)` | Values, not saved |
| `f.Create(ctx)`, `f.CreateMany(ctx, n)` | Inserted with `db.Create` (hooks run), one row at a time; stops at the first error |

## Sessions (`session`)

| API | Does |
|---|---|
| `m.Load(r)` | The request's `*session.Session` as the session middleware would load it (a new one if the cookie is missing or invalid) |
| `m.Edit(r, fn)` | `(*http.Cookie, error)`: the cookie of the request's session after fn changed it. fn sees the session as a handler would; what was flashed for the next request is kept for it (`Reflash`) |
| `session.ForApp`, `migrate.ForApp` | Also provide the manager and the runner to the app (`anetos.Resolve`), which is how `anetostest` finds them |

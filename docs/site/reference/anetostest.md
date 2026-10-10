---
title: Testing reference
since: v0.1.0
group: "Features"
weight: 402
---

# Testing reference

The APIs of packages `anetostest` and `db/factory`, and the hooks
they use. See [Test your app](../guides/testing.md) for a
walkthrough.

## Starting the app (`anetostest`)

| API | Does |
|---|---|
| `anetostest.New(t, setup, opts...)` | `*anetostest.App`: builds the app with `setup` (`func(*anetos.App) (*web.Server, error)`, or nil), boots it, runs its migrations, starts the test's transaction; closes the app when the test ends. Fails the test on any error. One per test or subtest: it reports to t |
| `anetostest.Env(map[string]string)` | Option: settings over everything else |
| `anetostest.WithoutMigrations()` | Option: don't run the migrations |
| `anetostest.WithoutTransaction()` | Option: don't wrap the test in a transaction; its writes are committed |
| `anetostest.LogLevel(level)` | Option: minimum level of the app's logs in the test's log. Default Info |
| `anetostest.FakeQueue()` | Option: dispatched jobs are recorded only (`queue.Queue.Fake`): not stored or run. Fails the test if `setup` has no queue |
| `anetostest.FakeEvents(events...)` | Option: events of the types of the values (all, with none) are recorded only (`events.Bus.Fake`): their listeners don't run. Fails the test if `setup` has no bus |
| `anetostest.FakePubSub()` | Option: published messages are recorded only (`pubsub.PubSub.Fake`): the broker doesn't get them. Fails the test if `setup` has no pub/sub |
| `anetostest.FakeAI(replies...)` | Option: the model's answers, one per request to the app's AI client, in order (`ai.FakeText`, `ai.FakeObject`, `ai.FakeToolCall`, `ai.FakeError`, or an `ai.FakeReply` function). Options add up. Fails the test if `setup` has no AI client (`ai.New`). See [AI](#ai-anetostest) |
| `anetostest.FakeSocial()` | Option: social login (`auth/social`) logs in through a stand-in OpenID Connect provider on a local TLS server, for every provider (GitHub's API and other `Provider.Profile` functions aren't called); `social.Configured` keeps every provider, with test credentials where settings are missing. Log in with `app.SocialLogin` |
| `app.Context()` | The context of the test's requests: the app's services, the database and the test's transaction. Pass it to your own code. (It hides the embedded `anetos.App.Context(parent)`; call `app.App.Context` for that) |
| `app.Router()` | The app's `*web.Router`, or nil |
| `app.App` | The embedded `*anetos.App`: `app.Config()`, `anetos.Resolve[T](app.App)`, … |

### Settings

Highest priority first. `.env` is never read.

| Source | Holds |
|---|---|
| `anetostest.Env` | Whatever the test passes |
| Forced | `APP_ENV=testing`, a random `APP_KEY`, a `CACHE_PREFIX`, `SESSION_PREFIX`, `QUEUE_PREFIX` and `PUBSUB_PREFIX` of the app's own (its items, server-side sessions, Redis jobs and streams are removed by shutdown hooks when the app stops: at the end of the test, or when the test's `app.Run` returns); `MAIL_DRIVER=memory` (emails are kept in the mailer's `*mailer.MemoryTransport`, not sent); `STORAGE_DRIVER=memory` (files are kept in memory, on every disk without a driver of its own); `AI_PROVIDER=fake` (no model is called: `FakeAI` scripts the answers; `anetostest.Env` can set another) and an empty `AI_EMBEDDING_PROVIDER` (embeddings are `AI_PROVIDER`'s: the fake's) |
| Process environment | `DB_*` in CI, … |
| `.env.testing` | Next to the test's `go.mod`; optional |
| Defaults | `HTTP_ACCESS_LOG=false`, `APP_URL=http://example.test` (the test client's site), `MAIL_FROM_ADDRESS=test@example.com` |

With `DB_DRIVER` unset or `sqlite`, and `DB_NAME` and `DB_URL`
unset or empty in every source, `DB_NAME=:memory:` wins over all of
them: never the default `database/app.db`.

### Database

| Database | Each test gets |
|---|---|
| SQLite in memory (the default; checked with SQLite after connecting) | Its own database, migrated; no transaction, so `db.AfterCommit` callbacks run at once |
| SQLite file, PostgreSQL, MySQL | The migrated database, and a transaction rolled back when the test ends (`db.WithTestTx`: `AfterCommit` callbacks run when a `db.Tx` inside it commits, or at once outside one). Each request runs in a savepoint (`anetostest_request`), rolled back if the request left the transaction failed. With `WithoutTransaction()`, neither |

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
| `app.PostMultipart(path, url.Values, anetostest.Upload{Field, Filename, Content, ContentType}…)` | Multipart form: the fields, then the files | `text/html` |
| `app.GetJSON(path)`, `app.DeleteJSON(path)` | No body | `application/json` |
| `app.PostJSON(path, v)`, `PutJSON`, `PatchJSON` | v encoded as JSON | `application/json` |
| `app.Do(req)` | req (from `httptest.NewRequest` with a path), adding the jar's cookies it doesn't have, headers it doesn't have, `Referer` and token | req's |
| `res.Follow()` | GET to the redirect's `Location`; fails the test for another site | the request's |
| `app.WithHeader(name, value)` | Sets a header on every later request; returns app | |
| `app.WithSession(func(*session.Session))` | Changes the session later requests carry; returns app. Needs `session.New` in setup | |
| `anetostest.ActingAs(app, u)` | Logs u in for later requests, as a password login without remember-me would, without asking for a two-factor code (`auth.Auth.LoginSession`), replacing whoever was logged in (and dropping a remember-me cookie); returns app. U is the type given to `auth.New` (`*models.User`); needs `session.New` and `auth.New` in setup; a disabled user fails the test (v0.3) | |
| `app.Session()` | The `*session.Session` the next request will carry (flash values and errors from the last response included), to read | |
| `app.SocialLogin(redirect, anetostest.SocialAccount{ID, Email, EmailVerified, Name, AvatarURL})` | GET redirect (the app's route to the provider, such as `/auth/google/redirect`), the stand-in provider's login as the account, then GET the app's callback; returns the callback's response. Needs `FakeSocial`; `ID` is required | `text/html` |

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

## Jobs, events, email and messages (`anetostest`)

`anetostest.New` records, from the end of `setup` on, what the app
dispatches, emits, mails and publishes, through the services' `Observe`
hooks, whether or not they are faked. `match` is a `func(T) bool`;
`nil` matches any. Assertions report with `t.Errorf` and list what was
recorded.

| API | Does |
|---|---|
| `app.Dispatched()` | `[]queue.Dispatched` (`ID`, `Job`, `Queue`, `Delay`, `Data`; `Decode(&v)`), every job, function jobs (`mail:send`, `event:…`) included, oldest first. A job is recorded once dispatched: when the store has it (the database driver writes it in the request's transaction: once that commits), with `AfterCommit` after the commit, with the sync driver before it runs; a dispatch that fails or is rolled back isn't recorded |
| `anetostest.Jobs[J](app)` | The `J` jobs dispatched, decoded. J must be registered (`queue.Register`); fails the test otherwise |
| `anetostest.AssertDispatched[J](app, match)` | A `J` job matches |
| `anetostest.AssertNotDispatched[J](app, match)` | None matches |
| `app.AssertNothingDispatched()` | No job of any type |
| `app.Emitted()` | `[]any`: every event, oldest first, recorded when `Emit` is called, before its listeners |
| `anetostest.Events[E](app)` | The events of type E (or implementing E, an interface) |
| `anetostest.AssertEmitted[E](app, match)`, `AssertNotEmitted[E]`, `app.AssertNothingEmitted()` | As for jobs |
| `app.Mail()` | `[]mailer.Record` (`Mailable`, rendered `Message`, `Queued`): emails sent with `mailer.Send` (once the transport took them) or queued with `mailer.Queue` (once the job is dispatched; with the sync driver, once the job sent it) |
| `anetostest.Mailables[M](app)` | The mailables of type M, sent or queued |
| `anetostest.AssertMailSent[M](app, match)` | An M sent with `mailer.Send` matches |
| `anetostest.AssertMailQueued[M](app, match)` | An M queued with `mailer.Queue` matches |
| `anetostest.AssertMailNotSent[M](app, match)` | No M sent or queued matches |
| `app.AssertNoMail()` | Nothing sent or queued |
| `app.Published()` | `[]pubsub.Published` (`Topic`, `Data`, `Attributes`; `Decode(&v)`), oldest first |
| `anetostest.Messages[T](app, topic)` | The messages of topic, decoded from JSON as T |
| `anetostest.AssertPublished[T](app, topic, match)`, `AssertNotPublished[T]` | A message of topic matches; none does |

The emails the transport got (queued ones the queue ran included) stay
available from the mailer: `anetos.MustResolve[*mailer.Mailer](app.App).Transport().(*mailer.MemoryTransport).Sent()`.

## AI (`anetostest`)

The app's AI client (`ai.New`) uses the fake provider in tests
(`*ai.Fake`): requests get the replies `FakeAI` scripted, in order, and a
request with no reply left fails with an error saying so. A test that
sets another `AI_PROVIDER` with `anetostest.Env` uses that provider,
unless it also uses `FakeAI`, which puts the fake in its place. The
fake makes embeddings too (`ai.Embed`, `ai.Embeddings`), without
replies: vectors of the texts' words, so texts sharing words are near,
of the size asked for (or, for a `FixedSize` model, the size expected).

| API | Does |
|---|---|
| `app.AI()` | The `*ai.Fake`: `Requests()` (`[]ai.Request`, oldest first: `Prompt()`, `System`, `Messages`, `Tools`, `Output`, `Model`…), `Add(replies...)` for more replies, `Remaining()`, `Embeddings()` (`[]ai.EmbedRequest`: `Model`, `Inputs`, `Dimensions`, `Purpose`). Fails the test if the app has no AI client, or its provider isn't the fake |
| `app.AssertPrompted(match)` | A request matched (`func(ai.Request) bool`; nil matches any); otherwise reports the last prompt |
| `app.AssertNotPrompted()` | No request was made |
| `ai.FakeText(text)` | Reply: text |
| `ai.FakeObject(v)` | Reply: v as JSON, the answer to `ai.GenerateObject` |
| `ai.FakeToolCall(name, input)` | Reply: a call of tool name with input (encoded as JSON); its result goes to the next request |
| `ai.FakeError(err)` | Reply: the request fails with err |

Replies' usage counts words, as a stand-in for tokens.

## Repeated queries (`anetostest`)

| API | Does |
|---|---|
| `app.RepeatedQueries()` | `[]db.RepeatedQuery` (`Unit`, `SQL`, `Count`, `Caller`; `String()`): the queries a request, job, listener, task or AI tool call of the test ran `DB_REPEATED_QUERIES` times or more (5 by default in tests), oldest first. Also logged as warnings |
| `app.AssertNoRepeatedQueries()` | None: no N+1. See [Find N+1 queries](../guides/n-plus-one.md) |

## Files (`anetostest`)

| API | Does |
|---|---|
| `app.Disk(name...)` | `*anetostest.Disk`: the default disk, or the named one (`STORAGE_DISKS`). Fails the test without `storage.New` or for an unknown disk |
| `d.AssertExists(paths...)` | The files exist |
| `d.AssertMissing(paths...)` | They don't |
| `d.AssertContent(path, want)` | The file exists with that content |
| `d.Files(prefix)` | The paths under prefix (`""`: all), in order |
| `d.Storage()` | The `*storage.Disk`, to add or read files |

## Clock (`anetostest`, `anetos`)

| API | Does |
|---|---|
| `app.Freeze(t)` | Stops the app's clock at t (now, for the zero time), truncated to microseconds; returns it |
| `app.Travel(d)` | Moves the clock by d (back if negative): a frozen clock stays frozen, a running one runs d ahead |
| `app.Unfreeze()` | Back to the system's time |
| `app.Now()`, `anetos.Now(ctx)` | The time on the app's clock (`anetos.Now` without an app in ctx: `time.Now()`) |
| `app.App.SetClock(fn)` | The hook underneath: the app reads the time from fn (nil: the system's clock) |
| `anetos.WithClock(ctx, fn)` | A context with its own clock, for code without an app |

Read from the app's clock: model timestamps (`created_at`, `updated_at`,
`deleted_at`); session lifetimes (whatever the store); the expiry of
the test client's cookies, password reset, verification and
remember-me tokens, API tokens (and their `last_used_at`), temporary
URLs signed with `APP_KEY`, social login state, and the memory cache's
items and locks; rate-limit windows; `after:now`-style validation
rules; emails' `Date`; memory disks' modification times. Not affected:
the clocks of database and Redis servers, which expire the items of the
database and Redis cache stores (rate-limit counters there included)
and time their queues; the clock of a store that signs its own URLs
(S3, which checks them in real time); queue delays and leases, in every
driver; timeouts and durations; the scheduler's and workers' loops;
log timestamps.

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
| `session.New`, `migrate.New` | Also provide the manager and the runner to the app (`anetos.Resolve`), which is how `anetostest` finds them |

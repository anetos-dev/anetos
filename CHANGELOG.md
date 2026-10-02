# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/) (see the roadmap's versioning rules).

## [Unreleased]

### Added
- Full-text search: `q.Search(text)` keeps the rows matching every word
  (as prefixes) of text in a search index's columns and orders them by
  relevance, with `Where`, soft deletes, `Paginate`, `Count` and the
  aggregates; `t.SearchIndex(cols...)` and `t.DropSearchIndex()` in
  migrations build each database's own index (PostgreSQL `tsvector` + GIN,
  MySQL/MariaDB `FULLTEXT`, SQLite FTS5 kept in sync by triggers), with
  column weights, recorded in the `search_indexes` table
  (`db.SearchIndexes`); `s.Drop` removes it with the table (S1, design
  §10.5, D154, D160, D162).
- `SEARCH_LANGUAGE` (`simple`, the default, or a language: `english` on
  PostgreSQL and SQLite, any text search configuration on PostgreSQL) and
  `SEARCH_RANKING` (`default` or `bm25`: SQLite's FTS5, PostgreSQL 17+ with
  pg_textsearch); the `search:reindex` command rebuilds indexes for them
  (S1, D155, D160).
- Database capabilities checked when the app boots: `db.Capability`
  (`db.FullText`, `db.BM25`), `d.Supports`, `d.Require(feature, caps...)`,
  `d.Check` and `d.CheckSearch`; `db.Connect` refuses to start when the
  database can't serve the `SEARCH_*` settings or a requirement, or when a
  search index was built for other settings, naming the setting, the
  database and the way out (S1, D153).
- `cmd.Command.ChangesSchema`, and `cmd.WithCommand`/`cmd.Running` to know
  at boot which command runs: the migration commands and `search:reindex`
  run even when the search indexes are out of date (S1, D162).
- `anetos new` writes `SEARCH_LANGUAGE` and `SEARCH_RANKING` to `.env` and
  `.env.example`, with what the chosen database supports (S1).
- `examples/forms` has a search box; guide "Add full-text search",
  references for the migration methods, the query builder, the settings
  and the command (S1).
- AI (`ai` package): `ai.Generate` (text), `ai.GenerateObject[T]` (an
  answer decoded into a struct and checked with its `validate` tags,
  retried once with the problems, else an `*ai.OutputError`, 502) and
  `ai.Stream` (an iterator of events), with options (`System`, `Model`,
  `MaxTokens`, `Temperature`, `Timeout`, `Messages`, `Tools`,
  `MaxSteps`, `ProviderOptions`, `Using`); every call returns an
  `*ai.Result` with the steps, their total usage and the conversation,
  which marshals to JSON (A1, design §14.4, D156, D157, D163).
- AI tools: `ai.Func(name, description, fn)` with a typed input whose
  JSON schema comes from its json, description and validate tags
  (`ai.SchemaFor[T]`), validated before fn runs; tools run with the
  caller's context, as the current user; a tool error with a 4xx status
  is told to the model as a web client would see it, others stop the
  call; `ai.Agent` bundles instructions, tools and options (A1, D157,
  D164).
- `ai.ForApp(app, drivers...)` with `AI_PROVIDER`, `AI_MODEL`,
  `AI_MAX_TOKENS` and `AI_TIMEOUT`; the `ai.Provider` contract for driver
  modules, `Request.Options`, `Response.Raw` and `Client.Provider()` for
  what it doesn't cover; each model request is logged with its tokens
  (never its content), and each tool call is a unit of work (kind
  `tool`) (A1, D156, D165).
- `ai.Fake` (`AI_PROVIDER=fake`) with scripted replies (`ai.FakeText`,
  `FakeObject`, `FakeToolCall`, `FakeError`); `anetostest` forces it, and
  `anetostest.FakeAI(replies...)`, `app.AI()`, `app.AssertPrompted` and
  `app.AssertNotPrompted` test what the app asks a model (A1, D158,
  D165).
- `examples/ai`, a support desk API with a typed summary, an agent with
  a tool over the customer's orders and a streamed answer; guide "Add AI
  to your app", concept and reference pages (A1).
- `web.HTTPError.ClientMessage()` and `ClientFields()`: the message and
  field messages clients see (A1).

### Changed
- `s.Rename` refuses a table with a search index, `Alter` refuses to drop
  or rename an indexed column unless it drops the index too, and
  `Update`, `Delete` and `CursorPaginate` refuse a query with `Search`
  (S1).
- `Count`, `Exists` and the count of `Paginate` no longer order the rows
  they count (S1).

## [0.2.0] - 2026-10-02

Batteries: the cache, server-side sessions and rate limiting,
authentication with API tokens and policies, social login (Google,
GitHub, OpenID Connect), queues, events, pub/sub listeners, the
scheduler, mail, file storage, plugins, test fakes and the app clock,
N+1 detection, and `anetos make:auth`. `examples/saas` puts them in one
app that runs as one binary or split by role. Like v0.1, this is a
private pre-release: the modules still use `replace` directives (roadmap
M1b). New modules: `drivers/redis`, `drivers/s3`, `drivers/gcppubsub`
and `plugins/postmark`, tagged `<path>/v0.2.0` with the others.

### Added
- Cache (`cache` package): `cache.ForApp` picks a store with
  `CACHE_STORE` (`memory`, `database`, or a driver's such as `redis`) and
  adds the cache to the app's contexts; `Get[T]`, `Has`, `Set`, `Add`,
  `Forget`, `Increment` (fixed-window counters), `Remember[T]` (one
  computation per key in a process; falls back to computing when the
  store fails) and `Flush`, with values encoded as JSON and keys prefixed
  by `CACHE_PREFIX` (default `APP_NAME` + `:cache:`); the `cache:clear`
  command (B1, design D88–D90).
- Locks across instances: `cache.NewLock` (`TryAcquire`, `Acquire`,
  `Release`, `Extend`), `cache.WithLock` and `cache.TryWithLock`
  (`cache.ErrLockHeld`), owned by a random token and expiring after a ttl
  (B1, design D91).
- The memory store and the database store (`cache.Migrations` creates its
  table, `CACHE_TABLE`); the `cache.Store` interface and the
  `cache/cachetest` conformance suite for other stores, run by `db/dbtest`
  on every database (B1, design D90).
- `db.WithoutTx`: queries that leave the context's transaction, so their
  writes stay after a rollback (B1, design D90).
- `anetostest.New` gives each test app its own `CACHE_PREFIX` and clears
  its cache items when the test ends (B1, design D92).
- `examples/database` caches `GET /stats` and forgets it when posts
  change; guide "Cache values", configuration reference sections for the
  cache and Redis (B1).
- Server-side sessions: `SESSION_DRIVER` (`cookie`, the default, or
  `database`; `redis` from `drivers/redis`) with `session.Driver`,
  `session.DatabaseDriver`, `session.Migrations` (`SESSION_TABLE`) and
  `session.WithStore` for any `cache.Store`, `SESSION_PREFIX` and
  `Manager.Store`. The cookie then holds only the encrypted session ID;
  the store holds the session encrypted, under a hash of the ID;
  `Regenerate` and `Invalidate` remove the old session, revoking every
  copy of its cookie, and a request still running with it can't bring it
  back; up to 1 MB per session (B2, design D93).
- `cache.Store` has `Replace` (set only if present), implemented by every
  store and checked by `cachetest` (B2, design D88).
- Rate limiting (`web/ratelimit`): `ratelimit.Middleware(name, limits...)`
  with `PerSecond`, `PerMinute`, `PerHour`, `PerDay`, `Per` and `By`
  (default key: client IP, IPv6 per /64), counted shortest window first
  without counting blocked requests against longer windows, 429 with
  `Retry-After` and `X-RateLimit-*` headers; `ratelimit.Allow` and
  `ratelimit.Clear` for actions such as logins; keys stored as hashes;
  counted with the app's cache (B2, design D94).
- `cache.CreateTable`, the table of a `cache.DatabaseStore`, for other
  packages' migrations (B2).
- `anetos new` projects set up the cache and include the cache and
  sessions tables (B2, design D95).
- `anetostest.New` also gives each test app its own `SESSION_PREFIX`,
  removes its server-side sessions when the test ends, and runs session
  helpers (`WithSession`, `AssertSessionHas`) in the test's context (B2).
- Authentication (`auth`): `auth.Authenticatable` and `auth.Users[U]`
  describe the app's users; `auth.ForApp`, `a.Middleware`, `a.Require`,
  `a.Guest`, `a.Attempt` (argon2id check, login throttling per login and
  IP, hash upgrades), `a.Login` (new session ID, remember-me cookie),
  `a.Logout` (signs out remembered browsers), `auth.User`,
  `auth.Current`, `auth.Check`, `auth.Intended`; a password change signs
  out other sessions; throttling per login, per account and per IP;
  `AUTH_*` settings (B3, design D96–D98).
- Password-reset and email-verification tokens, encrypted rather than
  stored; a reset token stops working once the password changes (B3,
  design D97).
- API tokens: `auth.Migrations` (`api_tokens`), `a.CreateToken`,
  `a.Tokens`, `a.RevokeToken`, `a.RevokeAllTokens`, `a.TokenMiddleware` (Bearer),
  `auth.CurrentToken`, `auth.TokenCan`; secrets stored as SHA-256 hashes
  (B3, design D99).
- Typed policies: `auth.Authorize`, `auth.AuthorizeUser`, `auth.Allows`,
  `auth.AllowsUser` (401/403 errors) (B3, design D100).
- `auth/password`: argon2id `Hash`, `Verify` (argon2id and bcrypt; a
  bounded number at once), `NeedsRehash` (weaker hashes), `Defaults`,
  `IsBcrypt`, `Dummy`; the core module now requires
  `golang.org/x/crypto` (B3, design D96).
- `web.WantsJSON(r)` for middleware; `ratelimit.Check` (without
  counting) and `ratelimit.Hit` (B3).
- Social login (`auth/social`): Google, GitHub (`GitHubAt` for
  Enterprise) and any OpenID Connect provider (`social.OIDC`, with
  discovery); `social.ForApp` and `social.Configured`
  (`SOCIAL_<NAME>_CLIENT_ID`/`_CLIENT_SECRET`), `s.Redirect` and
  `s.Callback` (state, PKCE and nonce; ID token issuer, audience, expiry
  and nonce checked); an app `Resolver` finds or creates the user;
  `social_accounts` links with `FindLink`, `Link`, `Links`, `Unlink`;
  the core module now requires `golang.org/x/oauth2` (B4, design
  D101–D103).
- `APP_URL`, the app's public URL; `a.CanRemember()` (B4).
- `examples/auth` signs in with configured providers (a fake OpenID
  Connect provider in its tests); guide "Social login" (B4).
- `examples/auth`: registration, login, remember me, logout, email
  verification, password reset, API tokens and policies; guides
  "Authentication" and "Authorization", concept page and reference (B3).
- `examples/database` rate-limits its API; `examples/forms` has the
  sessions table and a test with `SESSION_DRIVER=database`; guide "Rate
  limiting", sessions guide step "Keep sessions on the server" (B2).
- Queues (`queue` package): typed jobs (`Handle(ctx) error`) registered
  with `queue.Register[J]` (`queue.Tries`, `queue.Timeout`,
  `queue.Backoff`, `queue.Name`) and dispatched with `queue.Dispatch`
  (`queue.OnQueue`, `queue.Delay`, `queue.AfterCommit`); `queue.ForApp`
  picks a driver with `QUEUE_DRIVER` (`sync`, the default, `memory`,
  `database`, or a driver's such as `redis`); workers with `q.Work`
  (`queue.Queues` in priority order, `queue.Concurrency`,
  `queue.ShutdownGrace`), role `workers`, or `q.Run`; retries with
  exponential backoff and jitter, `queue.Permanent`, a `Failed` method,
  `queue.Current` (the job's ID, attempt and tries); at-least-once
  delivery with leased reservations (B5, design D104–D107).
- Failed jobs are kept by the store; the `queue:failed`, `queue:retry`,
  `queue:forget`, `queue:flush` and `queue:clear` commands (B5, design
  D107).
- Queue stores: memory, database (`queue.Migrations(table, failedTable)`,
  `queue.CreateTables`, `QUEUE_TABLE`, `QUEUE_FAILED_TABLE`; dispatches
  join the context's transaction) and Redis (`redis.QueueDriver`, `redis.NewQueueStore`,
  `QUEUE_PREFIX`); the `queue.Store` interface and the `queue/queuetest`
  conformance suite, run by `db/dbtest` on every database and by
  `drivers/redis` (B5, design D105, D106).
- `Supervisor.ShutdownDeadline`: when the components' shutdown budget
  runs out, for components that plan their stop, such as queue workers
  (B5, design D107).
- `db.WithTestTx`, for test helpers' transactions: `AfterCommit`
  callbacks run at once in it, or when a `db.Tx` directly inside it
  commits (B5, design D108).
- `anetostest.New` gives each test app its own `QUEUE_PREFIX` and removes
  its Redis jobs when the test ends (B5, design D108).
- `anetos new` projects set up the queue with workers, and include the
  jobs tables; `.env` sets `QUEUE_DRIVER=database` (B5).
- `examples/queue`: orders charged by a job, with retries, a declined
  card failing for good, and tests with the sync driver and with workers;
  guide "Queues" (B5).
- Events (`events` package): `events.ForApp` (or `events.New`),
  listeners of any event type added with `events.On` (in `Emit`, in its
  transaction; the first error stops `Emit`), `events.OnAsync` (a bounded
  goroutine pool per listener, after the commit; `Concurrency`, `Buffer`,
  `Timeout`; drained at shutdown, lost if the process stops) and
  `events.OnQueued` (a queue job per listener, `event:<name>`; `Job`,
  `Dispatch`), `events.Name`; `events.Emit`; `bus.Wait` and `bus.Close`
  (B6, design D109–D111).
- Function jobs: `queue.RegisterFunc` registers a function of a typed
  payload under a name, dispatched with `queue.DispatchFunc` (B6, design
  D111).
- `examples/queue` emits `OrderPlaced`, with an audit log (`On`), sales
  counts (`OnAsync`) and receipts (`OnQueued`); guide "Events" (B6).
- Pub/sub (`pubsub` package): `pubsub.ForApp` picks a broker with
  `PUBSUB_DRIVER` (`memory`, the default, or a driver's: `redis`, `gcp`);
  `pubsub.Publish` (JSON, or raw bytes; `Attributes`, `AfterCommit`);
  typed listeners with `pubsub.Listen[T]` (`Subscription`, default
  `<topic>.<APP_NAME>`; `Concurrency`, `Timeout`, `MaxAttempts`,
  `Backoff`, `DeadLetter`, `ShutdownGrace`), run as components with the
  role `listeners`, or with `ps.Run`; `pubsub.Permanent`,
  `pubsub.Current`; subscriptions prepared when the app boots; the
  `pubsub:publish` command (B7, design D112–D114).
- The `pubsub.Broker` contract, the memory broker and the
  `pubsub/pubsubtest` conformance suite (B7, design D112).
- New module `drivers/gcppubsub`: the Google Cloud Pub/Sub broker (B7).
- `anetostest.New` gives each test app its own `PUBSUB_PREFIX` and
  removes its Redis streams (B7).
- `examples/pubsub`: a billing service listening to `orders.created`,
  with a dead-letter topic; guide "Pub/sub listeners" (B7).
- Scheduler (`schedule` package): `schedule.ForApp` (or `schedule.New`
  and `Run`) runs named tasks, `func(ctx) error`, on schedules: `Cron`
  expressions (five fields, names, macros) and `EveryMinute`, `Every`,
  `Hourly`, `HourlyAt`, `Daily`, `DailyAt`, `WeeklyOn`, `MonthlyOn`, in
  `SCHEDULE_TIMEZONE` (default UTC) or `.In(tz)`; task options
  `WithoutOverlapping` and `OnOneServer` (cache locks) and `Timeout`;
  `schedule.Dispatch` for queue jobs; a component with the role
  `scheduler`; the `schedule:list` and `schedule:run` commands (B8,
  design D116–D119).
- `anetos new` projects set up the scheduler, with a `schedules` function
  for the tasks (B8).
- `examples/queue` prunes its audit log every night and dispatches an
  hourly sales report job; guide "Scheduling" (B8).
- Mail (`mailer` package): mailables (`Build(ctx) (*mailer.Message,
  error)`) with HTML bodies from templ components and a text body (a
  string, or made from the HTML), attachments (inline with a content
  ID), headers, tags and metadata; `mailer.Send`, `mailer.Queue` (rendered
  now, sent by the `mail:send` queue job), `mailer.URL` (links on
  `APP_URL`) and `mailer.Preview`; `mailer.ForApp` picks a transport with
  `MAIL_DRIVER`: `log` (the default), `smtp` (`MAIL_SMTP_URL`, STARTTLS or
  TLS, AUTH PLAIN or LOGIN, SMTPUTF8) or `memory`, with
  `MAIL_FROM_ADDRESS` and `MAIL_FROM_NAME` (B9, design D120–D123).
- New module `plugins/postmark`: the Postmark transport (B9), and the
  plugin `postmark.Plugin()`: a webhook (`POST /postmark/webhook`, basic
  auth from `POSTMARK_WEBHOOK_USER`/`PASSWORD`) that queues bounces,
  spam complaints and subscription changes for the job
  `postmark:webhook`, which keeps the `postmark_suppressions` list;
  `postmark.Suppressed`; the commands `postmark:suppressions` and
  `postmark:unsuppress` (B11, design D135).
- `anetostest` sets `MAIL_DRIVER=memory`, and defaults `APP_URL` to
  `http://localhost` and `MAIL_FROM_ADDRESS` to `test@example.com` (B9,
  design D123).
- `anetos new` projects set up the mailer (`MAIL_DRIVER=log`) (B9).
- Storage (`storage` package): disks on a `storage.Backend` (local
  directory through an `os.Root`, memory) with `Put`, `PutBytes`,
  `PutUpload`, `Get`, `Open`, `Stat`, `Exists`, `List` (by prefix, in
  path order), `Delete`, `DeleteAll`, `Copy`, `Move`, `URL` (public
  disks) and `TemporaryURL` (signed with `APP_KEY` for local disks,
  presigned on S3), `Serve` and `Handler` (a range, conditional
  requests; active content, `storage.IsActive`, sent as sandboxed
  downloads); `storage.ForApp` with `STORAGE_DRIVER`,
  `STORAGE_ROOT`, `STORAGE_URL`, `STORAGE_PUBLIC`, and named disks
  (`STORAGE_DISKS`, `STORAGE_<NAME>_*`); `storage.From(ctx, name...)`;
  path checking (`storage.CheckPath`, `ErrInvalidPath`); the
  `storage/storagetest` conformance suite (B10, design D124–D127).
- New module `drivers/s3`: S3 and S3-compatible stores (R2, MinIO, …)
  on minio-go (B10, design D128).
- `anetostest` sets `STORAGE_DRIVER=memory`; `app.PostMultipart` sends
  multipart forms with files (`anetostest.Upload`) (B10).
- `anetos new` projects set up storage (`STORAGE_DRIVER=local`) and
  ignore `storage/` (B10).
- `examples/files`: documents behind temporary URLs and public avatars;
  guide "Store files" (B10).
- Plugins (`ext` package): `ext.Plugin` (`Name`) and the optional
  `Compat`, `HasConfig`, `HasMigrations`, `HasRoutes`, `HasCommands`,
  `HasJobs`, `HasSchedule`, `HasListeners` and `HasBoot`; `ext.Load`
  wires them into an app in their namespaces (routes under `/<name>`
  named `<name>.…`, `ext.Mount`; commands `<name>:…`; a migration set
  per plugin; settings `<NAME>_…`, outside other plugins' prefixes;
  the framework's names reserved) after checking `Requires()` with
  `ext.Satisfies`, and adds the `plugins:list` and `plugins:env`
  commands, which don't boot the app (B11, design D129–D133).
- `anetos.Version()`: the core module's version in the app's build info
  (B11, design D132).
- `config.Keys`: the settings a struct reads, with their defaults (B11).
- `migrate.Runner.Add`: adds migration sets after `migrate.ForApp`
  (B11).
- `web.NewServer` provides the server as a service
  (`anetos.Resolve[*web.Server]`) (B11).
- `anetos add <module>[@version]` and `anetos remove <module>`: install
  and uninstall plugins (`go get`, the generated `plugins.go`, a build
  check and a load check that restore the project when the plugin is
  refused, settings appended to `.env.example`) (B11, design D134).
- `anetos new` projects have `plugins.go` and load it with `ext.Load`
  at the end of `setup` (B11).
- `examples/queue` uses the Postmark plugin and skips receipts to
  suppressed addresses; guides "Use plugins" and "Write a plugin" (B11).
- `anetos make:auth`: writes accounts into a `anetos new` project
  (registration, login with "remember me" and throttling, logout, email
  verification, password reset, API tokens): the `User` model, handlers,
  templ pages, verification and reset emails, routes, the users
  migration, `setupAuth` and tests, and wires `setupAuth` into `setup`;
  guide "Add accounts with make:auth" (B14, design D145–D147).
- Units of work: `anetos.Unit`, `App.AroundUnits`, `App.HasAroundUnits` and `App.StartUnit`,
  called for each request, queue job, async listener, pub/sub message
  and scheduled task (B13, design D141).
- Repeated-query (N+1) detection: `DB_REPEATED_QUERIES` (default 5 in
  development and testing, off elsewhere) logs a warning when a unit of
  work runs the same query that many times, with the app's line that ran
  it; `db.RepeatedQuery`, `DB.Track`, `DB.OnRepeatedQuery`,
  `db.WithRepeatedQueries`, `db.Untracked`; `anetostest`'s
  `app.RepeatedQueries()` and `app.AssertNoRepeatedQueries()`; guide
  "Find N+1 queries" (B13, design D142–D144).
- The app's clock: `anetos.Now(ctx)`, `App.Now`, `App.SetClock` and
  `anetos.WithClock`. Model timestamps, session lifetimes, auth tokens,
  `APP_KEY`-signed temporary URLs, memory cache expiry, rate-limit
  windows, `after:now`-style rules and emails' Date read it (B12, design
  D136).
- `Observe` hooks on the queue (`queue.Dispatched`), the event bus, the
  mailer (`mailer.Record`) and pub/sub (`pubsub.Published`), and
  `Fake()` on the queue, the bus (by event type) and pub/sub, which make
  them record only; `queue.Queue.NameOf`; `queue.OnDispatched` (B12,
  design D137).
- `anetostest` records the jobs, events, email and pub/sub messages of
  each test app; options `FakeQueue()`, `FakeEvents(…)`, `FakePubSub()`;
  typed assertions `AssertDispatched[J]`, `AssertNotDispatched[J]`,
  `AssertEmitted[E]`, `AssertNotEmitted[E]`, `AssertMailSent[M]`,
  `AssertMailQueued[M]`, `AssertMailNotSent[M]`, `AssertPublished[T]`,
  `AssertNotPublished[T]`, values `Jobs[J]`, `Events[E]`,
  `Mailables[M]`, `Messages[T]`, `app.Dispatched()`, `app.Emitted()`,
  `app.Mail()`, `app.Published()` and `app.AssertNothing…`; disk
  assertions (`app.Disk(name).AssertExists`, `AssertMissing`,
  `AssertContent`, `Files`); `app.Freeze`, `app.Travel`, `app.Unfreeze`
  (B12, design D138–D140).
- Examples: `examples/queue` tests with a faked queue and events and
  mail assertions, `examples/files` disk assertions and an expiring
  link, `examples/database` frozen timestamps, `examples/auth` an
  expired reset link; the examples read the time with `anetos.Now`
  (B12).
- `examples/queue` emails a receipt for each order from its queued event
  listener, with a templ template, queues one again with `POST
  /orders/{id}/receipt`, and previews it in development; guide "Send
  email" (B9).
- `anetos.Logger(ctx)`: the app's logger, from its contexts, for jobs,
  listeners and tasks (`slog.Default()` without an app) (v0.2 checks,
  design D148).
- `anetostest.FakeSocial()` signs social login in through a stand-in
  OpenID Connect provider, for every provider (through an internal hook,
  honored only with `APP_ENV=testing`); `app.SocialSignIn(redirect,
  anetostest.SocialAccount{…})`. `anetostest` now imports `auth/social`,
  so modules that use it list `golang.org/x/oauth2` and
  `golang.org/x/crypto` as indirect requirements (already the core's)
  (v0.2 checks, design D149).
- `social.Provider.Title` ("Google", "GitHub"; `OIDC`'s defaults to its
  name) and `Social.Title(name)`, for sign-in buttons;
  `social.WithHomeURL(path)`; `social.ForApp` checks its options also
  when no provider is configured (v0.2 checks).
- `anetos make:auth` adds sign-in with Google and GitHub: `handlers.SocialUser`,
  buttons on the login and registration pages, the `social.redirect` and
  `social.callback` routes, the `social_accounts` migration, the empty
  `SOCIAL_GOOGLE_*` and `SOCIAL_GITHUB_*` settings in `.env` and
  `.env.example`, and tests with `FakeSocial` (v0.2 checks, design D150).
- `examples/saas`: a `anetos new` + `make:auth` app with a welcome email
  from a queue job, a listener on a billing topic and a scheduled task
  that ends trials, and a test that runs the binary as `http`,
  `workers`, `listeners` and `scheduler` processes, then as one (v0.2
  checks, design D152).
- `plugins/postmark` has a README (v0.2 checks).

### Changed
- `anetos new --replace` also replaces the checkout's driver and plugin
  modules, so `go get` and `anetos add` take them from it; `anetos add`
  runs `go mod tidy` after writing `plugins.go`, so the plugin is a direct
  requirement (v0.2 checks, design D151).
- `auth` refuses `AUTH_LOGIN_URL` and `AUTH_HOME_URL` values, and
  intended pages, with control characters, which browsers drop
  (`/\t/host`) (v0.2 checks).
- `examples/auth` doesn't link a second account of the same provider to
  a user by email (a reused address) (v0.2 checks).
- The mailer's unknown-`MAIL_DRIVER` error suggests `postmark.Driver()`
  only for `MAIL_DRIVER=postmark` (v0.2 checks).
- `examples/auth` tests social login with `anetostest.FakeSocial` (its
  package variables for a fake provider are gone) and labels its buttons
  with the providers' titles; `examples/queue` and the queue and
  scheduling guides log with `anetos.Logger(ctx)` (v0.2 checks).
- `examples/auth` emails its verification and reset links with the
  mailer instead of logging them (B14).
- `pubsub.Publish` copies a byte-slice message, so the caller may reuse
  its buffer (B12).
- `ratelimit.Result.RetryAfter` counts from the time of the hit, on the
  app's clock (B12).
- The test client's cookies expire on the app's clock (B12).
- The Postmark transport moved from `drivers/postmark` to
  `plugins/postmark` (unreleased) (B11).
- `make docs-check` also checks regions claimed from first-party plugins
  (`plugins/…`) (B11).
- `anetostest`'s default `APP_URL` is `http://example.test`, the test
  client's own site, so absolute URLs a test gets can be fetched (B10).
- The cache conformance suite's lease test releases leases with
  `DeleteIf` and races Adds on an expired key separately, so it no
  longer depends on timing (B9).
- `anetostest` runs `db.AfterCommit` callbacks registered in a test's
  transaction (with a SQLite file, PostgreSQL or MySQL): when a `db.Tx`
  inside it commits, as in a request, or at once outside one. They never
  ran before, unlike in production (B5, design D108).
- `queue.IsPermanent` (and `pubsub.IsPermanent`) recognize any error with
  a `Permanent() bool` method, so each package's `Permanent` works in the
  other's handlers (B7).
- `anetostest` removes its test app's cache items, sessions and Redis jobs
  in shutdown hooks rather than test cleanups, so it also works when a
  test runs the app (whose shutdown closes the connections) (B7).
- The SQL clock and deadlock retries of the database cache store moved
  to an internal package shared with the queue; no change in behavior
  (B5).

## [0.1.1] - 2026-09-30

Relations and eager loading (the v0.1.x patch, work package F13).

### Added
- Relations (`db`): fields with a `rel` tag, `belongs_to` and `has_one`
  (`*R`), `has_many` and `many_to_many` (`[]R`), with conventional keys and
  pivot names and `fk`, `references`, `local`, `pivot` and `related_fk`
  options; typed handles `db.Rel[T, R]` (`db.RelOf`, checked at first
  use) with `With` (nested), `Where`, `OrderBy`, `WithTrashed`, `Name` and
  `Err` (F13, design D83–D85).
- Eager loading: `q.With(rels...)` for `Get`, `First`, `Find`,
  `Paginate` and `CursorPaginate`, and `db.Load`/`db.LoadMany` for rows you
  have: one query per relation and 1,000 parents, keys checked before the
  main query runs (F13, design D84).
- `q.WhereHas` and `q.WhereDoesntHave` (`EXISTS` subqueries), and
  `db.Attach`, `db.Detach`, `db.DetachAll` and `db.Sync` for many-to-many
  pivots; `Attach` is safe to repeat and to run concurrently (F13, design
  D86, D87).
- `anetos gen` writes `<Model>Rels`, one handle per relation field, and
  reports bad `rel` tags (F13).
- `examples/database` loads each post's author and lists authors with
  published posts; guide "Relations and eager loading", models reference
  section (F13).

### Changed
- A field with a `rel` tag and a `db` tag is an error; a `rel` tag with an
  unknown kind, an unknown option or the wrong field type is an error when
  the model is first used (F13).

## [0.1.0] - 2026-09-30

The foundation: the app kernel, configuration, runtime supervisor, HTTP
layer, validation, data layer with PostgreSQL, MySQL and SQLite drivers,
migrations and seeders, model code generation, server-rendered views with
sessions and forms, the `anetos` developer tool and app-binary commands,
and testing helpers. This is a private pre-release: the modules still
point at each other with `replace` directives, so `go install` from the
module proxy arrives with the public release (roadmap M1b). The driver
modules and the `cli` module are tagged `drivers/<name>/v0.1.0` and
`cli/v0.1.0`.

### Added
- Planning & roadmap, design document, documentation guide, ADR template.
- Apache-2.0 `LICENSE` and `NOTICE`.
- Repository tooling: Go module, Makefile (`make check`), golangci-lint config,
  SPDX header check, CI workflow, docs site skeleton, `examples/` (F1).
- Configuration (`config` package): `.env` parser with quoting, escapes and
  `${VAR}` expansion; layered loading (environment > `.env.<APP_ENV>` >
  `.env`); typed binding with `env`, `default` and `prefix` tags; every
  missing or invalid key reported at once; `Validate()` hook (F3).
- Runtime supervisor (`supervisor` package): components, roles, restart
  policies with exponential backoff and jitter, panic recovery, staged
  graceful shutdown with a deadline, readiness and status (F4).
- App kernel (`anetos` package): `anetos.New`, `AppConfig` (`APP_*`,
  `LOG_*`), structured logging with `log/slog`, typed service container
  (`Provide`, `Resolve`), providers with Register/Boot phases, `app.Go` and
  `app.Component`, shutdown hooks within one total shutdown budget,
  `app.Run` with roles, `app.Close` for boot-only programs (F2).
- Docs: configuration guide and reference, background tasks guide,
  application lifecycle and runtime supervisor concepts, `examples/lifecycle`.

- HTTP layer (`web` package, F5): router on `net/http.ServeMux` with groups,
  `With`, named routes and URL generation, exact trailing-slash patterns,
  404/405/`OPTIONS` handling; `web.Ctx` (a `context.Context`) with response
  helpers; typed handlers via `web.H` with body/query/header/path/file
  binding (plan built at registration) and a `Validate` hook; responders
  (`Created`, `NoContent`, `Redirect`, `RedirectRoute`, …); `HTTPError` and
  RFC 9457 problem JSON or HTML error pages, with a debug page in
  `APP_DEBUG`; middleware `Recover`, `RequestIDs`, `RealIP`, `AccessLog`,
  `SecureHeaders`, `CORS`, `BodyLimit`, `Timeout`; `web.NewServer` as a
  supervised component with `/health/live`, `/health/ready`, bounded
  graceful shutdown and `Stopping()`; `HTTP_*` configuration.
- `config.ByteSize` for sizes like `10MB` (F5).
- `make docs-check`: verifies that doc code blocks match their example
  regions (F1 follow-up).
- Docs: routing and handlers guides, HTTP request lifecycle concept, binding
  reference, HTTP configuration reference, `examples/notes`.
- Validation (`validate` package, F6): Laravel-style `validate` tags
  (`required|email|max:200`) compiled once per type; presence, size, string
  format, choice, comparison, date and file rules (content-sniffed
  `mimetypes` and `image`); nested structs, slices and maps with dotted
  keys; labels and per-struct message overrides; custom rules with
  `validate.Register`; `validate.Struct` for use outside HTTP;
  `*validate.Errors` (422, one message per field) and `validate.Fail`. No
  allocations for valid input with built-in rules.
- `web.H` runs validation rules after binding and before the `Validate`
  method; tag mistakes panic at registration. `web.FieldErrorer` lets any
  error fill the `errors` member of problem responses (F6).
- Docs: validation guide, validation rules reference, `examples/validation`;
  `make docs-check` ignores indentation shared by a whole snippet (F6).

- Data layer (`db` package, F7): `db.Connect` (DB_* config, driver chosen
  by `DB_CONNECTION`, ping at startup, DB in every app context), models as
  structs with `db.Model`, `db.Timestamps`, `db.SoftDeletes`, JSON and
  read-only columns and hooks; `Create`, `CreateMany`, `Update`, `Save`,
  `Delete`, `ForceDelete`, `Restore`, `Upsert`, `Find`; immutable typed
  query builder (`db.Query[T]`, `db.Col[T]`, `And`/`Or`/`Not`, `db.SQL`,
  joins, grouping, scopes, row locks) with `Get`, `First`, `All`
  iterator, `Count`, `Exists`, aggregates, `Pluck`, `Select`, offset and
  cursor pagination, mass updates and deletes; transactions carried in the
  context with savepoints and `AfterCommit`; `db.Raw`/`RawFirst`/`Exec`
  with `?` rebinding and `db.Named`; query and slow-query logging;
  `unique` and `exists` validation rules; `db/dbtest` conformance suite.
- Driver modules `drivers/sqlite` (pure Go, modernc.org/sqlite),
  `drivers/postgres` (pgx) and `drivers/mysql` (MySQL and MariaDB) (F7).
- `App.AddContextValue` and `App.Context`: values in every context the app
  creates (F7).
- Docs: database, models, queries, transactions and raw SQL guides; data
  layer concept; models and query builder references; DB_* configuration;
  `examples/database` (F7).

- Migrations (`db/migrate` package, F8): schema builder (portable column
  types, modifiers, indexes, foreign keys, `Alter` with rename, drop and
  `Change`, raw SQL), migration sets with Go and embedded SQL migrations,
  a runner with batches, rollback, reset, fresh (development only),
  status, per-migration transactions and cross-process locking, seeders,
  and the `migrate`, `migrate:rollback`, `migrate:reset`, `migrate:fresh`,
  `migrate:status` and `db:seed` commands via `Runner.Command`.
- `db.Plural`, the table-name pluralizer, and `db.WithTx` to share a
  `*sql.Tx` you manage (F8).
- Docs: migrations and seeders guides, migrations reference;
  `examples/database` uses migrations and seeders; `make docs-check`
  accepts links to example files other than `main.go` (F8).

- `anetos gen` (new `cli` module, `go tool anetos gen`, F9): writes
  `models_gen.go` with a typed column per field of every model
  (`PostCols.Title`), using the runtime's column rules; `-check` for CI;
  `//anetos:model` and `//anetos:skip` directives.
- `db.JSONCol` (JSON-encoded arguments, decoded by `db.Pluck`),
  `Column.Of` to qualify a column with a table, and `db.Columns[T]` (F9).
- Docs: typed columns guide and `anetos gen` reference; the guides and
  `examples/database` use generated columns (F9).

- Views (`view` package, F10): `view.Component` (templ components work as
  they are), `c.Render` and `web.View` with buffered rendering, helpers
  `CSRFField`, `CSRFToken`, `MethodField`, `Errors`, `Old`, `Flash`,
  `String`, `Template` (html/template); `view.Assets` for static files with
  content-hash URLs; bundled htmx 2.0.11 (`view/htmx`); `web.URL(ctx, …)`,
  `c.IsHTMX()`, `c.HTMX()`.
- Sessions (`session` package, F10): encrypted cookie sessions (`__Host-`
  names when Secure) with idle and absolute expiry,
  `Put`/`Get`/`Value`/`Flash`/`Keep`/`Reflash`, `Regenerate`, `Invalidate`,
  masked CSRF tokens, flashed form errors and input, `Cache-Control:
  private` for responses with a session; `SESSION_*` settings.
- Forms (F10): `web.CSRF` (cross-origin checks plus session token),
  `web.MethodOverride`, `c.Back()`/`web.Back()`, `c.Session()`,
  `web.WriteError` for middleware; browser form posts that fail validation
  redirect back with errors and old input; form posts key errors by `form`
  name (design D68).
- Encryption (`encryption` package, F10): AES-256-GCM with per-message keys,
  `APP_KEY` and `APP_PREVIOUS_KEYS` rotation, `GenerateKey`; `anetos
  key:generate`; `anetos.Secret` for values that must not be printed or
  logged.
- Docs: views, sessions and forms guides and reference; session and key
  settings; `examples/forms` (templ, htmx); `make docs-check` checks templ
  blocks (F10).

- App binary commands (F11): `app.Command`, `app.AddCommand`,
  `app.Commands`, `app.Execute` and `app.ExecuteArgs` (package `cmd`:
  `Command`, `Args.Parse`, `ErrUsage`, `Usagef`); built-in `run
  [--only=roles]` (the default) and `help`; `web.NewServer` adds `serve` and
  `routes:list`; `migrate.ForApp` adds the migration commands
  (`Runner.AppCommands`).
- `anetos new` (F11): creates a working project (templ layout and home
  page, sessions, CSRF, migrations, assets with htmx, a test, `.env` with
  a key) for SQLite, PostgreSQL or MySQL, and finishes it with the go
  commands.
- `anetos dev` (F11): rebuilds on changes (templ generate, anetos gen, go
  build), restarts the app on a free port behind a proxy on a stable
  address, reloads open pages, and shows build errors in the browser.
- `anetos make:handler`, `make:model [--migration]`, `make:migration`,
  `make:middleware` (F11).
- Docs: getting-started tutorial, commands guide, `anetos` tool and app
  commands reference; the examples use `app.Execute` (F11).
- Testing helpers (`anetostest`, F12): `anetostest.New(t, setup)` boots
  the app with test settings (`.env.testing`, never `.env`), runs its
  migrations and isolates the database (in-memory SQLite per test, or a
  transaction rolled back per test with a savepoint per request); a
  browser-like client (cookies, automatic CSRF token, `Referer`) with form
  and JSON methods and `Do`; chainable assertions for status, redirects,
  headers, text, JSON paths, validation errors and session values;
  `AssertDatabaseHas[T]`, `AssertDatabaseMissing[T]`,
  `AssertDatabaseCount[T]`, `AssertSoftDeleted[T]`, `Create`,
  `CreateMany` (design D74–D79).
- Model factories (`db/factory`, F12): `factory.New(func(n int) T)`,
  `With`, `Make`, `MakeMany`, `Create`, `CreateMany`.
- `session.Manager.Load` and `Edit` read and change the session a request
  carries, for test clients (F12).
- `dbtest.RunApp` tests a driver with a `anetostest` app (F12).
- `anetos new` projects test with `anetostest`; PostgreSQL and MySQL
  projects get a `.env.testing` for a `<name>_test` database (F12).
- Docs: testing guide and reference; the guides' "Testing it" sections
  use `anetostest` (F12).

- Pagination links for HTML lists: `web.PageURL(ctx, n)` (a relative
  link to page n that keeps the other query parameters), a trailing `url.Values` argument to
  `Router.URL`, `web.URL`, `RedirectRoute` and friends for query strings,
  and `db.Page.HasPrev`; `view.OldChecked` for checkboxes after a failed
  post; `db.Config` masks passwords when printed or logged (`String`,
  `GoString`, `LogValue`); runnable godoc examples for `config`, `validate`, `view`,
  `web` and `db/factory` (v0.1 checks, design D80).
- `examples/forms` stores its notes in SQLite and shows a paginated list,
  a seeder and a factory; `anetos new` projects get a `database/factories`
  package (v0.1 checks).
- `anetostest` stops a test whose settings name a database without
  `DB_CONNECTION` while `.env` uses another database (it would otherwise
  open SQLite instead; also for `DB_URL`) (v0.1 checks).
- Baseline benchmarks against plain `net/http` (`bench/`, results in
  `docs/benchmarks/`); `make api-docs` checks that every exported
  identifier, struct field and interface method has a doc comment
  (v0.1 checks, design D82).
- Docs: concept pages for configuration, validation, migrations, code
  generation, commands, server-rendered HTML and testing; switching a
  project to PostgreSQL or MySQL; HTML pagination; factories in seeders
  (v0.1 checks).

### Changed
- `migrate --seed` needs `--force` in production, like `db:seed`;
  `migrate` alone doesn't (v0.1 checks, design D81).
- `db.Find` and other model queries reuse each model's quoted column list
  (101 → 74 allocations for a `Find`) (v0.1 checks).
- The conformance suite's fixture models are unexported (v0.1 checks).
- `session.ForApp` and `migrate.ForApp` also provide the manager and the
  runner to the app's container (`anetos.Resolve`) (F12).
- The examples' `setup` functions take only the app, like `anetos new`
  projects' (F12).
- Migration flag errors wrap `cmd.ErrUsage` (exit status 2 from the
  binary) (F11).
- `db.Connect` pings the database when the app boots (at once if it has
  already booted) instead of immediately, so commands like `help` work
  without a database; `App.Booted` reports whether boot has started (F11,
  design D73).
- `anetos.AppConfig` has `Key` and `PreviousKeys` (`anetos.Secret` values;
  a slice), so it is no longer comparable with `==` (F10).
- Generated Go files (`// Code generated … DO NOT EDIT.`) are exempt from
  the SPDX header check; `make check` also runs `gen-check` (F9).
- Raw SQL without arguments is sent exactly as written (no `?`
  processing) (F8, design D56).
- A plain error returned from an input's `Validate(ctx)` method is now a 500
  (its text is not sent to clients); use `validate.Fail` for messages (F6,
  design D37).
- Booleans in configuration also accept `yes`/`no` and `on`/`off`.
- The repository is now several Go modules (core, `drivers/*`,
  `examples/database`); make targets and CI run in each, with PostgreSQL
  and MySQL services for the driver tests (F7).
- Minimum Go version is now 1.26 (the older of the two supported releases);
  code modernized for it (`errors.AsType`, `slices.Backward`,
  `sync.WaitGroup.Go`, …) and the `modernize` linter enabled (design D18).

### Fixed
- `db.LoadConfig` with a prefix names the prefixed keys in its errors
  (`ANALYTICS_DB_PORT`, not `DB_PORT`) (v0.1 checks).
- The conformance suite used `rank`, a reserved word in MySQL 8, unquoted
  in raw SQL; it fails on MySQL 8.0 but not on MariaDB (v0.1 checks).
- `db.Pluck`, `db.Min` and `db.Max` on `*time.Time` columns return UTC
  times, and read SQLite's text times (F9).

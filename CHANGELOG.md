# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/) (see the roadmap's versioning rules).

## [Unreleased]

### Added
- `anetos new --stack=api` writes an app that serves JSON only (AP1,
  D259–D262): no views, templ, static files, sessions or CSRF; routes
  under `/api/v1` in `routes/api.go`, a `GET /api/v1` welcome answered
  by a struct of its own, every error as JSON problem details,
  `HTTP_CORS_ORIGINS` in the settings files, and a test that calls the
  API. Its handlers are typed (`web.H`), so the OpenAPI spec (AP4) can
  describe them. The database, queue, mail, storage, scheduler, plugins, health
  routes, `doctor`, `anetos build` and deploy files are the web
  stack's. `--stack=web` is the default and writes what it did; `--css`
  is refused with `api`.
- `web.JSONErrors` middleware: the errors of the requests it handles are
  problem details whatever the client accepts (`Accept: */*` or none got
  the HTML page), never a page or a redirect back; under it `WantsJSON`
  is true, so `auth.Require` answers 401 rather than redirecting to the
  login page. Global (`UseGlobal`), it covers unmatched URLs and the
  server's middleware too: `Recover` answers a middleware's panic as
  problem details under it (AP1, D261). `examples/tracker`'s API uses it.
- In an API project (`routes/api.go`, no `routes/web.go`),
  `make:handler` (a typed handler answering a struct) and
  `make:middleware` write for the API, and `make:auth` and `make:crud`
  refuse, saying so, until AP2 and AP3; `make:admin` refuses (AP1,
  D262).

### Changed
- `plugins/postmark` works with Anetos v0.4 too (`Requires`:
  `>= v0.2.0, < v0.5.0`); an Anetos built from this source reports
  `v0.4.0-dev` (AP1).
- `anetos new` pins templ v0.3.1070 (was v0.3.1020), as the examples
  now use (M10).
- Dependencies updated in every module; the Google API client stays
  below v0.299.0, which requires gRPC 1.84 (GO-2026-6443, no fixed
  release yet).

## [0.3.0] - 2026-10-07

Search, AI and the starter experience: full-text search and search by
meaning with hybrid ranking; package `ai` with Anthropic, OpenAI (and
compatible servers) and Gemini, typed tools, agents, stored
conversations, budgets and streaming; roles and permissions;
internationalization; Google Cloud Storage; the audit log and soft
deletes; the admin; account settings; `anetos build`, a Dockerfile and a
systemd unit; the starter theme, `make:crud` and app error pages; the
security review and `doctor`; benchmarks and the performance gate.
Upgrading from v0.2: [the upgrade guide](docs/site/upgrade/v0.3.md).

### Added
- Error pages in the app's layout (M10, D256):
  `Router.ErrorPages` makes the HTML error page an app's component, fed
  a `web.ErrorPage` (status, translated title, the visitor-safe message,
  request ID); JSON clients, form redirects, logging and the debug page
  of a 5xx stay as they were. `anetos new` writes `views/errors.templ`
  and sets it in `routes/web.go`, with a test of the 404 page, and an
  icon (`public/static/favicon.svg`).
- `MIGRATE_ON_RUN=true` runs the pending migrations when the app starts
  with `run` or `serve`, before its components (M5, D255). A SQLite
  project's `Dockerfile` sets it.
- `make:auth` writes `database/factories/users.go`: `factories.Users`,
  verified users whose password is `factories.UserPassword`, for tests
  with `anetostest.ActingAs` (M10, D257).
- `anetos help <command>` shows the command's usage (M10).
- `doctor` warns, in production and staging, about settings still
  holding an example's domain (`example.com`, `.test`): `APP_URL`,
  `MAIL_SMTP_URL`'s server, `MAIL_FROM_ADDRESS` (M7).
- Benchmarks of v0.3 (M6, D251–D253): the page of an app made with
  `anetos new` and `make:auth` and the cost of each of its parts, each
  middleware, a list of 20 rows, and chi, Gin and Echo doing the same
  hello and JSON work; results, method and analysis in
  `docs/benchmarks/v0.3.md`, and the concept page "Performance".
- The performance gate (M6, D251): allocation budgets of requests and
  queries (`bench/budget_test.go`, `make bench-check`, in `make check`
  and every CI run), and on pull requests a comparison with the base
  branch run in turns on one runner (the `Benchmarks` workflow,
  `scripts/bench-compare.sh`, `bench/cmd/benchcmp`, `make
  bench-compare`), failing on more allocations or a median over 20%
  slower in every sample; the label `benchmark-ok` accepts one.
- `doctor`, a command of every app (M7, D245): runs the checks of the
  app's settings and prints problems, warnings and notes; exit 1 on a
  problem (`--strict`: on a warning). Features add their checks as they
  are set up: `APP_*` (key, URL, debug), `db.Connect` (TLS of a remote
  database, the query log), `migrate.ForApp` (pending migrations, after
  booting), `session.ForApp` (`SESSION_SECURE`, `SameSite`, domain),
  `web.NewServer` (trusted proxies, CORS, limits), `mailer.ForApp`,
  `cache.ForApp`, `queue.ForApp`. `app.AddCheck(anetos.Check{…})` adds
  an app's or a plugin's; `anetos.Finding`, `Note`, `Warning`,
  `Problem`; `Environment.Deployed()`; `db.Driver.InspectURL` reads a
  `DB_URL`'s host and TLS mode (PostgreSQL and MySQL drivers).
  **BREAKING:** an app that adds its own `doctor` command now fails at
  start ("registered twice"): rename it.
- `anetos doctor [--strict] [--vuln]` (M7, D245): checks `.env`'s
  permissions and that git tracks no file of secrets, runs
  `govulncheck` with `--vuln`, then builds the app and runs its
  `doctor`.
- `DB_TLS` (`verify`, `skip-verify`, `none`) and `DB_TLS_CA` (which
  means `verify`, also through a tunnel on localhost) for connections
  built from `DB_HOST` (M7, D246).
- `Column.Contains(s)`, `Column.StartsWith(s)` and `db.EscapeLike(s)`:
  `LIKE` conditions with the user's `%` and `_` taken literally (M7).
- `anetos dev --host=<name>`: another host name the dev server answers
  (M7, D247); `anetos add --yes` (M7, D249).
- `SECURITY.md` (how to report a vulnerability, supported versions),
  the framework's security review in `docs/security/checklist.md`, the
  guide "Secure your app", and Dependabot for the CI's actions and the
  modules' dependencies (M7, D249).
- A starter theme (M10, D240): `anetos new` writes
  `public/static/app.css`, plain CSS with no build step, light and dark,
  styling plain HTML and a few classes (layout, cards, fields, buttons,
  tables, badges, flash messages, pagination); the layout has a header
  with the app's nav. `anetos new --css=none` writes an almost empty
  stylesheet instead. Guide "Style your app".
- `anetos make:crud <Model> <field:type>...` (M10, D241): a model, its
  migration, handlers to list (with pages), show, create, edit and delete
  rows, a form with validation, templ views in the theme, routes, the
  pages' English text and a test; the routes join `routes/web.go`'s page
  group and the layout's nav links to the list. Types `string`, `text`,
  `email`, `int`, `float`, `bool`, `date`, with `:optional` and
  `:unique`.
- `session.Manager.Use(mw...)`: middleware that run inside the session
  middleware on every route that has it (M10, D238).
- `web.RouteIs(ctx, names...)`: whether the request's route has one of
  the names (`"issues.*"` for a prefix), for current-page links (M10,
  D239).
- Docs: getting started rewritten in the order of a first project
  (install, editor, database, a project, its structure, `make:crud`,
  `make:auth`, test and build); the docs site groups each section's
  pages in the sidebar, from the pages' `group` and `weight` front
  matter, which `make docs-check` checks (M2, D242).
- `auth.DefaultHomeURL(path)`, an option of `auth.ForApp` (which now
  takes options): `AUTH_HOME_URL`'s default, the page signing in leads
  to when there's no page the user asked for (M10, D237).
- The tutorial, "Build an issue tracker" (M2, D234): seven parts, from
  `anetos new` to deploying, in `docs/site/getting-started/tutorial`;
  its code is `examples/tutorial`.
- `examples/tracker`, the reference app (M3, D234): an issue tracker
  with projects and roles, labels, comments with htmx, files, history,
  search, emails from queue jobs, a weekday digest, an admin and a JSON
  API, tested on every database.
- `anetostest.ActingAs(app, u)` signs a user in for the requests that
  follow (dropping an earlier user's remember-me cookie), and
  `auth.Auth.LoginSession(s, u)` writes a signed-in session without a
  request; `auth.Auth.RememberCookie` names the remember-me cookie (M3,
  D235).
- `anetos build` (M5, D229): the production build, with `templ
  generate` and `anetos gen` first, then a static binary
  (`CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"`) at
  `bin/<module name>`; `--target=os/arch` for another system,
  `--version` for builds without the git repository, `--cgo`, `-o`, and
  `go build` flags after `--`.
- `anetos new` writes a `Dockerfile` (built in the Go image, run on
  distroless as a non-root user, `/data` for files and SQLite, a health
  check), `.dockerignore`, a systemd unit (`deploy/<name>.service`) and
  `deploy/production.env.example` (M5, D230). Its `main.go` prints the
  version before `setup`, so `version` needs no settings.
- The `version` command in every app (`anetos.VersionText`: the app's
  version, commit, Anetos and Go versions) and `health:check` from
  `web.NewServer` (asks the running server for `/health/ready`), for
  container health checks (M5, D231). **BREAKING:** an app that adds
  its own `version` or `health:check` command now fails at start
  ("registered twice"): rename it, or drop it for the built-in one.
- `supervisor.Supervisor.Declare`: roles known before a component has
  them. `schedule.ForApp` declares `scheduler` and `pubsub.ForApp`
  `listeners`, so `run --only=workers,scheduler` works in an app without
  tasks yet (M5, D233).
- Guide "Deploy": systemd, Docker and Compose, Fly.io, Render, HTTPS,
  splitting roles (M5).
- Module `anetos.dev/anetos/admin`: an admin interface. `admin.New(app,
  a)` for the users of an `auth.Auth[U]`, `admin.Add` with an
  `admin.Resource[T, F]` per model (list columns with sorting, search,
  filters, pages and a scope; a record's page; forms from a form struct
  `F`, rendered from its types and `admin` tags and validated by its
  `validate` tags, applied with `Edit` and `Apply`; choices from the
  database; actions and bulk actions; a trash for soft-deleted models),
  `Panel.Mount` under `ADMIN_PATH` or at `ADMIN_HOST`. Only users with
  `admin.access` get in; each resource has `admin.<name>.view`, `.create`,
  `.update` and `.delete` permissions (`admin.PermissionsOf`). Pages are
  embedded `html/template` with htmx and their own stylesheet, under a
  strict CSP. `anetos make:admin` and `anetos make:admin:resource
  <Model>`; guide "Add an admin panel", `examples/admin` (AD1a, D211–D214).
- An account settings page from `anetos make:auth` (AC1, D227):
  `/settings` (`AUTH_SETTINGS_URL`) for the name, the password, the
  language and time zone, the email address (the new one confirmed by a
  link to it; the old one told, with a link that undoes the change and
  secures the account; `Accounts.AllowEmailChange`, on) and
  deleting the account (`Accounts.AllowAccountDeletion`, off); columns
  `pending_email`, `locale` and `time_zone`, and `User.PreferredLocale`
  and `PreferredTimeZone`. The admin links the user's name to it.
- `auth.Auth.ChangePassword`: the current password checked, the user's
  other sessions and remember-me cookies ended; `auth.Auth.SignOutOthers`
  (AC1, D228).
- `web.Ctx.ForgetLocale`: back to the user's or browser's language (AC1).
- `auth.Auth.EmailRevertToken` and `CheckEmailRevertToken`, for the link
  that undoes a change of email address (`AUTH_REVERT_TTL`) (AC1).
- `i18n.TimeZones`: the time zones to choose from, from the IANA
  database's `zone.tab` (AC1, D228).
- Two-factor sign-in in package `auth` (AD2b, D224): TOTP codes from an
  authenticator app and recovery codes, the state stored encrypted with
  `APP_KEY` through `Users.TwoFactor` and `SetTwoFactor`;
  `StartTwoFactor`, `StartedTwoFactor`, `ConfirmTwoFactor`,
  `NewRecoveryCodes`, `DisableTwoFactor`, `TwoFactor`; `Attempt` and the
  new `SignIn` return `ErrTwoFactorRequired` for users who have it on,
  and `AttemptTwoFactor` finishes the sign-in (`AUTH_CHALLENGE_URL`,
  `AUTH_TWO_FACTOR_URL`); `auth.TwoFactorCode` for tests. Social login
  asks for the code too. Guide "Two-factor sign-in and password
  confirmation", `examples/auth`.
- Password confirmation (AD2b, D225): `Auth.ConfirmPassword`,
  `PasswordConfirmed` and the `RequireConfirmed` middleware
  (`AUTH_CONFIRM_URL`, `AUTH_CONFIRM_TTL`).
- Package `qr`: QR codes as SVG (AD2b, D226).
- The admin's protections (AD2b, D225, D226): the password asked again
  before dangerous actions (`ADMIN_CONFIRM`, default on),
  `ADMIN_TWO_FACTOR=required`, `ADMIN_ALLOW_IPS`; a user's page shows
  their two-factor sign-in, which can be turned off for them.
- `anetos make:auth` adds two-factor sign-in (the code after the
  password, a page to turn it on with a QR code and off, recovery
  codes) and the password confirmation page; a `two_factor` column in
  the users table (AD2b). Apps made before add the column and the two
  `Users` functions by hand.
- The admin's dashboard and operations (AD2a, D221–D223): widgets on its
  first page (`Panel.Dashboard`, `admin.Widget` with figures, a bar chart
  as SVG, a table, any component and a link; built in `admin.SignUps`,
  `admin.QueueHealth`, `admin.AIUsage`, `admin.RecentActivity`); the
  activity pages over the audit log, filtered, with an entry's changes
  field by field and a record's history on its page (`admin.Activity`);
  failed jobs to retry or forget (`admin.Jobs`); scheduled tasks with
  their next and last run, run now in the background (`admin.Schedule`).
  `make:admin` adds them as the app has a queue, a scheduler, an audit
  log and AI usage.
- `schedule`: each task's last run is kept in the cache
  (`Scheduler.LastRun`, `schedule.Run`) (AD2a, D223).
- `audit.Tracked(ctx, table)` (AD2a).
- `queue.CountFailed` and `queue.FindFailed`, with the optional store
  interfaces `queue.FailedCounter` and `queue.FailedFinder`, implemented
  by the memory, database and Redis stores; `queuetest` checks them
  (AD2a).
- The admin's users and roles (AD1b, D215–D220): `admin.Users` adds the
  app's users with their accounts managed on their pages (disable and
  enable, verification and reset emails, sign out everywhere, API tokens,
  roles and permissions by scope, acting as the user with a banner,
  `admin.Banner`), only by those with every permission the user has;
  `admin.Roles` lists the roles and manages those stored in the database;
  `admin.UserName`; `admin.AssignRoles`. Every step is recorded in the
  audit log. `make:admin` writes `app/admin/users.go`, adds the roles,
  and puts the banner in `views/layout.templ`.
- `auth`: disabled accounts (`Users.Disabled`, `auth.ErrDisabled`),
  signing out everywhere (`Users.SessionKey`, `SetSessionKey`,
  `Auth.SignOutEverywhere`), acting as another user
  (`Auth.Impersonate`, `StopImpersonating`, `auth.Impersonator`,
  `auth.ErrNotImpersonating`) (AD1b, D215–D217).
- `rbac`: `AuthorizeOver` (may the signed-in user manage this user),
  `GivenTo`, `RoleCounts`, `Holders` (AD1b, D218).
- `audit`: entries name the user an actor was acting as (`ActingAs`, the
  `acting_as` column, added by a new migration); `audit.Enabled`;
  `session_key` columns are redacted by default (AD1b, D217).
- `make:auth`: the users table has `disabled_at` and `session_key`, and
  `models.Users` reads them; signing in to a disabled account says so;
  `handlers.SendVerification` and `handlers.SendPasswordReset` are
  exported for the admin (AD1b, D220).
- `web.Router.Host(host)`: routes for one host, with absolute URLs;
  `RouteInfo.Host` (AD1, D214).
- `rbac.Registry.Declare`: permissions declared by packages at setup
  (AD1, D213).
- `db.SoftDeleting[T]()` reports whether a model embeds `db.SoftDeletes`
  (AD1).
- Package `audit`: an audit log of the models an app tracks
  (`audit.ForApp`, `audit.Track[T]` with `Except`, `Redact`, `Reveal`):
  who created, changed (the columns that changed, from what to what),
  soft-deleted, restored and permanently deleted each row, in which
  request, job, task or command, written in the change's transaction;
  bulk writes as one entry each (`audit_bulk`) with every row's key
  (`audit_bulk_items`); `audit.Record` for the app's own events,
  `audit.History` for a row's events, `audit.WithActor`, `audit.Prune`
  and `audit:prune` (`AUDIT_RETENTION_DAYS`), `audit.Anonymize` and
  `audit:anonymize` for erasure requests; client IPs only if `AUDIT_IP`
  says so. Guide "Keep an audit log", concept "The audit log",
  `examples/audit` (AU1, D202–D206).
- Watched writes in `db`: `DB.Watch(table, watcher, bulkValues)` tells a
  `db.Watcher` about every write to a table, in its transaction, with the
  values before and after (`db.Write`, `db.Bulk`); `db.KeyOf` (AU1,
  D203, D204).
- Soft deletes: `migrate.Table.UniqueLive` and `Column.UniqueLive`, unique
  indexes over the rows that aren't deleted (PostgreSQL and SQLite), and
  the validation rule `unique_live`; `db.PruneTrashed[T]`,
  `db.PruneAllTrashed` and the `db:prune-trashed` command delete rows
  soft-deleted longer ago than a model allows (AU1, D208, D209).
- Carriers: `anetos.Carrier` and `App.AddCarrier` move a value from the
  context of the code that dispatches a queue job or emits an event to an
  async listener into the job's or listener's (`App.Carried`,
  `App.WithCarried`); `web.ClientIPFrom(ctx)` (AU1, D207).
- Module `drivers/gcs`: Google Cloud Storage disks (`gcs.Driver()`,
  `STORAGE_DRIVER=gcs`, `STORAGE_GCS_*`), with Application Default
  Credentials and V4 signed temporary URLs (G1, D199). The storage
  package's error for a driver that wasn't passed to `storage.ForApp`
  names the driver's module; a `File`'s `Info().Size` may be -1 (unknown
  before reading), and `Disk.Serve` then sends no Content-Length.
- Formatting in the user's language: `i18n.Number`, `Fixed`, `Percent`,
  `Currency` (CLDR data from `golang.org/x/text`; the language's digits,
  or the catalog's `format.numbering`), `i18n.Date`, `Time`, `DateTime`
  and `Format` (CLDR patterns and month and day names from the catalogs'
  `format` section, times in the context's zone, `anetos.Date` as it
  is, pointers to either; styles `Short`, `Medium`, `Long`, `Full`),
  `i18n.Ago`, `Duration` and `DurationUp` (`relative.*` keys). Formats
  come from the locale's catalogs or English, never from a fallback in
  another language. Reference "Formats in catalogs", guide "Numbers,
  dates and languages" (I1c, D193, D196).
- `i18n.Dir` and `DirOf` (`rtl` for right-to-left scripts),
  `i18n.LanguageName` (`format.language`), `i18n.LocalNumber`;
  `web.Alternates` for `hreflang` links with `LOCALE_URL=prefix` or
  `subdomain`; error pages and `anetos new`'s layout set `<html dir>`,
  and the layout adds the `hreflang` links (I1c, D198).
- `anetos lang:add <locale>…` (alias `anetos add lang`): copies the
  framework's translations (`framework.yaml`, and `auth.yaml` for
  `make:auth`'s pages) from the module `anetos.dev/locales` (bn, es, fr)
  into `locales/<locale>/`, leaving out keys the app's catalogs for the
  locale define (I1c, D197).
- `lang:check` compares the placeholders of the framework's messages a
  locale translates with the English ones, reports lists of month and
  day names of the wrong length, and notes keys neither the fallback
  locale nor the framework has (a misspelling) and plural forms a
  language never uses (I1c).
- Package `i18n`: translations in YAML catalogs (a file or a folder per
  locale, nested keys, `{name}` placeholders, CLDR plural forms), loaded
  by `i18n.ForApp(app, fsys)` with `APP_LOCALE`, `APP_FALLBACK_LOCALE`,
  `APP_LOCALES` and `LOCALE_URL`; `i18n.T`, `i18n.Plural`, `Lookup`, `Has`;
  lookup through the locale's parents, the fallback locale and the
  framework's English catalog; missing keys logged in development;
  `lang:check`, `Translator.Check`. Guide "Translations", concept
  "Internationalization", `examples/i18n` (I1b, D189–D195).
- The request's locale, resolved on first use: with `LOCALE_URL=prefix`
  or `subdomain`, the URL's (pages without one redirect to the visitor's
  locale); with `none`, the `locale` cookie, the session, the signed-in
  user's preference, `Accept-Language` (`?locale=` on pages switches;
  `Vary: Accept-Language, Cookie`); `c.SetLocale`, `web.LocaleURL`,
  `web.LocalePath`, `web.ErrUnsupportedLocale`; route URLs keep the
  locale. Locales match exactly, by parent, or a close regional variant
  (I1b, D190).
- Users' preferences: `i18n.LocalePreference`, `CommunicationPreference`,
  `TimeZonePreference`; `i18n.ForUser` for mail; `i18n.TimeZone`,
  `WithLocale`, `WithTimeZone`, `WithResolver`, `SetCurrentUser`
  (`auth.ForApp` calls it), `Preferences`. Queue jobs run in the locale
  and time zone of the context that dispatched them (I1b, D191).
- `web.HTTPError.Key` and `Args`: a catalog message shown to clients in
  their language (I1b, D192).
- `anetos.Date`, a calendar date without time or zone, for `DATE` columns,
  forms (`<input type="date">`) and JSON; `NewDate`, `DateOf`, `Today`,
  `ParseDate`; the date rules (`after`, `before`…) compare dates, `now`
  being today in the app's zone. Guide "Times and dates" (I1a, D187).
- `APP_TIMEZONE` (default `UTC`), the app's zone; `App.Location`,
  `anetos.Location`. The time zone database is built in (`time/tzdata`)
  (I1a, D188).
- `DB.CheckTimeZone`, `DB_ALLOW_LOCAL_TIMEZONE` (`db.WithLocalTimeZone`),
  `dbtest.RunLocalTimeZone` (I1a, D186).
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
- AI providers, each a driver module on the provider's official SDK:
  `drivers/anthropic` (`AI_PROVIDER=anthropic`, `ANTHROPIC_API_KEY`),
  `drivers/openai` (`openai`, `OPENAI_API_KEY`; and `openai-compatible`
  for Ollama, vLLM, LM Studio, OpenRouter, Groq…:
  `OPENAI_COMPATIBLE_URL`, `OPENAI_COMPATIBLE_KEY`) and `drivers/gemini`
  (`gemini`, `GEMINI_API_KEY`), with text, streaming, tools, structured
  output and usage; `Options` for thinking budgets and reasoning effort,
  and a hook to the SDK's request parameters; `AI_MODEL` is required
  (A2, design §14.4, D166–D168).
- `ai.Reasoning`, the reasoning a model needs back after tool calls
  (Claude's thinking, Gemini's thought signatures), kept in
  conversations (A2, D166).
- `Schema.Map(ai.SchemaOptions{…})` and `ai.ConstraintKeywords`, to adapt
  a schema to a provider's JSON Schema dialect (A2, D167).
- `ai/aitest`, the providers' conformance suite on recorded HTTP
  exchanges, with record, live and update modes (A2, D168).

- Roles and permissions (`auth/rbac` package): permissions declared in
  code as `rbac.Permission` constants, roles of them (`rbac.Role`, super
  roles) checked by `rbac.ForApp`; users get roles and single permissions
  globally or in a scope such as a team (`rbac.ScopeOf("team", id)`),
  stored in `rbac_grants` (`rbac.Migrations`): `Assign`, `Unassign`,
  `Sync`, `Grant`, `Revoke`, `RemoveUser`, `RemoveScope`, `Assignments`,
  `UsersWith`. A global grant applies in every scope (R1, design §15,
  D169, D170).
- Checks of the signed-in user: `rbac.Authorize`/`AuthorizeIn` (401, 403),
  `Can`/`CanIn`, `HasRole`/`HasRoleIn`, `Require`/`RequireIn` middleware
  with `PathScope`, `rbac.Current` and `rbac.Of` (`*rbac.Grants`: roles,
  permissions and scopes of a user). Grants are read in one query per user
  per unit of work; a token-authenticated request may use only the
  permissions among its abilities; checking an undeclared permission is an
  error (R1, D171, D172, D174).
- `rbac.AuthorizeRole`: a user may give a role only if they have its
  permissions in the scope; `rbac.AuthorizeRolesOf`: and change or take
  away a user's roles only if they could give them (R1, D173).
- Roles of the database, which administrators add from the declared
  permissions: `rbac.CreateRole`, `UpdateRole`, `DeleteRole`, `Roles`,
  `FindRole` (R1, D169).
- `rbac:roles`, `rbac:user`, `rbac:assign` and `rbac:unassign` commands
  (R1).
- `auth.CurrentID(ctx)`: the signed-in user's `AuthID`, for code that
  works with any user type (R1).
- `examples/teams`: a JSON API with team roles, global roles, roles
  administrators add and API tokens; guide "Roles and permissions", a
  concept page, and the reference (R1).

- AI conversations stored in the database (`ai.Migrations`:
  `ai_conversations`, `ai_messages`): `ai.StartConversation`,
  `FindConversation` (the user's only), `Conversations`; `conv.Prompt`,
  `Stream`, `Add`, `Reply`, `StreamReply`, `Messages`, `Delete`. A call
  stores its messages once it succeeds, or refuses with
  `ai.ErrConversationChanged` (409) if another call added to the
  conversation (A3, design §14.4, D175).
- AI usage and budgets: `client.TrackUsage(ai.UsageConfig{Prices,
  Budget})` records every model response in `ai_usage` (`ai.UsageRecord`)
  with its cost, and refuses calls over a user's `ai.Budget` (tokens or
  cost per period) with an `*ai.BudgetError` (429); streams stopped
  partway count with an estimate; `ai.ForUser`,
  `ai.Price`, `ai.TotalUsage` (A3, D176).
- Queued replies: `ai.QueueAgents(app, agents...)` and
  `conv.QueueReply(ctx, agent)`, answered by a queue job as the
  conversation's user, with the conversation's `Status` and `Error` to
  poll (A3, D177).
- `auth.ActAs(ctx, userID, opts...)` and `a.ActAs`: a context signed in
  as a user, for jobs and commands working for them, with
  `auth.WithAbilities` for the limits of an API token (A3, D177).
- `AI_QUEUE_TIMEOUT` (default 15m) bounds a queued reply (A3, D177).
- Server-sent events: `c.Events()` (`*web.EventStream`: `Send`,
  `Comment`), without the request's or the server's write timeout;
  `web.WithoutTimeout(ctx)` for other streams; `ai.SSE(c, events)` sends a
  streamed answer as `text`, `tool`, `error` and `done` events for htmx.
  The htmx SSE extension is bundled in `view/htmx`
  (`htmx-ext-sse.min.js`, `htmx.SSEVersion`) (A3, D178).
- `ratelimit.AllowN`, counting an amount at once (A3, D176).
- `anetos make:agent <Name>` writes an AI agent with a typed tool to
  `app/agents` (A3, D179).
- `examples/assistant`: a help center whose assistant searches and reads
  its articles, with stored conversations, streamed answers, background
  replies and a daily budget; guide "Build an AI assistant" (A3).
- Vector search: `q.Similar(model, v)` orders records by their nearest
  chunk of an embedding model, and `q.Hybrid(text, model, v)` merges that
  with full-text search by reciprocal rank fusion; both combine with
  `Where`, scopes, `Count` and `Paginate`. `db.Vector`,
  `db.CosineDistance`, `db.Chunks`, `db.ReplaceChunks`,
  `db.NearestChunks`, `db.PruneChunks`, `db.RecordID`, `db.TableOf`, `db.EmbeddingsTable`,
  `q.WhereKeys`; the `db.VectorSearch` capability and
  `d.CheckCapabilities` (PostgreSQL with pgvector, MariaDB 11.7+,
  SQLite; refused on MySQL Community) (S2, design §10.5, D159, D182).
- Migrations: `t.Vector(name, dims)` and `s.CreateEmbeddings(table,
  dims)`/`s.DropEmbeddings(table)`, the companion table of a model's
  chunks with a vector index (HNSW on PostgreSQL, MariaDB's vector
  index) (S2, D161, D183).
- Embeddings in package `ai`: `ai.Embed`, `ai.EmbedQuery`, the optional
  `ai.Embedder` contract, `AI_EMBEDDING_PROVIDER` and
  `AI_EMBEDDING_MODEL`, `Client.EmbeddingModel`, `Client.SetEmbedder`;
  usage recorded and budgets enforced; the fake embeds by words, and
  `Fake.Embeddings` lists its requests (S2, D180).
- `ai.EmbeddingsFor` keeps a model's embeddings: `Sync` (queue jobs of
  a hundred records after the commit), `SyncNow`, `SyncAll` and the
  `ai:embed` command re-embed only changed chunks (`FixedSize` for models
  that make one size of vector); `Search` (hybrid with a search index)
  returns passages, and `Tool` gives agents the search, under the
  config's `Scope` (S2, D181, D184). With `db.Connect`, the app refuses
  to boot when its database can't search vectors (MySQL, MariaDB before
  11.7, PostgreSQL without pgvector), saying why (D258).
- `aitest.Config.EmbeddingModel` runs an `Embed` conformance test (S2).
- `examples/assistant` searches its articles by meaning and words; guide
  "Search by meaning" (S2). It runs on SQLite, PostgreSQL with pgvector
  and MariaDB 11.7+, and CI runs it and `examples/tracker` on
  PostgreSQL (with pgvector, which the driver's conformance suite now
  uses too) and the tracker on MySQL (A3).

### Changed
- With `migrate.ForApp`, the server isn't ready (`/health/ready`
  answers 503, `health:check` exits 1) while the database has migrations
  the app hasn't run, and logs which (M5, D255); `MIGRATE_READINESS=false`
  turns it off. See the upgrade guide.
- Boot errors name the database's provider `db.Connect(<driver>)`,
  without a memory address that changed on every run (S1).
- The deploy guide's container migration mounts the data volume (it
  migrated a throwaway SQLite database); the search guide shows a search
  box for `make:crud`'s pages in the starter theme, and the clean-up of a
  MySQL search test; members-only pages explain the test's sign-in (M5,
  M10).
- `make:auth`'s forms mark invalid fields (`aria-invalid`) as
  `make:crud`'s do, and its `SOCIAL_*` settings go to
  `deploy/production.env.example` too (M10).
- The version of a development build is `v0.3.0-dev` (it said
  `v0.2.0-dev`).
- Faster requests (M6, D252): a signed-in page 13–15% faster with about
  a fifth fewer allocations. `RequestIDs`, `RealIP` and the locale
  middleware keep their results in the router's request state instead
  of a context value each (a sub-request served through the router
  again gets a state of its own); request IDs come from `math/rand/v2`
  (the same format; they were never secrets); a session notes its changes
  rather than encoding itself twice per request to compare (storing a
  value it already holds still writes nothing); `encryption` caches the
  keys of the last 1024 to 2048 messages it sealed or opened; the row scanner
  reuses its scanners, so `Query.Get` allocates no more than a
  hand-written scan.
- `make:auth`'s pages use the starter theme (a card for the forms,
  cards on the dashboard and settings), and it adds `AccountMenu` to the
  layout's header (log in and register, or the user's name, settings and
  logout); `setupAuth` calls `sessions.Use(a.Middleware)` so every page
  knows the signed-in user (M10, D238).
- `make:auth`'s handlers send users to `AUTH_HOME_URL` after logging
  in, registering, the two-factor code and confirming the password (they
  went to `/dashboard` whatever the setting); `setupAuth` gives
  `/dashboard` as the default with `auth.DefaultHomeURL`, so a new app
  behaves as before until the setting or the default changes (M10,
  D237).
- `lang:check` treats a key the code completes at run time
  (`i18n.T(ctx, "issues.status."+s)`) as a prefix some catalog key must
  start with, instead of reporting it missing (M3, D236).
- `make:admin:resource` keeps the standard library's imports in their
  own group, and writes "an" before a vowel (M3).
- `storage.Disk.Serve` keeps an `attachment` Content-Disposition the
  handler set, with the uploaded file's name, for files a browser would
  run (it used to replace it with the stored name) (M3).
- `lang:check` matches key prefixes against the framework's messages
  too (`"validation."+rule`) (M3).
- Fixed: `web.Ctx.SetLocale` raced with database drivers that watch the
  request's context from goroutines of their own (SQLite's interrupt
  watcher): the request's context is now replaced atomically, as
  `Ctx.Events` already did, and error pages after `SetLocale` use the new
  locale (M3).
- Password-reset tokens stop working when the user's session key changes
  (signed out everywhere, or elsewhere), as well as when the password
  does (AC1).
- `auth/social`'s callback signs in with `Auth.SignIn`: users with
  two-factor sign-in on are sent to `AUTH_CHALLENGE_URL` for their code
  (AD2b).
- The admin asks for the user's password again before dangerous actions
  (`ADMIN_CONFIRM=false` turns it off) (AD2b).
- Writes to a table watched with `db.DB.Watch` (the audit log) run in a
  transaction, a savepoint inside an open one, so model hooks run inside
  it; on MySQL, `CreateMany` on such a table inserts rows one by one so
  their keys are known (AU1, D203, D204).
- `i18n.Plural` formats `{count}` for the locale (`1,234 posts`;
  Bangla and Arabic digits for `bn` and `ar`), and the numbers in size
  rules' validation messages (`min`, `max`, `size`, `between`…) follow
  the locale's digits and decimal separator (not grouped); the AI budget
  message's wait is in the user's language, rounded up
  (`i18n.DurationUp`: 89 minutes is "2 hours", was "89 minutes") (I1c,
  D196).
- The framework's messages come from the i18n catalogs, in the request's
  language: validation messages and labels (`validation.*`), error page
  titles and texts (`http.*`, `<html lang>`), CSRF, sign-in and AI budget
  messages (I1b, D192).
- **BREAKING:** values that can't be converted while binding report the
  catalog's `binding.<kind>` message. Before: `"page": "invalid integer
  \"x\""`, `"draft": "must be a boolean"`. After: `"page": "must be an
  integer"`, `"draft": "must be true or false"` (I1b).
- `anetos new` writes `locales/` (`locales.go`, `en/app.yaml`) and calls
  `i18n.ForApp`; its pages take their text from the catalog. `make:auth`
  writes `locales/en/auth.yaml`, sends its emails with `i18n.ForUser`, and
  needs the `locales` folder. `anetos dev` rebuilds when a catalog
  changes (I1b).
- `go.yaml.in/yaml/v3` and `golang.org/x/text` are dependencies of the core
  module (I1b, D195).
- **BREAKING:** an app whose `DB_URL` sets a session time zone other than
  UTC refuses to start, and so do its commands. Before:
  `DB_URL=postgres://…/app?timezone=Asia/Dhaka` started. After: remove
  `timezone=`/`time_zone=`, or set `DB_ALLOW_LOCAL_TIMEZONE=true` to keep
  it ([upgrade guide](docs/site/upgrade/v0.3.md)) (I1a, D186).
- Importing Anetos sets the process's local zone (`time.Local`) to
  `APP_TIMEZONE` from the environment, UTC when unset, and the first app
  to its configured zone; `TZ` is ignored. `time.Now()` and log times on
  a machine in another zone change accordingly (I1a, D188).
- `anetos.Now` and `App.Now` return times in the app's zone (I1a).
- `SCHEDULE_TIMEZONE` defaults to the app's zone (`APP_TIMEZONE`, itself
  UTC by default) (I1a).
- `ai` schemas describe an `anetos.Date` as a `date` string (I1a).
- The framework is named **Anetos** (M1, D185): module `anetos.dev/anetos`,
  command `anetos`, test package `anetostest`, environment variables
  `ANETOS_*`. The encryption key derivation and the signing contexts carry
  the name, so values encrypted by v0.2 (also under `APP_PREVIOUS_KEYS`)
  can't be decrypted, and sessions, remember-me cookies, password-reset and
  verification links and signed storage URLs issued by v0.2 are invalid.
- On MariaDB 11.6+, error 1020 ("Record has changed since last read",
  from `innodb_snapshot_isolation`) is retried like a deadlock by the
  database cache and queue stores, and many-to-many `Attach`, `Detach`
  and `Sync` outside a transaction retry it (S2, D183).
- `anetostest` clears `AI_EMBEDDING_PROVIDER`, so embeddings are the
  fake's (S2).
- `examples/ai` streams without the request timeout (`web.WithoutTimeout`) (A3).
- `examples/ai` is its own module, with the provider drivers (A2).
- `s.Rename` refuses a table with a search index, `Alter` refuses to drop
  or rename an indexed column unless it drops the index too, and
  `Update`, `Delete` and `CursorPaginate` refuse a query with `Search`
  (S1).
- `Count`, `Exists` and the count of `Paginate` no longer order the rows
  they count (S1).

### Fixed
- On MySQL and MariaDB, the tables the schema builder creates (and the
  migrations and search index tables) are utf8mb4 even when the
  database defaults to another character set: on MariaDB's latin1
  default (before 11.6, without a distribution's settings), queue jobs,
  search and any non-Latin text failed with error 1366. `doctor` warns
  about tables whose text columns aren't utf8mb4 (F8, D254). Existing
  tables: see the upgrade guide.

### Security
- A remote database is reached over TLS with its certificate checked by
  default (`DB_TLS=verify` for any `DB_HOST` but this machine;
  PostgreSQL used `sslmode=prefer`, MySQL no TLS). The production
  settings `anetos new` writes use `sslmode=verify-full` (M7, D246).
  Breaking for servers without TLS or with a private CA: see the
  upgrade guide.
- The client's address no longer comes from `X-Real-IP`, which a
  client could set itself when a proxy didn't; only `X-Forwarded-For`
  from `HTTP_TRUSTED_PROXIES` counts (M7, D247).
- `web.MethodOverride` overrides only urlencoded and multipart form
  posts, never cross-site ones (`Sec-Fetch-Site`, else an `Origin`
  other than the host); local redirect paths refuse control characters
  (M7, D247).
- `auth.Require` sets `Cache-Control: no-store` on the responses it lets
  through, so shared caches and the back button don't keep signed-in
  pages (M7, D247).
- `anetos dev` answers 403 to host names other than localhost, IP
  addresses, `APP_URL`'s host and `--host` (DNS rebinding), and warns
  when it listens beyond this machine (M7, D247).
- Two-factor sign-in: two requests with the same TOTP or recovery code
  at the same moment could both succeed; the check and its record now
  run under a lock per user (`cache.WithLock`), which every change of
  the state takes (`ConfirmTwoFactor`, `NewRecoveryCodes`,
  `DisableTwoFactor`), reading it again, so a change made meanwhile
  isn't undone (M7, D248).
- `ConfirmPassword` and `ChangePassword` allow 50 wrong passwords per
  account a day, so a stolen session can't be used to guess the
  password (M7, D248).
- `auth.CreateToken` refuses while acting as another user (403); the
  code `make:auth` writes puts token creation behind
  `RequireConfirmed`, and its password reset changes the password only
  once per link (in a transaction), revokes API tokens and, for an
  address never verified, verifies it and turns off two-factor sign-in
  and social links (M7, D248). Existing apps: see the upgrade guide.
- Search takes at most 10 terms, and matches a term as a prefix from 3
  letters; on MySQL, short words InnoDB skips are dropped (M7).
- SQLite connections refuse double-quoted strings (`_dqs=0`) (M7).
- The `log` mail driver leaves out messages' bodies (and their links)
  in production (M7).
- Local storage writes files 0640 and folders 0750 (M7).
- `anetos add` says that it runs the plugin's code to read its
  settings, and asks first in a terminal (M7, D249).
- The systemd unit `anetos new` writes is sandboxed further
  (`systemd-analyze security`: 1.2), and its `.dockerignore` leaves out
  `.env` files at any depth (M7).
- CI: actions pinned by commit, checkouts without persisted
  credentials, timeouts, govulncheck on the minimum and latest Go
  releases, each at its latest patch (M7, D249).

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
- `anetos make:auth`: writes accounts into an `anetos new` project
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
- `examples/saas`: an `anetos new` + `make:auth` app with a welcome email
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
- `dbtest.RunApp` tests a driver with an `anetostest` app (F12).
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

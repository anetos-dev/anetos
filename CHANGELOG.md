# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/) (see the roadmap's versioning rules).

## [Unreleased]

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

### Changed
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
- `db.Pluck`, `db.Min` and `db.Max` on `*time.Time` columns return UTC
  times, and read SQLite's text times (F9).

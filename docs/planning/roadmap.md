# Anetos — Planning & Roadmap

> **Name.** *Anetos*, from Greek άνετος ("at ease, comfortable"), said
> AH-neh-tos. Module `anetos.dev/anetos`, GitHub org `anetos-dev`, site
> anetos.dev, command `anetos` (chosen at [M1](#v03--search-ai--starter-experience), design D185).

| | |
|---|---|
| **Status** | Pre-alpha: planning and design |
| **Owner** | Samiul Hoque |
| **Last updated** | 2026-10-03 |
| **Related** | [Design document](../design/design.md) · [Documentation guide](../contributing/documentation-guide.md) |

---

## 1. Vision

Anetos is an open-source Go framework that gives developers the same comfort
Laravel gives PHP developers: create a project with one command, build CRUD and
complex applications quickly, add capabilities by installing packages, and
deploy easily. Anetos does this **the Go way**. It uses static types instead of
runtime magic and code generation instead of reflection. It stays compatible
with the standard library, and the whole application ships as a single binary.

What makes Anetos different from other Go frameworks is that **concurrency is a
core feature, not something you add yourself**. HTTP, queue workers, pub/sub
listeners and the scheduler all run as supervised goroutines inside one
process. They share config, database and logging, and they shut down together
gracefully.

## 2. Goals

1. **Zero to running in one command.** `anetos new blog && cd blog && anetos dev`
   gives you a working app with a database (SQLite by default), migrations and
   hot reload. Nothing else needs installing.
2. **Comfortable CRUD.** A data layer good enough that raw SQL is something you
   *choose*, not something you *need*: relationships, eager loading, scopes,
   soft deletes, pagination, transactions and migrations.
3. **Swappable drivers.** The database, cache, queue, mail, storage, session
   and pub/sub backends are each chosen through `.env`, like Laravel's
   contracts.
4. **Go-native concurrency.** Workers, listeners, events and the scheduler are
   first-class, typed and supervised.
5. **An open ecosystem.** Third-party packages register routes, migrations,
   config, commands, workers and listeners through one plugin interface and
   one command (`anetos add`).
6. **Single-binary deployment.** Templates, assets and migrations are embedded,
   and one binary runs every role.
7. **Fast by construction.** No reflection in the request path, and benchmarks
   are checked in CI.
8. **Documented as it's built.** No feature is finished until it's documented
   (see the [documentation guide](../contributing/documentation-guide.md)).

## 3. Non-goals

- **Not a Laravel port.** No facades, no global state, no magic methods, and no
  mirroring of Laravel's directory structure for its own sake.
- **No custom template language.** We use templ (Plush was a lesson learned).
- **No per-request reflection** in hot paths. Reflection is allowed only at
  startup or registration time.
- **Not a microservices toolkit** (go-kit, Kratos). Anetos targets
  applications. It can still run as a service, but that isn't the design
  centre.
- **No admin panel, debug dashboard or WebSockets before v1.0.** These are in
  the backlog.
- **No attempt to support every driver.** The core ships a few, and plugins
  cover the rest.
- **Not an LLM framework.** The `ai` package connects models to the app
  (typed output, tools, queues, storage, tests); it has no orchestration
  graphs, prompt-template language or vector database of its own.

## 4. Target users

- Go developers who want batteries included without assembling 15 libraries.
- Developers moving from Laravel, Rails or Django who want Go's performance and
  deployment model without giving up productivity.
- Small teams building CRUD-heavy products, internal tools and SaaS back ends,
  including ones that consume event streams (pub/sub, queues).

---

## 5. Release plan

### Versioning rules

- We follow [Semantic Versioning](https://semver.org). Before 1.0, a **minor**
  version (`v0.N.0`) marks a milestone, and **patch** versions (`v0.N.x`)
  extend it.
- **Extending a milestone.** While v0.1 is the latest release, anything added to
  the foundation ships as `v0.1.1`, `v0.1.2` and so on. **Once `v0.2.0` is
  tagged, all further work (including foundation additions) ships as `v0.2.x`.**
  This is how Go modules work: `go get @latest` takes the highest version, so a
  `v0.1.3` tagged after `v0.2.0` would never reach users on the main line. We
  only tag an older line if we deliberately maintain a release branch for it
  (for example, a security fix).
- Breaking changes are allowed before 1.0, but each one must be listed in the
  CHANGELOG with migration notes and an upgrade guide (see the documentation
  guide). From v0.5, a renamed or removed identifier is first deprecated for
  one minor release, with `//go:fix inline` where it applies, and removed in
  the next (design D309).
- We stay below `v2` for as long as possible. In Go, `v2+` requires a `/v2`
  import path, which is disruptive for users.
- Driver modules are versioned and tagged independently, for example
  `drivers/redis/v0.2.0`.

### Overview

| Version | Theme | Audience | Headline outcome |
|---|---|---|---|
| **v0.1** | Foundation | Us | Build a CRUD app with forms, validation, a DB and migrations using only the docs |
| **v0.2** | Batteries | Us + early testers | Auth, social login, queues, events, pub/sub listeners, scheduler, mail, storage, plugins |
| **v0.3** | Search, AI & starter experience | Us + early testers | Full-text search, AI-capable apps (typed LLM calls, tools, streaming), roles and permissions, an admin, a deploy story, and a new app that looks finished (theme, `make:crud`) |
| **v0.4** | API stack | Us + early testers | API-only apps: `anetos new --stack=api`, token auth endpoints, JSON CRUD, an OpenAPI spec generated from typed handlers |
| **v0.5** | Design kits & public release | **Public release** | Bootstrap, Bulma, Pico and Tailwind as kits a developer picks or switches to; API stability pass, release plumbing, versioned docs, launch |
| **v0.6** | Frontend | Public | Vite + Inertia starter kits (Vue, React, Svelte) on top of the API stack |
| **v1.0** | Stable | Public | API stability promise |

The plan changed on 2026-10-07 (design D243): the public release moved from
v0.3 to v0.5, so that it ships with an API-only stack (v0.4) and design
kits (v0.5), and the front-end stacks follow it (v0.6). Generated code
belongs to the app and never updates, so how the generators write
markup (D244) and handlers (OpenAPI) must settle before people generate
real apps.

Estimated effort, part-time, with Claude Code assisting: v0.1 **6–10 weeks**,
v0.2 **6–10 weeks**, v0.3 **4–6 weeks** (plus **4–6 weeks** for the search
and AI work packages added on 2026-10-02), v0.4 **3–5 weeks** (OpenAPI is
the least predictable part), v0.5 **3–5 weeks** with the launch work, v0.6
**4–6 weeks**. These are rough;
the data layer (F7–F9) is the least predictable. We'll re-estimate at the end
of each milestone.

Each work package (WP) below has an ID so issues, PRs and docs can refer to it.
**Every WP includes its own tests and documentation**; that isn't listed
separately.

---

### v0.1 — Foundation

Goal: the core an application stands on.

| WP | Work package | Notes |
|---|---|---|
| F1 | Repository & tooling | Multi-module layout, CI (golangci-lint, `go test -race`, govulncheck), release/tag scripts, CHANGELOG, docs skeleton, PR template with Definition of Done |
| F2 | App kernel | Lifecycle (register → boot → run → shutdown), typed service container, internal provider mechanism (the future plugin API, used internally first) |
| F3 | Configuration | `.env` loading, typed config structs, environments, fail-fast validation at boot |
| F4 | Runtime supervisor | Components (`Run(ctx) error`), roles (`run --only=…`), restart policies, graceful shutdown ordering, supervised `app.Go` |
| F5 | HTTP layer | Router on `net/http` with groups, named routes and URL generation; `web.Ctx`; three handler forms (plain, `func(*Ctx) error`, typed); binding; responses; error handling; dev error page; core middleware |
| F6 | Validation | Rule tags, custom rules, messages, 422 JSON for APIs. (Error bags and old input for HTML forms need sessions: moved to F10. Database rules `unique`/`exists`: F7.) |
| F7 | Data layer core | Connections and dialects (Postgres, MySQL, SQLite), generic query builder, model runtime (CRUD, timestamps, soft deletes), transactions, raw SQL scanning into structs, pagination, `unique`/`exists` validation rules |
| F8 | Migrations & seeders | Go schema builder, runner, embedding, `migrate`, `migrate:rollback`, `migrate:status`, `migrate:fresh` (dev only), seeders. (Commands run through `Runner.Command` until F11 registers them.) |
| F9 | Model code generation | `anetos gen`: typed column references generated from model structs, `-check` for CI. (Relation handles need relations: moved to v0.1.x.) |
| F10 | Views, sessions & forms | templ integration, layouts, view helpers (route URLs, CSRF field, errors, old input, assets), cookie sessions, flash messages, CSRF, method override, bundled htmx; validation failures on HTML forms redirect back with the error bag and old input; `APP_KEY` and encryption |
| F11 | CLI | Global `anetos new`, `anetos dev` (watch, rebuild, restart, browser reload), `make:handler`, `make:model`, `make:migration`, `make:middleware`; app-binary command framework (`serve`, `run`, `migrate*`, `routes:list`, custom commands) |
| F12 | Testing helpers | App bootstrap for tests, fluent HTTP test client, per-test DB transaction rollback, model factories (`factory.New`). (Clock, mail and queue fakes arrive with those features in v0.2.) |
| F13 | Relations (v0.1.1) | has-one, has-many, belongs-to, many-to-many, eager loading with `With`, `Load`, `WhereHas`, pivot writes, generated relation handles |

**Progress**

| WP | Status |
|---|---|
| F1 Repository & tooling | ✅ Done 2026-09-30 |
| F2 App kernel | ✅ Done 2026-09-30 |
| F3 Configuration | ✅ Done 2026-09-30 |
| F4 Runtime supervisor | ✅ Done 2026-09-30 |
| F5 HTTP layer | ✅ Done 2026-09-30 |
| F6 Validation | ✅ Done 2026-09-30 |
| F7 Data layer core | ✅ Done 2026-09-30 (SQLite, PostgreSQL 16 and MariaDB 10.11 tested locally; MySQL 8.4 in CI) |
| F8 Migrations & seeders | ✅ Done 2026-09-30 |
| F9 Model code generation | ✅ Done 2026-09-30 |
| F10 Views, sessions & forms | ✅ Done 2026-09-30 |
| F11 CLI | ✅ Done 2026-09-30 |
| F12 Testing helpers | ✅ Done 2026-09-30 |
| F13 Relations (v0.1.1) | ✅ Done 2026-09-30 |

All v0.1 work packages are done, and the exit criteria were checked on
2026-09-30 (see below). **v0.1.0 was tagged on 2026-09-30** (a private
tag: `v0.1.0`, `cli/v0.1.0`, `drivers/{sqlite,postgres,mysql}/v0.1.0`;
the modules still use `replace` directives until M1b). **v0.1.1**, the
relations patch below, followed the same day. Next: v0.2.

**Patch v0.1.1, work package F13 (✅ done 2026-09-30):** **relations and eager loading**
(has-one, has-many, belongs-to, many-to-many, `With(...)`, `db.Load`,
`WhereHas`, `Attach`/`Detach`/`DetachAll`/`Sync`) and the relation handles `anetos gen`
writes for them (`PostRels.Author`). This was kept out of v0.1.0 so the
core query builder settled first, and had to ship before v0.2 starts. Pop's
weak relations were one of the main reasons for this project. N+1
detection moved to v0.2 (design D87).

**Exit criteria**

- A "blog" (posts with create, edit, delete, validation, flash messages and
  pagination) can be built by following only the docs, on SQLite and Postgres.
- CI is green with `-race` against SQLite, Postgres and MySQL.
- A baseline benchmark against plain `net/http` is recorded (hello world,
  typed JSON handler, single-row DB read).
- Every exported identifier has a doc comment, and every component has a
  concept page.

**Exit criteria check (2026-09-30)**

| Criterion | Result |
|---|---|
| Blog from the docs alone | ✅ Built by a reader who used only `docs/site`, linked examples and `go doc`, in about 25 minutes, on SQLite and then PostgreSQL. No blockers; the gaps it found (HTML pagination links, switching databases, factory locations, checkboxes) are fixed in the docs and API (D80) |
| CI green with `-race` | ✅ with a caveat: the repository has no remote yet, so GitHub Actions has never run. The CI steps pass locally on Go 1.26.8 and 1.27.1 against SQLite, PostgreSQL 16, MariaDB 10.11 and MySQL 8.0.46 (8.4, CI's version, couldn't be installed here). MySQL 8.0 caught a test bug MariaDB didn't |
| Baseline benchmark | ✅ [docs/benchmarks/v0.1-baseline.md](../benchmarks/v0.1-baseline.md) |
| Doc comments | ✅ enforced by `make api-docs` (D82), fields and interface methods included |
| Concept pages | ✅ one per component: lifecycle, configuration, runtime supervisor, HTTP requests, validation, data layer, migrations, code generation, commands, server-rendered HTML, testing |

---

### v0.2 — Batteries

Goal: everything a real application needs beyond CRUD.

**Progress**

| WP | Status |
|---|---|
| B1 Cache | ✅ Done 2026-09-30 (memory, database and Redis stores; locks) |
| B2 Sessions & rate limiting | ✅ Done 2026-09-30 (database and Redis session drivers; `web/ratelimit`) |
| B3 Authentication & authorization | ✅ Done 2026-09-30 (library: login, remember me, throttling, reset and verification tokens, API tokens, policies; scaffolding moved to B14) |
| B4 Social login | ✅ Done 2026-10-01 (Google, GitHub, generic OIDC; `social_accounts` links; `APP_URL`) |
| B5 Queue | ✅ Done 2026-10-01 (sync, memory, database and Redis drivers; workers; failed-job commands; `db.WithTestTx`) |
| B6 Events | ✅ Done 2026-10-01 (sync, async and queued listeners; `queue.RegisterFunc`) |
| B7 Pub/sub listeners | ✅ Done 2026-10-01 (memory, Redis Streams and Google Pub/Sub brokers; typed listeners, retries, dead letters; ordering keys deferred) |
| B8 Scheduler | ✅ Done 2026-10-01 (cron and fluent schedules with time zones; `WithoutOverlapping`, `OnOneServer`, `Timeout`; `schedule:list`, `schedule:run`; in new projects) |
| B9 Mail | ✅ Done 2026-10-01 (mailables with templ bodies; log, SMTP and memory transports; queued mail; Postmark, now the `plugins/postmark` plugin) |
| B10 Storage | ✅ Done 2026-10-01 (local, memory and S3-compatible disks; named disks; signed temporary URLs and a file handler) |
| B11 Plugin system | ✅ Done 2026-10-01 (package `ext`, `ext.Load`, `anetos add` / `anetos remove`, enforced namespaces, `Requires()` checks; `plugins/postmark` adds settings, a migration, a route, a job and commands through the public API) |
| B12 Test fakes | ✅ Done 2026-10-01 (recording of jobs, events, mail and pub/sub messages with typed assertions; queue, event and pub/sub fakes; disk assertions; the app clock with `Freeze`/`Travel`) |
| B13 N+1 detection | ✅ Done 2026-10-02 (units of work in the kernel; repeated-query warnings with the caller, in development and tests by default, for requests, jobs, listeners, messages and tasks; `anetostest` assertions) |
| B14 Auth scaffolding | ✅ Done 2026-10-02 (`anetos make:auth`: model, handlers, templ pages, emails, routes, migration, `setupAuth` and tests written into the app; links emailed with the mailer; `examples/auth` emails its links too; sign-in with Google and GitHub added by the exit check) |

All v0.2 work packages are done, and the exit criteria were checked on
2026-10-02 (see below). **v0.2.0 was tagged on 2026-10-02** (private
tags, as for v0.1: `v0.2.0`, `cli/v0.2.0`,
`drivers/{sqlite,postgres,mysql,redis,s3,gcppubsub}/v0.2.0` and
`plugins/postmark/v0.2.0`; the modules still use `replace` directives
until M1b). The stretch goal, basic i18n, wasn't done: it joins i18n in
the backlog unless v0.3 takes it up. Next: v0.3.

| WP | Work package | Notes |
|---|---|---|
| B1 | Cache | Contract + memory, database and Redis (`drivers/redis`) stores; locks (used by the scheduler); `cache:clear` |
| B2 | Sessions & rate limiting | DB and Redis session drivers; rate limiter middleware and `Allow` for login throttling |
| B3 | Authentication & authorization | Passwords (argon2id), login, logout, remember-me, email verification and password reset tokens, API tokens, typed policies |
| B4 | Social login | OAuth2/OIDC: Google, GitHub, generic OIDC |
| B5 | Queue | Typed jobs, dispatch, delay, retries with backoff, timeouts, failed-jobs store and retry command; drivers: sync, memory, database, Redis |
| B6 | Events | In-process typed events: sync, async (bounded pool), queued (durable) |
| B7 | Pub/sub listeners | `app.Listen` abstraction, typed decode, concurrency, ack/nack, retries, DLQ where supported; drivers: Redis Streams, Google Pub/Sub |
| B8 | Scheduler | Cron and fluent schedules, overlap prevention, single-instance execution through cache locks |
| B9 | Mail | Mailables with templ templates, SMTP and log drivers, plus one API driver shipped as a first-party plugin (Resend or Postmark) |
| B10 | Storage | Contract + local driver, S3-compatible driver module |
| B11 | Plugin system (public) | Stable `Plugin` interface, `anetos add` / `anetos remove`, namespacing, compatibility checks; at least one first-party plugin built **only** through the public API |
| B12 | Test fakes | Mail, queue, events, storage and clock fakes with assertions |
| B13 | N+1 detection | In development, warn when a request runs the same query shape many times (request-scoped query tracking); from v0.1.1 |
| B14 | Auth scaffolding | `anetos make:auth` generates registration, login, logout, email verification, password reset and API token pages into the app, from `examples/auth`; sends verification and reset links with mail (after B9) |

**Stretch:** basic i18n for validation and auth messages.

**Exit criteria**

- An example app has sign-up and login (password and Google), sends a welcome
  email from a queued job, consumes a pub/sub topic, and runs a scheduled task,
  **all from one binary**, with `--only=` role splitting shown working.
- A first-party plugin installs with one command and adds routes, a migration,
  config, a command and a worker.

**Exit criteria check (2026-10-02)**

A reader who used only `docs/site`, the examples and `go doc` built the
app of the first criterion with `anetos new` and `make:auth` and
installed the Postmark plugin, in about 12 minutes, without reading the
framework's source. What it found is fixed: `anetos new --replace` didn't
cover drivers and plugins (D151); adding social login to a `make:auth`
app took guesswork, and its test helper existed only in an example
(D149, D150); jobs logged through Go's default logger (D148); smaller
items (a misleading mail-driver hint, the plugin left `// indirect`, no
plugin README, lower-case provider names on buttons). The app is now
[`examples/saas`](../../examples/saas), with a test that runs it split
by role (D152).

| Criterion | Result |
|---|---|
| Sign-up and login, password and Google | ✅ `make:auth` writes both (Google and GitHub, each on once its settings are set). Tested through the real OAuth/OpenID Connect flow against a stand-in provider (`anetostest.FakeSocial`); the reader checked the redirect to Google with dummy credentials. A real Google account hasn't signed in: there are no credentials here |
| Welcome email from a queued job | ✅ `examples/saas` dispatches `jobs.SendWelcome` after the user commits; split, the `workers` process sends it, not the web process |
| Consumes a pub/sub topic | ✅ `billing.subscription_changed` over Redis Streams, applied by the `listeners` process. Across processes it needs Redis: without `ANETOS_TEST_REDIS_URL` that step of the test is skipped (the memory broker is per process) |
| Runs a scheduled task | ✅ `end-trials`, every minute, run by the `scheduler` process (`OnOneServer` locks in the database cache) |
| All from one binary, `--only=` shown working | ✅ `roles_test.go` builds the binary, runs `http`, `workers`, `listeners` and `scheduler` processes on one SQLite file (and Redis), follows a sign-up across them, then runs everything in one process; an unknown role exits with an error. It takes up to a minute (the scheduler's tick); `-short` skips it |
| Plugin with one command: routes, migration, config, command, worker | ✅ `anetos add anetos.dev/anetos/plugins/postmark`: `POST /postmark/webhook`, the `postmark_suppressions` migration, two settings, two commands, and the `postmark:webhook` job, run by the app's queue workers (in the reader's split run, by the `workers` process). The plugin API has no hook for a component of its own; a plugin starts one with `app.Go` from `Boot` |

---

### v0.3 — Search, AI & starter experience

Goal: good enough for strangers to build and deploy real apps, including
apps with search and AI features. Tagged for us and early testers; the
public release moved to v0.5 on 2026-10-07 (design D243), and with it
the launch work (M1b, M8, M9, versioned docs) and the volunteer test.

**Progress**

| WP | Status |
|---|---|
| S1 Database capabilities & full-text search | ✅ Done 2026-10-02 (`q.Search`, `t.SearchIndex`, `SEARCH_LANGUAGE`/`SEARCH_RANKING`, boot-time capability checks, `search:reindex`; tested on SQLite, PostgreSQL 16, PostgreSQL 17 with pg_textsearch, MySQL 8.0 and MariaDB) |
| A1 AI core | ✅ Done 2026-10-02 (package `ai`: `Generate`, `GenerateObject[T]`, `Stream`, `Func` tools, `Agent`, schemas from struct tags, `ForApp` and `AI_*` settings, the fake; `anetostest.FakeAI`; `examples/ai`; tested with the fake only, the providers come in A2) |
| A2 AI providers | ✅ Done 2026-10-02 (`drivers/anthropic`, `drivers/openai` with `openai-compatible`, `drivers/gemini`; `ai.Reasoning`, `Schema.Map`, the `ai/aitest` conformance suite on cassettes. The cassettes are written by hand from the APIs' documented formats, not yet recorded from the live APIs: that needs the providers' keys, `ANETOS_AI_RECORD=1`) |
| R1 Roles & permissions | ✅ Done 2026-10-02 (package `auth/rbac`: permissions in code, roles in code and in the database, global and scoped grants (teams), checks and middleware for the signed-in user with API token abilities as a ceiling, `AuthorizeRole` against escalation, `rbac:*` commands; `auth.CurrentID`; `examples/teams`; tested on SQLite, PostgreSQL 16 and 17, MySQL 8.0 and MariaDB) |
| A3 AI in the app | ✅ Done 2026-10-02 (stored conversations with `Add`/`Reply`/`StreamReply`, usage records and per-user budgets (`TrackUsage`, `ratelimit.AllowN`), queued replies (`QueueAgents`, `QueueReply`, acting as the user with the new `auth.ActAs`), `ai.SSE` over the new `c.Events()` with the htmx SSE extension bundled, `make:agent`; `examples/assistant`, a help center whose agent searches its articles, checked in a browser against a stand-in OpenAI-compatible server; tested on SQLite, PostgreSQL 16 and 17, MySQL 8.0 and MariaDB. Files as model inputs went to the backlog) |
| S2 Vectors & hybrid search | ✅ Done 2026-10-02 (`t.Vector`, `CreateEmbeddings` (companion `<table>_embeddings`), `q.Similar` and `q.Hybrid` (reciprocal rank fusion), `db.VectorSearch` capability; `ai.Embed`/`EmbedQuery` with an optional `Embedder` contract on the OpenAI, compatible and Gemini drivers and `AI_EMBEDDING_PROVIDER`/`AI_EMBEDDING_MODEL`; `ai.EmbeddingsFor` with `Sync` (queue job), `ai:embed`, `Search` and the agent `Tool`; `examples/assistant` searches its articles by meaning and words; tested on SQLite, PostgreSQL 16 and 17 with pgvector, MariaDB 11.8 (built from source), with the refusals on MySQL 8.0 and MariaDB 10.11. Embedding cassettes are hand-written. Found on MariaDB 11.8 and fixed: binary vector parameters, cascades skipping the vector index, snapshot-isolation errors in pivot writes) |
| I1 Internationalization | ✅ Done 2026-10-03 (§10.6, §14.5). I1a (times and dates) ✅ done 2026-10-03: the database session's zone checked at boot (`DB_ALLOW_LOCAL_TIMEZONE`), `anetos.Date` (columns, forms, JSON, date rules), `APP_TIMEZONE` as the process's zone with the zone database built in, `SCHEDULE_TIMEZONE` defaulting to it; tested on SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8. I1b (translations) ✅ done 2026-10-03: package `i18n` (YAML catalogs, `T`, `Plural`, fallbacks, `lang:check`), the request's locale (the URL with `LOCALE_URL=prefix` or `subdomain`; otherwise the cookie, session, user's preference, `Accept-Language`, `?locale=` on pages), users' display and communication locales and time zones (`ForUser`), queue jobs carrying them, the framework's messages translated, `anetos new` and `make:auth` with catalogs, `examples/i18n` in English and Bangla. I1c (formatting and languages) ✅ done 2026-10-03: `i18n.Number`, `Fixed`, `Percent`, `Currency` (x/text, the language's digits or `format.numbering`), `Date`, `Time`, `DateTime`, `Format` (CLDR patterns and names in the catalogs, in the user's zone), `Ago`, `Duration`, `Dir`, `LanguageName`, `web.Alternates` for `hreflang`, `anetos lang:add` from `anetos.dev/locales` (bn, es, fr; in the public repository `anetos-dev/locales`) |
| G1 Google Cloud Storage | ✅ Done 2026-10-03 (`drivers/gcs` on the official client: `STORAGE_DRIVER=gcs`, Application Default Credentials or a service account key, V4 signed URLs by the key or the IAM API, generation-pinned reads; the conformance suite against fake-gcs-server; not yet run against a real bucket, which `ANETOS_TEST_GCS_BUCKET` enables) |
| AU1 Audit log & soft deletes | ✅ Done 2026-10-06 (design §10.7, D202–D209): package `audit` (`Track` with `Except`/`Redact`/`Reveal`, entries in the change's transaction, bulk entries with every key, `Record`, `History`, `WithActor`, `Prune`, `Anonymize`, `AUDIT_*`), watched writes in `db` (`DB.Watch`), kernel carriers (the actor follows jobs and async listeners), `UniqueLive` and `unique_live`, `db.PruneTrashed`; `examples/audit`; tested on SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8 |
| AD1 Admin engine, users & roles | ✅ Done 2026-10-06 (design §12.3, D211–D220): **AD1a** the module `anetos.dev/anetos/admin` (resources with lists, search, filters, sorting, pages and a scope; record pages; forms from form structs with validation and choices from the database; actions, bulk actions; a trash; a permission per resource and action; `ADMIN_*`; pages in `html/template` with htmx under a strict CSP), `Router.Host`, `rbac.Registry.Declare`, `anetos make:admin` and `make:admin:resource`; **AD1b** users (`admin.Users`: disable, verification, sign out everywhere, API tokens, roles by scope, acting as a user with a banner; only by those with every permission the user has, `rbac.AuthorizeOver`) and roles (`admin.Roles`), with auth's disabled accounts, session keys and impersonation, and the audit log's `acting_as`; `examples/admin` |
| AD2 Admin dashboard, activity & security | ✅ Done 2026-10-06 (design §12.3, D221–D226): **AD2a** the dashboard (widgets: figures, bar charts as SVG, tables; built-in sign-ups, queue health, AI usage, recent activity), the activity pages over the audit log with a history on records' pages, failed jobs (`queue.CountFailed`, `queue.FindFailed`), scheduled tasks with their last run (`Scheduler.LastRun`); `make:admin` adds them. **AD2b** two-factor sign-in in `auth` (TOTP, recovery codes, `SignIn` for social login), password confirmation (`RequireConfirmed`), the admin's confirmation before dangerous actions (`ADMIN_CONFIRM`), `ADMIN_TWO_FACTOR=required`, `ADMIN_ALLOW_IPS`, package `qr`; `make:auth`'s pages, translated in `anetos.dev/locales`; `examples/auth`; guide "Two-factor sign-in and password confirmation" |
| AC1 Account settings | ✅ Done 2026-10-06 (design §15, D227, D228): `make:auth`'s settings page (`/settings`, `AUTH_SETTINGS_URL`): name, password (`auth.ChangePassword`), language and time zone (`i18n.TimeZones`), a new email address confirmed by a link to it with the old one told (on by default), deleting the account (off by default); the admin links the user's name to it; translated in `anetos.dev/locales` |
| M5 Build & deploy | ✅ Done 2026-10-06 (design §21, D229–D231): `anetos build` (generation, a static `-trimpath` binary, `--version`, `--cgo`); `anetos new` writes a `Dockerfile` (distroless, non-root, `/data`, a health check), `.dockerignore`, a hardened systemd unit and `deploy/production.env.example`; the `version` and `health:check` app commands; roles declared before their components (`Supervisor.Declare`, so `run --only=workers,scheduler` works without tasks); guide "Deploy" (systemd, Docker, Compose, Fly.io, Render, HTTPS, roles). The images were built and run with SQLite and PostgreSQL, and the Compose example with PostgreSQL; the Fly.io and Render configurations follow their documentation and weren't deployed |
| M3 Reference example app | ✅ Done 2026-10-06 (design D234–D236): `examples/tracker`, an issue tracker: projects with owners, members and viewers (`auth/rbac` scopes), issues numbered per project with labels, priorities and assignees, filters, search and pages, comments and closing with htmx, files, each issue's history from the audit log, emails from queue jobs in each user's language, a weekday digest, an email preference on the settings page, an admin with a trash and the activity, a JSON API with read-only tokens; 40 tests through HTTP (the accounts' included), on SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8. Found and fixed on the way: `anetostest.ActingAs` (with `auth.Auth.LoginSession`), `lang:check` on keys made at run time, `make:admin:resource`'s imports and articles, the new project's stylesheet |
| M1 Identity | 🟡 Partly done 2026-10-03: name, org, domain and rename (Anetos, from Greek άνετος, "at ease"; GitHub org `anetos-dev`; anetos.dev; module `anetos.dev/anetos`, a vanity import path; command `anetos`, package `anetostest`, `ANETOS_*` variables). Logo ✅: a mark drawn from the founder's sketch with the name in Varela Round, in `anetos-dev/website`'s `brand/` and the READMEs |
| M10 Starter experience | ✅ Done 2026-10-06 (design D237–D241): the starter theme (`public/static/app.css`, plain CSS, light and dark, no build step; `anetos new --css=none` without it), the layout's header with `navLink` (`web.RouteIs`) and `make:auth`'s `AccountMenu` (with `session.Manager.Use`), `make:auth`'s pages restyled, `anetos make:crud` (model, migration, handlers, views, routes, catalog, test; seven field types, `:optional`, `:unique`), the page after signing in from `AUTH_HOME_URL` with a default in code (`auth.DefaultHomeURL`); the tutorial and the tracker restyled; guide "Style your app"; generated tests pass on SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8 |
| M7 Security | ✅ Done 2026-10-07 (design §20, D245–D250): a self-audit in four parts (HTTP, accounts, data, tools and deployment) with proofs of concept, no critical or high finding; the medium and low ones fixed (verified TLS to remote databases with `DB_TLS`, `X-Real-IP` dropped, form-only method override, `no-store` under `Require`, the dev server's host check, two-factor codes under a lock, daily caps on password confirmation, token creation behind confirmation and not while impersonating, the generated reset revoking more, search term caps, SQLite `_dqs=0`, `LIKE` helpers, mail log bodies hidden in production, storage permissions, `anetos add` asking first, a tighter systemd unit and `.dockerignore`, pinned CI actions and Dependabot) or accepted with reasons in `docs/security/checklist.md`; the dependency review (licenses, govulncheck); `SECURITY.md`; the `doctor` command of every app, with checks the features add (`App.AddCheck`), and `anetos doctor`; guide "Secure your app" and the v0.3 upgrade notes. Tested on SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8, and Go 1.26; the guide's `systemd-run` command and the hardened unit weren't run here (no systemd). GitHub's private vulnerability reporting must be turned on in the repository's settings |
| M6 Performance | ✅ Done 2026-10-07 (design §22, D251–D253): benchmarks of the page an app made with `anetos new` and `make:auth` serves and of each of its parts, each middleware, lists, and chi, Gin and Echo doing the same work; optimizations (one context value per request for the router's state, request IDs without a system call, sessions tracking their changes, cached derived keys, reused row scanners: a signed-in page 13–15% faster with about 20% fewer allocations); allocation budgets in `make check` and every CI run, and a blocking comparison with the base branch on pull requests (the `Benchmarks` workflow; `benchmark-ok` to accept one); results, method and analysis in `docs/benchmarks/v0.3.md`, the concept page "Performance". The `Benchmarks` workflow wasn't run here (it needs GitHub); the script it runs was. An independent review's findings were fixed (the label didn't re-run the check; a sub-request could overwrite the request's ID and client IP; doc claims the data didn't support) |
| M2 Docs site | 🟡 Partly done 2026-10-06 (design D200, D201): docs.anetos.dev built with Hugo and Hextra by `anetos-dev/docs` from `docs/site` (search, diagrams, edit links, relative links resolved), and anetos.dev with the go-import pages (M1b's vanity path), both live on Cloudflare Pages (www.anetos.dev redirects to anetos.dev; `go-import` checked for `anetos.dev/anetos`, a nested module and `anetos.dev/locales`); the framework's CI triggers a docs rebuild through a deploy hook when `docs/site` changes. The tutorial ✅ done 2026-10-06 (design D234): "Build an issue tracker" in seven parts, from `anetos new` to deploying, every code block a region of `examples/tutorial` (a reader's project, checked against the generators by cli's `TestTutorialProject`). The navigation ✅ reworked 2026-10-06 (design D242): getting started rewritten as the order of a first project (installing, editor, database, a project, its structure, `make:crud`, `make:auth`, testing and building, then the tutorial), and every section's pages grouped in the sidebar (front matter `group` and `weight`, checked by `make docs-check`; URLs unchanged). Left: versioned docs at the v0.3 tag |

All v0.3 work packages are done but M1's and M2's parts that moved to
v0.5 with the public release (D243), and the exit criteria were checked
on 2026-10-07 (see below). **v0.3.0 was tagged on 2026-10-07**:
`v0.3.0`, `cli/v0.3.0`, `admin/v0.3.0`,
`drivers/{sqlite,postgres,mysql,redis,s3,gcppubsub,gcs,anthropic,openai,gemini}/v0.3.0`
and `plugins/postmark/v0.3.0` (the modules still use `replace`
directives until M1b, in v0.5). Next: v0.4.

The audit log (AU1) and the admin interface (AD1, AD2) were added on
2026-10-06, before the public release and before M4: the audit log
changes the data layer, so it must land before the API stability pass
(M8), and the admin builds on it (its actions are logged). Teams and a
content review workflow went to the backlog (design D210).

The search and AI work packages come first, so the public release has
them (decided 2026-10-02; design §10.5, §14.4, D153–D159). They follow
one order: full-text search in the data layer, then the AI core and its
providers, then vectors and hybrid search, which joins the two. Roles and
permissions (R1) came in after A2, before A3: multi-user apps need them
from the first release, and A3's per-user budgets and tools build on
them (decided 2026-10-02; design §15, D169–D174).

| WP | Work package | Notes |
|---|---|---|
| S1 | Database capabilities & full-text search | Each dialect's capabilities (`FullText`, `BM25`), probed on the server at boot where it matters (extensions, versions); features declare what they need (`d.Require`), and the app refuses to start when the configured database can't provide it, or when a search index was built for other settings, with the alternatives in the message (commands that change the schema still run); `anetos new` writes settings its database supports, and migrations check them. Search indexes declared in migrations (`t.SearchIndex`: PostgreSQL `tsvector` generated column + GIN, SQLite FTS5 with sync triggers, MySQL/MariaDB generated column + `FULLTEXT`), `q.Search(text)` ranked and paginated, `SEARCH_LANGUAGE` (default `simple`, prefix matching), `SEARCH_RANKING=default\|bm25` (BM25: SQLite FTS5 natively, PostgreSQL 17+ with pg_textsearch, refused on MySQL), `search:reindex`; a search box in `examples/forms` |
| A1 | AI core | Package `ai`: the provider contract (`Generate`, `Stream` as an iterator of events), messages (JSON for storage), `ai.Generate` (text), `ai.GenerateObject[T]` (JSON schema from the struct's json, description and validate tags; the answer checked with the `validate` rules, retried once), `ai.Stream`, typed tools (`ai.Func(name, description, fn)`, input validated, run as the current user, 4xx errors told to the model) and `ai.Agent` with a step limit, usage on every response and in total, an escape hatch to the provider's own client and options; `ai.ForApp` with `AI_PROVIDER`, `AI_MODEL`, `AI_MAX_TOKENS`, `AI_TIMEOUT`; a log line per request (no content) and a unit of work per tool call; the `Fake` provider and `anetostest.FakeAI` (forced in tests) with scripted replies and tool calls; `examples/ai`; no provider SDK in the core |
| A2 | AI providers | Driver modules wrapping the official SDKs: `drivers/anthropic`, `drivers/openai` (with an OpenAI-compatible mode: Ollama, OpenRouter, Groq, vLLM…), `drivers/gemini`; per-provider keys and options (`ProviderOptions` types), translation of the common schema to each structured-output dialect (`Schema.Map`), reasoning state kept in conversations (`ai.Reasoning`); each passes an `ai/aitest` conformance suite against recorded HTTP exchanges, and live when its key is set |
| R1 | Roles & permissions | Package `auth/rbac`: permissions declared in code as typed constants; roles declared in code (super roles) and roles administrators store in the database, built from declared permissions; grants of roles and single permissions to users globally or in a scope (`team:42`), a global grant applying everywhere; checks of the signed-in user (`Authorize`/`AuthorizeIn`, `Can`, `HasRole`, `Require`/`RequireIn` middleware) with API token abilities as a ceiling, and of any user (`rbac.Of`); grants read once per unit of work; `AuthorizeRole` so no one gives more than they have; `Assignments`, `UsersWith`; `rbac:*` commands; `examples/teams`; guide, concept, reference |
| A3 | AI in the app | Conversations stored in the database (`ai.Migrations`), usage and cost records with per-user budgets (on the rate limiter), queued replies (generation as a queue job, with retries: `QueueReply`), streaming to the browser (server-sent events, htmx-friendly), `make:agent`; an AI assistant with tools over its data in an example; guides |
| S2 | Vectors & hybrid search | Embeddings in `ai` (`ai.Embed`, A2's providers), vector columns in migrations, `q.Similar(model, vector)` (PostgreSQL with pgvector, MariaDB 11.7+, SQLite by a scan for small data; refused on MySQL Community, whose `DISTANCE()` is HeatWave-only), `q.Hybrid(text, model, vector)` merging keyword and vector rankings by reciprocal rank fusion, a retrieval helper for agents (`ai.Embeddings`, its `Tool`), capability checks as in S1 |
| I1 | Internationalization | Full i18n before the public release (added 2026-10-03; design §10.6, §14.5, D186–D198), in three parts. **I1a** times and dates: the database session's zone checked at boot (`DB_ALLOW_LOCAL_TIMEZONE`), `anetos.Date`, `APP_TIMEZONE` (default UTC) as the process zone. **I1b** translations: YAML catalogs in `locales/`, `i18n.T`/`Plural` with fallbacks, the request's locale (URL prefix or subdomain, user, session, cookie, `Accept-Language`), users' display and communication locales, queue jobs and mail carrying the locale, the framework's messages translated, `make:auth` strings in `locales/`, `lang:check`. **I1c** formatting and languages: numbers, currencies, dates and relative times, time zones per user, `i18n.Dir`, `hreflang` links, `anetos lang:add` from `anetos.dev/locales` (`anetos-dev/locales`) |
| G1 | Google Cloud Storage | A storage driver module for GCS (added 2026-10-03 at the user's request: apps on Google Cloud, such as a dashboard over files in GCS, run without S3 interoperability keys); design §14.3, D199 |
| AU1 | Audit log & soft deletes | Package `audit` (opt-in per model, D202): every create, change (field by field), soft delete, restore and permanent delete of a tracked model, and the app's own events, written in the change's transaction from watched writes in `db` (D203); bulk writes as one entry each with every affected key (D204, D205); the actor (user, carried into jobs, or system), the unit of work, the client IP if configured (D206, D207); redaction, retention, anonymization. Soft deletes: unique indexes over live rows and `unique_live` (D208), pruning trashed rows (D209). Added 2026-10-06 at the user's request, before the public release; design §10.7 |
| AD1 | Admin engine, users & roles | The module `anetos.dev/anetos/admin` and `make:admin`, `make:admin:resource` (design §12.3, D211–D214), in two parts: **AD1a** the engine and resources; **AD1b** users and roles. Scope: mounting at a path or host (host routes in the router), resources (tables with search, filters, sorting, pagination; forms from struct tags with validation; actions, bulk actions), users (search, view, disable, verification, sessions and tokens, acting as a user with a banner, logged), roles and grants (rbac) |
| AD2 | Admin dashboard, activity & security | In two parts (design §12.3, D221–D226): **AD2a** the dashboard, activity, failed jobs and scheduled tasks; **AD2b** two-factor sign-in, password confirmation and the IP allowlist. Dashboard widgets (users and sign-ups, queue health, AI usage, recent activity; apps add their own), activity views (filters, a history tab per record, by permission), failed jobs and scheduled tasks, two-factor sign-in (TOTP, recovery codes), password confirmation for dangerous actions, optional IP allowlist |
| AC1 | Account settings | A settings page for signed-in users from `make:auth` (added 2026-10-06 at the user's request): name, password, language and time zone, email address, deleting the account (a switch, off by default); `auth.ChangePassword`; the admin links to it |
| M1 | Identity | Final name, GitHub org, domain, logo/mascot; rename pass |
| M1b | Release plumbing (moved to v0.5) | Tag the modules independently; remove the `replace` directives from `cli`, the drivers, the plugins, `bench` and the examples' published `go.mod` files (a module with `replace` can't be `go install`ed), so `go install …/cli/cmd/anetos@latest` and `anetos new` without `--replace` work; serve the `go-import` meta tag on anetos.dev for `anetos.dev/anetos` and every path under it (the vanity import path, D185), pointing at `github.com/anetos-dev/anetos` |
| M2 | Docs site | Choose the generator, full tutorial, guides for every feature; versioned docs at the public release (v0.5) |
| M3 | Reference example app | A realistic app with tests, used as living documentation |
| M4 | OpenAPI | Moved to v0.4 (2026-10-07, design D243): AP4 |
| M5 | Build & deploy | `anetos build`, generated Dockerfile, guides for VPS/systemd, Docker and common PaaS |
| M6 | Performance | Published benchmarks with their method; CI regression gate |
| M7 | Security | Self-audit against a checklist, SECURITY.md, dependency review, secure-defaults check in `anetos doctor` |
| M8 | API stability pass (moved to v0.5) | Review every exported identifier, add deprecations, CONTRIBUTING, CODE_OF_CONDUCT, issue templates, governance note (done in v0.5 as M8a–c) |
| M9 | Launch (moved to v0.5) | Announcement post, awesome-go submission, community channels |
| M10 | Starter experience | A new app is usable and good-looking in minutes (added 2026-10-06 at the user's request): a starter theme written by `anetos new` (`--css=none` without it), `make:crud` for a model's pages, `make:auth`'s pages in the theme with account links in the header, the page after signing in configurable (`AUTH_HOME_URL`, `auth.DefaultHomeURL`) |

**Exit criteria**

- An example app has full-text search on its content, on PostgreSQL and
  SQLite, and an AI assistant that answers from that content with typed
  tools and hybrid retrieval, streams its answers, and is tested with
  `anetostest.FakeAI`; a configuration its database can't support stops
  at boot with a clear message.
- A new project with `make:crud` and `make:auth` is styled, tested and
  deployable without hand-written markup.
- No known P0/P1 bugs; every public API documented; the example app's tests
  run in CI.

**Exit criteria check (2026-10-07)**

A reader who used only `docs/site`, the examples and `go doc` made a
shop with `anetos new`, `make:auth` and `make:crud`, added search,
switched it to PostgreSQL and MySQL, and built it for production, in
about 75 steps; an audit checked the other criteria against the code.
What they found is fixed: the container guide migrated a throwaway
SQLite database while the server called itself healthy (readiness now
waits for migrations, and a SQLite image migrates when it starts:
D255); error pages didn't use the app's layout (D256); the documented
members-only step broke the generated test, with no user factory to fix
it (D257); vector search wasn't checked at boot (D258); the assistant
example ran only on SQLite with tests that assumed IDs; the search
guide's markup didn't fit the theme; and smaller items (`doctor` passing
the example settings' `example.com`, `aria-invalid` on `make:auth`'s
forms, a favicon, the version string, `anetos help <command>`, boot
errors printing a memory address). Before it, the same day: tables on a
latin1 MariaDB couldn't store non-Latin text (D254), and a gRPC advisory
in the Gemini driver.

| Criterion | Result |
|---|---|
| Search and an AI assistant over an example's content | ✅ `examples/assistant`: full-text and hybrid search, typed tools (`search_articles`, `read_article`), streamed answers (SSE), tests with `anetostest.FakeAI`; on SQLite, PostgreSQL 16 and 17 with pgvector and MariaDB 11.8, and refused at boot, with the reason, on MySQL 8.0 and MariaDB 10.11. `examples/tracker` has full-text search on every database. CI runs both on PostgreSQL with pgvector |
| A configuration the database can't support stops at boot | ✅ search language and ranking, stale search indexes, vector search (D258), each naming the setting, the database and the fix |
| `make:crud` and `make:auth`: styled, tested, deployable, no hand-written markup | ✅ the pages, the 404 and 500 pages and the header use the starter theme; the generated tests pass on SQLite, PostgreSQL 16 and MySQL 8.0; `anetos build`, the systemd unit, `doctor`, `/health/*` and `health:check` work. The Docker image wasn't built here (no Docker daemon); its migrate path is tested through `MIGRATE_ON_RUN`. Search needs markup, which the search guide gives for `make:crud`'s pages |
| No known P0/P1 bugs | ✅ none known; the security audit's accepted items are low or medium, with reasons (`docs/security/checklist.md`) |
| Every public API documented | ✅ `make api-docs` (every exported identifier), every package has a package comment, every setting is in the configuration reference |
| The examples' tests run in CI | ✅ every example module on SQLite (`make test`), and the tracker and the assistant on PostgreSQL, the tracker on MySQL |

Left for later, from the walkthrough: a money (decimal) field type in
`make:crud`, a confirmation before `make:crud`'s delete, binding errors
("must be a number") as sentences with the field's name and shown with
the other errors of the form, and installing from a checkout before the
modules are published (M1b, v0.5).

---

### v0.4 — API stack

Goal: an API-only app is as quick to start as an HTML one, and its API is
described by a spec generated from the code (design D243).

| WP | Work package | Notes |
|---|---|---|
| AP1 | API project | `anetos new --stack=api` (the HTML stack is `--stack=web`, the default): no views, templ, htmx, theme, sessions or CSRF; JSON errors everywhere; `routes/api.go` under `/api/v1`; CORS settings; health check, Dockerfile and deploy files as today; a test that calls the API. `anetos new` learns to compose stacks, which the design kits (v0.5) and front-end stacks (v0.6) plug into |
| AP2 | API accounts | `make:auth` in an API project: register, log in (issues a token, with abilities), log out (revokes it), `/me`, password change, password reset and email verification by email with links to the client app (`AUTH_CLIENT_URL`), two-factor codes at login, token management; throttling and the same security rules as the HTML pages; tests |
| AP3 | JSON CRUD | `make:crud` in an API project (or `--api`): handlers for list (pages, sorting, filters), show, create, update and delete with validation (422), JSON shapes chosen on purpose rather than the model as is (an output struct per resource, so new columns never leak), routes, tests |
| AP4 | OpenAPI | Was M4 (D232, superseded by D243). OpenAPI 3.1 generated from the routes: typed inputs (path, query, body, with their validate rules as schema constraints) and declared responses (a typed responder or a route option, settled before M8), errors as problem details, security schemes (bearer tokens with abilities); `routes:openapi` (or `anetos openapi`) writes the spec, `-check` in CI; optional served spec and docs page (a static page, no CDN). No client code generation. |
| AP5 | Docs & example | Guide "Build an API", an API example app (the tracker's API as its own project, or a new small one), OpenAPI guide and reference; getting started gains the API path |

**Exit criteria**

- `anetos new shop --stack=api`, `make:auth`, `make:crud Product …` give a
  tested API with token auth whose OpenAPI spec validates and documents
  every route, on SQLite, PostgreSQL and MySQL.

**Progress**

| WP | Status |
|---|---|
| AP1 API project | ✅ Done 2026-10-08 (design §12.2, D259–D262): `anetos new --stack=api` (no views, templ, static files, sessions or CSRF; `routes/api.go` under `/api/v1`; a typed welcome handler (`web.H`) answering its own struct; `HTTP_CORS_ORIGINS` in the settings files; tests of the welcome and of a 404's problem details), `anetos new` composed of template layers (`base`, `web`, `api`; the web stack's output unchanged, byte for byte), `web.JSONErrors` (problem details whatever the client accepts, `WantsJSON` true under it; used by `examples/tracker`'s API), the generators in an API project (`routes/api.go`, no `routes/web.go`: `make:handler` typed, `make:middleware` for the API; `make:auth`, `make:crud` refuse until AP2, AP3; `make:admin` refuses), `Recover` answering a middleware's panic as problem details under `JSONErrors`; an independent review's findings fixed (that panic was plain text; `IsAPI` keyed on `views/`, which AP2's emails may add; untyped handlers AP4 couldn't describe); `develVersion` `v0.4.0-dev`, postmark's `Requires` widened to `< v0.5.0`. The generated project's tests pass on SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8; `anetos dev`, `anetos build` and `serve` were run with it |
| AP2 API accounts | ✅ Done 2026-10-08 (design §12.2, D263–D268): `make:auth` in an API project writes JSON endpoints under `/api/v1` that sign in with API tokens: registration and login answering a token (30 days, every ability, named after `device_name`), a two-factor challenge and `/login/two-factor` for users with it on, logout revoking the token, `/me`, email verification and password reset by emails linking to the client app (`AUTH_CLIENT_URL`), `PUT /password` revoking the other tokens, `/tokens` (list, make with abilities and the password, revoke), `/two-factor` (status, start and new recovery codes and turning off with the password, confirm with a code); typed handlers answering output structs; emails from an `html/template` file (no templ); 11 tests through HTTP. Package `auth`: `AttemptCredentials`, `TwoFactorChallenge`, `AttemptTwoFactorChallenge`, `CheckPassword`, `RevokeOtherTokens`, `ClientLink` and `AUTH_CLIENT_URL`; `Require`'s 401 behind `TokenMiddleware` names `Bearer` with sessions too (review47's finding). Left out (D268): social sign-in, changing the email address, deleting the account, preferences. Guide "Add accounts to an API". An independent review (review48) found no high-severity issue; fixed: narrow tokens could manage the account (now 403 without `*`), abilities and token names unchecked, a 429 without `Retry-After`, inaccurate throttling docs |
| AP3 JSON CRUD | ✅ Done 2026-10-08 (design §8.2, §12.2, D269–D274): first, how typed handlers declare their responses (D269): `Route.Status(code)` sets a typed result's status (201 on creating routes), `web.Empty` answers 204, so a handler's signature and its route say what it answers, for AP4; AP2's generated accounts and `examples/notes` moved to it. Then `make:crud` in an API project: the model and migration, `<Model>Response` (an output struct; optional dates `null`), `<Model>Input` with the pages' validate rules, the list as `db.Page[<Model>Response]` (new `db.MapPage`) with `?page=`, `?per_page=` (≤ 100), `?sort=` (whitelisted columns, `-` for descending, the ID as tiebreaker) and exact filters on string, email, int, bool and date fields, show, create (201, `Location`), replace (PUT), delete (204), routes added to `routes/api.go`'s `api` group, a test; no PATCH. Generated tests pass on SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8. The API accounts' tests now stop the clock (a login-throttling test could straddle a minute's window) |
| AP4 OpenAPI | ✅ Done 2026-10-08 (design §12.2, D275–D280): package `web/openapi` writes an OpenAPI 3.1.0 document from the routes' typed handlers: parameters and JSON (or multipart) bodies as `web.H` binds them, with what their `validate` rules say as JSON Schema; results as `encoding/json` writes them, with the route's status; components named after the Go types; errors as problem details (400, 422, the middleware's, `default`); security from the middleware, which now declares itself with `web.Documented` (`auth.Require`: a bearer token; `rbac`, `ratelimit`, `TokenMiddleware`: their statuses; the generated `fullAccess`: the `*` ability). `openapi.ForApp` adds the `openapi` command (`--check`; no database needed) and serves the document; `openapi.Check` in a test keeps the committed `openapi.json` current, which is the CI check. API projects get the config, the file and the test; `anetos new`, `make:auth` and `make:crud` update the file; `examples/tracker` describes its API (its `CreateIssue` and `/api/me` became typed). `RouteInfo` gained `Handler` and `Middleware`. The documents of the tests, the tracker and a generated project validate with `openapi-spec-validator` and Redocly's recommended rules; a generated app's live responses (24 requests: accounts, tokens, CRUD, errors) match its document. An independent review (review51) found no high-severity issue; fixed: validate's empty-value and non-zero `required` semantics, a user type named `Problem`, names under `go test` for package `main`, bodies on GET, durations in parameters, multipart fields, embedded pointers, paths OpenAPI can't tell apart, Windows line ends. No docs page or client generation (D280) |
| AP5 Docs & example | ✅ Done 2026-10-08 (design §12.2, D281–D284): "Tutorial: build an API" in getting started, and `examples/bookmarks`, the JSON API it builds (with the user's choice of a new small example): `anetos new --stack=api`, `make:auth`, `make:crud`, then bookmarks owned by their user, the routes behind a token, abilities per route, an archive action returning `web.Empty`, tests (ownership, abilities) and `openapi.json`; the example is checked against the generators (`TestAPITutorialProject`). Getting started's API path; the OpenAPI reference page (the guide keeps the steps). New `auth.RequireAbilities` (abilities on routes, documented as scopes), which `make:auth`'s API account group now uses in place of a generated `fullAccess`. An independent review (review52), which followed the tutorial step by step, found its tests step incomplete and the archive route ahead of its handler (both fixed), and that the example check missed changes to files the tutorial edits (it now undoes the tutorial's edits and compares whole files); also fixed: `RequireAbilities`' guest 401 names `Bearer` and a user load failure is a 500, the `url` rule's schemes are a `pattern` in the document |

All v0.4 work packages are done, and the exit criterion was checked on
2026-10-08 (see below). **v0.4.0 was tagged on 2026-10-08**: `v0.4.0`,
`cli/v0.4.0`, `admin/v0.4.0`,
`drivers/{sqlite,postgres,mysql,redis,s3,gcppubsub,gcs,anthropic,openai,gemini}/v0.4.0`
and `plugins/postmark/v0.4.0` (the modules still use `replace`
directives until M1b, in v0.5). Next: v0.5.

**Exit criteria check (2026-10-08)**

A reader who used only `docs/site`, the examples and `go doc` made a
shop API with `anetos new --stack=api`, `make:auth` and `make:crud
Product …`, called it as a client would, switched it to PostgreSQL and
MySQL, and validated its document; an audit checked the release against
the code, the docs and v0.3's API (`apidiff`). What they found is fixed:
a JSON body's bad date was "not valid JSON" and only the first wrong
type was reported (D285); a project switched to a database server
without `.env.testing` tested on SQLite (D286); `openapi.Check` passed
with an untyped route under the prefix left out of the document (D287);
browser clients couldn't read `Location` or the rate limits (D288);
the responses handing out tokens could be cached (D289); an optional
date and a `url` rule were wider in the document than the server
(D290); `examples/teams` and `examples/validation` had untyped
handlers; and doc gaps (deploying an API project, the deploy files that
still name SQLite, protecting `make:crud`'s endpoints, the upgrade guide,
the changelog's tense and missing entries).

| Criterion | Result |
|---|---|
| `anetos new shop --stack=api`, `make:auth`, `make:crud Product …` give a tested API | ✅ the generated tests (every account endpoint, the CRUD endpoints, the document) pass; `examples/bookmarks` and `TestAPITutorialProject` check the tutorial's project against the generators |
| with token auth | ✅ register, login with two-factor codes, tokens with abilities (`auth.RequireAbilities`), logout, password reset and verification by links to the client app |
| whose OpenAPI spec validates | ✅ the shop's, `examples/bookmarks`' and `examples/tracker`'s documents pass `openapi-spec-validator` and Redocly's linter (OpenAPI 3.1.0) |
| and documents every route | ✅ `openapi.Check` fails when a route under the prefix is left out (D287); the generated test runs it |
| on SQLite, PostgreSQL and MySQL | ✅ SQLite, PostgreSQL 16 and 17, MySQL 8.0, MariaDB 10.11 and 11.8 |

Left for later: 429s of the login routes and 409s of the two-factor
routes in the document (the handlers answer them, the document doesn't
say); pruning expired tokens; `?page=` past the last int; `doctor`
checks of `AUTH_CLIENT_URL` and CORS; `Vary: Cookie` on API responses;
`expires_at` with nanoseconds; a summary per route (`Route.Summary`);
`make:middleware` mentioning `web.Documented`; validating the documents
in CI; installing from a checkout before the modules are published
(M1b, v0.5).

---

### v0.5 — Design kits & public release

Goal: the developer picks the look (or none) at `anetos new` and can switch
it later; then the public release. **This is the first public release.**

| WP | Work package | Notes |
|---|---|---|
| CI1 | CI in parts | Before the kits, which add a generated project each: CI's tests in parts per Go release, side by side, the required checks keeping their names (design D291) |
| K1 | UI components | `anetos new` writes `views/ui`, a small set of templ components (button, field, card, table, badge, alert, nav, pagination…) in the app; the layout and the generators' pages (`make:auth`, `make:crud`, the tutorial) use them instead of raw class names (design D244), so a kit restyles the generated pages |
| K2 | Kits without a build step | Pico, Bootstrap and Bulma (with Bootstrap's small script for menus): their CSS vendored into `public/`, embedded in the binary, no CDN; each kit's `views/ui` and layout; light and dark where the framework has it |
| K3 | Tailwind | `anetos dev` and `anetos build` run Tailwind's standalone CLI (no Node): pinned version, checked download per platform, cached; `views/ui` with Tailwind classes |
| K4 | Picking and switching | `anetos new --css=anetos\|none\|pico\|bootstrap\|bulma\|tailwind`; `anetos css:use <kit>` swaps the stylesheet and `views/ui` (refusing to overwrite a changed `views/ui` without `--force`). Pages written with their own class names keep them; the docs say so |
| K5 | Docs | Guide per kit in "Style your app", screenshots in the docs |
| M8 | API stability pass | From v0.3: review every exported identifier, add deprecations, CONTRIBUTING, CODE_OF_CONDUCT, issue templates, governance note. In three parts: **M8a** the API guidelines and the API files (`api/*.txt`, checked in CI); **M8b** the review against them and its fixes (renames shimmed with `//go:fix inline` until v0.6, findings agreed with the user first); **M8c** the community files |
| M1b | Release plumbing | From v0.3: tag the modules, remove the `replace` directives from published `go.mod` files, the vanity import paths |
| M2v | Versioned docs | From v0.3 (M2): docs per minor version from v0.5, `main` labeled "unreleased" |
| M9 | Launch | From v0.3: announcement post, awesome-go submission, community channels |

**Exit criteria**

- 3–5 volunteers who haven't seen Anetos before each go from nothing to a
  deployed app with auth **in under 30 minutes** using only the docs, one
  of them with the API stack. We fix whatever slows them down.
- Each kit's generated pages pass the generated tests and look right in
  light and dark (checked in a browser).
- No known P0/P1 bugs; every public API documented.

**Order** (agreed 2026-10-09): CI1, K1–K5, M8, M1b, M2v, the volunteer
test (the exit), M9. M1b is real work: the v0.4.0 tags' `go.mod` files
require `anetos.dev/anetos v0.0.0-…` with `replace` directives, which
builds outside the repository ignore, so the modules can't yet be
installed from the module proxy.

**Progress**

| WP | Status |
|---|---|
| CI1 CI in parts | ✅ Done 2026-10-09 (design D291): `test-part` jobs (core with the repository-wide checks and budgets; cli; the other modules with the examples on PostgreSQL and MySQL) for each Go release; `test (…)` jobs under the required names pass when every part does |
| K1 UI components | ✅ Done 2026-10-09 (design §12.2, D292–D297; D244 accepted): `views/ui` in every web project, about 35 templ components (shell, page structure, forms, buttons, data; typed looks and tones; CSRF, method override and field errors built in), written by `anetos new` from a kit (`templates/kits/common` + `<kit>`: `anetos`, the starter theme, and `none`, plain HTML without classes, which `--css=none` now is); the layout, home and error pages and `make:crud`'s and `make:auth`'s pages call them and carry no classes (a test enforces it); older projects get the kit's `views/ui` from the first generator that needs it; `web.MustURL`; the tutorial (example and pages) and the tracker (with its own components) moved to it. Screenshots of a project made with `new`, `make:crud` and `make:auth` match v0.4.0's pixel for pixel in light and dark, except the error page's detail (now muted) and timestamps; the tracker's differ where its components now use the kit's spacing, and where two of its bugs are fixed |
| K2 Kits without a build step | ✅ Done 2026-10-09 (design §12.2, D298–D300): `anetos new --css=pico\|bootstrap\|bulma` (Pico 2.1.1, Bootstrap 5.3.8, Bulma 1.0.4), each kit's `views/ui` in its framework's markup with the framework's files as released and its license vendored into `public/static/` (`scaffold.KitVersions`, `scripts/update-kits.sh`, a test checking the versions), an `app.css` for the rest, Bootstrap's bundle and a dark-mode script, Bulma's menu script; one layout and the same pages for every kit (a test builds the pages against each kit's `views/ui`); `ui.NavItem`; `view.Assets` gzips text files. Checked in screenshots, light and dark and at a phone's width |
| K3 Tailwind | ✅ Done 2026-10-09 (design §12.2, D301–D303): `anetos new --css=tailwind` (Tailwind CSS 4.3.3): `views/ui` with utility classes, `views/ui/tailwind.css`, and the `app.css` compiled from the kit's components, prebuilt and committed; package `cli/internal/tailwind` downloads the standalone CLI per platform (musl detected) from GitHub releases into the user cache, checked against digests in the source, `ANETOS_TAILWIND` to override; `anetos dev` (offline: warns, keeps app.css), `anetos build` and the new `anetos css:build [--check]` compile it; the Dockerfile caches the download; CI's latest Go `cli` part downloads the real binary and checks the prebuilt CSS and a generated project's. Checked in screenshots, light and dark and at a phone's width |
| K4 Picking and switching | ✅ Done 2026-10-09 (design §12.2, D304–D306): `anetos new --css` takes all six kits (K1–K3); `views/ui/kit.json` records the kit and the digest of each file it wrote (`anetos new`, the generators' `WriteUI`); `anetos css:use <kit> [--force]` switches (or, with the same kit, updates) the kit's files, removes the old kit's, refuses without `--force` when a kit file changed or a file in the way isn't the kit's, keeps and lists the app's own `views/ui` files, adds `nav.menu` to older locale files, prints the Dockerfile's Tailwind cache line; a test switches a project with `make:crud` and `make:auth` through every kit, each building and passing its tests, and back to the starter theme's files; the tutorial and the tracker have records (the tracker's kit files, changed, are refused without `--force`) |
| K5 Docs | ✅ Done 2026-10-10 (design §12.2, D307): a guide per kit (starter theme, none, Pico, Bootstrap, Bulma, Tailwind CSS: what it looks like, changing its colors, its other components, common problems), "Style your app" with a table of the kits and a gallery, the Tailwind section moved to its guide; 24 WebP screenshots (list and form, light and dark; the phone menu of Bootstrap and Bulma) made by `scripts/kit-screenshots/run.sh`; `make docs-check` checks images (exist, alt text, every image shown); the documentation guide says how |
| M8a API rules and files | ✅ Done 2026-10-10 (design §23, D308, D309): `docs/contributing/api-guidelines.md` (names, constructors and options, context and errors, types and interfaces, what to export, doc comments, changing the API with `//go:fix inline` deprecations kept one minor); `internal/cmd/apisnap` writes `api/<module>.txt` for the 13 library modules (about 2,900 lines) and `make api-check`, part of `make check` and CI's core part, fails when they differ from the source |
| M8b-1 Constructing services | ✅ Done 2026-10-10 (design D310): every `X.ForApp(app, …)` is `X.New(app, …)` (17 packages; `openapi.Register`), the constructors from parts renamed (`cache.NewWithStore`, `events.NewBus`…, `session.NewSession`), `ForApp` kept deprecated with `//go:fix inline` until v0.6; a second `New`/`db.Connect` for one app is an error in every package (`encryption.New` returns the same one); the guidelines' first rule is the familiar word; the lifecycle page explains `setup`; the upgrade guide's table. Part of M8b, after the API and vocabulary reviews (`docs/planning/m8b-plan.md`) |
| M8b-2 Settings | ✅ Done 2026-10-10 (design §7, D311): every key starts with its area; `DB_DRIVER`, `DB_NAME`, `DB_USER`, `DB_MIGRATE_ON_START`, `DB_MIGRATE_READINESS`, `DB_SEARCH_*`, `CACHE_DRIVER`, `SESSION_TTL`, `SESSION_MAX_TTL`, `AUTH_REMEMBER_TTL`, `QUEUE_POLL_INTERVAL`, `APP_LOCALE_STRATEGY`, the config fields with them (`db.Config.Driver`/`Name`/`User`…); the former names read until v0.6 through `config`'s new `was` tag, logged once and listed by doctor; doctor warns about `.env` keys of a known area that nothing reads, with a pointer for other frameworks' names; `PORT` and `DATABASE_URL` as fallbacks |
| M8b-3 Logging in | ✅ Done 2026-10-10 (design §15, D312): "log in"/"log out" in the API, the pages, the messages and the docs; `Auth.Login` asks for the two-factor code (`SignIn` deprecated); `LogoutOthers`, `LogoutEverywhere`, `Supports…` feature checks, `WithUser` (was `ActAs`), `ErrNoPendingLogin`, `ErrUserNotFound`, `social.NoAccountError`, `anetostest.SocialLogin`; the admin's "Impersonate"; "two-factor authentication"; the locale keys `login_with`, `login_again`, `login_first` (and the bn, es, fr translations in anetos-dev/locales); old names deprecated until v0.6 |

---

### v0.6 — Frontend

Goal: modern SPA-style frontends without giving up server-side routing.

| WP | Work package | Notes |
|---|---|---|
| V1 | Vite integration | Dev-server proxy, manifest reading, assets embedded in the production binary |
| V2 | Inertia adapter | Server-side protocol: shared props, partial reloads, validation errors, redirects (SSR later) |
| V3 | Starter kits | `anetos new --stack=vue\|react\|svelte`, each with auth UI on v0.4's API pieces, and login by session cookie for single-page apps on the app's own domain |
| V4 | Docs | A guide per stack, plus a migration guide from htmx to an SPA stack |

---

### Backlog (after v0.6, unordered)

OpenAPI client code generation · WebSockets/broadcasting · debug dashboard (Telescope-like) · notifications (mail, SMS, Slack channels) · multi-tenancy · feature
flags · search engine drivers behind `Search` (Meilisearch, Typesense, OpenSearch) · Inertia SSR · read/write DB splitting ·
roles and permissions: scope hierarchies, roles a team defines for itself, `make:auth` with roles ·
teams (a teams module extending auth, managed in the admin) · content review workflow (drafts, reviewers, a review queue in the admin) · soft deletes that cascade to related rows · audit log: tamper evidence (hash chain), database triggers for raw SQL on PostgreSQL, pivot writes ·
AI: MCP server (the app's tools to MCP clients) and client (remote tools for agents), provider failover, images and files as model inputs, speech and transcription, providers' own tools (web search, code execution), more providers (as plugins), Vertex AI, Bedrock and Azure OpenAI authentication for the drivers ·
pub/sub ordering keys (Google) · more drivers (NATS, Kafka, SQS, RabbitMQ, GCS, Azure Blob), mostly as plugins ·
`make:crud`: money (decimal) fields, a confirmation before deleting · binding errors as sentences with the field's name, shown with the form's other errors.

---

## 6. First-party packages

The plugin system has to be good enough for our own use, so any first-party
feature that isn't core ships as a plugin built through the **public** API.
Candidates:

- Mail API drivers (Resend, SES, Mailgun; Postmark is done, in `plugins/postmark`)
- Queue and pub/sub drivers beyond the core pair (SQS, NATS, Kafka)
- Storage drivers (GCS, Azure)
- AI providers beyond the first three (Mistral, Bedrock, Azure OpenAI…),
  as plugins or driver modules
- Later: debug dashboard, admin panel, notifications

If a first-party package needs a private hook, the plugin API is missing
something, and we fix the API rather than add the hook.

## 7. How we work

- **One WP → one issue** (or a small set). PRs reference the WP ID.
- **Definition of Done** for every PR (enforced by the PR template):
  code + tests + docs + CHANGELOG entry, all passing CI. See the
  [documentation guide](../contributing/documentation-guide.md#2-definition-of-done).
- **Write the docs first for public APIs.** Draft the guide page or example
  before implementing a new API. If it's awkward to explain, it's awkward to
  use.
- **Decisions** that change the design get an ADR (`docs/design/adr/`) and are
  reflected in the design doc's decision log.
- **Milestone review** at the end of each version: update this roadmap,
  re-estimate, and move items between versions if needed (bumping versions
  as described in the versioning rules).
- **Dependencies:** adding a third-party dependency to the *core* module needs
  a short justification in the PR. Heavy dependencies go in driver or plugin
  modules.

## 8. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Data layer takes far longer than planned | High | High | Narrow v0.1 scope (relations in v0.1.x); keep the query API behind a small interface; fallback plan: adapter over Bun if we stall badly |
| Scope creep | High | High | Non-goals list; backlog instead of "quick additions"; milestone reviews |
| Single maintainer (bus factor, part-time) | High | High | Docs-first culture, ADRs, clean contribution path from the public release (v0.5) so others can join |
| Go community wariness of frameworks | Medium | High | `net/http` compatibility everywhere, a way out of the framework at any point, honest benchmarks, no magic |
| API churn upsets early users | Medium | Medium | Public release only at v0.5; API stability pass (M8); upgrade guides |
| Third-party dependency risk (templ, drivers) | Medium | Medium | Views behind a renderer interface; heavy dependencies isolated in modules |
| Naming/trademark conflict found late | Low | Medium | Name settled at M1 (D185): no ANETOS mark found in the US or Australia, nothing in software by that name; a professional search (EUIPO, WIPO, national offices) before any trademark filing, where the Greek meaning ("comfortable") may count as descriptive |
| Competing with Goravel for "Laravel-like Go" | Medium | Medium | Different positioning: Go-native, typed, concurrency-first, stdlib-compatible |
| AI provider APIs change fast; three provider drivers to maintain | High | Medium | A thin common contract with an escape hatch to each provider's own client; official SDKs in driver modules; recorded-response conformance tests; more providers as plugins |
| Search and AI work delays the public release | Medium | High | Fixed MVP scope (S1, A1–A3, R1, S2, I1), everything else in the backlog; re-estimate after A1 |
| Search behaves differently per database (stemming, stop words, scores) | High | Low | One API, documented differences, capability checks at boot, tests on the deployed database |

## 9. Open questions

| # | Question | Decide by |
|---|---|---|
| Q1 | ~~Final name, org, domain~~ **Decided 2026-10-03: Anetos** (Greek άνετος, "at ease, comfortable"), GitHub org `anetos-dev`, domain anetos.dev, module `anetos.dev/anetos` (design D185) | Decided |
| Q2 | ~~License: MIT vs Apache-2.0~~ **Decided 2026-09-30: Apache-2.0**, for its explicit patent grant, patent retaliation clause, trademark clarification and contribution terms (design D16) | Decided |
| Q3 | ~~Minimum Go version~~ **Decided 2026-09-30:** the older of the two Go-supported releases, currently **Go 1.26**; CI tests minimum + latest (design D18) | Decided |
| Q4 | ~~Docs site generator (VitePress, Hugo, Starlight…)~~ **Decided: Hugo with the Hextra theme**, hosted on Cloudflare Pages; the pages stay in `docs/site` (design D200) | Decided |
| Q5 | ~~Which mail API driver is first-party first (Resend vs Postmark)~~ **Decided 2026-10-01: Postmark**, for its transactional focus, a stable documented API with error codes that tell permanent from temporary failures, and a test token (`POSTMARK_API_TEST`) that checks requests without sending; Resend can follow as a plugin | Decided |
| Q6 | ~~Public repo from day one, or private until v0.3?~~ **Decided 2026-09-30:** private until ready for public release (v0.3; v0.5 since 2026-10-07, D243). A private repo can use the working codename; GitHub redirects renamed repos, and the module path is a find-and-replace while nobody depends on it | Decided |
| Q7 | ~~Search: the default `SEARCH_LANGUAGE`~~ **Decided 2026-10-02: `simple`**, with every word matched as a prefix; languages are opt-in, and an index built for another language stops the app until `search:reindex` (design D160) | Decided |
| Q8 | ~~Embeddings: a column on the model's table, or a table per model~~ **Decided 2026-10-02: a table per model**, one row per chunk, filled by queue jobs (design D161) | Decided |

## 10. Change log for this document

| Date | Change |
|---|---|
| 2026-09-29 | Initial plan: v0.1–v0.4, Vite/Inertia moved to v0.4, v0.3 is the public MVP |
| 2026-09-30 | Q6 decided: private repo until public release |
| 2026-09-30 | Q2 decided: Apache-2.0 license |
| 2026-09-30 | F1–F4 done; Q3 proposal recorded |
| 2026-09-30 | Q3 decided: minimum Go 1.26 |
| 2026-09-30 | F5 done |
| 2026-09-30 | F6 done; HTML error bags and old input moved to F10, `unique`/`exists` rules to F7 |
| 2026-09-30 | F7 done |
| 2026-09-30 | F8 done; factories moved to F12 |
| 2026-09-30 | F9 done; relation handles moved to v0.1.x |
| 2026-09-30 | F10 done |
| 2026-09-30 | F11 done |
| 2026-09-30 | F12 done; all v0.1 work packages complete; fakes moved to the v0.2 features they fake |
| 2026-09-30 | v0.1 exit criteria checked; pagination links, `migrate --seed --force`, concept pages, benchmarks and doc-comment check added |
| 2026-09-30 | v0.1.0 tagged |
| 2026-09-30 | v0.1.1 (relations) done; N+1 detection moved to v0.2 |
| 2026-09-30 | B1 (cache) done; database store added to its scope |
| 2026-09-30 | B2 (sessions and rate limiting) done |
| 2026-09-30 | B3 (authentication library) done; `make:auth` and emailed links split into B14, after mail (B9) |
| 2026-10-01 | B4 (social login) done |
| 2026-10-01 | B5 (queue) done; memory driver added to its scope |
| 2026-10-01 | B6 (events) done; function jobs added to the queue |
| 2026-10-01 | B7 (pub/sub listeners) done; ordering keys deferred to the backlog |
| 2026-10-01 | B8 (scheduler) done |
| 2026-10-01 | B9 (mail) done; Q5 decided (Postmark); the Postmark driver ships as a driver module until the plugin system (B11) |
| 2026-10-01 | B10 (storage) done |
| 2026-10-01 | B11 (plugin system) done; Postmark moved from `drivers/postmark` to `plugins/postmark`, the first first-party plugin |
| 2026-10-01 | B12 (test fakes) done; pub/sub recording and a fake added to its scope; the app clock (`anetos.Now`) |
| 2026-10-02 | B13 (N+1 detection) done; jobs, listeners, messages and tasks tracked too, through units of work |
| 2026-10-02 | B14 (auth scaffolding) done; social login and policies left out of the scaffolding (library and examples cover them) |
| 2026-10-02 | v0.2 exit criteria checked; social login added to `make:auth`, `anetostest.FakeSocial`, `anetos.Logger`, `examples/saas` with a role-splitting test; v0.2.0 tagged; basic i18n (stretch) to the backlog |
| 2026-10-02 | Search and AI added to v0.3, before the public release: S1 (database capabilities, full-text search), A1–A3 (AI core, Anthropic/OpenAI/Gemini providers, app integration), S2 (vectors, hybrid search); exit criterion and risks added; AI extras and search engines in the backlog |
| 2026-10-02 | S1 (database capabilities, full-text search) done; Q7 and Q8 decided |
| 2026-10-02 | A1 (AI core) done; `ai.ForApp` and the `AI_*` settings moved into A1 from A2, and a unit of work per tool call and a log line per request done in A1 (removed from A3); scope of A2, A3 and S2 unchanged |
| 2026-10-02 | A2 (AI providers) done; `ai.Reasoning` and `Schema.Map` added to the core for it; the conformance cassettes are hand-written from the APIs' formats until they're recorded with keys; Vertex AI, Bedrock and Azure OpenAI authentication to the backlog |
| 2026-10-02 | R1 (roles and permissions) added to v0.3 after A2 and before A3, at the user's request: multi-user apps need it from the first public release |
| 2026-10-02 | R1 (roles and permissions) done; scope hierarchies, team-defined roles and `make:auth` with roles to the backlog |
| 2026-10-02 | A3 (AI in the app) done; `ai.Queue` became queued replies on stored conversations (`QueueReply`); files as model inputs to the backlog |
| 2026-10-03 | I1 (internationalization) added to v0.3, before the public release, from the backlog |
| 2026-10-03 | I1 designed and split into I1a (times and dates), I1b (translations), I1c (formatting and languages); I1a done |
| 2026-10-03 | I1b (translations) done; the order of the request's locale per `LOCALE_URL` strategy (a choice made on the device before the user's preference), `?locale=` switches on pages |
| 2026-10-03 | I1c (formatting and languages) done, so I1 is done; `anetos add lang` became `anetos lang:add` (`add lang` kept as an alias); translations for bn, es and fr in a new module `anetos.dev/locales`, its repository to be created under `anetos-dev` |
| 2026-10-03 | G1 (Google Cloud Storage driver) added to v0.3 and done, at the user's request |
| 2026-10-03 | Q1 decided: Anetos (`anetos-dev`, anetos.dev); M1 rename done, the logo/mascot still open; the `go-import` meta tag on anetos.dev added to M1b |
| 2026-10-02 | S2 (vectors, hybrid search) done; `q.SearchSimilar` and `Search(…).Hybrid(…)` became `q.Similar` and `q.Hybrid` (a hybrid search needs the vector and its model); switching embedding models re-embeds in place (D161) |
| 2026-10-06 | M1 logo done; M2 docs site and anetos.dev built and live; Q4 decided (Hugo, Hextra) |
| 2026-10-06 | AU1 (audit log, soft deletes), AD1 and AD2 (admin interface) added to v0.3 before M4, at the user's request; teams, content review and soft-delete cascades to the backlog |
| 2026-10-06 | AD1a (admin engine and resources) done; the form struct's function is `Apply` (D213 said `Save`); `make:admin` sets up roles when the app doesn't |
| 2026-10-06 | AD1b (users and roles) done, so AD1 is done |
| 2026-10-06 | AD2a (dashboard, activity, jobs and scheduled tasks) done |
| 2026-10-06 | AD2b (two-factor sign-in, password confirmation, the admin's protections) done, so AD2 is done |
| 2026-10-06 | AC1 (account settings) added and done, at the user's request |
| 2026-10-06 | M5 (build and deploy) done; M4 (OpenAPI) moved to the backlog, after the public release; the order of the rest: M2 tutorial with M3, M7, M6, M8, M1b, M9 |
| 2026-10-06 | M2's tutorial and M3 (reference app, an issue tracker chosen by the user) done |
| 2026-10-06 | M10 (starter experience: theme, `make:crud`, account links, configurable home page) added and done; V3 gains the CSS frameworks; M2's docs navigation grouped |
| 2026-10-07 | Release plan changed with the user (design D243): v0.4 the API stack (OpenAPI back from the backlog as AP4), v0.5 design kits and the public release (M1b, M8, M9, versioned docs and the volunteer test moved there), v0.6 the front-end stacks |
| 2026-10-07 | M7 (security) done: review, fixes, `SECURITY.md`, `doctor`; next M6 |
| 2026-10-07 | M6 (performance) done: benchmarks, optimizations, budgets and the pull-request gate |
| 2026-10-07 | v0.3 exit criteria checked (a docs-only walkthrough and an audit; gaps fixed, D254–D258); v0.3.0 tagged; next v0.4 |
| 2026-10-08 | v0.4 started: AP1 (API project) done (design D259–D262) |
| 2026-10-08 | AP2 (API accounts) done (design D263–D268) |
| 2026-10-08 | AP3 (JSON CRUD) done, with typed results' status (design D269–D274) |
| 2026-10-08 | AP4 (OpenAPI) done (design D275–D280) |
| 2026-10-08 | AP5 (API docs and example) done (design D281–D284) |
| 2026-10-08 | v0.4 exit criteria checked (a docs-only walkthrough and an audit; gaps fixed, D285–D290); v0.4.0 tagged; next v0.5 |
| 2026-10-09 | v0.5 started: the order agreed; CI1 (CI in parts, D291) done |
| 2026-10-09 | K1 (UI components) done (design D292–D297) |
| 2026-10-09 | K2–K4 (kits, switching) done (design D298–D306) |
| 2026-10-10 | K5 (kit docs) done (design D307) |
| 2026-10-10 | M8 split into M8a–c; M8a (API guidelines, API files) done (design D308, D309) |
| 2026-10-10 | M8b in six parts after the API and vocabulary reviews (`m8b-plan.md`); M8b-1 (`ForApp` → `New`) done (design D310) |
| 2026-10-10 | M8b-2 (settings) done (design D311) |
| 2026-10-10 | M8b-3 (logging in) done (design D312) |

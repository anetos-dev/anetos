# Anetos — Planning & Roadmap

> **Name.** *Anetos*, from Greek άνετος ("at ease, comfortable"), said
> AH-neh-tos. Module `anetos.dev/anetos`, GitHub org `anetos-dev`, site
> anetos.dev, command `anetos` (chosen at [M1](#v03--public-mvp), design D185).

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
  guide).
- We stay below `v2` for as long as possible. In Go, `v2+` requires a `/v2`
  import path, which is disruptive for users.
- Driver modules are versioned and tagged independently, for example
  `drivers/redis/v0.2.0`.

### Overview

| Version | Theme | Audience | Headline outcome |
|---|---|---|---|
| **v0.1** | Foundation | Us | Build a CRUD app with forms, validation, a DB and migrations using only the docs |
| **v0.2** | Batteries | Us + early testers | Auth, social login, queues, events, pub/sub listeners, scheduler, mail, storage, plugins |
| **v0.3** | Search, AI & Public MVP | **Public release** | Full-text search, AI-capable apps (typed LLM calls, tools, streaming) and roles and permissions; anyone can go from zero to a deployed web app with auth |
| **v0.4** | Frontend | Public | Vite + Inertia starter kits (Vue, React, Svelte) |
| **v1.0** | Stable | Public | API stability promise |

Estimated effort, part-time, with Claude Code assisting: v0.1 **6–10 weeks**,
v0.2 **6–10 weeks**, v0.3 **4–6 weeks** (plus **4–6 weeks** for the search
and AI work packages added on 2026-10-02), v0.4 **4–6 weeks**. These are rough;
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

### v0.3 — Search, AI & Public MVP

Goal: good enough for strangers to build and deploy real apps, including
apps with search and AI features. **This is the first public release.**

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
| M1 Identity | 🟡 Partly done 2026-10-03: name, org, domain and rename (Anetos, from Greek άνετος, "at ease"; GitHub org `anetos-dev`; anetos.dev; module `anetos.dev/anetos`, a vanity import path; command `anetos`, package `anetostest`, `ANETOS_*` variables). Logo ✅: a mark drawn from the founder's sketch with the name in Varela Round, in `anetos-dev/website`'s `brand/` and the READMEs |
| M2 Docs site | 🟡 Partly done 2026-10-06 (design D200, D201): docs.anetos.dev built with Hugo and Hextra by `anetos-dev/docs` from `docs/site` (search, diagrams, edit links, relative links resolved), and anetos.dev with the go-import pages (M1b's vanity path), both live on Cloudflare Pages (www.anetos.dev redirects to anetos.dev; `go-import` checked for `anetos.dev/anetos`, a nested module and `anetos.dev/locales`); the framework's CI triggers a docs rebuild through a deploy hook when `docs/site` changes. Left: versioned docs at the v0.3 tag, the full tutorial |

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
| M1 | Identity | Final name, GitHub org, domain, logo/mascot; rename pass |
| M1b | Release plumbing | Tag the modules independently; remove the `replace` directives from `cli`, the drivers, the plugins, `bench` and the examples' published `go.mod` files (a module with `replace` can't be `go install`ed), so `go install …/cli/cmd/anetos@latest` and `anetos new` without `--replace` work; serve the `go-import` meta tag on anetos.dev for `anetos.dev/anetos` and every path under it (the vanity import path, D185), pointing at `github.com/anetos-dev/anetos` |
| M2 | Docs site | Choose the generator, publish versioned docs, full tutorial, guides for every feature |
| M3 | Reference example app | A realistic app with tests, used as living documentation |
| M4 | OpenAPI | Spec generated from typed handlers; optional docs UI |
| M5 | Build & deploy | `anetos build`, generated Dockerfile, guides for VPS/systemd, Docker and common PaaS |
| M6 | Performance | Published benchmarks with their method; CI regression gate |
| M7 | Security | Self-audit against a checklist, SECURITY.md, dependency review, secure-defaults check in `anetos doctor` |
| M8 | API stability pass | Review every exported identifier, add deprecations, CONTRIBUTING, CODE_OF_CONDUCT, issue templates, governance note |
| M9 | Launch | Announcement post, awesome-go submission, community channels |

**Exit criteria**

- An example app has full-text search on its content, on PostgreSQL and
  SQLite, and an AI assistant that answers from that content with typed
  tools and hybrid retrieval, streams its answers, and is tested with
  `anetostest.FakeAI`; a configuration its database can't support stops
  at boot with a clear message.
- 3–5 volunteers who haven't seen Anetos before each go from nothing to a
  deployed CRUD app with auth **in under 30 minutes** using only the docs. We
  fix whatever slows them down.
- No known P0/P1 bugs; every public API documented; the example app's tests
  run in CI.

---

### v0.4 — Frontend

Goal: modern SPA-style frontends without giving up server-side routing.

| WP | Work package | Notes |
|---|---|---|
| V1 | Vite integration | Dev-server proxy, manifest reading, assets embedded in the production binary |
| V2 | Inertia adapter | Server-side protocol: shared props, partial reloads, validation errors, redirects (SSR later) |
| V3 | Starter kits | `anetos new --stack=htmx\|vue\|react\|svelte\|api`, each with auth UI |
| V4 | Docs | A guide per stack, plus a migration guide from htmx to an SPA stack |

---

### Backlog (after v0.4, unordered)

WebSockets/broadcasting · debug dashboard (Telescope-like) · admin panel
generator · notifications (mail, SMS, Slack channels) · multi-tenancy · feature
flags · search engine drivers behind `Search` (Meilisearch, Typesense, OpenSearch) · Inertia SSR · read/write DB splitting ·
roles and permissions: scope hierarchies, roles a team defines for itself, `make:auth` with roles ·
AI: MCP server (the app's tools to MCP clients) and client (remote tools for agents), provider failover, images and files as model inputs, speech and transcription, providers' own tools (web search, code execution), more providers (as plugins), Vertex AI, Bedrock and Azure OpenAI authentication for the drivers ·
pub/sub ordering keys (Google) · more drivers (NATS, Kafka, SQS, RabbitMQ, GCS, Azure Blob), mostly as plugins.

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
| Single maintainer (bus factor, part-time) | High | High | Docs-first culture, ADRs, clean contribution path from v0.3 so others can join |
| Go community wariness of frameworks | Medium | High | `net/http` compatibility everywhere, a way out of the framework at any point, honest benchmarks, no magic |
| API churn upsets early users | Medium | Medium | Public release only at v0.3; API stability pass (M8); upgrade guides |
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
| Q6 | ~~Public repo from day one, or private until v0.3?~~ **Decided 2026-09-30:** private until ready for public release (v0.3). A private repo can use the working codename; GitHub redirects renamed repos, and the module path is a find-and-replace while nobody depends on it | Decided |
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

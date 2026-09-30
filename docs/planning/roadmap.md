# Anetos — Planning & Roadmap

> **Working codename.** "Anetos" is a placeholder. The final name, GitHub org and
> domain must be settled before the v0.3 public release (see [M1](#v03--public-mvp)).
> Renaming is a repository-wide find-and-replace of `anetos` / `Anetos` / `anetos-dev`.

| | |
|---|---|
| **Status** | Pre-alpha: planning and design |
| **Owner** | Samiul Hoque |
| **Last updated** | 2026-09-29 |
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
| **v0.3** | Public MVP | **Public release** | Anyone can go from zero to a deployed web app with auth |
| **v0.4** | Frontend | Public | Vite + Inertia starter kits (Vue, React, Svelte) |
| **v1.0** | Stable | Public | API stability promise |

Estimated effort, part-time, with Claude Code assisting: v0.1 **6–10 weeks**,
v0.2 **6–10 weeks**, v0.3 **4–6 weeks**, v0.4 **4–6 weeks**. These are rough;
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
| F9 | Model code generation | Typed column references and relation helpers generated from model structs |
| F10 | Views, sessions & forms | templ integration, layouts, view helpers (route URLs, CSRF field, errors, old input, assets), cookie sessions, flash messages, CSRF, bundled htmx; validation failures on HTML forms redirect back with the error bag and old input |
| F11 | CLI | Global `anetos new`, `anetos dev` (watch, rebuild, restart, browser reload), `make:handler`, `make:model`, `make:migration`, `make:middleware`; app-binary command framework (`serve`, `run`, `migrate*`, `routes:list`, custom commands) |
| F12 | Testing helpers | App bootstrap for tests, fluent HTTP test client, per-test DB transaction rollback, model factories (`factory.New[T]`) |

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
| F9–F12 | Not started (next: F9 model code generation) |

**Planned patch:** **v0.1.x** adds **relations and eager loading** (has-one,
has-many, belongs-to, many-to-many, `With(...)`). This is kept out of v0.1.0 so
the core query builder settles first, but it **must ship before v0.2 starts**.
Pop's weak relations were one of the main reasons for this project.

**Exit criteria**

- A "blog" (posts with create, edit, delete, validation, flash messages and
  pagination) can be built by following only the docs, on SQLite and Postgres.
- CI is green with `-race` against SQLite, Postgres and MySQL.
- A baseline benchmark against plain `net/http` is recorded (hello world,
  typed JSON handler, single-row DB read).
- Every exported identifier has a doc comment, and every component has a
  concept page.

---

### v0.2 — Batteries

Goal: everything a real application needs beyond CRUD.

| WP | Work package | Notes |
|---|---|---|
| B1 | Cache | Contract + memory and Redis drivers; locks (used by the scheduler) |
| B2 | Sessions & rate limiting | DB and Redis session drivers; rate limiter middleware |
| B3 | Authentication & authorization | Passwords (argon2id), login, logout, remember-me, email verification, password reset, API tokens, typed policies; `make:auth` scaffolding |
| B4 | Social login | OAuth2/OIDC: Google, GitHub, generic OIDC |
| B5 | Queue | Typed jobs, dispatch, delay, retries with backoff, timeouts, failed-jobs store and retry command; drivers: sync, database, Redis |
| B6 | Events | In-process typed events: sync, async (bounded pool), queued (durable) |
| B7 | Pub/sub listeners | `app.Listen` abstraction, typed decode, concurrency, ack/nack, retries, DLQ where supported; drivers: Redis Streams, Google Pub/Sub |
| B8 | Scheduler | Cron and fluent schedules, overlap prevention, single-instance execution through cache locks |
| B9 | Mail | Mailables with templ templates, SMTP and log drivers, plus one API driver shipped as a first-party plugin (Resend or Postmark) |
| B10 | Storage | Contract + local driver, S3-compatible driver module |
| B11 | Plugin system (public) | Stable `Plugin` interface, `anetos add` / `anetos remove`, namespacing, compatibility checks; at least one first-party plugin built **only** through the public API |
| B12 | Test fakes | Mail, queue, events, storage and clock fakes with assertions |

**Stretch:** basic i18n for validation and auth messages.

**Exit criteria**

- An example app has sign-up and login (password and Google), sends a welcome
  email from a queued job, consumes a pub/sub topic, and runs a scheduled task,
  **all from one binary**, with `--only=` role splitting shown working.
- A first-party plugin installs with one command and adds routes, a migration,
  config, a command and a worker.

---

### v0.3 — Public MVP

Goal: good enough for strangers to build and deploy real apps. **This is the
first public release.**

| WP | Work package | Notes |
|---|---|---|
| M1 | Identity | Final name, GitHub org, domain, logo/mascot; rename pass |
| M2 | Docs site | Choose the generator, publish versioned docs, full tutorial, guides for every feature |
| M3 | Reference example app | A realistic app with tests, used as living documentation |
| M4 | OpenAPI | Spec generated from typed handlers; optional docs UI |
| M5 | Build & deploy | `anetos build`, generated Dockerfile, guides for VPS/systemd, Docker and common PaaS |
| M6 | Performance | Published benchmarks with their method; CI regression gate |
| M7 | Security | Self-audit against a checklist, SECURITY.md, dependency review, secure-defaults check in `anetos doctor` |
| M8 | API stability pass | Review every exported identifier, add deprecations, CONTRIBUTING, CODE_OF_CONDUCT, issue templates, governance note |
| M9 | Launch | Announcement post, awesome-go submission, community channels |

**Exit criteria**

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
flags · full i18n · search adapters · Inertia SSR · read/write DB splitting ·
more drivers (NATS, Kafka, SQS, RabbitMQ, GCS, Azure Blob), mostly as plugins.

---

## 6. First-party packages

The plugin system has to be good enough for our own use, so any first-party
feature that isn't core ships as a plugin built through the **public** API.
Candidates:

- Mail API drivers (Resend, Postmark, SES, Mailgun)
- Queue and pub/sub drivers beyond the core pair (SQS, NATS, Kafka)
- Storage drivers (GCS, Azure)
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
| Naming/trademark conflict found late | Low | Medium | Settle name at M1 before any public promotion |
| Competing with Goravel for "Laravel-like Go" | Medium | Medium | Different positioning: Go-native, typed, concurrency-first, stdlib-compatible |

## 9. Open questions

| # | Question | Decide by |
|---|---|---|
| Q1 | Final name, org, domain | Before v0.3 (M1) |
| Q2 | ~~License: MIT vs Apache-2.0~~ **Decided 2026-09-30: Apache-2.0**, for its explicit patent grant, patent retaliation clause, trademark clarification and contribution terms (design D16) | Decided |
| Q3 | ~~Minimum Go version~~ **Decided 2026-09-30:** the older of the two Go-supported releases, currently **Go 1.26**; CI tests minimum + latest (design D18) | Decided |
| Q4 | Docs site generator (VitePress, Hugo, Starlight…) | At M2 |
| Q5 | Which mail API driver is first-party first (Resend vs Postmark) | At B9 |
| Q6 | ~~Public repo from day one, or private until v0.3?~~ **Decided 2026-09-30:** private until ready for public release (v0.3). A private repo can use the working codename; GitHub redirects renamed repos, and the module path is a find-and-replace while nobody depends on it | Decided |

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

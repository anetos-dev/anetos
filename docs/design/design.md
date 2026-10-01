# Anetos — Design Document

> **Working codename.** "Anetos", `anetos` and `anetos.dev/anetos` are
> placeholders until the final name is chosen (roadmap Q1).
>
> **Code in this document is illustrative.** It shows the intended developer
> experience, not final signatures. The source of truth for APIs is the code
> and its godoc; this document records *intent and decisions*.

| | |
|---|---|
| **Status** | Draft v0.1 (design phase) |
| **Owner** | Samiul Hoque |
| **Last updated** | 2026-09-30 |
| **Related** | [Roadmap](../planning/roadmap.md) · [Documentation guide](../contributing/documentation-guide.md) · [ADRs](adr/) |

## Contents

1. [Summary](#1-summary)
2. [Design principles](#2-design-principles)
3. [Architecture overview](#3-architecture-overview)
4. [Application lifecycle](#4-application-lifecycle)
5. [Repository & module layout](#5-repository--module-layout)
6. [Generated application layout](#6-generated-application-layout)
7. [Configuration](#7-configuration)
8. [HTTP layer](#8-http-layer)
9. [Validation](#9-validation)
10. [Data layer](#10-data-layer)
11. [Migrations, seeders & factories](#11-migrations-seeders--factories)
12. [Views & frontend](#12-views--frontend)
13. [Runtime & concurrency](#13-runtime--concurrency)
14. [Services, contracts & drivers](#14-services-contracts--drivers)
15. [Authentication & authorization](#15-authentication--authorization)
16. [Plugin system](#16-plugin-system)
17. [CLI](#17-cli)
18. [Testing](#18-testing)
19. [Observability](#19-observability)
20. [Security defaults](#20-security-defaults)
21. [Build & deployment](#21-build--deployment)
22. [Performance strategy](#22-performance-strategy)
23. [Compatibility & versioning](#23-compatibility--versioning)
24. [Decision log](#24-decision-log)
25. [Open questions](#25-open-questions)

---

## 1. Summary

Anetos is a batteries-included Go web framework. It aims for the developer
experience Laravel is known for (scaffolding, a capable data layer, swappable
drivers, queues, auth, an ecosystem of packages) while staying idiomatic Go:
static types, explicit wiring, the standard `net/http`, and code generation in
place of runtime reflection.

Its defining feature is a **supervised runtime**. One application binary runs
HTTP, queue workers, pub/sub listeners, event handlers and the scheduler as
goroutines under one supervisor. They share configuration and resources and
shut down together. The same binary can run a single role (`--only=http`)
when you scale out.

### Lessons carried over from Buffalo

These come from building production apps with Buffalo. Each maps to a design
response:

| Buffalo pain point | Anetos response | Section |
|---|---|---|
| Pop/Fizz too primitive; raw SQL often needed | Relations, eager loading, scopes and soft deletes in the data layer, with raw SQL as a pleasant escape hatch | §10 |
| Plush templates | templ: compiled, type-checked, IDE support | §12 |
| Features outside the core (e.g. Google login) were a lot of work | Social login built in; plugin system for everything else | §15, §16 |
| Goroutines and channels not used at the core; pub/sub needed a separate app | Supervised runtime with workers, listeners, events and scheduler | §13 |
| Vue integration was difficult | Vite + Inertia starter kits (v0.4) | §12 |

---

## 2. Design principles

Principles settle arguments. When two good options conflict, the higher
principle wins.

1. **Standard library first.** Every handler is ultimately an `http.Handler`.
   Every middleware is `func(http.Handler) http.Handler`. Any Go middleware
   works in Anetos, and any Anetos handler works outside it.
2. **Explicit over magic.** No facades, no package-level mutable state, no
   hidden global container. Dependencies arrive through constructors, the
   `App`, or the request `Ctx`. If you can't *go to definition* on something,
   it's too magic.
3. **Let the types do the work.** Typed handlers, typed jobs, typed events and
   typed config. Binding, validation and API docs come from types, not
   strings.
4. **Code generation over runtime reflection.** Reflection that inspects
   types (walking fields, parsing tags) is allowed at startup and
   registration time and must never run per request or per message; per
   request, only precomputed plans run. Generated code is plain, readable
   Go, committed to the repo.
5. **Batteries included, all swappable.** Every service is an interface
   (contract) with drivers selected by config. Defaults work with zero setup.
6. **Small core, modular drivers.** Heavy dependencies (cloud SDKs, broker
   clients, DB drivers) live in separate Go modules so you only pull what you
   use.
7. **Concurrency is a first-class citizen.** Background work is a normal part
   of an application, not a separate service.
8. **One binary.** Views, assets, migrations and all roles in a single
   deployable file.
9. **Escape hatches everywhere.** Raw SQL, plain handlers, custom drivers and
   plain Go. The framework never blocks you from dropping down a level.
10. **Documentation is part of the feature.** An API that's hard to document
    is hard to use; redesign it.

---

## 3. Architecture overview

```mermaid
flowchart TB
    subgraph Binary["Application binary"]
        CLI["App commands<br/>serve · run · work · listen · schedule · migrate · custom"]
        subgraph Kernel["App kernel"]
            Cfg["Config"]
            Ctr["Service container"]
            Plg["Plugins / providers"]
        end
        subgraph Runtime["Runtime supervisor"]
            HTTP["HTTP server"]
            WRK["Queue workers"]
            LSN["Pub/sub listeners"]
            SCH["Scheduler"]
            BG["Supervised goroutines<br/>(app.Go)"]
        end
        subgraph Services["Services (contracts)"]
            DB["DB"]; CA["Cache"]; QU["Queue"]; PS["PubSub"]
            ML["Mail"]; ST["Storage"]; SE["Session"]; EV["Events"]; AU["Auth"]
        end
    end
    Drivers["Driver modules<br/>postgres · mysql · sqlite · redis · gcp-pubsub · s3 · smtp · …"]
    CLI --> Kernel --> Runtime
    Runtime --> Services --> Drivers
```

**Layers, from the bottom up:**

- **Drivers** implement contracts. Heavy ones are separate modules.
- **Services** are the contracts the app uses (`db`, `cache`, `queue`,
  `pubsub`, `mailer`, `storage`, `session`, `events`, `auth`).
- **Kernel** loads config, builds the service container and registers
  plugins.
- **Runtime** supervises long-running *components* (HTTP, workers, listeners,
  scheduler, ad-hoc goroutines).
- **App commands** are the binary's CLI: choose which components to run, or
  run one-off tasks (migrate, seed, custom commands).

The **global `anetos` CLI** sits outside the binary. It creates projects,
generates code, runs the dev loop and builds releases.

---

## 4. Application lifecycle

```
New → Register → Boot → Run → Shutdown
```

| Phase | What happens | Who participates |
|---|---|---|
| **New** | Load `.env` and typed config; create the logger | Kernel |
| **Register** | Plugins and providers bind services and default config. No I/O. | Framework providers, plugins, app |
| **Boot** | Open connections, register routes, workers, listeners, schedules and commands; validate everything (fail fast) | Same |
| **Run** | Supervisor starts the selected components | Runtime |
| **Shutdown** | On SIGINT/SIGTERM or fatal error, stop components stage by stage (§13.3), then run shutdown hooks in reverse registration order | Runtime |

Every provider's Register runs before any provider's Boot, so Boot may use
services from providers added later. If Register or Boot fails, the shutdown
hooks registered so far run before the error is returned. *(Implemented in
v0.1: F2. `Provider`, `App`, `Provide`/`Resolve` live in the root package
`anetos`.)*

A generated app's `main.go` stays short and readable. Wiring lives in
ordinary Go files the developer owns:

```go
// main.go (illustrative)
func main() {
    app, err := anetos.New()           // loads config, validates, builds logger
    if err != nil {
        log.Fatal(err)
    }

    app.Use(plugins.All()...)          // generated by `anetos add`
    srv, err := web.NewServer(app)     // HTTP component (roles: http)
    if err != nil {
        log.Fatal(err)
    }
    routes.Register(srv.Router())      // routes/web.go, routes/api.go
    q, _ := queue.ForApp(app)          // QUEUE_DRIVER (B5)
    queue.Register[jobs.SendWelcome](q)
    q.Work(queue.Queues("emails"), queue.Concurrency(10))
    app.Listen(pubsub.Topic("orders.created"), listeners.OrderCreated,
        anetos.Concurrency(20), anetos.Retry(5))
    app.Schedule(schedule.DailyAt("02:00"), tasks.PruneSessions)

    app.Execute() // parses os.Args: run (default) | serve | migrate | routes:list | … (F11)
}
```

F11 implemented `app.Execute` and the generated `main.go` (with a
`setup` function the project's test reuses), and B5 the queue's workers
(§13.4); `app.Use(plugins.All()...)`, listeners and schedules arrive
later in v0.2.

---

## 5. Repository & module layout

A multi-module monorepo. The **core module** has few dependencies; drivers and
first-party plugins are **separate modules** with their own `go.mod`, tagged
independently (e.g. `drivers/redis/v0.2.0`).

```
anetos.dev/anetos/            ← core module
├── app.go …              kernel (root package `anetos`): App, New, options,
│                        AppConfig, Provider, Provide/Resolve, app.Go
├── config/              .env loading, typed config
├── supervisor/          runtime supervisor, components, roles, app.Go
├── web/                 router, Ctx, handler forms, binding, responses, middleware, server
├── validate/            rules, messages, error bags
├── db/                  query builder, model runtime, dialects, tx, raw SQL
│   ├── dbtest/          conformance suite every driver module runs
│   ├── factory/         model factories for tests and seeders (F12)
│   └── migrate/         schema builder, runner (F8)
├── view/                Component interface, helpers, assets (F10); view/htmx: bundled htmx
├── session/             encrypted cookie sessions, flash, CSRF token (F10)
├── encryption/          AES-256-GCM with APP_KEY and key rotation (F10)
├── cache/               cache, memory and database stores, locks (B1); cache/cachetest: store conformance suite
├── auth/                login, remember me, API tokens, reset/verification tokens, policies (B3); auth/password: argon2id; auth/social: OAuth/OIDC sign-in (B4)
├── queue/               jobs, workers, sync/memory/database stores, failed jobs (B5); queue/queuetest: store conformance suite
├── events/              typed in-process events: sync, async (bounded pools), queued listeners (B6)
├── pubsub/ schedule/ mailer/ storage/
├── ext/                 public plugin API (package `ext`)
├── cmd/                 app-binary command types (F11); App.Execute dispatches
├── anetostest/          test app, browser-like client, assertions (F12); fakes arrive with their features
├── internal/            everything not part of the public API
│   ├── convert/         string → typed value conversion (config and web binding)
│   ├── dbutil/          the database server's clock and deadlock retries in SQL (cache, queue)
│   ├── naming/          column and table naming rules (db and `anetos gen`)
│   ├── appkey/          APP_KEY parsing and generation (kernel, encryption, cli)
│   └── cmd/docsnippets/ checks doc code blocks against example regions
├── cli/                 ← separate module: the `anetos` developer tool (cmd/anetos: new, dev, make:*, gen, key:generate)
├── drivers/             ← each a separate module
│   ├── postgres/ mysql/ sqlite/   database/sql driver + DSN; dialects are in db/
│   ├── redis/           shared client (redis.Connect), cache store with locks (B1), sessions (B2), queue (B5); later pubsub (Streams)
│   ├── gcppubsub/
│   ├── s3/
│   └── …
├── plugins/             ← first-party plugins, each a separate module
│   ├── resend/ postmark/ …
├── examples/            compiled examples used by the docs
└── docs/
```

**Package naming.** No framework package shares a name with a standard
library package, so users never need import aliases. That's why the HTTP
package is **`web`** (not `http`), the runtime is **`supervisor`** (not
`runtime`), mail is **`mailer`** (not `net/mail`'s `mail`), encryption is
**`encryption`** (not `crypto`), password hashing is **`auth/password`** (not
`hash`), and the plugin API is **`ext`** (not `plugin`). We don't provide a
`log` package; we use `log/slog`.

**Why the database drivers are modules too:** `pgx`, the MySQL driver and a
SQLite implementation are each sizeable. A Postgres app shouldn't compile or
download MySQL code.

---

## 6. Generated application layout

What `anetos new blog` produces (F11). It's familiar to Laravel developers
without copying Laravel. Directories for later features (jobs, events,
mail, policies, tasks, factories, plugins) are added when those arrive;
seeders live with the migrations (`migrations.Seeders`), and settings
the framework doesn't cover go in typed config structs you add.

```
blog/                        (F11 generates the unmarked lines)
├── main.go                  wiring (see §4)
├── go.mod
├── .env / .env.example
├── main_test.go             a test of the home page
├── routes/                  web.go (api.go when you add an API)
├── app/
│   ├── handlers/            HTTP handlers
│   ├── middleware/          (make:middleware)
│   ├── models/              model structs (+ generated models_gen.go)
│   ├── jobs/ events/ listeners/ mailers/ policies/ tasks/   (v0.2)
├── database/
│   └── migrations/          Go migrations (the All set) and Seeders
├── views/                   templ components: layout, pages (+ generated *_templ.go)
├── public/                  public.Assets; static/ files (embedded) and htmx
├── plugins.go               generated by `anetos add` (v0.2)
└── tmp/                     anetos dev's builds (gitignored)
```

Everything under `app/`, `routes/`, `views/` and `database/` is the
developer's code.
Generated files end in `_gen.go` or carry a `// Code generated … DO NOT EDIT.`
header.

---

## 7. Configuration

- `.env` holds per-environment values; typed Go structs define the shape.
  No stringly-typed `config("app.name")` lookups in application code.
- Loaded and validated at startup: `AppConfig` in **New**, each package's
  own settings (`HTTP_*`, `DB_*`, `SESSION_*`) when the app wires it
  (`web.NewServer`, `db.Connect`, `session.ForApp`), all before `Run`.
  Invalid config stops startup with a
  message listing **every** missing or invalid key.
- Tags: `env:"KEY"` / `env:"KEY,required"`, `default:"…"`, and `prefix:"P_"`
  on nested structs. Rules tags can't express go in a `Validate() error`
  method. A key set to the empty string counts as unset (D20).
- Real environment variables override `.env`. In production, `.env` is
  optional and secrets come from the environment.
- `APP_URL` (B4) is the app's public base URL, for absolute links that
  leave the app (OAuth callbacks, mail); nothing derives it from the
  request's Host (D103).

```go
// illustrative: an app's own settings
type Billing struct {
    StripeKey anetos.Secret `env:"STRIPE_KEY,required"`
    Currency  string        `env:"BILLING_CURRENCY" default:"usd"`
}

func (b Billing) Validate() error {
    if len(b.Currency) != 3 {
        return fmt.Errorf("BILLING_CURRENCY %q must be a 3-letter code", b.Currency)
    }
    return nil
}

billing, err := config.Get[Billing](app.Source())
```

Plugins (B11) will contribute their own config structs the same way,
loaded from namespaced variables (e.g. `STRIPE_KEY`).

---

## 8. HTTP layer

*(Implemented in v0.1: F5, package `web`. User docs: routing and handlers
guides, HTTP request lifecycle concept, binding reference.)*

`web` imports `anetos`; the kernel doesn't import `web` (D25). Worker-only
binaries don't link the HTTP stack, and the router works standalone
(`web.NewRouter`) for tests and libraries.

```go
// main.go (illustrative)
srv, err := web.NewServer(app) // reads HTTP_*, adds the "http" component
routes.Register(srv.Router())
```

### 8.1 Router

- Built **on `net/http.ServeMux`** (Go 1.22+ method and wildcard patterns)
  with a thin layer: groups with prefixes and middleware, `With` for
  per-route middleware, **named routes** with name prefixes (`As`), **URL
  generation** (`r.URL("posts.show", id)`, path-escaped, rejecting dot
  segments), and `Routes()` for `routes:list` (D3, accepted: the layer adds
  about 5 allocations and no measurable latency over raw `ServeMux` in
  benchmarks).
- Patterns ending in `/` are **exact** (`{$}` is appended); inside a group,
  `""` and `"/"` mean the group's own path (D26).
- A catch-all route gives 404, 405 with an `Allow` header (probing every
  method used by registered routes, custom ones included) and automatic
  `OPTIONS` 204.
- Middleware rules: `UseGlobal` wraps everything (before routing, including
  404s) and must be set before serving; `Use` applies to routes registered
  afterwards and panics if the router *or any group derived from it*
  already has routes, so middleware can't silently miss routes.

### 8.2 Handler forms

Three forms, all ending up as `http.Handler`:

| Form | Signature | Use it for |
|---|---|---|
| Plain | `r.HandleStd(method, pattern, http.Handler)` | Existing code, full control |
| Context | `func(c *web.Ctx) error` | Pages and small handlers |
| Typed | `web.H(func(c *web.Ctx, in In) (Out, error))` | Forms and APIs: automatic binding, validation and response |

Go methods can't have type parameters, so typed handlers go through the
generic function `web.H` (D15). The output is written by its `Responder`
method if it has one (`web.Created`, `web.NoContent`, `web.Redirect`,
`web.RedirectRoute`, `web.JSON`, `web.Text`, or your own), otherwise as JSON
with 200; a nil Responder gives 204. Redirect helpers default to 303 See
Other (D27).

```go
// illustrative
func (h *Posts) Store(c *web.Ctx, in StorePost) (web.Responder, error) {
    post, err := h.posts.Create(c, in.Title, in.Body)
    if err != nil {
        return nil, err
    }
    return web.RedirectRoute("posts.show", post.ID), nil
}
```

### 8.3 `web.Ctx`

A per-request value wrapping the response writer and request, with access to
the app, route, logger (annotated with request ID and route name) and
response helpers. It **implements `context.Context`** by delegating to the
request context, so it can be passed straight to the data layer (open
question O1, resolved). Ctx values are **not pooled** (D24): the saving is
small, and pooling would turn a retained Ctx into a data race instead of a
stale value. Sessions, auth user and view helpers join the Ctx in F10 and
v0.2.

### 8.4 Binding

- Sources: JSON body (`json`), forms (`form`, falling back to the JSON name
  for form-representable types), `query`, `header`, `path`, and multipart
  files.
- **Precedence** (D29): body first, then query, header and path, which the
  body can never set: after JSON decoding those fields are cleared, and form
  values are applied before URL and header values. File fields are never
  taken from JSON. Embedded *pointer* structs carrying source tags are
  rejected at startup.
- **Reflection happens once at route registration**: `web.H` builds a bind
  plan (field indexes and converters from `internal/convert`, shared with
  the config binder). Per request, only values are converted and set.
- Friendly form semantics: empty values count as absent for non-strings;
  booleans accept `on`/`off` and `yes`/`no`.
- Failures: 400 with per-field messages, 413 for oversized bodies, 415 for
  unsupported content types, 400 for trailing JSON data. Validation follows
  (§9): tag rules, then a `Validate(ctx) error` method.

### 8.5 Errors

- Handlers return errors: `web.Error(status, msg)`, `*web.HTTPError` with
  `Fields`, or any error implementing `HTTPStatus() int`. Everything else is
  a 500.
- `DefaultErrorHandler` negotiates the format (q-value aware `Accept`, JSON
  body, `X-Requested-With`): RFC 9457 problem JSON with `request_id`, or an
  HTML page. For 5xx, the message and fields are hidden unless debug mode is
  on. The debug page shows the cause chain, the stack for panics, and
  request details with credentials and sensitive query values redacted (D28).
- If an error occurs **after the response started**, it is logged and the
  connection is aborted (`http.ErrAbortHandler`), so a truncated body can't
  look successful.
- Cancellation: 499 and nothing written only if the *client* went away; a
  `context.Canceled` from inside the app is a 500. Deadlines are 503.
- Multipart temp files are removed after every request (net/http only cleans
  the original request, not `WithContext` copies).

### 8.6 Middleware & server

- Standard `func(http.Handler) http.Handler`. Built in (F5): `Recover`,
  `RequestIDs`, `RealIP` (forwarding headers trusted only from
  `HTTP_TRUSTED_PROXIES`), `AccessLog`, `SecureHeaders` (HSTS in
  production), `CORS` (refuses `*` with credentials), `BodyLimit`, `Timeout`.
  F10: `CSRF`, `MethodOverride`, and the session middleware (package
  `session`). Middleware reports errors through the router's error handler
  with `web.WriteError`. B2: `ratelimit.Middleware` (package
  `web/ratelimit`, D94). Later: auth (v0.2), compression, maintenance mode.
- `web.NewServer` adds the server as the `http` component (role `http`,
  `StageIngress`, `StopOnFailure`), plus `/health/live` and `/health/ready`
  (supervisor readiness).
- **Shutdown** (D30): stop accepting, give in-flight requests
  `HTTP_SHUTDOWN_GRACE` (capped at half of `APP_SHUTDOWN_TIMEOUT` so later
  stages keep their time), then cancel request contexts and close
  connections. `srv.Stopping()` lets streaming handlers end immediately.

---

## 9. Validation

Package `validate` (F6). `web.H` runs it automatically; `validate.Struct`
works on any struct (job payloads, CLI input).

- **Tag syntax is Laravel's** (D31): `validate:"required|max:200|in:a,b"`.
  Rules are separated by `|`, parameters follow `:` and are comma-separated.
  Rule names and default messages follow Laravel ("The email field must be a
  valid email address."). A `label` tag changes the field name in messages;
  otherwise it is derived from the request key (`first_name` → "first
  name").
- **Compiled once** (D12): the first use of a type parses its tags into a
  cached `Plan` of compiled checks; nested types without rules are pruned.
  Unknown rules (with hints for Laravel rules that aren't needed, like
  `nullable`), bad parameters, rules on the wrong kind of field, references
  to missing fields, undetectable media types and unknown message keys are
  errors then, so `web.H` panics at registration. Per request only the plan
  runs: precomputed field indexes and closures, keys built only on failure,
  and no allocation for valid input with the built-in rules (maps of
  structs and file rules excepted; enforced by a test).
- **Fields follow `encoding/json`** (D36): embedded structs and pointers to
  structs without a json name are flattened, a shadowed field is ignored,
  and a field inside a nil embedded pointer counts as empty (so `required`
  still fails). Rules that could never apply as written (on a field json
  drops as ambiguous, or two fields reporting under one key) are startup
  errors. Keys are the json name, then `form`/`query`/`path`/`header`,
  then the Go name.
- **Empty fields** (D32): only the `required` family and `accepted` run on
  an empty field (nil pointer, blank string, empty slice or map, zero
  struct or array such as a time or UUID); other rules pass, so optional
  fields need no extra marker. Element-wise rules skip empty elements.
  Numbers and booleans are never empty; pointers express "not sent".
  `required` additionally rejects zero numbers and `false` in non-pointer
  fields.
- **Rule set:** presence (`required`, `required_if`, `required_unless`,
  `required_with`, `required_without`, `accepted`), size (`min`, `max`,
  `size`, `between`, measured by kind), string formats (`email`, `url`,
  `uuid`, `alpha…`, `numeric`, `ip`, `date`, …, applied element-wise to
  `[]string`), choice (`in`, `not_in`, `distinct`), comparison (`same`,
  `different`, `confirmed`, `after`/`before` with `now` or a field) and files
  (`max_size`, `mimetypes` and `image` by content sniffing, `extensions`).
  Safety defaults: `url` allows only http and https unless schemes are
  listed, `ip` rejects zones, `image` excludes SVG. Numbers compare exactly
  (integers never go through float64). The full list is in the
  [rules reference](../site/reference/validation-rules.md).
- **Nesting:** struct fields, slices and string-keyed maps of structs are
  validated recursively with dotted keys (`items.0.name`). Recursive types
  are supported; cyclic data returns an error after 10,000 struct levels
  (`encoding/json`'s limit) instead of overflowing the stack.
- **Custom rules** (D33): `validate.Register(name, message, fn)` from an
  `init` function, like `sql.Register`. A rule gets the field's key, label,
  dereferenced value, parameters and parent struct, plus the request
  context. Returning an error aborts validation (500), distinct from a
  failed check (422).
- **Order in `web.H`** (D34): bind (400 on conversion errors) → tag rules
  (422, handler not called) → `Validate(ctx) error` method, only if the tags
  passed → handler. Checks that need the database or several fields go in
  the method or the handler and report with `validate.Fail(field, msg)` or
  a `*validate.Errors`. Any other error from `Validate` is a 500 (D37), so
  a failing database check never leaks its text as a "validation message".
- **Result:** `*validate.Errors`, one message per field in field order
  (first failing rule wins). It implements `HTTPStatus() int` (422) and
  `FieldErrors() map[string]string`; any error implementing the latter
  (`web.FieldErrorer`) fills the `errors` member of problem JSON and the
  HTML error page, also when wrapped in an `HTTPError` without `Fields`.
  `*validate.Errors` marshals to a JSON object in field order.
- **Messages:** English defaults; a struct can override templates with
  `ValidationMessages() map[string]string` keyed by `key.rule` or `rule`.
  Templates use `{label}`, `{0}`…, `{list}`. Translation arrives with i18n
  (v0.2 stretch).
- **HTML forms** (F10): a browser's form post that fails validation (422,
  or 400 with field errors) on a route with a session is redirected back
  with the errors and **old input** flashed; `view.Errors` and `view.Old`
  render them (Laravel's `$errors` / `old()`). Form posts key errors by
  `form` name where it differs from the json name (D68, resolving O7).

---

## 10. Data layer

The part of the framework that matters most, and the riskiest.
**Target:** Eloquent-level comfort for about 90% of queries, fully typed, with
raw SQL as a first-class option for the remaining 10%. The core (F7) is
package `db`; model code generation came in F9, relations and eager
loading in v0.1.1.

### 10.1 Models

Models are plain structs. Embedding gives common behaviour; nothing is
required.

```go
// illustrative
type Post struct {
    db.Model                  // id, created_at, updated_at
    db.SoftDeletes            // deleted_at + default scope
    Title    string `db:"title"`
    Body     string `db:"body"`
    AuthorID int64            // untagged: column author_id
    Meta     map[string]any `db:"meta,json"`

    Author   *User     `rel:"belongs_to"` // v0.1.1; untagged structs are never columns
    Comments []Comment `rel:"has_many"`
    Tags     []Tag     `rel:"many_to_many"` // pivot post_tag
}
```

- Columns are `db` tags or snake_case field names; options `pk`, `json`,
  `readonly`. Untagged struct, pointer-to-struct and slice-of-struct
  fields are reserved for relations.
- Tables are the snake_case plural of the type name, or `TableName()`.
- Timestamps and soft deletes are enabled **only by embedding**
  `db.Timestamps`/`db.Model` and `db.SoftDeletes` (D46), not by column-name
  convention.
- Metadata is computed once per type and cached (D12); scanning uses a
  plan cached per (type, result columns).

Queries use **typed columns**, so conditions are checked by the compiler.
`anetos gen` (F9) writes them: for each model `Post`, a `models_gen.go`
declares `PostCols`, one `db.Column[T]` per column, with `T` the field's
type (D58–D62). They can also be declared by hand (`db.Col[int]("views")`)
or left untyped (`db.C("views")`). Relation handles (`PostRels`, v0.1.1)
join them:

```go
// illustrative
posts, err := db.Query[models.Post](c).
    Where(models.PostCols.AuthorID.Eq(user.ID)).
    Where(models.PostCols.Title.Like("%go%")).
    With(models.PostRels.Author, models.PostRels.Comments). // eager load, no N+1
    OrderBy(models.PostCols.CreatedAt.Desc()).
    Paginate(page, 20)
```

### 10.2 Capabilities

| Capability | Design | Status |
|---|---|---|
| CRUD | `db.Create`, `db.Update`, `db.Save`, `db.Delete`, `db.Find[T]`; `Get`, `First`, `Count`, `Exists`, streaming `All()` (`iter.Seq2`) | F7 |
| Timestamps | Set automatically (UTC, µs) when `db.Timestamps`/`db.Model` is embedded, including mass updates | F7 |
| Soft deletes | Default scope; `WithTrashed()`, `OnlyTrashed()`, `Restore`, `ForceDelete` | F7 |
| Scopes | Plain functions: `func(q *db.Q[Post]) *db.Q[Post]`, applied with `.Scope(published)` | F7 |
| Conditions | Typed columns (`Eq`, `In`, `Between`, `Like`, `IsNull`…), `And`/`Or`/`Not`, `db.SQL` fragments; `Eq(nil)` is `IS NULL` | F7 |
| Relations | has-one, has-many, belongs-to, many-to-many (pivot) declared with `rel` tags (D83); loaded **explicitly** with `With(...)`, `db.Load` or `db.LoadMany`, one query per relation (D84), nested and constrained through typed handles (D85); `WhereHas`/`WhereDoesntHave` (D86); `Attach`/`Detach`/`DetachAll`/`Sync` for pivots (D87) | v0.1.1 (F13) |
| Lazy loading | **Not supported by design.** Go has no property-access hooks, and hidden queries are the N+1 bug | — |
| Hooks | Opt-in interfaces on the pointer type: `BeforeSave`, `BeforeCreate`, `AfterCreate`, `BeforeUpdate`, `AfterUpdate`, `AfterSave`, `BeforeDelete`, `AfterDelete`. Not run by mass writes | F7 |
| Transactions | `db.Tx(ctx, fn)`: the transaction travels in the context, so nested calls join it; nested `Tx` uses savepoints; `db.AfterCommit`; `ForUpdate`/`ForShare` | F7 |
| Pagination | Offset (`Paginate`, Laravel-compatible JSON) and cursor (`CursorPaginate`, keyset with next/prev cursors) | F7 |
| Aggregates | `db.Sum`, `Avg`, `Min`, `Max`, `Pluck`; `GroupBy`, `Having`, `db.Select[R]` for grouped rows | F7 |
| Raw SQL | `db.Raw[T]`, `db.RawFirst[T]`, `db.Exec`; `?` everywhere (rebound per dialect, `??` escapes), `:name` with `db.Named` | F7 |
| Upserts, bulk insert | `db.Upsert` (`ON CONFLICT` / `ON DUPLICATE KEY`), `db.CreateMany` in batches; generated keys set where `RETURNING` exists | F7 |
| Validation | `unique` and `exists` rules registered by importing `db` | F7 |
| Mass writes | `q.Update(col.Set(v))`, `q.Delete()`; refuse `Join`/`OrderBy`/`Limit` (D49) | F7 |

### 10.3 Dialects, drivers & connections

- Built on `database/sql`. A **dialect** (in core `db`) handles
  placeholders, quoting, `RETURNING` vs `LastInsertId`, upsert syntax,
  `LIMIT` forms, row locks and argument conversion. **Driver modules**
  (`drivers/postgres` with pgx, `drivers/mysql` with go-sql-driver,
  `drivers/sqlite` with modernc.org/sqlite) pair a dialect with a
  `database/sql` driver and build connection strings (D40). The
  `db/dbtest` conformance suite runs against each in CI.
- The default dev driver is SQLite in **pure Go** (modernc.org/sqlite, D10,
  D38): WAL, busy timeout, foreign keys, immediate transactions.
- **The connection travels in the context** (D39): `db.Connect(ctx, app,
  drivers...)` reads `DB_*`, picks the driver named by `DB_CONNECTION`,
  pings when the app boots (fail fast; `help` needs no database, D73),
  adds the DB to every context the app creates
  (`App.AddContextValue`), provides `*db.DB`, and closes it at shutdown.
  Extra connections: `db.LoadConfig(src, "PREFIX_")` + `db.Open` +
  `db.WithDB(ctx, d)`. Read/write splitting stays in the backlog.
- Queries are **immutable** builders (D41) and **times are UTC** on write
  and read (D43). NULL into a non-pointer field is an error (D44).
- Query log at debug level in development (`DB_LOG_QUERIES`), slow-query
  warnings everywhere (`DB_SLOW_QUERY`). Relations can't cause hidden
  N+1 queries (no lazy loading); detecting hand-written ones (the same
  query repeated within a request) needs request-scoped query tracking and
  is deferred to v0.2 (D87).

### 10.4 Build vs. buy

We build our own thin layer (decision D5) because relations, eager loading,
typed columns, soft deletes and pagination have to feel like one designed
thing, and existing Go ORMs each force trade-offs (GORM's heavy runtime
reflection, `sqlc`'s SQL-first model, `ent`'s schema DSL). **Fallback:** if
the layer stalls badly, an adapter over Bun behind the same public API. The
public API is kept deliberately small to make that possible. F7 landed
without needing it.

---

## 11. Migrations, seeders & factories

Package `db/migrate` (F8).

```go
// database/migrations/2026_10_01_120000_create_posts.go (illustrative)
func init() { All.Add("2026_10_01_120000_create_posts", createPosts{}) }

type createPosts struct{}

func (createPosts) Up(s *migrate.Schema) error {
    return s.Create("posts", func(t *migrate.Table) {
        t.ID()
        t.String("title", 200)
        t.Text("body")
        t.ForeignID("author_id").Constrained().CascadeOnDelete()
        t.Timestamps()
        t.SoftDeletes()
        t.Index("author_id", "created_at")
    })
}

func (createPosts) Down(s *migrate.Schema) error { return s.Drop("posts") }
```

- **Sets, not a global registry** (D51): migrations are added to a
  `migrate.Set` owned by the app (`All = migrate.NewSet("app")`) or by a
  plugin (its own set, named after it). IDs are explicit strings starting
  with a timestamp; all sets run in ID order. `set.AddFS` adds embedded
  `ID.up.sql`/`ID.down.sql` files.
- **Compiled into the binary:** a deploy ships exactly the migrations its
  code expects.
- **Schema builder:** portable column types, modifiers, indexes and
  foreign keys named like Laravel's, `Alter` with `Change`, and raw SQL
  via `s.Exec` (split into statements) and `s.Dialect()`. `t.ID()`,
  `t.Timestamps()` and `t.SoftDeletes()` match `db.Model`,
  `db.Timestamps` and `db.SoftDeletes`; timestamps default to the current
  time so SQL inserts get them too. Operations SQLite can't do are
  errors, not silent no-ops.
- **Runner:** records applied migrations with their source and **batch**
  in the `migrations` table. `Up` applies pending ones as one batch,
  `Rollback(n)` undoes the last n batches, `Reset`, `Status` (applied,
  pending, missing), `Fresh` (drop every table; development and testing
  only, D53), `Seed`. Each migration runs in its own transaction on
  PostgreSQL and SQLite (MySQL commits DDL immediately); `WithoutTransaction`
  opts out. A PostgreSQL advisory lock or MySQL named lock serializes
  concurrent runs (D52).
- **Commands:** `migrate [--seed]`, `migrate:rollback --step`,
  `migrate:reset`, `migrate:fresh --seed`, `migrate:status`,
  `db:seed --seeder`, registered on the app binary by `migrate.ForApp`
  (F11). Rollback, reset, `db:seed` and `migrate --seed` need `--force` in
  production (any `APP_ENV` but development, testing and staging);
  `migrate:fresh` runs only in development and testing.
- **Seeders** are named functions run in order, each in a transaction.
  **Factories** (`db/factory`, F12) make valid model values for tests and
  seeders: `factory.New(func(n int) Post {…})` with a sequence number,
  `With(func(*Post))` for states (a new factory each time), `Make`,
  `Create(ctx)` through `db.Create` (D77).

---

## 12. Views & frontend

### 12.1 Server-rendered (default, v0.1)

Implemented in F10 (packages `view`, `session`, `encryption`; helpers in
`web`).

- **templ** for all HTML: compiled, type-checked, component-based. Apps add
  it as a Go tool; `anetos dev` (F11) will run `templ generate` on change.
- **`view.Component`** (D63) is `Render(ctx, io.Writer) error`, which templ
  components already implement, so the core doesn't depend on templ and
  other engines plug in (`view.Template` adapts `html/template`).
  `c.Render(status, comp)` renders into a pooled buffer with the `*web.Ctx`
  as context (a render error becomes an error page) and `web.View` is the
  responder. Layouts are templ components with `{ children... }`.
- **View helpers** read the request context: `web.URL(ctx, name, args...)`
  (route paths, `(string, error)`, which templ accepts),
  `view.CSRFField`, `view.CSRFToken`, `view.MethodField`, `view.Errors`,
  `view.Old`, `view.Flash`. Auth user and pagination links come with auth
  (v0.2) and later.
- **Assets** (D66): `view.NewAssets(prefix, fsys...)` hashes files once at
  startup; `assets.URL(name)` adds `?v=<hash>`, and requests with the current
  hash are cacheable for a year. The app declares its `*view.Assets` and
  templates use it directly (no context lookup).
- **htmx** 2.0.11 is bundled (`view/htmx`, 0BSD) and served through
  `view.NewAssets(…, htmx.FS)`. `c.IsHTMX()` (adds `Vary: HX-Request`) and
  `c.HTMX()` support partial rendering; the layout passes the CSRF token in
  `hx-headers`.
- **Sessions** (D64): the whole session is an encrypted cookie (AES-256-GCM
  with a per-message HKDF key from `APP_KEY`, authenticated with the cookie
  name), holding values, flash data, the CSRF token, and flashed errors and
  input. Idle (`SESSION_LIFETIME`) and absolute (`SESSION_MAX_LIFETIME`,
  7 days, restarted by `Regenerate` and `Invalidate`) expiry are enforced from timestamps
  inside the cookie, since a cookie session can't be revoked on the server.
  A Secure cookie is named `__Host-…`. No cookie is set until something is
  stored; unchanged sessions are rewritten at most every tenth of
  `SESSION_LIFETIME` (at least a minute apart); responses of requests with a session get
  `Cache-Control: private` and `Vary: Cookie`. About 4 KB: oversized old
  input is dropped first, then the save is skipped and logged.
- **Server-side sessions** (B2, D93): `SESSION_DRIVER=database` (table from
  `session.Migrations`) or `redis` (`drivers/redis`) keeps the session
  payload, encrypted with `APP_KEY` and without the ID, in a `cache.Store`
  under `SESSION_PREFIX` (default `APP_NAME:session:`) plus the SHA-256 of
  the session ID, for `SESSION_LIFETIME`; the cookie holds only the
  encrypted ID. The idle and absolute checks still use the payload's
  timestamps. An existing session is saved with `Replace`, so one ended
  meanwhile stays ended; a new or regenerated one with `Set`, after which
  the old entry is deleted, so `Regenerate` and `Invalidate` revoke every
  copy of the cookie (a late request whose `Replace` fails leaves the
  cookie alone). Entries are encrypted with the key in the context, so a
  value copied to another key doesn't load. Failed-form input over 64 KB is dropped; sessions
  over 1 MB aren't saved. A store read failure answers 503 through the
  router's error handler (via `internal/httperr`, which `web` wires up); a
  write failure is logged. Concurrent requests of one session are
  last-write-wins.
- **CSRF** (D65): `web.CSRF` combines Go's `http.CrossOriginProtection`
  (Sec-Fetch-Site / Origin) with a session token (masked per render against
  BREACH) from `_token` or `X-CSRF-Token`; 403 on failure.
  `web.MethodOverride` routes `_method` posts as PUT/PATCH/DELETE, reading
  URL-encoded bodies (restored for handlers) or the query string, never
  multipart bodies.
- **Redirect back** goes to the same-origin `Referer` (else `/`); the
  session doesn't track pages. htmx requests get the 422 unless boosted.

### 12.2 SPA-style (v0.4)

- **Vite integration**: in dev, proxy to the Vite server with HMR; in
  production, read the Vite manifest and serve hashed assets **embedded in
  the binary**.
- **Inertia protocol adapter** (server side): `c.Inertia("Posts/Index",
  props)`, shared props, partial reloads, validation errors mapped to Inertia's
  error bag, redirect semantics. Inertia's own client adapters cover Vue,
  React and Svelte.
- `anetos new --stack=htmx|vue|react|svelte|api`.

---

## 13. Runtime & concurrency

The feature that sets Anetos apart from other Go frameworks.

### 13.1 Components & the supervisor

Everything long-running is a **component**:

```go
type Component interface {
    Name() string
    Run(ctx context.Context) error // returns when ctx is canceled or on failure
}
```

The HTTP server, each worker pool, each listener, the scheduler and every
`app.Go` goroutine is a component. The **supervisor**:

- starts the components selected for this process's **roles**;
- applies a **restart policy** per component (HTTP: fail the process; workers
  and listeners: restart with backoff; `app.Go`: configurable);
- recovers panics, logs them with stack traces and counts restarts;
- exposes liveness and readiness state for health endpoints;
- on shutdown, stops components **in order within a deadline**.

### 13.2 Roles

```bash
./blog run                         # everything (dev, small deployments)
./blog run --only=http             # web nodes
./blog run --only=workers,listeners
./blog serve | work | listen | schedule   # shortcuts
```

### 13.3 Graceful shutdown order

Components belong to **stages**. On shutdown the supervisor cancels stages in
ascending order and waits for each to drain before canceling the next. The
rule is **producers of work stop before its consumers** (D21):

1. Mark not-ready (load balancers stop routing new traffic).
2. `StageIngress` (HTTP): stop accepting connections; finish in-flight
   requests.
3. `StageScheduler`: don't start new runs; wait for running tasks.
4. `StageListeners`: stop pulling new messages; finish or nack in-flight ones.
5. `StageWorkers`: stop reserving jobs; give in-flight jobs (including
   ones produced in steps 2–4) the shutdown grace period (half of the
   budget by default), then cancel them and put those that stop back on
   the queue without counting the attempt (D107).
6. `StageBackground`: ad-hoc `app.Go` tasks.
7. Shutdown hooks, in reverse registration order: flush logs and traces,
   close pools (DB, Redis).

`APP_SHUTDOWN_TIMEOUT` (default 30s) is the total budget: components use it
first, and hooks always keep the smaller of 5s and a fifth of it, so a hung
component can't prevent cleanup and the whole shutdown fits a Kubernetes
default grace period. Anything still running after the
deadline is logged by name and reported in `Run`'s error. *(Implemented in
v0.1: F4, package `supervisor`.)*

### 13.4 Queue & jobs

```go
// app/jobs/send_welcome.go (illustrative)
type SendWelcome struct {
    UserID int64 `json:"user_id"`
}

func (j SendWelcome) Handle(ctx context.Context) error {
    user, err := db.Find[models.User](ctx, j.UserID)
    if err != nil { return err }
    return mailer.Send(ctx, mailers.Welcome{User: user})
}

// setup
q, err := queue.ForApp(app, redis.QueueDriver())       // QUEUE_DRIVER
err = queue.Register[jobs.SendWelcome](q, queue.Tries(5))
err = q.Work(queue.Queues("emails", "default"), queue.Concurrency(10))

// dispatching
queue.Dispatch(ctx, jobs.SendWelcome{UserID: user.ID},
    queue.OnQueue("emails"), queue.Delay(time.Minute))
```

*(Implemented in B5: package `queue`, `drivers/redis`.)*

- **Jobs** are typed structs with `Handle(ctx) error`, registered at
  startup with `queue.Register[J]` (D104). A dispatch is a JSON envelope
  `{"id", "job", "data"}`: a UUIDv7, the registered name (the Go type by
  default, `queue.Name` to keep it across renames) and the fields. The
  worker finds the type by name in the registry map and decodes into a
  new value; nothing else is reflective. Dispatching an unregistered type
  is an error. Jobs get their dependencies from `ctx`, which carries the
  app's context values (`db`, `cache`, the queue, `app.AddContextValue`),
  `queue.Info` and the timeout.
- **Workers** are a component (`q.Work`; role `workers`,
  `StageWorkers`, restarted on failure) that reserves jobs from its
  queues in priority order, runs up to `Concurrency` at once and polls
  every `QUEUE_POLL` when idle. `q.Run` runs them in a component of your
  own.
- **Delivery is at-least-once** (D17). Reserved jobs are leased, with a
  token per reservation; outcomes are recorded only with the token (D105).
- **Retries**: `QUEUE_TRIES` (3) attempts, exponential backoff with
  jitter (`QUEUE_BACKOFF` 10s doubling to `QUEUE_BACKOFF_MAX` 10m), or
  per type `queue.Tries`, `queue.Backoff`, `queue.Timeout`;
  `queue.Permanent(err)` fails at once. A job out of tries is kept as
  failed (also when its worker died during the last try: it isn't run
  again), and its `Failed(ctx, err)` method (if any) runs; `queue:failed`,
  `queue:retry`, `queue:forget`, `queue:flush` and `queue:clear` manage
  them (D107).
- **Function jobs**: `queue.RegisterFunc(q, name, fn)` registers a
  function of a typed payload under a name, dispatched with
  `queue.DispatchFunc`; queued event listeners use it (D111).
- **Drivers** (D105, D106): `sync` (runs at once in `Dispatch`, for
  development and tests; the default), `memory`, `database` (dispatches
  join the context's transaction) and `redis`. SQS, NATS and others come
  as plugins implementing `queue.Store`; `queue/queuetest` is its
  conformance suite.

### 13.5 Pub/sub listeners

For consuming *external* streams, the case that needed a separate app with
Buffalo:

```go
app.Listen(pubsub.Topic("orders.created"), listeners.OrderCreated,
    anetos.Concurrency(20), anetos.Retry(5), anetos.DeadLetter("orders.dlq"))

// app/listeners/order_created.go
func OrderCreated(ctx context.Context, msg events.OrderCreated) error {
    // typed message, decoded for you; return nil → ack, error → nack/retry
}
```

- A `pubsub` contract with subscribe (and publish) plus drivers: **Redis
  Streams** and **Google Pub/Sub** first (v0.2). NATS, Kafka and SQS come as
  plugins.
- Bounded concurrency per listener, ordered processing where the broker
  supports ordering keys, and backpressure (stop pulling when the pool is
  full).
- The same at-least-once and idempotency caveat applies.

### 13.6 Events (in-process)

```go
bus, err := events.ForApp(app)
events.On(bus, recordAudit)                                    // sync, in Emit's transaction
events.OnAsync(bus, sales.countSale, events.Concurrency(8))    // goroutine pool, after the commit
events.OnQueued(bus, emailReceipt, events.Job(queue.Tries(5))) // durable: a queue job

events.Emit(ctx, OrderPlaced{OrderID: o.ID})
```

*(Implemented in B6: package `events`.)*

- Events are any type; listeners are `func(ctx, E) error`, added with
  generic functions, so the event type is checked by the compiler. `Emit`
  looks the event's dynamic type up in a map; nothing else is reflective
  (D109).
- **On** listeners run in `Emit`, in order, with its context and
  transaction; the first error stops them and is returned.
- **OnAsync** listeners run in a bounded pool of their own (`Concurrency`,
  `Buffer`; `Emit` waits for room), after the transaction commits, with a
  fresh context carrying the app's values. Errors and panics are logged.
  They are **lost if the process stops** before handling them; at
  shutdown the bus gives them the rest of the budget (a shutdown hook)
  (D110, resolving O4).
- **OnQueued** listeners are queue jobs (`queue.RegisterFunc`, named
  `event:<listener>`), dispatched with `queue.AfterCommit()`: durable and
  retried (D111).

### 13.7 Scheduler

```go
app.Schedule(schedule.Cron("*/5 * * * *"), tasks.SyncInventory)
app.Schedule(schedule.DailyAt("02:00").Timezone("Asia/Dhaka"), tasks.PruneSessions,
    schedule.WithoutOverlapping(), schedule.OnOneServer())
```

`WithoutOverlapping` and `OnOneServer` use cache locks (`cache.TryWithLock`,
§14.1), so running the scheduler on several instances is safe with a
shared store (database or Redis).

### 13.8 Ad-hoc background work

`app.Go(name, func(ctx context.Context) error, opts...) error` starts a
**supervised** goroutine: it recovers panics, is canceled on shutdown, can be
restarted (`anetos.Restart(supervisor.RestartOnFailure)`), and appears in
`app.Supervisor().Status()`. It works before or during `Run`. This replaces bare `go func()` calls that leak or
get killed mid-work on deploy.

---

## 14. Services, contracts & drivers

Each service is an interface in the core module. Drivers are chosen in
`.env`, and heavy drivers live in separate modules.

| Service | Contract package | Core drivers | Driver modules / plugins | Version |
|---|---|---|---|---|
| Database | `db` | — | `drivers/postgres`, `drivers/mysql`, `drivers/sqlite` | v0.1 |
| Session | `session` | cookie (encrypted), database | `drivers/redis` | v0.1 / v0.2 (B2 done) |
| Cache (+ locks) | `cache` | memory, database | `drivers/redis` | v0.2 (B1 done) |
| Queue | `queue` | sync, memory, database | `drivers/redis`; SQS, NATS (plugins) | v0.2 (B5 done) |
| Pub/sub | `pubsub` | in-memory (tests/dev) | `drivers/redis` (Streams), `drivers/gcppubsub`; NATS, Kafka (plugins) | v0.2 |
| Mail | `mailer` | SMTP, log (dev) | Resend / Postmark / SES / Mailgun (plugins) | v0.2 |
| Storage | `storage` | local | `drivers/s3` (S3-compatible, incl. R2/MinIO); GCS, Azure (plugins) | v0.2 |
| Password hashing | `auth/password` | argon2id, bcrypt | — | v0.2 (B3 done) |
| Encryption | `encryption` | AES-GCM with `APP_KEY`, key rotation | — | v0.1 |
| Rate limiting | `web/ratelimit` | on the app's cache | (the cache's stores) | v0.2 (B2 done) |
| Logging | `log/slog` (stdlib) | text, JSON handlers | OpenTelemetry bridge (module) | v0.1 |

**Rule:** a driver belongs in the core module only if it uses nothing but the
standard library (or a tiny, stable dependency). Everything else is a module.

Switching the database is a config change plus an import:

```env
DB_DRIVER=postgres
DB_URL=postgres://app:secret@localhost:5432/blog
```

The generated project imports the selected driver in `main.go`, and
`anetos new --db=postgres` and `anetos add driver postgres` manage that
import.

### 14.1 Cache

```go
c, err := cache.ForApp(app, redis.CacheDriver()) // CACHE_STORE: memory | database | redis
stats, err := cache.Remember(ctx, "stats", time.Minute, computeStats)
err = cache.TryWithLock(ctx, "reports:monthly", 10*time.Minute, buildReport)
```

- **Contract.** `cache.Store` is bytes in, bytes out: `Get`, `Set`, `Add`
  (atomic add-if-absent), `Replace` (atomic set-if-present, B2), `Delete`, `Increment` (atomic; the ttl applies
  when the counter is created), `DeleteIf`/`ExpireIf` (atomic
  compare-and-delete/expire, for locks), `Flush(prefix)` and `Close`. A
  ttl of 0 is forever. `cache/cachetest` is the conformance suite every
  store runs (D88).
- **Use.** Like `db`, the cache travels in the context: `cache.ForApp`
  adds it to every context the app creates, and package functions
  (`Get[T]`, `Set`, `Add`, `Has`, `Forget`, `Increment`, `Remember[T]`,
  `Flush`) find it there (`cache.WithCache` for other contexts). Values are
  JSON; keys are prefixed with `CACHE_PREFIX` (default `APP_NAME:cache:`,
  so `cache:clear` doesn't touch sessions or queues sharing a Redis
  database) and must be UTF-8 text without NUL, at most 250 bytes with it,
  so every store accepts the same keys. Counters are canonical decimal
  text; a non-integer or an overflow is an error (D88).
- **Remember** runs the function once per key per process for concurrent
  misses (a waiter whose leader's context ended computes itself; a panic
  reaches waiters as an error), stores nothing on error, and treats the
  cache as an optimization: a store error or a value that no longer
  decodes is logged and the function runs (D89).
- **Stores.** Memory (per process, lazy expiry plus a periodic sweep);
  database (`cache.Migrations` table: key, value, expires_at in Unix ms
  by the database server's clock; exact key comparison, `VARBINARY` on
  MySQL; `Add` is an insert-or-nothing plus removal of an expired row,
  `Increment` a row lock in a short transaction; deadlocks are retried;
  own connections on PostgreSQL and MySQL, the context's transaction on
  SQLite) (D90); Redis in `drivers/redis`, on the app's shared client from
  `redis.Connect`, with `SET NX PX`, `MULTI` for counters, Lua scripts for
  the owner-checked operations and `SCAN`+`UNLINK` for flushes (D92).
- **Locks** are keys (`lock:<name>`) holding a random owner token, taken
  with `Add` and a ttl, released and extended only by their owner through
  `DeleteIf`/`ExpireIf`. `Acquire` polls with backoff until the context
  ends; `WithLock` waits, `TryWithLock` returns `ErrLockHeld`. They are
  leases, not fenced: work longer than the ttl must `Extend` (D91).

---

## 15. Authentication & authorization

Implemented in B3 (packages `auth` and `auth/password`) and B4
(`auth/social`), except scaffolding and the mail parts of verification
and reset (B14, after mail).

```go
a, err := auth.ForApp(app, users) // users: auth.Users[*models.User]{ByID, ByLogin, …}
pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
pages.Group("", a.Require).Get("/dashboard", h.Dashboard)
u, err := a.Attempt(c, in.Email, in.Password, in.Remember)
if err := auth.Authorize(c, policies.Post.Update, &post); err != nil { return nil, err } // 401/403
```

- **Users** are the app's type, implementing `AuthID()` and
  `AuthPassword()`; `auth.Users[U]` is a struct of functions (find by ID
  and login; optionally store remember tokens and upgraded hashes), so
  the app keeps its model and queries (D96).
- **Sessions:** the session holds the user ID and a fingerprint of the
  password hash (a password change signs out other sessions). `Login`
  regenerates the session ID; `Logout` invalidates it. The user loads
  lazily, once per request (D96).
- **Password auth:** argon2id (OWASP parameters, at most GOMAXPROCS
  computations at once) via `golang.org/x/crypto`, bcrypt verified for
  migrated users; weaker hashes upgraded after login; `Attempt` throttles
  per login and IP and per account and IP (`AUTH_THROTTLE`), and failures
  per IP (`AUTH_THROTTLE_IP`), through `ratelimit` (cache), and spends a
  dummy hash for unknown and passwordless users (D96).
- **Remember me:** an encrypted cookie with the user ID, a remember token
  stored with the user, the password fingerprint and an expiry; `Logout`
  rotates the token (D98). One Auth per app (fixed session keys and
  cookie).
- **Reset and verification tokens** are encrypted (not stored), with an
  expiry; reset tokens include the password fingerprint, so they're
  single-use (D97).
- **API tokens:** personal access tokens with abilities (Sanctum-like),
  `<id>|<secret>`, SHA-256 of the secret stored in `api_tokens`; Bearer
  middleware; session users pass `TokenCan` (D99).
- **Social login** (B4, package `auth/social`): the authorization-code
  flow via `golang.org/x/oauth2` with state, PKCE (S256) and, for OpenID
  Connect, a nonce, kept in the session for ten minutes and used once.
  Built-in **Google** and **GitHub** (profile and primary verified
  address from the API) and **generic OIDC** (endpoints from discovery).
  ID tokens are checked for issuer, audience (and `azp`), expiry and
  nonce (and `azp` when present), not signature: they come from the token
  endpoint over TLS (OIDC Core 3.1.3.7), so every provider URL must be
  https and redirects to anything else are refused (D101). Discovery runs
  once in the background, with a 30-second backoff after a failure. An app
  `Resolver` maps a profile to its user; `social_accounts` links accounts
  to users, and email is used only when verified on both sides (D102).
  Credentials come from `SOCIAL_<NAME>_*`, callbacks from the new
  `APP_URL` (D103). Failures return to the login page with a `social`
  field error; success signs in with `a.Login`.
- **Authorization:** typed policies, `func(ctx, U, T) bool`, checked by
  generic `auth.Authorize` (and `AuthorizeUser`, `Allows`); errors carry
  401/403 (D100).
- **Scaffolding** (B14): `anetos make:auth` generates handlers, views,
  migrations and routes **into the app**, where the developer owns them
  (the Breeze approach), from `examples/auth`. Security-critical pieces
  (hashing, tokens, session handling, throttling) stay in the library so
  fixes reach everyone through `go get -u`.

---

## 16. Plugin system

The goal: a third-party package can plug routes, migrations, config,
commands, workers, listeners, schedules, views and assets into an app **with
one command**.

### 16.1 Interface

```go
// package ext (illustrative)
type Plugin interface {
    Name() string                          // unique, used for namespacing
    Register(r *Registrar) error           // bind services, declare config; no I/O
    Boot(ctx context.Context, a *App) error // routes, workers, listeners, commands…
}

// Optional capabilities, discovered by interface assertion at boot:
type HasMigrations interface { Migrations() fs.FS }
type HasViews      interface { Views() fs.FS }
type HasAssets     interface { Assets() fs.FS }
type HasConfig     interface { Config() any }          // typed struct with env tags
type HasCommands   interface { Commands() []cmd.Command }
type Compat        interface { Requires() string }     // e.g. ">= v0.2.0, < v0.4.0"
```

The framework's own features (sessions, auth, queue…) are implemented as
internal providers using the **same** mechanism, and first-party plugins use
only the public API (roadmap §6).

### 16.2 Installation

Go compiles everything in; there's no runtime package discovery. So
installation is **code generation**:

```bash
anetos add github.com/acme/anetos-stripe
```

1. `go get github.com/acme/anetos-stripe`
2. Regenerate `plugins.go` (a generated list of `ext.Plugin` values
   imported by `main.go`).
3. Write a config stub (`config/stripe.go`) and add the plugin's env keys to
   `.env.example`.
4. Print next steps (e.g. "run `./blog migrate`"). Migrations never run
   automatically.

`anetos remove` reverses steps 1–3.

### 16.3 Namespacing & safety

- Routes mount under a prefix the app can override; route names are
  prefixed (`stripe.webhook`).
- Migrations are tracked per plugin; commands are prefixed (`stripe:sync`).
- Version compatibility is checked at boot through `Requires()`, with a clear
  error if it fails.
- **Trust model:** plugins are compiled Go code with full privileges, with no
  sandbox. The docs say so, and `anetos add` shows the module path and
  version before installing.

---

## 17. CLI

### 17.1 Developer tool `anetos`

Installed per project as a Go tool (`go get -tool
anetos.dev/anetos/cli/cmd/anetos`, run with `go tool anetos`), so
a project pins its version in `go.mod`; `go install` works too (D62), and
is how `anetos new` is run before a project exists. F9 shipped `gen`, F10
`key:generate`, F11 `new`, `dev` and `make:handler|model|migration|middleware`
(D69–D72); `anetos version` prints the tool's version. The other rows are
planned.

| Command | Purpose |
|---|---|
| `anetos new <dir> [--module=…] [--db=…] [--stack=…]` | Create a project (F11; v0.4 adds `--stack`) |
| `anetos dev` | Watch (polling) → `templ generate` → `anetos gen` → build → restart on a free port → browser reload; stable address through a proxy that shows build errors (F11) |
| `anetos make:<thing>` | handler, model (`--migration`), migration, middleware (F11); job, event, listener, mail, policy, task, command, test, plugin (later) |
| `anetos gen` | Run code generators: typed model columns (F9), relation handles (v0.1.1). `-check` for CI |
| `anetos key:generate` | Print a new `APP_KEY` line (F10) |
| `anetos add` / `anetos remove` | Plugins and drivers |
| `anetos build` | Production build: `-trimpath`, version via ldflags, `CGO_ENABLED=0` by default |
| `anetos doctor` | Check the environment and project (Go version, `APP_KEY`, debug in prod, pending migrations) |
| `anetos stub:publish` | Copy generator templates into the project for customization |

Generators produce plain Go that the developer owns.

### 17.2 App binary commands

Implemented in F11 (package `cmd`, `App.Execute`; D69): `run [--only=…]`
(the default), `serve`, `routes:list`, `migrate*`, `db:seed`, `help`,
plus **custom commands**. Later: `work`, `listen`, `schedule`,
`queue:failed`, `queue:retry`, `schedule:list`, `down` / `up`
(maintenance).

```go
// illustrative
app.Command("reports:send", "Email the weekly report", func(ctx context.Context, args *cmd.Args) error { … })
```

Packages that add components add their commands where they are wired:
`web.NewServer` adds `serve` and `routes:list`, `migrate.ForApp` the
migration commands.

---

## 18. Testing

The `anetostest` package (F12) gives testing the Laravel comfort:

```go
// illustrative
func TestCreatePost(t *testing.T) {
    app := anetostest.New(t, setup)   // the app's own setup; migrated, isolated DB
    author := anetostest.Create(app, factories.Authors)

    app.Get("/posts/new")
    app.PostForm("/posts", url.Values{"title": {"Hello"}, "author_id": {fmt.Sprint(author.ID)}}).
        AssertRedirectRoute("posts.index").
        AssertSessionHas("status", "Post created.")

    anetostest.AssertDatabaseHas[models.Post](app, models.PostCols.Title.Eq("Hello"))
}
```

- **Boot.** `anetostest.New(t, setup, opts...)` runs the function `main`
  uses to wire the app, boots it, runs the migrations `migrate.ForApp`
  registered, and closes it at the end of the test (D74). Settings:
  options > `APP_ENV=testing` and a random `APP_KEY` > the environment >
  `.env.testing` > test defaults; `.env` is never read, so tests can't
  reach the development database. Logs go to `t.Log`.
- **Database isolation** (D75): SQLite in memory (the default) gives each
  test its own database; with a server or a SQLite file, each test runs in
  a transaction rolled back at the end, and each request in a savepoint
  so a failed statement doesn't poison PostgreSQL's transaction. Whether
  the database is in memory is asked of SQLite after connecting, and an
  unset or empty `DB_DATABASE` never falls back to `database/app.db`.
  The test's transaction comes from `db.WithTestTx`: work at its level
  counts as committed, so `AfterCommit` callbacks run when a `db.Tx`
  directly inside it (a request's) commits, or at once outside one (D108).
  Known differences from production (connections of their own, such as
  queue workers', statements after a PostgreSQL error, timeouts, MySQL
  DDL) are documented, with `WithoutTransaction()` as the way out.
- **Client** (D76): requests go straight to the router (no network) and act
  like a browser: its own cookie jar (Path and expiry honored; Secure
  and Domain ignored, so `SESSION_SECURE` and `__Host-` cookies work over
  the in-process HTTP), the session's CSRF token added through
  `session.Manager.Edit`, the last HTML page as `Referer` (so validation
  redirects back). Chained assertions report with `t.Errorf`: status,
  redirects (by path or route name), headers, text (as is or
  HTML-escaped), JSON and JSON paths, validation errors (422 problem or
  flashed), session values. `Follow()` loads a redirect.
- **Data** (D77, D78): factories (`db/factory`) and generic database
  assertions (`AssertDatabaseHas[T]`, `…Missing`, `…Count`,
  `AssertSoftDeleted`) using the typed columns.
- **Fakes** for mail, queue, events, pub/sub, storage and the **clock**
  (`app.Freeze(time)`) arrive in v0.2 (D79, B12); time will be read from
  an injectable clock inside the framework. Until the queue fake, tests
  run jobs with the sync driver. Each test app gets its own
  `CACHE_PREFIX`, `SESSION_PREFIX` and `QUEUE_PREFIX`, cleaned up at the
  end.
- Everything works with `go test` and `-race`; `t.Parallel()` works with
  in-memory SQLite and server databases (not a SQLite file, whose write
  lock each test's transaction holds). One app per test or subtest.

---

## 19. Observability

- Structured logging with `log/slog`. Request ID, route name, user ID and
  job/message IDs are attached automatically.
- OpenTelemetry tracing and metrics as an optional module: HTTP spans, DB
  spans, job and listener spans with context propagation through queue
  payloads.
- Health endpoints (`/health/live`, `/health/ready`) fed by the supervisor
  and service checks.
- `pprof` available in dev, or behind an explicit flag and auth in
  production.
- A debug dashboard (Telescope-like) is in the backlog.

---

## 20. Security defaults

Secure by default, opt-out only when you mean it:

- CSRF protection on state-changing HTML routes (origin checks plus a
  masked session token, D65); `SameSite=Lax`, `Secure` (outside development)
  and `HttpOnly` cookies; encrypted session cookies with key rotation (D64).
- argon2id password hashing; constant-time comparisons; signed URLs.
- Security headers middleware on by default (HSTS in production, CSP helpers
  for templ with nonces).
- Every query parameterized; the raw SQL API has no string-interpolation
  helpers.
- templ escapes output by default.
- Login and password-reset throttling built in.
- Boot-time checks refuse `APP_DEBUG=true` in production and a malformed
  `APP_KEY`; features that need the key refuse to start without it (D67).
  `anetos doctor` (F11) reports both.
- govulncheck in CI; SECURITY.md with a disclosure process before v0.3.

---

## 21. Build & deployment

- `anetos build` → one static binary (`CGO_ENABLED=0`) with migrations,
  compiled templ views and `public/` assets embedded.
- `anetos new` generates a multi-stage **Dockerfile** (distroless or scratch
  runtime) and an example **systemd** unit.
- Configuration comes entirely from the environment (12-factor); `.env` is
  for dev.
- Deployment guides (v0.3): single VPS (all roles in one process), Docker,
  common PaaS, and scaling out by roles.

---

## 22. Performance strategy

- **Budgets:** Anetos overhead compared with plain `net/http` for (a) hello
  world, (b) a typed JSON handler with binding and validation, and (c) a
  single-row DB read. The v0.1 baseline is in `docs/benchmarks/` (code in
  `bench/`): the router and typed handlers add about 0.3–1.3µs, the default
  middleware about 3µs, and `db.Find` about 5µs over hand-written
  `database/sql`. Targets are set from it and tracked from then on.
- **Rules:** no per-request reflection; bind plans and route data
  precomputed at boot; pooled `Ctx` and buffers; no allocations in the
  router's hot path where avoidable.
- **CI:** `go test -bench` on every PR against `main`, with a regression gate
  (initially a warning, blocking from v0.3).
- **Honesty:** published benchmarks include their method, hardware and code,
  and compare fairly (the same work done in each framework).

---

## 23. Compatibility & versioning

- **Go version:** support the Go releases the Go team supports (the latest
  two). The core module's `go` directive is the **older** of those two, so
  anyone on a supported Go can build it; CI tests the minimum and the latest
  (D18). The directive is raised only with a CHANGELOG note.
- **SemVer**, with the pre-1.0 rules in the [roadmap](../planning/roadmap.md#versioning-rules).
- **Deprecation (from v1.0):** deprecate in a minor release (`// Deprecated:`
  plus a CHANGELOG entry) and remove no earlier than the next major.
- **Multi-module tags:** core `vX.Y.Z`; modules `drivers/redis/vX.Y.Z` and so
  on. Driver modules declare the minimum core version they need.
- **Internal packages** keep the public API surface small. Anything not
  intended for users goes under `internal/`.

---

## 24. Decision log

Short record of design decisions. Significant or contested decisions get a
full ADR in [`adr/`](adr/). Status: **Accepted**, **Proposed** (default
unless new information arrives), **Open**, **Superseded**.

| ID | Decision | Status | Notes |
|---|---|---|---|
| D1 | Working codename "Anetos"; final name before v0.3 | Accepted | Roadmap Q1 |
| D2 | Everything is `net/http`-compatible; handlers are `http.Handler` | Accepted | Principle 1 |
| D3 | Router on `http.ServeMux` + thin layer (groups, names, URL gen) | Accepted | Implemented in F5; overhead ≈5 allocs vs raw ServeMux. Swap to a radix tree only if benchmarks ever demand it |
| D4 | templ for views, behind a `view.Renderer` interface | Accepted | Buffalo/Plush lesson |
| D5 | Own data layer on `database/sql` with generics + code generation | Accepted | Core implemented in F7. Fallback: Bun adapter behind the same API |
| D6 | Multi-module monorepo; heavy drivers in separate modules | Accepted | Keeps dependency trees small |
| D7 | Supervised runtime; one binary with roles | Accepted | Core differentiator |
| D8 | Plugins compiled in; installed by code generation (`anetos add`) | Accepted | No runtime discovery in Go |
| D9 | Vite + Inertia starter kits in v0.4, not v0.3 | Accepted | v0.3 = public MVP |
| D10 | Default dev DB: SQLite in pure Go (no CGO) | Accepted | Protects cross-compile and single-binary builds; implementation D38 |
| D11 | Logging via `log/slog`; no custom logger | Accepted | |
| D12 | Reflection only at startup/registration, never per request | Accepted | Principle 4. Clarified in F6: type inspection and tag parsing happen once; per request, precomputed plans read and set fields by index (bind and validation plans) |
| D13 | No lazy loading of relations; explicit `With`/`Load` | Accepted | Prevents hidden N+1 |
| D14 | No package name shadows the standard library (`web`, `supervisor`, `mailer`, `ext`, …) | Accepted | No import aliasing needed |
| D15 | Typed handlers via generic `web.H(...)` adapter | Accepted | Go methods can't take type parameters |
| D16 | License: Apache-2.0; `LICENSE` + `NOTICE` at repo root; SPDX header in every Go file | Accepted | Patent grant and contribution terms suit a framework seeking company adoption and outside contributors (roadmap Q2) |
| D17 | At-least-once delivery for queue and pub/sub; idempotency documented | Accepted | |
| D18 | Minimum Go = older of the two Go-supported releases; CI tests minimum + latest | Accepted | `go 1.26` since 2026-09-30 (Go 1.26 and 1.27 supported); raise when Go 1.28 ships |
| D19 | Kernel lives in the root package `anetos` (no `app/` package) | Accepted | `anetos.New`, `anetos.Provide` read naturally; avoids an extra import |
| D20 | A config key set to the empty string counts as unset (default applies, `required` fails) | Accepted | Blank entries copied from `.env.example` shouldn't break int/duration parsing |
| D21 | Shutdown stages: ingress → scheduler → listeners → workers → background, then hooks | Accepted | Producers stop before consumers so accepted work gets done |
| D22 | `APP_ENV` defaults to `production`; `APP_DEBUG=true` is rejected in production | Accepted | Fail safe when config is missing (§20) |
| D23 | Providers get `*App` in Register and Boot; the restricted `Registrar` view (§16.1) is decided at B11 | Proposed | Keep v0.1 simple; revisit when the plugin API goes public |
| D24 | `web.Ctx` implements `context.Context` and is not pooled | Accepted | Resolves O1; a retained Ctx is stale, never a data race |
| D25 | `web` imports `anetos`, never the reverse | Accepted | HTTP stays optional; worker-only binaries don't link it |
| D26 | Trailing-slash patterns are exact; a group's `"/"` is the group root | Accepted | Matches expectations from Laravel/Rails; subtrees use `{rest...}` |
| D27 | Redirect helpers default to 303 See Other | Accepted | Correct after form POSTs |
| D28 | Errors: problem JSON or HTML by negotiation; 5xx details only in debug; abort the connection if the response already started | Accepted | Safe defaults; no truncated "successful" responses |
| D29 | Binding precedence: body < query/header/path; body can never set URL/header fields; files never from JSON; embedded pointer structs with source tags rejected | Accepted | Prevents parameter tampering through the body |
| D30 | HTTP shutdown grace capped at half of `APP_SHUTDOWN_TIMEOUT`, then requests are canceled | Accepted | A stuck request can't starve workers and hooks of shutdown time |
| D31 | Validation tags use Laravel syntax: `required\|max:200\|in:a,b` | Accepted | Familiar to the target audience (roadmap §4); commas stay free for parameters. go-playground-style tags (`required,email`) fail at startup as malformed, never silently |
| D32 | Only `required*` and `accepted` run on empty fields; numbers and booleans are never empty | Accepted | Optional fields need no `omitempty`/`nullable` marker; pointers say "not sent" (§9) |
| D33 | Custom rules live in a package-level registry filled from `init` (`validate.Register`) | Accepted | Exception to principle 2, like `database/sql` drivers: tags can only reference rules by name. Write-once at init; duplicates and built-in names panic |
| D34 | `web.H` order: bind → tag rules → `Validate(ctx)` (only if tags pass) → handler | Accepted | The method can rely on well-formed input; one 422 shape for both via `*validate.Errors` |
| D35 | HTML error bags/old input deferred to F10, `unique`/`exists` to F7 | Accepted | They need sessions and the DB layer; the F6 API (`*validate.Errors`, `FieldErrorer`) is what they build on |
| D36 | Validation resolves fields like `encoding/json` (flattening, shadowing, nil embedded pointers count as empty) | Accepted | Rules apply to the fields a client can actually send; a nil embedded pointer can't skip `required` |
| D37 | A plain error from `Validate(ctx)` is a 500, not a 422 | Accepted | Supersedes the F5 behaviour. Messages for clients go through `validate.Fail`/`*validate.Errors`; internal failures stay internal (§20) |
| D38 | SQLite driver: modernc.org/sqlite | Accepted | Resolves O6. Pure Go (C translated to Go), the most widely used and longest-maintained option; ncruces/go-sqlite3 (WebAssembly on wazero) was the alternative. Swappable behind `db.Driver` |
| D39 | The database handle travels in the context; `db.Connect` adds it app-wide via `App.AddContextValue` | Accepted | `db.Query[T](ctx)` works anywhere a context does, and transactions join through the same context. Explicit alternative for other DBs: `db.WithDB` |
| D40 | Dialects live in core `db`; driver modules only pair a dialect with a `database/sql` driver; `db/dbtest` conformance suite per driver | Accepted | SQL generation is tested without databases; heavy drivers stay out of the core module (D6) |
| D41 | Query builders are immutable (each method returns a new `Q`) | Accepted | Shared base queries can't leak conditions into each other; the copy cost is small next to a round trip |
| D42 | Typed columns (`db.Col[T]`) are the condition API; F9 generates them | Accepted | Compiler-checked conditions from day one; generated by `anetos gen` since F9 |
| D43 | Times are written and read in UTC with µs precision; time arguments converted to UTC; driver sessions in UTC; SQLite text in `CURRENT_TIMESTAMP` format | Accepted | Values round-trip exactly and compare alike on every database, including against column defaults and `timestamp` without time zone |
| D44 | NULL into a non-pointer field is an error | Accepted | Silent zero values hide data problems; the error names the fix (pointer or `sql.Null[T]`) |
| D45 | Raw SQL uses `?` on every database (rebound per dialect, `??` escape) or `:name` with `db.Named` | Accepted | One way to write parameters; quoted text (including PostgreSQL `E''` strings), comments (nested on PostgreSQL, `#` on MySQL) are skipped; a whole query without `?` passes through unchanged, fragments must use `?` |
| D46 | Timestamps and soft deletes only via embedded `db.Timestamps`/`db.Model`/`db.SoftDeletes` | Accepted | Explicit (principle 2); a plain `created_at` column isn't silently managed |
| D47 | SQL fragment constructor is `db.SQL`; `db.Raw[T]` is the raw query function | Accepted | Go has no overloading; the query function is the more common call |
| D48 | Multi-module repo without `go.work`; driver modules use `replace ../..`; make targets loop over modules | Accepted | `replace` in non-main modules is ignored by consumers, so published modules are unaffected |
| D49 | Mass `Update`/`Delete` accept only `Where` (refuse `Join`, `OrderBy`, `Limit`, `Offset`, `GroupBy`, `Having`, `Distinct`, locks) | Accepted | Those forms differ or don't exist across dialects (MySQL also rejects `LIMIT` and same-table subqueries in them); `db.Exec` covers the rest |
| D50 | MySQL connections use `clientFoundRows=true` | Accepted | Rows *matched*, as on PostgreSQL and SQLite, so `Update` can report `ErrNotFound` and mass updates count alike everywhere |
| D51 | Migrations live in `migrate.Set`s owned by the app or a plugin, with explicit timestamped IDs; no global registry | Accepted | Supersedes the `migrate.Register` sketch. Principle 2; plugins ship their own set, which the status shows as its source |
| D52 | Migration runs are serialized with a PostgreSQL advisory lock / MySQL `GET_LOCK` named per database (SQLite: in-process only); the lock needs a second connection, so a pool of 1 is refused | Accepted | Instances starting together during a deploy can't apply a migration twice |
| D53 | `migrate:fresh` only runs in development and testing; rollback, reset and seed need `--force` in production | Accepted | Destroying data takes a deliberate step |
| D54 | Timestamps from the schema builder default to the current time; decimals scan into strings (TEXT on SQLite) | Accepted | Rows inserted by SQL get timestamps; decimals stay exact on every database |
| D55 | SQLite migrations run with foreign keys off on a dedicated connection, checked by `PRAGMA foreign_key_check` before commit | Accepted | The table-rebuild recipe SQLite needs can't cascade-delete child rows |
| D56 | Raw SQL without arguments is sent exactly as written | Accepted | Operators like jsonb `?` in migrations and SQL files need no escaping; `??` only matters with arguments |
| D57 | The schema builder refuses non-portable requests up front (NOT NULL column added without default, `DEFAULT NULL` on NOT NULL, schema-qualified names) and shortens identifiers over 63 bytes with a hash | Accepted | A migration that passes on SQLite in development must not fail on PostgreSQL in production |
| D58 | `anetos gen` reads packages with the Go type checker (`golang.org/x/tools/go/packages`, in the `cli` module) and applies the runtime's column rules, shared through `internal/naming` and checked against `db.Columns` by a fixture test | Accepted | Generation never runs user code, works while unrelated code doesn't compile, and keeps the core module free of dependencies |
| D59 | A model is a struct embedding `db.Model`/`db.Timestamps`/`db.SoftDeletes` (at any depth), with a `TableName` method, or marked `//anetos:model`; `//anetos:skip` opts out; models must be in files without build constraints | Accepted | Covers the usual models with no annotation, and plain structs explicitly; DTOs next to models aren't picked up; the output doesn't depend on the platform that runs the generator |
| D60 | Output is one `models_gen.go` per package declaring `<Model>Cols`, an anonymous struct of `db.Column[FieldType]`; the column type is the field's type, pointers included | Accepted | Godoc and go-to-definition show every column; `Set(nil)` and `Pluck` work for nullable columns (compare with `new(v)`). Only files carrying the generator's header are replaced or removed |
| D61 | JSON fields get `db.JSONCol` (values encoded as JSON, `Pluck` decodes); `Column.Of(table)` qualifies a column; `db.Columns[T]` lists a model's columns | Accepted | Generated columns must work for every kind of field, including in joins |
| D62 | The developer tool is a Go tool dependency (`go get -tool`, `go tool anetos`); generated files are exempt from the SPDX header | Accepted | Each project pins the generator version it builds with; generated code belongs to the app, not to the framework's license |
| D63 | `view.Component` is `Render(ctx, io.Writer) error`, matching `templ.Component`; `web` renders any component, buffered | Accepted | templ works with no dependency in the core; html/template and others plug in; failed renders never send half a page |
| D64 | Sessions are encrypted cookies in v0.1 (AES-256-GCM, per-message HKDF keys from `APP_KEY`, a session-specific context with the cookie name as associated data), with idle and absolute expiry inside the payload, `__Host-` names when Secure, and lazy cookie creation | Accepted | No server state to run, nothing readable or forgeable by clients, no nonce-reuse limit per key; absolute expiry bounds replay since cookies can't be revoked; server-side stores come with v0.2 drivers |
| D65 | CSRF = `http.CrossOriginProtection` plus a masked session token; failures are 403 (not Laravel's 419) | Accepted | Browser-provided origin checks stop cross-site posts; the token covers browsers without those headers; standard status codes |
| D66 | Static assets are served by a `view.Assets` the app declares, with content-hash URLs; htmx is bundled as `htmx.FS` | Accepted | Explicit (no context lookup), cacheable forever, no build step for the default stack |
| D67 | `APP_KEY` is required only by features that use it, which fail at startup with a generated suggestion; a set key must be 32 bytes of base64; `APP_PREVIOUS_KEYS` rotates keys; keys are `anetos.Secret` values that print, log and encode as `[redacted]` | Accepted | Apps without sessions don't need a key; a missing or malformed key is found at boot, not at the first request |
| D68 | Validation failures of browser form posts on routes with sessions redirect back with flashed errors and input; form posts key errors by `form` name | Accepted | Laravel's form experience; resolves O7 |
| D69 | Binary commands are registered on the `App` (`app.Command`, `app.AddCommand`) and dispatched by `app.Execute`: `run` is the default; commands run between Boot and Close unless they manage the app; exit 0/1/2; the packages that wire components register their commands | Accepted | One binary for serving, migrating and maintenance tasks, like artisan; commands get the booted app's context; no global registry |
| D70 | `anetos new` writes a fixed, working project (templ views, sessions, CSRF, migrations, assets, a test, `.env` with a key), pins templ, and runs the go commands to finish it; `--replace` points at a checkout | Accepted | A new project runs and passes its test immediately; the end-to-end test of the tool builds one against this repository |
| D71 | `anetos dev` polls for changes (no fsnotify), regenerates, rebuilds, restarts the app on a free port and serves it through a proxy on the stable address, with SSE live reload and error pages | Accepted | No extra dependency, same behaviour on every OS and editor; the browser never sees a refused connection during restarts |
| D72 | `make:*` generators write new files from embedded templates and never overwrite; customizing templates (`stub:publish`) comes later | Accepted | Generated code is plain Go the developer owns |
| D73 | `db.Connect` opens the pool at once but pings when the app boots (right away if already booted) | Accepted | Commands that don't boot (`help`) work without a database; everything that boots still fails fast |
| D74 | `anetostest.New(t, setup)` builds the app with the app's own setup function (no test-only wiring) and finds what it needs (session manager, migration runner, database) in the container, which `session.ForApp` and `migrate.ForApp` now provide to; tests read `.env.testing`, never `.env` | Accepted | Tests exercise production wiring; a test can't migrate or wipe the development database by accident |
| D75 | Test isolation: a fresh in-memory SQLite database per test by default; otherwise migrations plus a per-test transaction rolled back at the end, with a savepoint per request | Accepted | Fast and parallel on SQLite; one migrated database on a server with no cleanup code; savepoints keep a failed statement from aborting the rest of a PostgreSQL test |
| D76 | The test client calls the router in-process and behaves like a browser (cookie jar, automatic CSRF token, `Referer`), rather than disabling CSRF in tests | Accepted | Tests go through the same middleware as users; forms tests need no token scraping |
| D77 | Factories are values (`factory.New(func(n int) T)`), with states as `With(func(*T))` returning a new factory, in `db/factory` so seeders can use them; related rows are made explicitly | Accepted | Typed, no reflection or registry; shared base factories can't be mutated by a test; relations stay explicit until relation handles (v0.1.x) |
| D78 | Response assertions are chainable methods reporting with `t.Errorf`; database assertions and `Create` are generic functions (`AssertDatabaseHas[T](app, conds...)`) | Accepted | Go has no generic methods; `Errorf` shows every failed check of a request at once |
| D79 | Fakes (clock, mail, queue, events, storage) ship with their features, not in F12 | Accepted | Nothing to fake in v0.1; each fake is designed with the API it replaces |
| D80 | Pagination links come from `web.PageURL(ctx, n)` (the current URL with `page=n`, other query parameters kept), and a trailing `url.Values` argument gives named-route URLs a query string; no pagination component | Accepted | Found building the blog from the docs alone; keeps filters across pages; markup stays the app's |
| D81 | `migrate --seed` needs `--force` in production like `db:seed`; plain `migrate` never does | Accepted | Seeding production by accident was possible; migrating on deploy must stay one command |
| D82 | Every exported identifier of public packages, struct fields and interface methods included, has a doc comment, checked by `make api-docs` in CI | Accepted | The v0.1 exit criterion, kept true by a tool rather than review |
| D83 | Relations are fields with a `rel` tag (`belongs_to`, `has_one` as `*R`; `has_many`, `many_to_many` as `[]R`), with Laravel's key and pivot naming conventions and `fk`/`references`/`local`/`pivot`/`related_fk` options; keys are resolved on first use | Accepted | Declared where the data is, typed by the field; lazy resolution allows cycles (Post ↔ Comment); conventions Laravel users know |
| D84 | Eager loading runs one query per relation (and nesting level) and chunk of 1,000 parent keys, `WHERE key IN (…)` (many-to-many: related rows joined with the pivot), so each parent's rows come from one ordered query; primary-key order unless ordered; a loaded empty slice is `[]`, a missing pointer `nil`; parents sharing a key share one `*R`; relation keys are checked before the main query; `All` refuses `With`; a relation given twice is an error | Accepted | Predictable query count whatever the row count; stays under every database's parameter limit; stable output across databases |
| D85 | Relation handles are typed values, `db.Rel[T, R]`, generated in `TRels` by `anetos gen` (or `db.RelOf[T, R]("Field")`, checked at first use, so package-level variables are safe; `Err()` checks early); `With` takes `db.Relation[T]`, and nesting, conditions and order are methods on the handle | Accepted | The compiler rejects a relation of another model; no dotted strings (`"comments.author"`); no work at package initialization (a `TableName` reading configuration would run too early) |
| D86 | `WhereHas`/`WhereDoesntHave` are `EXISTS` subqueries (many-to-many through `IN (SELECT … FROM pivot)`), honoring the related model's soft deletes; a self-referencing relation aliases the inner table | Accepted | No duplicate rows and no `Distinct`; conditions use the related model's own column names |
| D87 | Pivot writes are `Attach` (idempotent, also under concurrency, through the dialect's conflict clause on the pivot's unique key), `Detach` (by ids; none is a no-op), `DetachAll` and `Sync`, each in a transaction; no pivot columns or timestamps yet. N+1 detection is deferred to v0.2 (B13) | Accepted | Covers the common many-to-many needs; an empty id list from a request can't wipe links by accident; request-scoped query tracking belongs with the v0.2 observability work |
| D88 | The cache store contract is bytes with a ttl (0 = forever) plus atomic `Add`, `Replace` (added in B2 for sessions), `Increment` (ttl only on creation; canonical decimal text; errors on non-integers and overflow) and owner-checked `DeleteIf`/`ExpireIf`; the typed API encodes values as JSON and finds the cache in the context, like `db`; keys carry `CACHE_PREFIX` (default `APP_NAME:cache:`), are UTF-8 without NUL and at most 250 bytes with it | Accepted | Small enough for any backend (memory, SQL, Redis, Memcached later) and all that locks and rate limits need; JSON is debuggable and survives restarts of mixed versions; fixed windows are what Redis `INCR`+`EXPIRE` gives; the prefix keeps `cache:clear` away from sessions and queues in a shared Redis |
| D89 | `Remember` computes once per key per process for concurrent misses (no cross-instance stampede lock), stores nothing when the function fails, and falls back to computing (with a warning) when the store fails or a stored value no longer decodes | Accepted | A cache outage or a deploy that changes a cached type must not take pages down; a distributed lock per miss would cost more than most recomputations |
| D90 | The database store uses its own connections on PostgreSQL and MySQL (a value cached or a lock taken inside a transaction stays after a rollback; `db.WithoutTx` makes that possible; it costs a second pool connection per transaction) but joins the context's transaction on SQLite, whose single writer would otherwise deadlock; keys compare exactly (MySQL `VARBINARY`: its text collations ignore case, accents or trailing spaces); expiry is Unix milliseconds on the database server's clock; `Add` is `INSERT … ON CONFLICT DO NOTHING` (MySQL `INSERT IGNORE`) plus deleting an expired row, `Increment` a `SELECT … FOR UPDATE` transaction; deadlocks and serialization failures are retried; expired rows are swept every few minutes after writes | Accepted | Locks must be visible to other instances at once and survive a rollback; on SQLite there is only one instance and one writer; one clock for every instance keeps leases exclusive; single statements and row locks stay correct under contention where compare-and-swap loops failed on MySQL |
| D91 | Locks are leases: a key holding a random owner token with a ttl, taken with `Add`, released and extended only by the owner; `Acquire` polls with exponential backoff (25ms to 1s) until the context ends; `WithLock`/`TryWithLock` release with a non-canceled context; no fencing tokens | Accepted | Works on every store with the same contract; a crashed holder can't keep a lock forever; fencing needs cooperation from the protected resource, which the scheduler and typical jobs don't have |
| D92 | Redis lives in `drivers/redis` (go-redis v9): `redis.Connect` makes one client per app from `REDIS_URL`, provides it, pings it at boot and closes it at shutdown, for the cache now and sessions, queues and pub/sub later; `anetostest` gives each test app its own `CACHE_PREFIX` and flushes it at the end | Accepted | A heavy dependency stays out of the core module; one connection pool per app; tests sharing a database or Redis cache can't see each other's items, even in parallel |
| D93 | Server-side sessions reuse the cache store contract: `SESSION_DRIVER` picks `cookie` (default), `database` or a passed driver; the cookie holds the encrypted session ID, the store holds the payload (encrypted, with its timestamps, without the ID) under a hash of the ID and `SESSION_PREFIX`; existing sessions are saved with `Replace` so a concurrent logout can't be undone; new IDs are written before the old entry is deleted; stored input is capped (64 KB, sessions 1 MB); a store read failure is a 503, a write failure is logged; no locking between concurrent requests | Accepted | One store implementation per backend for cache and sessions; revocable logins without a 4 KB limit; a leaked store or backup holds no usable session and no readable data; a failed write never loses the current session; forms can't fill the store; serving an empty session during an outage would log users out and overwrite their sessions; per-session locks cost more than rare lost writes |
| D94 | Rate limits (`web/ratelimit`) are fixed windows aligned to the clock, one atomic `cache.Increment` per limit per request, counted shortest window first and stopping at the first limit exceeded; keys hash the middleware name (or `Allow`), the key's kind and the client IP (IPv6 per /64) or `By` key; over a limit is 429 through the error handler with `Retry-After` and `X-RateLimit-*` headers; a cache failure fails the request; `Allow`/`Clear` serve login-style throttling | Accepted | Works on every cache store and across instances with a shared one; one round trip per limit; a /64 is what one IPv6 client controls; a limiter that silently switches off during an outage would make brute-force protection unreliable |
| D95 | `anetos new` projects call `cache.ForApp` and migrate the cache and sessions tables, so switching `CACHE_STORE` or `SESSION_DRIVER` to `database` is a setting, not a code change (renaming the tables with `CACHE_TABLE`/`SESSION_TABLE` also means passing the names to `Migrations`) | Accepted | Laravel creates both tables by default; the tables are small and unused until selected |
| D96 | Auth works with the app's own user type through `Authenticatable` (two methods) and `auth.Users[U]` (functions to find users and store tokens), generic over U; the session holds the ID and a password fingerprint; the user loads lazily; the core module depends on `golang.org/x/crypto` for argon2id and bcrypt | Accepted | No base model or table schema imposed; typed `auth.User[*models.User]`; a password change ends other sessions (Laravel's AuthenticateSession, built in); x/crypto is maintained by the Go team, and password hashing must be in the core for scaffolded apps |
| D97 | Password-reset and email-verification tokens are encrypted with `APP_KEY` (ID, expiry; resets add the password fingerprint) instead of stored | Accepted | Nothing to store, clean up or index; reset tokens become single-use by construction; rotating APP_KEY invalidates them, as expected |
| D98 | "Remember me" stores a random token with the user (a column) and puts it, encrypted with the ID and password fingerprint, in a second cookie; logout rotates it | Accepted | Laravel's model, revocable per user without a sessions table; the fingerprint ends remembered logins on password change |
| D99 | API tokens are `<id>\|<secret>` with a SHA-256 hash of a 240-bit secret in `api_tokens`, abilities as JSON, optional expiry, last use written at most once a minute; session-authenticated requests pass every ability | Accepted | Sanctum's design; a fast hash suffices for random secrets; lookups by primary key; bounded writes |
| D100 | Authorization is typed policies (`func(ctx, U, T) bool`) checked by generic functions returning errors with 401/403; no string gates or registry | Accepted | The compiler checks user and subject types; handlers return the error as is; nothing to register |
| D101 | Social login verifies ID tokens by issuer, audience, expiry and nonce but not by signature, because they are received from the token endpoint over TLS (OIDC Core 3.1.3.7); all endpoints must be https; state and PKCE protect the flow | Accepted | No JOSE/JWKS dependency or key-rotation handling in the core; the spec allows it for the code flow; PKCE and nonce stop code injection and replay |
| D102 | Social login doesn't create or link users itself: an app `Resolver` does, with `social_accounts` helpers; the guide and example link by provider account, and by email only when both the provider and the app verified it | Accepted | Account models differ per app; linking by unverified email allows pre-registration takeovers |
| D103 | `APP_URL` (the public base URL) joins the app config; social login needs it for redirect URIs, and mail links will | Accepted | Deriving URLs from the request's Host is unreliable behind proxies and spoofable |
| D104 | Jobs are structs with `Handle(ctx) error`, registered with `queue.Register[J]` and stored as a JSON envelope (`id` a UUIDv7, `job` the registered name, `data` the fields); the name defaults to the Go type; dispatching an unregistered type is an error and a worker fails an unknown one; dependencies come from the context, not from `Handle`'s parameters | Accepted | Typed and explicit like typed handlers; one registry lookup per job, no per-job reflection beyond decoding; JSON is debuggable, survives mixed versions and resolves O3 (no codec plug-ins); the context already carries the app's services |
| D105 | Stores lease reserved jobs: reserving moves the job's available time to the lease's end (the longest registered timeout plus 30s), adds an attempt and sets a fresh token, and `Delete`, `Release` and `Fail` act only with that token; a job reserved past its tries (its worker died during the last one) fails without running; times follow the store server's clock; the database store reserves with `FOR UPDATE SKIP LOCKED` (PostgreSQL, MySQL 8.0, MariaDB 10.6+; SQLite updates atomically), Redis with a sorted set per queue and Lua scripts; failed jobs are kept by each store | Accepted | At-least-once with one index and no sweeper: a crashed worker's job comes back by itself; the token stops a worker whose lease ran out from deleting or failing the job under the next one; no lease extension keeps the store contract small, at the cost of slower redelivery when one job type has a long timeout |
| D106 | The database store writes dispatches in the context's transaction (also with `queue.AfterCommit()`, when the transaction is on its database); other stores push at once, and `queue.AfterCommit()` defers the dispatch to `db.AfterCommit` (errors logged; returned when it runs at once, without a transaction); the sync driver runs the job once, in `Dispatch`, with the caller's context, ignoring delays and returning its error | Accepted | The database driver is a transactional outbox for free; Redis can't join a SQL transaction, so the app chooses; sync is for development and tests, where seeing the error matters more than retries |
| D107 | Retries default to 3 tries with exponential backoff (10s doubling to 10m, ±20% jitter), per type `Tries`/`Timeout`/`Backoff`; `queue.Permanent` fails at once; a job out of tries is kept as failed and its optional `Failed` method runs; workers run in `StageWorkers`, stop reserving at shutdown, give running jobs half of `APP_SHUTDOWN_TIMEOUT` (`ShutdownGrace`, capped by `Supervisor.ShutdownDeadline` minus 2s), then cancel them and put those that stop back without counting the attempt (unless they return a permanent error); failed jobs keep a sanitized error (valid UTF-8, no NUL, at most 64 KB); `queue:retry all` retries the jobs failed when it started | Accepted | Laravel's knobs with safer defaults; jitter avoids retry storms; producers (HTTP) stop before workers so their last jobs still run; a deploy doesn't use up a job's tries; a failure every store can record never leaves a job looping |
| D108 | `db.WithTestTx` marks a test's transaction: `AfterCommit` callbacks registered in it run at once, and those of a `db.Tx` directly inside it run when that commits; `anetostest` uses it, and gives each app its own `QUEUE_PREFIX`, purging its Redis keys at the end | Accepted | Tests behave like production for after-commit work (Laravel does the same), so `queue.AfterCommit()` jobs run in tests; `db.WithTx` keeps never running them, since its owner commits outside db's view |
| D109 | Events are values of any type, matched by their exact dynamic type; listeners are `func(ctx, E) error` added with `events.On`/`OnAsync`/`OnQueued[E]`; `On` listeners run in order in `Emit` and the first error stops them; emitting a type without listeners is fine, and interface types can't be listened to (an error); listener names default to the function's name | Accepted | Typed like handlers and jobs, with one map lookup per Emit; no event interface to implement; exact types keep matching predictable (no interface or embedding dispatch); events are announcements, so no listener is no error |
| D110 | Async listeners each have a bounded pool (`Concurrency`, default 1) and buffer (`Buffer`, default 1000; `Emit` waits for room until its context ends), started lazily; they get events after the transaction commits, with a fresh context of the app's values; errors and panics are logged; the bus is closed in a shutdown hook (added last in setup) that refuses new events except those async listeners emit, gives them the remaining budget, then drops what is buffered and cancels them | Accepted | A pool per listener isolates a slow listener (O4); backpressure instead of unbounded memory or silent drops; after-commit avoids acting on rolled-back changes; a shutdown hook (not a supervised component) also drains in tests and commands, which never call Run |
| D111 | Queued listeners are function jobs: `queue.RegisterFunc[T](q, name, fn)` / `DispatchFunc`, named `event:<listener>`, dispatched with `queue.AfterCommit()`; anonymous and generic functions must be named; function jobs can't take interface payloads | Accepted | Each listener gets its own tries, timeouts and failed jobs; one generic job type per event couldn't; function jobs are also useful on their own (closures over dependencies); stable names keep queued events working across deploys |

---

## 25. Open questions

| # | Question | Section | Decide by |
|---|---|---|---|
| O1 | ~~Does `web.Ctx` implementing `context.Context` cause confusion?~~ Resolved: yes it implements it, not pooled (D24) | §8.3 | Done |
| O2 | ~~Model code generation: triggered by `anetos dev` automatically or only explicitly?~~ Resolved: both. `anetos gen` (or `go generate`) explicitly, `anetos dev` on every rebuild (F11), `anetos gen -check` in CI | §10.1 | Done |
| O3 | ~~Job serialization: JSON only, or pluggable codecs (msgpack, protobuf)?~~ Resolved: JSON only (D104) | §13.4 | Done |
| O4 | ~~Should async events share one global pool or have a pool per listener?~~ Resolved: a pool per listener (D110) | §13.6 | Done |
| O5 | Plugin config: generated Go struct in the app vs loaded from the plugin's own struct only | §16.2 | B11 |
| O6 | ~~Which pure-Go SQLite implementation?~~ Resolved: modernc.org/sqlite (D38) | §10.3 | Done |
| O7 | ~~Error keys use the json name even for form posts; should HTML forms key errors by the `form` name when it differs?~~ Resolved: yes, for form posts (D68) | §9 | Done |

---

## Document history

| Date | Change |
|---|---|
| 2026-09-29 | Initial draft |
| 2026-09-30 | D16 accepted: Apache-2.0 |
| 2026-09-30 | F2–F4 implemented: §4, §5, §7, §13.3, §13.8, §23 updated; D18–D23 added |
| 2026-09-30 | D18 accepted: minimum Go 1.26 |
| 2026-09-30 | F5 implemented: §5, §8 rewritten; D3 accepted; D24–D30 added; O1 resolved |
| 2026-09-30 | F6 implemented: §9 rewritten; principle 4 and D12 clarified; D31–D37 added; O7 opened |
| 2026-09-30 | F7 implemented: §5 and §10 rewritten; D5, D10 updated; D38–D50 added; O6 resolved |
| 2026-09-30 | F8 implemented: §11 rewritten; D51–D57 added; factories moved to F12 |
| 2026-09-30 | F9 implemented: §5, §10.1, §17.1 updated; D42 updated; D58–D62 added; O2 resolved; relation handles moved to v0.1.x |
| 2026-09-30 | F10 implemented: §5, §8.6, §9, §12.1, §17.1, §20 updated; D63–D68 added; O7 resolved |
| 2026-09-30 | F11 implemented: §4, §5, §6, §17 updated; D69–D73 added |
| 2026-09-30 | F12 implemented: §5, §11, §18 rewritten; D74–D79 added |
| 2026-09-30 | v0.1 release checks: §7, §11, §12.1, §17.1, §18 corrected against the code; D80–D82 added |
| 2026-09-30 | v0.1.1 relations: §10, §17.1 updated; D83–D87 added |
| 2026-09-30 | B1 cache implemented: §5, §13.7, §14 updated, §14.1 added; D88–D92 added |
| 2026-09-30 | B2 sessions and rate limiting implemented: §8.6, §12.1, §14 updated; D93–D95 added |
| 2026-09-30 | B3 authentication and authorization implemented: §5, §14, §15 updated; D96–D100 added; scaffolding moved to B14 |
| 2026-10-01 | B4 social login implemented: §5, §7 (APP_URL), §15 updated; D101–D103 added |
| 2026-10-01 | B5 queue implemented: §4, §5, §13.4, §14, §18 updated; D104–D108 added; O3 resolved |
| 2026-10-01 | B6 events implemented: §5, §13.4, §13.6 updated; D109–D111 added; O4 resolved |

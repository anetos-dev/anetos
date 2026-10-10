---
title: Glossary
since: v0.5.0
group: "The app"
weight: 99
---

# Glossary

The words these pages use, what each means in Anetos, and the word
Laravel or Rails uses when it's a different one. Anetos uses the word
Go developers already know for Go-shaped things, and Laravel's or Rails'
for framework concepts. A new word appears only for a new idea, and is
listed here.

## The app

| Word | In Anetos | Elsewhere |
|---|---|---|
| **App** | The `*anetos.App` that `anetos.New()` returns. It holds the settings, the logger, the services, the commands and the background components | Laravel's application |
| **`setup`** | The function in your `main.go` that builds the app's services in order (`db.Connect`, `cache.New`, `queue.New`… `web.NewServer`) and registers routes and plugins. See [Application lifecycle](application-lifecycle.md#setting-up-the-services) | Laravel's service providers and `config/*.php`; Rails' initializers |
| **Service** | Something `setup` builds from the settings, such as the cache, the queue or the mailer. `New(app)` builds one, and `Connect` builds a connection to a server (`db.Connect`, `redis.Connect`). Handlers and jobs get a service from their context: `cache.From(ctx)` | A binding in Laravel's container, reached through a facade |
| **Setting** | A value read from `.env` or the environment, named after its area: `DB_DRIVER`, `CACHE_PREFIX`. See the [configuration reference](../reference/configuration.md) | Laravel's `.env` keys, without the `config/` files |
| **Operation** | One request, queue job, async or queued event listener call, pub/sub message, scheduled task or AI tool call (`anetos.Operation`). Per-operation things, such as repeated-query detection, are counted within one. See [Operations](application-lifecycle.md#operations) | "Per request or per job" |
| **Background component** | Long-running work that the supervisor runs inside the app's process: the HTTP server, queue workers, pub/sub listeners, the scheduler, and tasks started with `app.Go`. It is not a UI component (see Views). See [Runtime supervisor](runtime-supervisor.md#components) | What Supervisor or Horizon runs as separate programs |
| **Supervisor** | The part of the app that starts, restarts and stops the background components | The Supervisor process manager, inside the binary |
| **Process type** | `web`, `worker`, `scheduler` or `listener`: which background components a process runs (`./app run --only=worker`). A process runs all of them by default. See [Runtime supervisor](runtime-supervisor.md#process-types-one-binary-many-shapes) | Heroku's process types; Laravel runs `queue:work` and `schedule:work` instead |
| **Carrier** | A context value that moves with a queued job or an async event listener, such as the audit log's actor (who asked for the work): an `anetos.Carrier` with `Capture` and `Restore`. See [Write a plugin](../guides/writing-plugins.md#4-add-jobs-tasks-listeners-and-commands) | Laravel's `Context` (dehydrated into jobs); OpenTelemetry calls it a propagator |
| **Doctor, finding** | `doctor` checks the settings and the project. Each result is a finding with a severity: note, warning or problem. See [the doctor command](../reference/cli.md#the-doctor-command) | |
| **Provider** | A piece of your own functionality with a `Register` and a `Boot` phase (`anetos.Provider`), added with `app.Use`. The framework's services don't need one: `setup` builds them. See [Providers](application-lifecycle.md#providers) | Laravel's service provider |

## Web

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Handler** | A function that answers a route. It can be a plain handler, `func(c *web.Ctx) error`; a typed handler; or any `http.Handler`, through `web.WrapHandler`. See [Handlers and requests](../guides/handlers.md) | A controller action |
| **Typed handler, `web.H`** | A handler that takes its input as a struct and returns its output: `func(c *web.Ctx, in CreateNote) (Note, error)`. `web.H` (H for handler) turns it into a route's handler and binds and validates the input. It has nothing to do with Gin's `gin.H` map. See [Write a typed handler](../guides/handlers.md#2-write-a-typed-handler) | A controller action with a form request |
| **Responder** | A value that writes its own response (`web.Responder`): `web.Created(v)`, `web.Redirect(url)`, `web.Render(page)` | Laravel's `Responsable` |
| **Route name** | `.Name("notes.show")`, used to build URLs (`web.URL`, `web.RedirectRoute`) | The same as Laravel's |
| **CRUD actions** | `index`, `new`, `create`, `show`, `edit`, `update`, `delete`. The route name and the handler match: `notes.create` is `Create`, which saves the form that `New` shows | Rails' names, except `destroy`; Laravel says `create` for the form, `store` and `destroy` |
| **Stack** | What `anetos new` writes: `web` (pages, sessions, CSRF; the default) or `api` (JSON only). See [`anetos new`](../reference/cli.md#anetos-new-directory) | Laravel Breeze's stacks; Rails' `--api` |
| **Middleware** | `func(http.Handler) http.Handler`, on a router or a group | The same idea as Laravel's |
| **Rule** | A named check in a `validate` tag (`validate:"required\|max:200"`); your own are added with `validate.Register`. See the [validation rules reference](../reference/validation-rules.md) | Laravel's validation rules, with the same names |

## Data

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Model** | A struct mapped to a table, embedding `db.Model`. You save it with functions such as `db.Create(ctx, &post)`, not with methods on the model | Eloquent or Active Record model |
| **Typed columns** | The values `anetos generate` writes for each model (`PostCols.Title`), which conditions are built from, so the compiler checks column names and types. They aren't SQL's generated columns. See [Generate typed columns](../guides/code-generation.md) | |
| **Scope (query)** | A reusable `func(*db.Q[T]) *db.Q[T]`, applied with `.Scope(Popular)` | Laravel's local scopes |
| **Dialect** | The SQL flavor of a database: placeholders, quoting, upserts | Laravel's grammar |
| **Soft deletes, trashed** | `db.SoftDeletes` adds `deleted_at`. A deleted row is trashed: queries skip it unless you call `WithTrashed`. `Restore` brings it back and `ForceDelete` removes it | The same as Laravel's |
| **Prunable** | `db.Prunable[T](app, after)` registers a soft-deleting model: `db:prune-trashed` then deletes for good its rows trashed more than `after` ago | Close to Laravel's `Prunable`, which prunes any query you define |
| **Repeated queries** | The same query run again and again in one operation, which is likely an N+1. The app warns about it unless `db.AllowRepeatedQueries(ctx)` allows it. See [Find N+1 queries](../guides/n-plus-one.md) | Laravel's `preventLazyLoading`; Rails' Bullet gem |

## Services and their drivers

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Driver** | The backend a service uses, picked with the area's `_DRIVER` setting (`CACHE_DRIVER=redis`), and the Go value that provides it (`cache.DatabaseDriver()`, `redis.CacheDriver()`). The AI's is `AI_PROVIDER` | Laravel's `CACHE_STORE`, `QUEUE_CONNECTION`, `MAIL_MAILER`… |
| **Store, backend, transport, broker** | The interface a driver implements: `cache.Store` and `queue.Store`, `storage.Backend`, `mailer.Transport`, `pubsub.Broker`, `ai.Provider` | Laravel's contracts |
| **Disk** | A named place files are kept, such as a folder or an S3 or Google Cloud Storage bucket. `storage.From(ctx)` is the default disk and `storage.DiskFrom(ctx, name)` another. See [Store files](../guides/storage.md) | The same as Laravel's |
| **Job, worker** | A typed piece of work sent to a queue, and the background component that runs jobs. See [Queues](../guides/queues.md) | The same as Laravel's |
| **Event, event listener** | A typed message inside the app (`events.Emit`), and a function that handles it, run right away, asynchronously or queued. See [Events](../guides/events.md) | The same as Laravel's |
| **Pub/sub listener** | A background component that reads a topic of an external broker. See [Pub/sub listeners](../guides/pubsub.md) | |

## Accounts and security

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Log in, log out** | The one pair of words, in code (`Login`, `/login`) and in the pages | Laravel's words; Rails (Devise, the Rails 8 generator) says sign in |
| **Permission, role** | A permission names something a user may do (`posts.edit`). A role is a named set of permissions. You assign a role to a user, or grant a permission directly. See [Roles and permissions](../guides/roles-and-permissions.md) | spatie/laravel-permission's words |
| **Scope (roles)** | Where a role or a permission applies, such as one team (`rbac.ScopeOf("team", id)`), or everywhere (`rbac.Global`) | Teams in spatie/laravel-permission; scopes in Bouncer |
| **Ability** | What an API token may do (names such as `posts:read`; `*` for all), checked with `auth.TokenCan` | Sanctum's abilities |
| **Impersonate** | An admin using the app as another user, from the admin panel. See [Impersonate a user](../guides/admin.md#6-impersonate-a-user) | lab404/laravel-impersonate |
| **`ActingAs`** | In tests, `anetostest.ActingAs(app, u)` logs `u` in for the requests that follow | Laravel's `actingAs` |
| **Audit log** | The record of who did what (`audit.Trail`). See [Keep an audit log](../guides/audit-log.md) | |

## Views

| Word | In Anetos | Elsewhere |
|---|---|---|
| **templ** | The template language of the pages: `.templ` files compiled to Go functions, type-checked like the rest of the app. See [Render HTML with templ](../guides/views.md) | Blade; Rails' ERB |
| **Component (UI)** | A templ component (`view.Component`): a page, or a piece of one. The shared ones, which the CSS framework styles, are in your project's `views/ui` (`@ui.Button(…)`). See the [UI components reference](../reference/ui.md) | Blade components |
| **CSS framework** | What `--css` picks: the starter theme, `none`, Pico, Bootstrap, Bulma or Tailwind CSS, with the matching `views/ui` components. `css:use` switches. See [Style your app](../guides/styling.md) | `rails new --css`. Not Laravel's starter kits, which scaffold accounts |
| **Starter theme** | The default CSS framework (`--css=anetos`): light and dark, no build step. See [The starter theme](../guides/starter-theme.md) | |
| **Variant, size, tone** | A component's options: a variant is its kind (`ui.Primary`, `ui.Secondary`), a size changes it (`ui.Small`, `ui.FullWidth`), and a tone is the color meaning of a badge or a message (`ui.Success`, `ui.Error`) | Bootstrap's and shadcn's variant and size; Polaris' tone |
| **Catalog** | One locale's translations, in YAML files under `locales/`. See [Translations](../guides/translations.md) | Laravel's `lang/` files |

## Plugins

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Plugin** | A Go module that adds routes, tables, jobs, commands or settings to an app (`anetos add`). Its package is `ext`, because Go's standard library has a package named `plugin`. See [Use plugins](../guides/plugins.md) | A Laravel package with a service provider |

## AI

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Provider** (AI) | The AI service a client calls: Anthropic, OpenAI or Gemini, picked with `AI_PROVIDER` | The AI SDKs' word |
| **Tool** | A function the model may ask the app to run (`ai.NewTool`), which runs as the user who asked. See [Give the model tools](../guides/ai.md#4-give-the-model-tools) | The AI SDKs' word |
| **Agent** | Instructions, a model and tools kept together (`ai.Agent`), for an assistant. See [Build an AI assistant](../guides/ai-assistant.md) | The AI SDKs' word |
| **Embedding** | A vector of numbers that stands for a text's meaning (`ai.Embed`), stored with the rows to search by meaning. See [Search by meaning](../guides/semantic-search.md) | The same in every RAG library |

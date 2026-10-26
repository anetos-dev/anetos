---
title: Glossary
since: v0.5.0
group: "The app"
weight: 99
---

# Glossary

The words these pages use, what each means in Anetos, and the words
other frameworks use for the same thing when they differ. Anetos uses
the word Go developers already know for Go-shaped things, and the word
web frameworks share for framework concepts. A new word appears only for
a new idea, and is listed here.

## The app

| Word | In Anetos | Elsewhere |
|---|---|---|
| **App** | The `*anetos.App` that `anetos.New()` returns. It holds the settings, the logger, the services, the commands and the background components | Django's project, Laravel's application, Rails' application |
| **`setup`** | The function in your `main.go` that builds the app's services in order (`db.Connect`, `cache.New`, `queue.New`… `web.NewServer`) and registers routes and plugins. See [Application lifecycle](application-lifecycle.md#setting-up-the-services) | Django's settings and `AppConfig.ready()`, Laravel's service providers, Rails' initializers |
| **Service** | Something `setup` builds from the settings, such as the cache, the queue or the mailer. `New(app)` builds one, and `Connect` builds a connection to a server (`db.Connect`, `redis.Connect`). Handlers and jobs get a service from their context: `cache.From(ctx)` | A service in a dependency-injection container |
| **Setting** | A value read from `.env` or the environment, named after its area: `DB_DRIVER`, `CACHE_PREFIX`. See the [configuration reference](../reference/configuration.md) | Environment variables, read into Django's `settings.py`, Laravel's `config/` files or Rails' `config/` |
| **Operation** | One request, queue job, async or queued event listener call, pub/sub message, scheduled task or AI tool call (`anetos.Operation`). Per-operation things, such as repeated-query detection, are counted within one. See [Operations](application-lifecycle.md#operations) | "Per request or per job" |
| **Background component** | Long-running work that the supervisor runs inside the app's process: the HTTP server, queue workers, pub/sub listeners, the scheduler, and tasks started with `app.Go`. It is not a UI component (see Views). See [Runtime supervisor](runtime-supervisor.md#components) | The worker, scheduler and listener processes other frameworks run as separate programs |
| **Supervisor** | The part of the app that starts, restarts and stops the background components | A process manager (systemd, Supervisor), inside the binary |
| **Process type** | `web`, `worker`, `scheduler` or `listener`: which background components a process runs (`./app run --only=worker`). A process runs all of them by default. See [Runtime supervisor](runtime-supervisor.md#process-types-one-binary-many-shapes) | Heroku's process types (a Procfile's `web` and `worker`) |
| **Carrier** | A context value that moves with a queued job or an async event listener, such as the audit log's actor (who asked for the work): an `anetos.Carrier` with `Capture` and `Restore`. See [Write a plugin](../guides/writing-plugins.md#4-add-jobs-tasks-listeners-and-commands) | Context propagated into jobs; OpenTelemetry calls it a propagator |
| **Doctor, finding** | `doctor` checks the settings and the project. Each result is a finding with a severity: note, warning or problem. See [the doctor command](../reference/cli.md#the-doctor-command) | |
| **Security check, security rule** | `security:check` checks the app's code, dependencies, routes, settings and (with `--url`) running site against security rules (`source.sql`, `route.csrf`, `setting.session-secure`), each finding with its OWASP Top 10 category; findings kept on purpose go in `security-ignore.yaml` with a reason. See [Check your app's security](../guides/security-check.md) | Django's `check --deploy`; Brakeman (Rails) and gosec read the code instead |
| **Coding agent** | A tool that writes code in your project with you: Claude Code, Codex, Cursor, Gemini CLI, GitHub Copilot. Not an AI agent of your app (see Agent, under AI). See [Build with a coding agent](../getting-started/coding-agents.md) | |
| **Agent files, skill** | What coding agents read in a project: `AGENTS.md`, the instructions, and skills, task guides (`SKILL.md`) an agent loads only when a task needs one. `anetos new` and `agent:install` write them | AGENTS.md and Agent Skills are open formats many agents share |
| **MCP server** | A program a coding agent starts to call its tools, through the Model Context Protocol. `go tool anetos mcp` is the project's: the docs, the app's routes, models and schema, and `check`, as tools. See the [reference](../reference/cli.md#anetos-mcp---dirproject) | An open protocol most coding agents speak |
| **llms.txt** | A site's list of its pages for language models and coding agents, linking to each page's Markdown. docs.anetos.dev has one per version, and `llms-full.txt` with every page. See [Build with a coding agent](../getting-started/coding-agents.md#how-it-works) | An open proposal many documentation sites follow |
| **Check** | `go tool anetos check`: generate, the frontend's build (with a `frontend/`), build, vet, test, doctor and the security check's basic rules, stopping at the first failure; what you or an agent runs before calling a change done. See the [reference](../reference/cli.md#anetos-check---short---json) | |
| **Provider** | A piece of your own functionality with a `Register` and a `Boot` phase (`anetos.Provider`), added with `app.Use`. The framework's services don't need one: `setup` builds them. See [Providers](application-lifecycle.md#providers) | Django's `AppConfig`, Laravel's service provider, Rails' initializers |

## Web

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Handler** | A function that answers a route. It can be a plain handler, `func(c *web.Ctx) error`; a typed handler; or any `http.Handler`, through `web.WrapHandler`. See [Handlers and requests](../guides/handlers.md) | A view (Django) or a controller action (Laravel, Rails) |
| **Typed handler, `web.H`** | A handler that takes its input as a struct and returns its output: `func(c *web.Ctx, in CreateNote) (Note, error)`. `web.H` (H for handler) turns it into a route's handler and binds and validates the input. It has nothing to do with Gin's `gin.H` map. See [Write a typed handler](../guides/handlers.md#2-write-a-typed-handler) | A view or controller action with a form or form request |
| **Responder** | A value that writes its own response (`web.Responder`): `web.Created(v)`, `web.Redirect(url)`, `web.Render(page)` | A response object (Django's `HttpResponse`, Laravel's `Responsable`) |
| **Route name** | `.Name("notes.show")`, used to build URLs (`web.URL`, `web.RedirectRoute`) | Named routes: Django's `name=`, Laravel's `->name()`, Rails' `as:` |
| **CRUD actions** | `index`, `new`, `create`, `show`, `edit`, `update`, `delete`. The route name and the handler match: `notes.create` is `Create`, which saves the form that `New` shows | Rails' and Phoenix's names, except Rails' `destroy`; Laravel says `create` for the form, `store` and `destroy` |
| **Stack** | What `anetos new` writes: `web` (pages, sessions, CSRF; the default) or `api` (JSON only). See [`anetos new`](../reference/cli.md#anetos-new-directory) | Laravel's starter kits' stacks; Rails' `--api` |
| **Middleware** | `func(http.Handler) http.Handler`, on a router or a group | The same idea in Django, Laravel and Rails |
| **Rule** | A named check in a `validate` tag (`validate:"required\|max:200"`); your own are added with `validate.Register`. See the [validation rules reference](../reference/validation-rules.md) | Validators (Django), validation rules (Laravel), validations (Rails) |

## Data

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Model** | A struct mapped to a table, embedding `db.Model`. You save it with functions such as `db.Create(ctx, &post)`, not with methods on the model | Django's `Model`, Eloquent, Active Record |
| **Typed columns** | The values `anetos generate` writes for each model (`PostCols.Title`), which conditions are built from, so the compiler checks column names and types. They aren't SQL's generated columns. See [Generate typed columns](../guides/code-generation.md) | |
| **Scope (query)** | A reusable `func(*db.Q[T]) *db.Q[T]`, applied with `.Scope(Popular)` | Django's custom querysets, Laravel's local scopes, Rails' scopes |
| **Dialect** | The SQL flavor of a database: placeholders, quoting, upserts | A database backend (Django), a grammar (Laravel), an adapter (Rails) |
| **Soft deletes, trashed** | `db.SoftDeletes` adds `deleted_at`. A deleted row is trashed: queries skip it unless you call `WithTrashed`. `Restore` brings it back and `ForceDelete` removes it | Laravel's soft deletes; Rails' discard and paranoia gems |
| **Prunable** | `db.Prunable[T](app, after)` registers a soft-deleting model: `db:prune-trashed` then deletes for good its rows trashed more than `after` ago | A cleanup task run on a schedule (Laravel's `Prunable` takes any query; Django and Rails leave it to a command or a recurring job) |
| **Repeated queries** | The same query run again and again in one operation, which is likely an N+1. The app warns about it unless `db.AllowRepeatedQueries(ctx)` allows it. See [Find N+1 queries](../guides/n-plus-one.md) | Django's nplusone, Laravel's `preventLazyLoading`, Rails' `strict_loading` and Bullet |

## Services and their drivers

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Driver** | The backend a service uses, picked with the area's `_DRIVER` setting (`CACHE_DRIVER=redis`), and the Go value that provides it (`cache.DatabaseDriver()`, `redis.CacheDriver()`). The AI's is `AI_PROVIDER` | A backend: Django's `CACHES` backends, Laravel's stores and connections, Rails' cache stores and adapters |
| **Store, backend, transport, broker** | The interface a driver implements: `cache.Store` and `queue.Store`, `storage.Backend`, `mailer.Transport`, `pubsub.Broker`, `ai.Provider` | Contracts, adapters or backends |
| **Disk** | A named place files are kept, such as a folder or an S3 or Google Cloud Storage bucket. `storage.From(ctx)` is the default disk and `storage.DiskFrom(ctx, name)` another. See [Store files](../guides/storage.md) | Django's storages, Laravel's disks, Rails' Active Storage services |
| **Job, worker** | A typed piece of work sent to a queue, and the background component that runs jobs. See [Queues](../guides/queues.md) | A Celery task and worker (Django), a queued job (Laravel), an Active Job (Rails) |
| **Event, event listener** | A typed message inside the app (`events.Emit`), and a function that handles it, run right away, asynchronously or queued. See [Events](../guides/events.md) | Django's signals and receivers, Laravel's events and listeners, Rails' notifications and subscribers |
| **Pub/sub listener** | A background component that reads a topic of an external broker. See [Pub/sub listeners](../guides/pubsub.md) | |

## Accounts and security

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Log in, log out** | The one pair of words, in code (`Login`, `/login`) and in the pages | Django's and Laravel's words, and Devise's pages'; Rails' authentication generator (and Devise's helpers) say sign in |
| **Permission, role** | A permission names something a user may do (`posts.edit`). A role is a named set of permissions. You assign a role to a user, or grant a permission directly. See [Roles and permissions](../guides/roles-and-permissions.md) | Django's permissions and groups; spatie/laravel-permission's words; Rolify's roles (Rails) |
| **Scope (roles)** | Where a role or a permission applies, such as one team (`rbac.ScopeOf("team", id)`), or everywhere (`rbac.Global`) | Teams or resources in role libraries (spatie/laravel-permission's teams, Rolify's resource roles) |
| **Ability** | What an API token may do (names such as `posts:read`; `*` for all), checked with `auth.TokenCan` | A token's scopes or abilities (OAuth scopes, Sanctum's abilities) |
| **Impersonate** | An admin using the app as another user, from the admin panel. See [Impersonate a user](../guides/admin.md#6-impersonate-a-user) | Impersonation in admin panels (Filament, Nova, django-hijack) |
| **`ActingAs`** | In tests, `anetostest.ActingAs(app, u)` logs `u` in for the requests that follow | Django's `force_login`, Laravel's `actingAs`, Devise's `sign_in` (Rails) |
| **Audit log** | The record of who did what (`audit.Trail`). See [Keep an audit log](../guides/audit-log.md) | |

## Views

| Word | In Anetos | Elsewhere |
|---|---|---|
| **templ** | The template language of the pages: `.templ` files compiled to Go functions, type-checked like the rest of the app. See [Render HTML with templ](../guides/views.md) | Django templates, Blade, ERB |
| **Component (UI)** | A templ component (`view.Component`): a page, or a piece of one. The shared ones, which the CSS framework styles, are in your project's `views/ui` (`@ui.Button(…)`). See the [UI components reference](../reference/ui.md) | Template components (Blade components, ViewComponent) |
| **CSS framework** | What `--css` picks: the starter theme, `none`, Pico, Bootstrap, Bulma or Tailwind CSS, with the matching `views/ui` components. `css:use` switches. See [Style your app](../guides/styling.md) | A `--css` option when creating a project; not a starter kit, which scaffolds accounts |
| **Starter theme** | The default CSS framework (`--css=anetos`): light and dark, no build step. See [The starter theme](../guides/starter-theme.md) | |
| **Variant, size, tone** | A component's options: a variant is its kind (`ui.Primary`, `ui.Secondary`), a size changes it (`ui.Small`, `ui.FullWidth`), and a tone is the color meaning of a badge or a message (`ui.Success`, `ui.Error`) | Bootstrap's and shadcn's variant and size; Polaris' tone |
| **Frontend** | The project's `frontend/` folder (since v0.6): scripts and styles that Vite builds into `public/build`, which the binary embeds. See [Bundle JavaScript and CSS with Vite](../guides/vite.md) | Django's static files with a bundler, Laravel's `resources/js` with Vite, Rails' `app/javascript` with jsbundling or vite_ruby |
| **Entry** | A file of the frontend that a page loads (`src/main.ts`), listed in `vite.config`'s inputs and loaded with `@vite.Tags`; Vite bundles what it imports | Vite's and webpack's entry point |
| **Hot reload** | In development, Vite replacing a changed module or stylesheet in the open page without reloading it (hot module replacement, HMR); `anetos dev` reloads the page for Go and templ changes | HMR |
| **Inertia page** | A page written as a Vue, React or Svelte component, which Inertia's client renders from the props a Go handler sends (`inertia.NewPage`, since v0.6); links and forms visit the server without reloading the page. See [Pages in Vue, React or Svelte with Inertia](../guides/inertia.md) | Inertia's pages, with the adapters for Django, Laravel or Rails |
| **Prop** | A value an Inertia page's component gets: a field of the page's props struct, named by its `json` tag. An `inertia.Prop[T]` field is loaded as it says: computed only when sent, after the page shows (deferred), only when asked for (optional), merged into the value the page holds, or once | A component's props (React, Svelte, Vue) |
| **Shared props** | Props every Inertia page gets (`inertia.WithShared`), such as the app's name or the logged-in user | Inertia's shared data |
| **Root view** | The templ component of an Inertia app's first visit: it loads the frontend and places `@inertia.App()`, which holds the page | Inertia's root template |
| **Generated types** | The TypeScript types a frontend imports, written from the app's Go types by its `types` command (`typescript.Register`, since v0.6): the typed handlers' inputs and results, a client with a function per API route, and the props of the Inertia pages `inertia.WithPages` lists. See [Generate TypeScript types for the frontend](../guides/typescript.md) | Generated API clients (OpenAPI generators, tRPC); types shared between server and client |
| **First-party SPA** | The app's own single-page app, at an origin listed in `HTTP_SPA_ORIGINS` (since v0.6): it logs in to the API with a session cookie and CSRF protection, where other clients use tokens. See [Log in from your SPA](../guides/spa-login.md) | Cookie-based SPA authentication (Django, Laravel, Rails) |
| **Catalog** | One locale's translations, in YAML files under `locales/`. See [Translations](../guides/translations.md) | Message files: Django's `.po` files, Laravel's `lang/`, Rails' `config/locales` |

## Plugins

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Plugin** | A Go module that adds routes, tables, jobs, commands or settings to an app (`anetos add`). Its package is `ext`, because Go's standard library has a package named `plugin`. See [Use plugins](../guides/plugins.md) | A Django app, a Laravel package, a Rails engine or gem |

## AI

| Word | In Anetos | Elsewhere |
|---|---|---|
| **Provider** (AI) | The AI service a client calls: Anthropic, OpenAI or Gemini, picked with `AI_PROVIDER` | The AI SDKs' word |
| **Tool** | A function the model may ask the app to run (`ai.NewTool`), which runs as the user who asked. See [Give the model tools](../guides/ai.md#4-give-the-model-tools) | The AI SDKs' word |
| **Agent** | Instructions, a model and tools kept together (`ai.Agent`), for an assistant in your app (`make:agent`); not a coding agent. See [Build an AI assistant](../guides/ai-assistant.md) | The AI SDKs' word |
| **Embedding** | A vector of numbers that stands for a text's meaning (`ai.Embed`), stored with the rows to search by meaning. See [Search by meaning](../guides/semantic-search.md) | The same in every RAG library |

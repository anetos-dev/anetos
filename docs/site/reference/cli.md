---
title: anetos tool and app commands reference
since: v0.1.0
group: "Tools"
weight: 100
---

# `anetos` tool and app commands reference

Two command lines: the `anetos` developer tool (module
`anetos.dev/anetos/cli`), which creates projects and writes
code, and your application's binary, which runs the app and its
commands. See [Commands](../guides/commands.md) and
[Getting started](../getting-started/README.md) for walkthroughs.

## Installing the tool

| Command | Use |
|---|---|
| `go install anetos.dev/anetos/cli/cmd/anetos@latest` | For `anetos new`, before you have a project |
| `go get -tool anetos.dev/anetos/cli/cmd/anetos@latest` | In a project: pins the version in `go.mod`; run it as `go tool anetos` (projects made by `anetos new` have it) |

Flags may come before or after the other arguments. `anetos help
<command>` (or `anetos <command> -h`) shows a command's usage. Exit
status: 0 on success, 1 on errors, 2 for bad usage.

## `anetos new <directory>`

| Flag | Default | Meaning |
|---|---|---|
| `--module` | the directory's name | Go module path |
| `--db` | `sqlite` | `sqlite`, `postgres` or `mysql`: the driver in `main.go` and the `DB_*` settings in `.env` |
| `--stack` | `web` | `web`: pages rendered on the server, with sessions and CSRF protection; `api` (v0.4): JSON only, no views, static files or sessions ([the API project](#the-api-project)) |
| `--css` | `anetos` | The web stack's design kit (v0.3): the components of `views/ui` (v0.5) and the stylesheet, `public/static/app.css`. `anetos`: the starter theme (light and dark, no build step) and components writing its classes; `none`: components writing plain HTML without classes, and a stylesheet holding only a comment, for your own CSS or a CSS framework (before v0.5, the pages kept the starter theme's classes); `pico`, `bootstrap`, `bulma` (v0.5): components writing Pico 2.1.1's, Bootstrap 5.3.8's or Bulma 1.0.4's markup, with the framework's files as released in `public/static/` (and its license), and an `app.css` for the rest; `tailwind` (v0.5): components with Tailwind CSS 4.3.3's classes, `views/ui/tailwind.css` and the `app.css` Tailwind compiles from it (`css:build`). See [Style your app](../guides/styling.md). Refused with `--stack=api` |
| `--skip-install` | `false` | Only write the files |
| `--replace` | | A local Anetos checkout, used through `replace` directives (framework development): the core, the tool, and every driver and plugin module of the checkout, so `go get` and `anetos add` take them from it too |

The directory must not exist or be empty; its name (letters, digits, `-`
and `_`, starting with a letter) becomes the app's name. `anetos new .`
uses the current directory. It writes the project, then runs `go get` for
Anetos and the driver (latest) and the `anetos` tool (its own version),
pinned as tools with templ, then `go tool templ generate` and
`go mod tidy` (the api stack has no templ, and runs `go run . openapi`
last, writing `openapi.json`). With `--db=postgres` or
`mysql`, create the database before `migrate`.

The web stack's files:

| Path | Holds |
|---|---|
| `main.go` | `anetos.New`, `i18n.New` (the catalogs in `locales/`), `db.Connect`, `migrate.New`, `cache.New`, `queue.New` (with workers), `events.New`, `mailer.New`, `storage.New`, `schedule.New`, `web.NewServer`, `session.New`, `routes.Register`, then `ext.Load(app, plugins())`, and `app.Execute()`; `//go:generate` lines for templ and `anetos gen` |
| `plugins.go` | The plugins, written by `anetos add` and `anetos remove` (an empty list at first) |
| `main_test.go` | Tests requesting the home page and a missing page with `anetostest` |
| `.env`, `.env.example` | Settings; `.env` has a fresh `APP_KEY` (file mode 0600) and stays out of git |
| `.env.testing` | PostgreSQL and MySQL projects: the test database's settings (`<name>_test`) |
| `README.md`, `.gitignore` | How to run it; what stays out of git |
| `routes/web.go` | Routes: the error pages (`ErrorPages` with `views.ErrorPage`), assets, and the page group with sessions and CSRF (`pages`), which `make:crud` adds its routes to |
| `app/handlers/home.go` | The home page handler |
| `app/models/` | Models (empty at first) |
| `database/migrations/migrations.go` | The `All` migration set and `Seeders` |
| `database/factories/factories.go` | The package for model factories (empty at first) |
| `views/layout.templ`, `views/home.templ` | templ layout (`<html lang dir>` in the request's locale, `hreflang` links with `LOCALE_URL=prefix` or `subdomain`, the header with the app's name and its nav (`ui.Header`, `ui.Nav`, and `navLink`, which marks the current page with `web.RouteIs`), flash messages, CSRF header for htmx) and home page, its text from the catalog; `views/errors.templ`: error pages (404, 500…) in the layout |
| `views/ui/` | The design kit's components (v0.5), which the layout and the pages of `make:crud` and `make:auth` call: `ui.go` (the types: `Look`, `Tone`, `Option`, `Pages`), `shell.templ`, `page.templ`, `form.templ`, `data.templ`, and with every kit but `none`, `classes.go` (the classes of each look and tone), and `kit.json`, the kit's record (`css:use`). See the [UI components reference](ui.md) |
| `locales/locales.go`, `locales/en/app.yaml` | The translations, embedded: the home page's English text. Add a language with its folder (`locales/bn/app.yaml`); see [Translations](../guides/translations.md) |
| `public/public.go`, `public/static/app.css`, `public/static/favicon.svg` | `public.Assets`: the static files and htmx under `/assets`; the stylesheet (`--css`; a CSS framework's kit adds the framework's files); the icon browsers show |
| `Dockerfile`, `.dockerignore` | A container image (v0.3): `anetos build` in `golang:<go version>`, the binary alone in `gcr.io/distroless/static-debian12:nonroot` (user 65532), `/data` for the files (and the SQLite database, migrated when the server starts: `MIGRATE_ON_RUN=true`), `HEALTHCHECK` with `health:check`. See [Deploy](../guides/deployment.md#run-it-in-a-container) |
| `deploy/<name>.service`, `deploy/production.env.example` | A systemd unit (v0.3: migrations before start, restart on failure, sandboxed) and the production settings to fill in. See [Deploy](../guides/deployment.md#run-it-on-a-server-with-systemd) |

### The API project

`--stack=api` (v0.4) writes the same `main.go` without sessions or
templ, `plugins.go`, `.env` files, `README.md`, `.gitignore`,
`app/models/`, `database/`, `Dockerfile`, `.dockerignore` and
`deploy/`, and in place of the web stack's routes, handlers, views and
static files:

| Path | Holds |
|---|---|
| `routes/api.go` | `Register(r)`: `r.UseGlobal(web.JSONErrors)` (every error is problem JSON, whatever the client accepts; [Return errors](../guides/handlers.md#json-errors)) and the group `api`, `/api/v1` with route names `api.…`, where `make:handler` suggests routing its handlers; `OpenAPI`, the API's description's `openapi.Config` (v0.4) |
| `app/handlers/welcome.go` | `GET /api/v1` (`api.welcome`): a typed handler (`web.H`) answering `WelcomeResponse`, `{"name", "message"}`, the message from the catalog in the request's language |
| `locales/en/app.yaml` | The API's English messages |
| `main_test.go` | Tests of `GET /api/v1`, of a missing URL's problem details, and that `openapi.json` is up to date (`openapi.Check`) |
| `openapi.json` | The API's OpenAPI 3.1 description, written by `go run . openapi` (not with `--skip-install`) and served at `/api/v1/openapi.json`; `main.go` calls `openapi.Register`. See [Describe an API with OpenAPI](../guides/openapi.md) |

`.env`, `.env.example` and `deploy/production.env.example` have
`HTTP_CORS_ORIGINS=` (empty: no browser on another origin may call the
API; [configuration](configuration.md)) and no `SESSION_DRIVER` or
`LOCALE_URL`. In an API project (`routes/api.go` and no `routes/web.go`),
`make:handler` (a typed handler, routed with `web.H`, answering a struct),
`make:middleware`, `make:auth` (accounts signing in with API tokens,
v0.4) and `make:crud` (JSON endpoints, v0.4) write for the API, and
`make:admin`, which writes pages, refuses.

## `anetos dev`

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | `HTTP_ADDR` from `.env`, else `:8080`; without a host, on `127.0.0.1` only | Where to browse the app (`--addr=0.0.0.0:8080` to reach it from other machines, with a warning: the dev server shows build errors and your code's paths) |
| `--host` | `localhost`, `*.localhost`, IP addresses and `APP_URL`'s host | Another host name browsers may use (repeat the flag for more); requests for other names get 403, which stops DNS-rebinding pages from reading the app (v0.3) |
| `-- args…` | `run` | Arguments for the app binary |

Run it in the project (any directory under `go.mod`). On start and on
every change it:

1. runs `go tool templ generate` (if there are `.templ` files) and `anetos gen`,
   and in a project of the `tailwind` kit compiles `public/static/app.css`
   as `css:build` does (v0.5; when Tailwind can't run, it says why and
   keeps the `app.css` there is until it's restarted),
2. builds the app into `tmp/anetos-dev/`,
3. stops the previous app (interrupt, then kill after 10 s) and starts the
   new one with `HTTP_ADDR` set to a free local port,
4. once the app accepts connections, tells open pages to reload.

Watched: `.go` (not tests or generated files), `.templ`, `go.mod`,
`go.sum`, `.env*`, everything under `public/` and `views/ui/tailwind.css`
(but not a Tailwind project's `public/static/app.css`, which the build
writes); directories starting
with a dot, `node_modules` and `testdata`, and `tmp`, `vendor`, `bin` and
`storage` at the project's root are skipped. Requests during a restart
wait for the new version. The proxy adds a small script to HTML pages
(not htmx responses, HEAD requests or empty responses) that reloads them;
build errors, and an app that stops or doesn't start, show as an error
page until the next change. On Linux the app is stopped even if
`anetos dev` is killed.

## `anetos build`

`anetos build [-o file] [--target=os/arch] [--version=v1.2.0] [--cgo] [-- go build flags]` (v0.3)

| Flag | Default | Meaning |
|---|---|---|
| `-o` | `bin/<name>`: the module path's last element, or the one before a `/v2` suffix (`.exe` for Windows) | The binary to write |
| `--target` | `go env GOOS`/`GOARCH` | The system to build for: `linux/amd64`, `linux/arm64`, `windows/amd64`… The generators still run on this one |
| `--version` | the version Go records from git: the tag of the commit (`v1.2.0`), else a pseudo-version (`v0.0.0-20261006100000-1a2b3c4d5e6f`); `(devel)` outside a repository | The app's version, for its `version` command: letters, digits and `. + - _ ~ /` |
| `--cgo` | `false` | Build with `CGO_ENABLED=1` (only for a driver or package that needs cgo; SQLite doesn't) |
| `-- flags…` | | More `go build` flags (`-tags=…`); an `-ldflags` is added to the build's own |

Run it in the project. It runs `go tool templ generate` (if there are
`.templ` files), `anetos gen` and, in a project of the `tailwind` kit,
Tailwind as `css:build` does (failing if it can't run; v0.5), then `go build -trimpath
-ldflags="-s -w" .` with `CGO_ENABLED=0`, and prints the binary, its
system and size (`built bin/blog (linux/amd64, 18.3 MB)`). Use
`--target` rather than `GOOS` and `GOARCH`: with those set, `go tool`
builds the tool itself for the other system, which then can't run. The
binary holds the migrations, views, `public/` and `locales/`: copy it
alone. Built in a git repository, it records the commit (and whether
files were changed) for `version`.

## `anetos make:*`

Run anywhere in a project. Existing files are never overwritten
(`make:auth` adds one call to `setup` in `main.go` and one line to
`views/layout.templ`; `make:crud` adds one line to `routes/web.go` and
one to `views/layout.templ`; `make:admin` changes `setup` and adds a
line to `views/layout.templ`; `make:admin:resource` adds a line to
`app/admin/admin.go`).

| Command | Writes |
|---|---|
| `make:handler <Name>` | `app/handlers/<name>.go`: a handler type with an `Index` method; in an API project (v0.4), a typed `Index` (for `web.H`) answering `<Name>Response` as JSON, its comment routing it in `routes/api.go` |
| `make:model <Name> [--migration]` | `app/models/<name>.go`: a model embedding `db.Model`, then its typed columns (`anetos gen`); with `--migration`, also `create_<table>_table` |
| `make:migration <name>` | `database/migrations/<YYYY_MM_DD_HHMMSS>_<name>.go`: `create_posts_table` creates a table; `add_x_to_posts_table` (the last `to`, `from`, `in` or `on`) gets commented `Alter` code for that table; other names get empty functions. The timestamp is always after the newest migration's, so migrations made in the same second keep their order |
| `make:middleware <Name>` | `app/middleware/<name>.go`: a `func(http.Handler) http.Handler` |
| `make:crud <Model> <field:type[:optional\|:unique]>...` | A model with its table and the pages to list, show, create, edit and delete its rows (v0.3): `app/models/<model>.go`, a `create_<table>_table` migration, `app/handlers/<table>.go` (`handlers.<Models>`: `Index` with 20 rows a page, `Show`, `New`, `Create`, `Edit`, `Update`, `Delete`; the form `<Model>Input` with its validate tags), `views/<table>.templ` (the list, the page, the form, calling the components of `views/ui`; v0.5), `routes/<table>.go` (`routes.<Models>(r)`: `/<path>`, the table with `-` for `_`, named `<path>.index`, `.new`, `.store`, `.show`, `.edit`, `.update`, `.destroy`), `locales/en/<table>.yaml` (the pages' text) and `<table>_test.go` (its comment shows how to sign a user in for pages in `make:auth`'s `members` group); then `anetos gen`, `templ generate` and `go build ./...`. Types: `string` (255), `text`, `email`, `int` (`int64`), `float` (`float64`), `bool`, `date` (`anetos.Date`). Strings, emails, texts and dates are required unless `:optional`; `:unique` (required strings and emails, dates) adds a unique index and the `unique` rule. Refused: a plural that is the name (`News`), the tables of the framework and `make:auth` (`users`, `api_tokens`…), the field `model`, and two fields with one Go name (`a_1` and `a1`). Adds `<Models>(pages)` at the end of `Register` in `routes/web.go` and a `navLink` to the list at the end of the header's nav in `views/layout.templ`: the `@ui.Nav` block (v0.5), or before the `</nav>` of a layout made earlier (else it prints what to add). In a web project without `views/ui` (made before v0.5), it first writes the components of the kit closest to `public/static/app.css` (`anetos` if it has the starter theme's `.card` rule, else `none`), and says so; the stylesheet and the layout are left as they are. Writes nothing if a file or the migration exists or a name is taken. See [Add pages for a model](../getting-started/crud.md). In an API project (v0.4): JSON endpoints instead, with the model and migration, `app/handlers/<table>.go` (`<Model>Response`, the output struct; `<Model>Input`; `<Model>List`, the query: `page`, `per_page` ≤ 100, `sort` from a list of columns, exact filters on string, email, int, bool and date fields, so no field may be named `page`, `per_page` or `sort`; `Index` answering `db.Page[<Model>Response]`, `Show`, `Create` with `Location`, `Update` (PUT), `Delete`), `routes/<table>.go` (`/api/v1/<path>`, named `api.<path>.index`…; the store route `.Status(201)`) and `<table>_test.go`; `<Models>(api)` added to `Register` in `routes/api.go` (else it prints the call); no views or catalog; then `go run . openapi`, updating `openapi.json` (a failure is reported, not fatal). See [Add pages for a model](../getting-started/crud.md#in-an-api-project) |
| `make:agent <Name>` | `app/agents/<name>.go`: an `ai.Agent` with a typed tool; prints how to set up `ai.New` if `main.go` doesn't call it (v0.3) |
| `make:auth` | Accounts, with sign-in with Google and GitHub: `app/models/user.go` (`User` with `disabled_at` and `session_key` since v0.3, `models.Users`), `app/handlers/auth.go` (`handlers.Accounts`, `handlers.SocialUser`, `handlers.SendVerification` and `SendPasswordReset` since v0.3), `app/mailers/auth.go` and `views/auth_mail.templ` (verification and reset emails), `views/auth.templ` (pages, and `AccountMenu`, the header's account links, since v0.3), `routes/auth.go` (`routes.Auth`), `auth.go` (`setupAuth`: `auth.DefaultHomeURL("/dashboard")` and `sessions.Use(a.Middleware)` since v0.3), `auth_test.go`, `locales/en/auth.yaml`, `database/factories/users.go` (v0.3: `factories.Users`, verified users whose password is `factories.UserPassword`, for tests with `anetostest.ActingAs`), and a `create_users_table` migration; the empty `SOCIAL_GOOGLE_*` and `SOCIAL_GITHUB_*` settings appended to `.env`, `.env.example` and `deploy/production.env.example` (unless there); then `go mod tidy`, `anetos gen`, `templ generate` and `go build ./...`, and a `setupAuth` call in `setup` after its `routes.Register(srv.Router(), sessions)` statement (else it prints the call to add), and `@AccountMenu()` after the layout header's nav: the `@ui.Nav` block (v0.5), or the `</nav>` line of a layout made earlier (v0.3; else it prints the line to add). In a web project without `views/ui` (made before v0.5), it first writes the components, as `make:crud` does. Writes nothing if one of the files or a `create_users_table` migration exists, or a name the files declare is taken in its package; removes what it wrote if a write fails. See [Add accounts with make:auth](../guides/accounts.md). In an API project (v0.4), JSON endpoints under `/api/v1` signing in with API tokens instead: `app/models/user.go` (without `remember_token`, `pending_email` and the preferences), `app/handlers/auth.go` (`handlers.Accounts`, typed handlers answering `UserResponse`, `SignInResponse`, `TokenResponse`…), `app/mailers/auth.go` and `app/mailers/auth.html` (the emails, an `html/template` file), `routes/auth.go` (`routes.Auth(r, a)`, with `TokenMiddleware`; the account's routes need a token with every ability, `auth.RequireAbilities("*")`), `auth.go` (`setupAuth(app, r)`; refuses to boot without `AUTH_CLIENT_URL` in production and staging), `auth_test.go`, `locales/en/auth.yaml`, `database/factories/users.go` and the migration; `AUTH_CLIENT_URL` appended to the settings files; the call after `routes.Register(srv.Router())`; no templ; then `go run . openapi`, updating `openapi.json`. See [Add accounts to an API](../guides/api-accounts.md) |
| `make:admin` | The admin interface (v0.3), after `make:auth`; refused in an API project (v0.4): adds the module `anetos.dev/anetos/admin` (`go get`; in a project whose core module is replaced by a checkout, from the checkout), writes `admin.go` (`setupAdmin`: roles and permissions with `rbac.New` and an `admin` role, unless a Go file of the project already calls `rbac.New`; then `admin.New`, the users, `admin.Roles`, the dashboard (`admin.SignUps` of the users, `admin.QueueHealth`, `admin.AIUsage` if the project tracks AI usage, `admin.RecentActivity` if it keeps an audit log), `admin.Jobs` and `admin.Schedule` (for the app's queue and scheduler), `admin.Activity` (with an audit log), the resources of `app/admin`, and `Mount` with the pages' middleware), `app/admin/admin.go` (`Resources`, empty), `app/admin/users.go` (`Users`: `admin.Users` for `models.User`, with the columns and `handlers.SendVerification` and `SendPasswordReset` the model and handlers have) and `admin_test.go` (with `rbac.New` and `make:auth`'s tests); adds `@admin.Banner()` after `<body>` in `views/layout.templ` (else it prints the line to add); appends the empty `ADMIN_PATH` and `ADMIN_HOST` to `.env` and `.env.example`; replaces `make:auth`'s `setupAuth` call in `setup` with one that keeps its `*auth.Auth` and calls `setupAdmin` (else it prints the calls to add); then `go mod tidy`, `templ generate` and `go build ./...`. Restores `go.mod` and `go.sum` if it fails before writing. See [Add an admin panel](../guides/admin.md) |
| `make:admin:resource <Model>` | `app/admin/<models>.go` (v0.3): the admin's resource for a model of `app/models`, named after the type (`Post`: `posts` at `/admin/posts`, function `Posts`): columns (the ID and the first four fields the form edits, or the first five without `db.Model`; `created_at`), search over its first three string fields, and a form struct `<Model>Form` with the fields of types a form can edit (strings, numbers, bools, `time.Time` as `admin.DateTime`, `anetos.Date`, and pointers to them), leaving out the key, the timestamps, JSON and read-only columns and names like password, token or secret; adds the function to `Resources` in `app/admin/admin.go` |

Names may be `BlogPost`, `blog_post` or `blog-post`; files use snake case.

## `anetos add <module>[@version]` and `anetos remove <module>`

Install and uninstall [plugins](../guides/plugins.md), from the project's
directory or below. Both rewrite `plugins.go` (generated: `DO NOT EDIT`),
which `setup` passes to `ext.Load`.

`anetos add [--yes] <module>[@version]` (version default `latest`):

1. Prints the module and the version, and that a plugin is code that
   runs with the app's privileges; runs `go get <module>@<version>`.
2. Adds the package's `Plugin()` to `plugins.go`, after the others, and
   runs `go mod tidy` (the module becomes a direct requirement).
3. Runs `go build`, so a module without a `Plugin() ext.Plugin`
   function, or one that doesn't compile against this version of
   Anetos, is refused.
4. In a terminal, asks before going on (v0.3; `--yes`, or a stdin that
   isn't a terminal, doesn't ask): the next step runs the plugin's
   code on your machine, with your environment and `.env`.
   Then runs the built app's `plugins:env` (stopped after 2 minutes), which loads the
   plugins without booting the app: if `ext.Load` refuses the plugin
   (its `Requires()`, its name, its settings' names), so does `anetos
   add`. Otherwise it appends the settings `.env.example` doesn't have
   yet (commented and `export` keys count as present) under a
   `# <name> plugin` line; if the app fails for another reason, it
   prints the error and keeps the plugin.
5. Prints the next steps: `plugins:list`, and `migrate` if it adds
   migrations. Migrations never run by themselves.

If step 1 to 4 refuses the plugin, `go.mod`, `go.sum` and `plugins.go`
are put back as they were (exit 1). When `go get` upgrades Anetos itself
(the plugin requires a newer version), `anetos add` says so. Adding a
module that `plugins.go` already lists is an error (exit 1). Import
names in `plugins.go` avoid the names `package main` declares.

`anetos remove` takes the module out of `plugins.go`, runs `go mod tidy`
and `go build` (putting the three files back if one fails, for example
because your code still imports the plugin). The plugin's settings stay
in `.env` and `.env.example`, and its tables in the database: drop them
with a migration of your own if you want them gone (`migrate:rollback`
rolls back a whole batch, your app's migrations included). Anetos stays
at the version `anetos add` left it at.

## `anetos lang:add [-from dir] [-version v] [-force] <locale>...`

Copies the translations of the framework's messages for each locale
from the module `anetos.dev/locales`
([anetos-dev/locales](https://github.com/anetos-dev/locales)) into the
project's `locales/<locale>/`, from the project's directory or below
(v0.3). `anetos add lang <locale>...` does the same.

1. Downloads the module (`go mod download`, version `-version`, default
   `latest`), or uses the checkout `-from` names. The project's `go.mod`
   doesn't change.
2. Writes `framework.yaml` (validation messages, error pages, sign-in
   messages, `format` and `relative`), and `auth.yaml` when the project
   has `locales/en/auth.yaml` (`make:auth`'s pages and emails), without
   the keys the project's other catalogs for the locale define (it lists
   them). A file the project already has is kept, unless `-force`; an
   identical one is reported as up to date.
3. Prints the next step: `go run . lang:check`.

Locale names match the module's folders regardless of case, and a
regional locale matches its language's folder (`bn-BD` writes
`locales/bn/`). With no locale, it lists those available. It fails
(exit 1), before downloading or writing anything, without a `locales`
folder or for a locale the module doesn't have. The download runs with
`GOWORK=off`; without network, pass `-from`.

## `anetos doctor [--strict] [--vuln]`

Checks the project, then builds the app and runs its
[`doctor` command](#the-doctor-command) (v0.3). Run it in the project.
The project's checks:

| Check | Finds |
|---|---|
| `.env` | a warning when the file is readable by other users of the machine (`chmod 600 .env`; Unix) |
| `git` | a problem for each tracked file of settings with secrets (`.env`, `.env.*`, `*.env`, not `*.example`, `*.sample`, `*.dist`, nor the `.env.testing` that `anetos new` writes to be committed); a warning when an untracked `.env` isn't ignored. Skipped outside a git repository |

`--vuln` also runs `govulncheck ./...` (install it with `go install
golang.org/x/vuln/cmd/govulncheck@latest`); its findings fail the
command. `--strict` makes warnings fail it too, and is passed to the
app's `doctor`. Exit 1 when a check finds a problem, the app doesn't
build, or the app's `doctor` fails. A project that doesn't use Anetos,
or uses a version without `doctor` (before v0.3), gets the project's
checks only, with a note. The app's checks use the settings
of this machine (`.env` and the environment): run `./<app> doctor` with
the production settings too, on the server.

## Other commands

| Command | Does |
|---|---|
| `anetos gen [-check] [packages]` | Typed model columns ([reference](anetos-gen.md)) |
| `anetos css:build [--check]` | In a project of the `tailwind` kit (v0.5): compiles `views/ui/tailwind.css` into `public/static/app.css` with Tailwind CSS's standalone CLI (`--input views/ui/tailwind.css --minify`), writing it only when it changes. The first run downloads Tailwind CSS 4.3.3 for the platform from its GitHub releases into the user cache directory (`<cache>/anetos/tailwindcss/v4.3.3/`), checking the SHA-256 written in `anetos` then and before each run (a download that sends nothing for 30 s stops); `ANETOS_TAILWIND=<path>` runs that binary instead (its version is checked, and a different one warned about). `--check` writes nothing and exits 1 when `app.css` is out of date. Exits 1 in a project without `views/ui/tailwind.css` ([Tailwind CSS](../guides/kit-tailwind.md)) |
| `anetos css:use [<kit>] [--force]` | Switches a web project's design kit (v0.5): writes the kit's files in `views/ui` and `public/static/`, removes the old kit's files the new one lacks (and their `_templ.go`), records the kit in `views/ui/kit.json` (the kit, its framework's version, the SHA-256 of each file), adds `nav.menu` to `locales/en/app.yaml` if missing (or says what to add, when it can't edit the file safely; and names the other locales without it), runs `templ generate`, and for `tailwind` `css:build` (going on without Tailwind: the kit's `app.css` is compiled for its components). Exits 1, writing nothing, when a recorded file changed since (line endings aside) or is a symbolic link, when a file in the new kit's way isn't the old kit's (it lists them), or when the project has no usable `kit.json` (missing, not JSON, or naming a file outside `views/ui` and `public/static`): `--force` replaces them (without a record, it lists `public/static`'s files that aren't the new kit's instead of removing them). Each file is written through a temporary file, the record last. Then runs `go build ./...`, exiting 1 when the project doesn't build with the new kit. With the project's own kit, updates its files to this version's. Lists your own files in `views/ui`, which keep their classes, and the `Dockerfile` cache line to add or remove when moving to or from Tailwind. Without `<kit>`, prints the kit. Refused in a project without `views/ui` (an API project, or one made before v0.5) ([Style your app](../guides/styling.md#8-switch-kits)) |
| `anetos key:generate` | Prints `APP_KEY=base64:…` |
| `anetos version` | Prints the tool's version |

## App binary commands

`app.Execute()` runs the command named by the first argument.

| Command | Added by | Does |
|---|---|---|
| (none), `run [--only=role,…]` | every app | Runs the components (all, or those with the roles, plus those without roles) until SIGINT/SIGTERM (exit 0) |
| `serve` | `web.NewServer` | `run --only=http` |
| `routes:list` | `web.NewServer` | Method, path and name of every route |
| `openapi [--check] [--out=FILE]` | `openapi.Register` | Writes the API's OpenAPI 3.1 description to `openapi.json` (`Config.File`; `--out=-`: the standard output), warning about routes it leaves out; `--check` exits 1 if the file differs. Doesn't boot the app (v0.4). See [Describe an API with OpenAPI](../guides/openapi.md) |
| `health:check [--live] [--timeout=5s]` | `web.NewServer` | Asks the server running on `HTTP_ADDR` (on `127.0.0.1` when its host is empty, `0.0.0.0` or `[::]`) for `/health/ready` (`--live`: `/health/live`), for container health checks: prints `ok`, or exits 1. An error with `HTTP_HEALTH_ROUTES=false` (v0.3) |
| `migrate`, `migrate:rollback`, `migrate:reset`, `migrate:fresh`, `migrate:status`, `db:seed` | `migrate.New` | See the [migrations reference](migrations.md#commands) |
| `search:reindex [table…]` | `migrate.New` | Rebuilds the search indexes (all, or the tables') for `SEARCH_LANGUAGE` and `SEARCH_RANKING`. See [Search](../guides/search.md) |
| `ai:embed [table…]` | `ai.EmbeddingsFor` | Embeds the records whose text or embedding model changed (all tables', or the named ones), a hundred at a time; unchanged chunks aren't embedded again. See [Search by meaning](../guides/semantic-search.md) |
| `cache:clear` | `cache.New` | Removes the app's cache items (keys with `CACHE_PREFIX`), locks included |
| `queue:failed [--limit=N]`, `queue:retry <id>…\|all`, `queue:forget <id>…`, `queue:flush [--force]`, `queue:clear [--force] [queue]` | `queue.New` | List, retry and delete failed jobs; delete the jobs waiting on a queue. `flush` and `clear` need `--force` in production. See [Queues](../guides/queues.md#4-handle-failed-jobs) |
| `pubsub:publish <topic> <message>` | `pubsub.New` | Publishes a message (its body as given) to a topic. See [Pub/sub listeners](../guides/pubsub.md#4-publish) |
| `lang:check [dir]` | `i18n.New` | Reports keys a supported locale lacks (against `APP_FALLBACK_LOCALE`'s), placeholders that differ, plural forms a language needs, and keys the `.go` and `.templ` files under `dir` (default `.`) use that no catalog has (a literal followed by `+` is a prefix some key must start with); notes the framework's messages a locale leaves in English. Exits 1 on a problem; doesn't boot the app. See [Translations](../guides/translations.md#7-check-the-catalogs) |
| `schedule:list` | `schedule.New` | Each task, its schedule, its next run and options. See [Scheduling](../guides/scheduling.md#4-check-and-run-tasks) |
| `schedule:run <task>` | `schedule.New` | Runs a task now, whatever its schedule (`WithoutOverlapping` applies, across processes only with a shared cache store; `OnOneServer` doesn't) |
| `rbac:roles`, `rbac:user <user-id>`, `rbac:assign [--scope=kind:id] <user-id> <role>`, `rbac:unassign …` | `rbac.New` | List the roles and their users; show a user's grants; give or take a role. See [Roles and permissions](../guides/roles-and-permissions.md) |
| `db:prune-trashed [--dry-run]` | `db.PruneTrashed` | Deletes for good the rows of the registered models soft-deleted longer ago than their duration; `--dry-run` counts them. See [Keep an audit log](../guides/audit-log.md#soft-deletes-that-play-well-with-the-log) |
| `audit:prune` | `audit.New` | Deletes the audit entries older than `AUDIT_RETENTION_DAYS`, and records that it did |
| `audit:anonymize <actor-type> <actor-id>` | `audit.New` | Replaces an actor (`user 42`) with `erased` in the audit log, as the actor and as the user someone acted as, and drops the IP addresses of their entries, for erasure requests. See [Keep an audit log](../guides/audit-log.md#8-keep-entries-for-as-long-as-you-must-and-no-longer) |
| `plugins:list` | `ext.Load` | Each plugin, its version constraint, its route prefix and what it adds (or that its settings are missing); doesn't boot the app. See [Use plugins](../guides/plugins.md) |
| `plugins:env [plugin]` | `ext.Load` | The plugins' settings as `.env` lines with their defaults (double-quoted when they need it; `# required` after required ones); doesn't boot the app, so it works before they are set |
| `doctor [--strict]` | every app | Runs the app's checks of its settings and prints what they find (v0.3; see [below](#the-doctor-command)). Exit 1 on a problem (`--strict`: on a warning too) |
| `version` | every app | The app's version (`--version` of `anetos build`, else the git tag Go recorded), commit, commit time and `modified` if the files differed from it; the Anetos version; the Go version and system. Doesn't boot the app; the `main.go` of `anetos new` prints it before `setup`, so it needs no settings (`anetos.VersionText`) (v0.3) |
| `help [command]`, `-h`, `--help` | every app | The command list, or a command's usage (`<command> -h` too, as the first argument); doesn't boot the app |

| API | Does |
|---|---|
| `app.Command(name, description, run)` | Adds a command; panics if the name is invalid (lowercase words joined by `:` or `-`) or taken |
| `app.AddCommand(cmd.Command{Name, Usage, Description, Run, ManagesApp, ChangesSchema})` | The same, returning an error. `ChangesSchema`: the command changes the database's structure, so boot checks that the schema matches the settings (search indexes) don't stop it |
| `cmd.Running(ctx)` | The command the app is booting or running for, in boot code (`cmd.WithCommand` sets it) |
| `app.Commands()` | Every command, sorted |
| `app.Execute()` | Runs `os.Args[1:]` with a context canceled by SIGINT/SIGTERM, then exits |
| `app.ExecuteArgs(ctx, args, stdout, stderr)` | Runs and returns the exit status (tests) |
| `args.Parse(fs)` | Parses `args.Args` with a `flag.FlagSet`; bad flags give an error matching `cmd.ErrUsage` (exit 2) |
| `cmd.Usagef(format, …)` | A usage error (exit 2) |

### The doctor command

`doctor` (v0.3) runs every check added with `app.AddCheck`, the ones
features add as they are set up included, and prints one line per
finding (`ok` for a check with none), then the counts. Checks that
don't need services run first, without booting the app; then it boots
the app (connecting to the database…), reporting a failed boot as a
problem, and runs the others, with those added while it booted. An app
that was booted already (in a test) stays open. Checks of deployment settings apply with `APP_ENV`
production or staging only.

```text
Checking blog (APP_ENV=production).
  ok       app
  ok       db
  warning  mail: MAIL_DRIVER=log in production: emails aren't sent; set MAIL_DRIVER=smtp (with MAIL_SMTP_URL) or a plugin's driver
  ok       session
  ok       http
  note     cache: CACHE_STORE=memory: each instance of the app has its own cache, rate limits and locks (cache.WithLock); with more than one instance, use redis or database
  ok       migrations
0 problems, 1 warning, 1 note.
```

| Check | Added by | Finds |
|---|---|---|
| `app` | every app | In production and staging: `APP_KEY` not set (warning), `APP_URL` not set (warning), `http://` for a host other than this machine (problem in production, warning in staging), or an example's domain (`example.com`, `.test`: warning, v0.3), `APP_DEBUG=true` in staging (warning; production refuses it) |
| `db` | `db.Connect` | In production and staging: `DB_LOG_QUERIES=true` (warning); for PostgreSQL and MySQL, a server other than this machine reached without verifying its certificate (warning), from `DB_TLS` (`none`, `skip-verify`) or from `DB_URL`: PostgreSQL's `sslmode` must be `verify-full`, or `verify-ca` or `require` with an `sslrootcert` file (the server's own authority); MySQL's `tls` must be `true` or a registered configuration that checks certificates, without `allowFallbackToPlaintext`. With several hosts, the worst one counts. A `DB_URL` the driver can't read is a warning |
| `migrations` | `migrate.New` | After booting: migrations that haven't run (warning), applied ones the app no longer has (note); on MySQL and MariaDB, tables (not views) with text columns in a character set other than utf8mb4 or ascii (warning, with the `ALTER TABLE … CONVERT TO CHARACTER SET utf8mb4` that fixes them) |
| `session` | `session.New` | `SESSION_SECURE=false` in production or staging (problem); `SESSION_SAME_SITE=none` (warning); `SESSION_DOMAIN` set (note) |
| `http` | `web.NewServer` | `HTTP_TRUSTED_PROXIES` with `0.0.0.0/0` or `::/0` (problem) or a range wider than /8 (IPv4) or /16 (IPv6) (warning); `HTTP_CORS_ORIGINS=*` (warning); in production and staging, `HTTP_MAX_BODY`, `HTTP_REQUEST_TIMEOUT` or `HTTP_READ_HEADER_TIMEOUT` set to 0 (warning) |
| `mail` | `mailer.New` | `MAIL_DRIVER` `log` or `memory` in production or staging (warning); no `MAIL_FROM_ADDRESS` for a driver that sends (warning); `tls=none` in `MAIL_SMTP_URL` for a server other than this machine (warning); in production and staging, an example's domain (`example.com`, `.test`) as `MAIL_SMTP_URL`'s server or `MAIL_FROM_ADDRESS`'s (warning) |
| `cache` | `cache.New` | `CACHE_STORE=memory` in production or staging (note) |
| `queue` | `queue.New` | In production and staging: `QUEUE_DRIVER=memory` (warning), `sync` (note) |

| API | Does |
|---|---|
| `app.AddCheck(anetos.Check{Name, Booted, Run})` | Adds a check: `Run(ctx) []anetos.Finding`, nothing when all is well; `Booted` runs it after the app boots (for checks that need a service). Checks run in the order added, the booted ones last; a panic is reported as a problem. Panics without a name or `Run` |
| `anetos.Finding{Severity, Message}` | What a check found; the message names the setting and says what to do |
| `anetos.Note`, `anetos.Warning`, `anetos.Problem` | The severities: information; often a mistake; unsafe or broken (exit 1) |
| `env.Deployed()` | Whether an `anetos.Environment` is production or staging |
| `db.Driver.InspectURL` | For drivers: reads a `DB_URL`'s host and TLS mode (`db.TLSVerify`, `TLSSkipVerify`, `TLSNone`), for the `db` check |

A command other than `run` and `serve` runs after `app.Boot`, with a
context from `app.Context`; `app.Close` follows, also after a panic. The
context is canceled by the first SIGINT/SIGTERM (the command fails with
exit 1 if it stops early); the second ends the program. An app runs one
command.

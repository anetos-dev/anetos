---
title: anetos tool and app commands reference
since: v0.1.0
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

Flags may come before or after the other arguments. Exit status: 0 on
success, 1 on errors, 2 for bad usage.

## `anetos new <directory>`

| Flag | Default | Meaning |
|---|---|---|
| `--module` | the directory's name | Go module path |
| `--db` | `sqlite` | `sqlite`, `postgres` or `mysql`: the driver in `main.go` and the `DB_*` settings in `.env` |
| `--skip-install` | `false` | Only write the files |
| `--replace` | | A local Anetos checkout, used through `replace` directives (framework development): the core, the tool, and every driver and plugin module of the checkout, so `go get` and `anetos add` take them from it too |

The directory must not exist or be empty; its name (letters, digits, `-`
and `_`, starting with a letter) becomes the app's name. `anetos new .`
uses the current directory. It writes the project, then runs `go get` for
Anetos and the driver (latest) and the `anetos` tool (its own version),
pinned as tools with templ, then `go tool templ generate` and
`go mod tidy`. With `--db=postgres` or `mysql`, create the database
before `migrate`.

| Path | Holds |
|---|---|
| `main.go` | `anetos.New`, `db.Connect`, `migrate.ForApp`, `cache.ForApp`, `queue.ForApp` (with workers), `events.ForApp`, `mailer.ForApp`, `storage.ForApp`, `schedule.ForApp`, `web.NewServer`, `session.ForApp`, `routes.Register`, then `ext.Load(app, plugins())`, and `app.Execute()`; `//go:generate` lines for templ and `anetos gen` |
| `plugins.go` | The plugins, written by `anetos add` and `anetos remove` (an empty list at first) |
| `main_test.go` | A test requesting the home page with `anetostest` |
| `.env`, `.env.example` | Settings; `.env` has a fresh `APP_KEY` (file mode 0600) and stays out of git |
| `.env.testing` | PostgreSQL and MySQL projects: the test database's settings (`<name>_test`) |
| `README.md`, `.gitignore` | How to run it; what stays out of git |
| `routes/web.go` | Routes: assets, and the page group with sessions and CSRF |
| `app/handlers/home.go` | The home page handler |
| `app/models/` | Models (empty at first) |
| `database/migrations/migrations.go` | The `All` migration set and `Seeders` |
| `database/factories/factories.go` | The package for model factories (empty at first) |
| `views/layout.templ`, `views/home.templ` | templ layout (flash messages, CSRF header for htmx) and home page |
| `public/public.go`, `public/static/app.css` | `public.Assets`: the static files and htmx under `/assets` |

## `anetos dev`

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | `HTTP_ADDR` from `.env`, else `:8080`; without a host, on `127.0.0.1` only | Where to browse the app (`--addr=0.0.0.0:8080` to reach it from other machines) |
| `-- args…` | `run` | Arguments for the app binary |

Run it in the project (any directory under `go.mod`). On start and on
every change it:

1. runs `go tool templ generate` (if there are `.templ` files) and `anetos gen`,
2. builds the app into `tmp/anetos-dev/`,
3. stops the previous app (interrupt, then kill after 10 s) and starts the
   new one with `HTTP_ADDR` set to a free local port,
4. once the app accepts connections, tells open pages to reload.

Watched: `.go` (not tests or generated files), `.templ`, `go.mod`,
`go.sum`, `.env*`, and everything under `public/`; directories starting
with a dot, `node_modules` and `testdata`, and `tmp`, `vendor`, `bin` and
`storage` at the project's root are skipped. Requests during a restart
wait for the new version. The proxy adds a small script to HTML pages
(not htmx responses, HEAD requests or empty responses) that reloads them;
build errors, and an app that stops or doesn't start, show as an error
page until the next change. On Linux the app is stopped even if
`anetos dev` is killed.

## `anetos make:*`

Run anywhere in a project. Existing files are never overwritten
(`make:auth` adds one call to `setup` in `main.go`).

| Command | Writes |
|---|---|
| `make:handler <Name>` | `app/handlers/<name>.go`: a handler type with an `Index` method |
| `make:model <Name> [--migration]` | `app/models/<name>.go`: a model embedding `db.Model`, then its typed columns (`anetos gen`); with `--migration`, also `create_<table>_table` |
| `make:migration <name>` | `database/migrations/<YYYY_MM_DD_HHMMSS>_<name>.go`: `create_posts_table` creates a table; `add_x_to_posts_table` (the last `to`, `from`, `in` or `on`) gets commented `Alter` code for that table; other names get empty functions. The timestamp is always after the newest migration's, so migrations made in the same second keep their order |
| `make:middleware <Name>` | `app/middleware/<name>.go`: a `func(http.Handler) http.Handler` |
| `make:auth` | Accounts, with sign-in with Google and GitHub: `app/models/user.go` (`User`, `models.Users`), `app/handlers/auth.go` (`handlers.Accounts`, `handlers.SocialUser`), `app/mailers/auth.go` and `views/auth_mail.templ` (verification and reset emails), `views/auth.templ` (pages), `routes/auth.go` (`routes.Auth`), `auth.go` (`setupAuth`), `auth_test.go`, and a `create_users_table` migration; the empty `SOCIAL_GOOGLE_*` and `SOCIAL_GITHUB_*` settings appended to `.env` and `.env.example` (unless there); then `go mod tidy`, `anetos gen`, `templ generate` and `go build ./...`, and a `setupAuth` call in `setup` after its `routes.Register(srv.Router(), sessions)` statement (else it prints the call to add). Writes nothing if one of the files or a `create_users_table` migration exists, or a name the files declare is taken in its package; removes what it wrote if a write fails. See [Add accounts with make:auth](../guides/accounts.md) |

Names may be `BlogPost`, `blog_post` or `blog-post`; files use snake case.

## `anetos add <module>[@version]` and `anetos remove <module>`

Install and uninstall [plugins](../guides/plugins.md), from the project's
directory or below. Both rewrite `plugins.go` (generated: `DO NOT EDIT`),
which `setup` passes to `ext.Load`.

`anetos add` (version default `latest`):

1. Prints the module and the version, and that plugins run with the
   app's privileges; runs `go get <module>@<version>`.
2. Adds the package's `Plugin()` to `plugins.go`, after the others, and
   runs `go mod tidy` (the module becomes a direct requirement).
3. Runs `go build`, so a module without a `Plugin() ext.Plugin`
   function, or one that doesn't compile against this version of
   Anetos, is refused.
4. Runs the built app's `plugins:env` (stopped after 2 minutes), which loads the
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

## Other commands

| Command | Does |
|---|---|
| `anetos gen [-check] [packages]` | Typed model columns ([reference](anetos-gen.md)) |
| `anetos key:generate` | Prints `APP_KEY=base64:…` |
| `anetos version` | Prints the tool's version |

## App binary commands

`app.Execute()` runs the command named by the first argument.

| Command | Added by | Does |
|---|---|---|
| (none), `run [--only=role,…]` | every app | Runs the components (all, or those with the roles, plus those without roles) until SIGINT/SIGTERM (exit 0) |
| `serve` | `web.NewServer` | `run --only=http` |
| `routes:list` | `web.NewServer` | Method, path and name of every route |
| `migrate`, `migrate:rollback`, `migrate:reset`, `migrate:fresh`, `migrate:status`, `db:seed` | `migrate.ForApp` | See the [migrations reference](migrations.md#commands) |
| `search:reindex [table…]` | `migrate.ForApp` | Rebuilds the search indexes (all, or the tables') for `SEARCH_LANGUAGE` and `SEARCH_RANKING`. See [Search](../guides/search.md) |
| `cache:clear` | `cache.ForApp` | Removes the app's cache items (keys with `CACHE_PREFIX`), locks included |
| `queue:failed [--limit=N]`, `queue:retry <id>…\|all`, `queue:forget <id>…`, `queue:flush [--force]`, `queue:clear [--force] [queue]` | `queue.ForApp` | List, retry and delete failed jobs; delete the jobs waiting on a queue. `flush` and `clear` need `--force` in production. See [Queues](../guides/queues.md#4-handle-failed-jobs) |
| `pubsub:publish <topic> <message>` | `pubsub.ForApp` | Publishes a message (its body as given) to a topic. See [Pub/sub listeners](../guides/pubsub.md#4-publish) |
| `schedule:list` | `schedule.ForApp` | Each task, its schedule, its next run and options. See [Scheduling](../guides/scheduling.md#4-check-and-run-tasks) |
| `schedule:run <task>` | `schedule.ForApp` | Runs a task now, whatever its schedule (`WithoutOverlapping` applies, across processes only with a shared cache store; `OnOneServer` doesn't) |
| `plugins:list` | `ext.Load` | Each plugin, its version constraint, its route prefix and what it adds (or that its settings are missing); doesn't boot the app. See [Use plugins](../guides/plugins.md) |
| `plugins:env [plugin]` | `ext.Load` | The plugins' settings as `.env` lines with their defaults (double-quoted when they need it; `# required` after required ones); doesn't boot the app, so it works before they are set |
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

A command other than `run` and `serve` runs after `app.Boot`, with a
context from `app.Context`; `app.Close` follows, also after a panic. The
context is canceled by the first SIGINT/SIGTERM (the command fails with
exit 1 if it stops early); the second ends the program. An app runs one
command.

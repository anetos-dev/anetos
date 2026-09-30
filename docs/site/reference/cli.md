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
| `--replace` | | A local Anetos checkout, used through `replace` directives (framework development) |

The directory must not exist or be empty; its name (letters, digits, `-`
and `_`, starting with a letter) becomes the app's name. `anetos new .`
uses the current directory. It writes the project, then runs `go get` for
Anetos and the driver (latest) and the `anetos` tool (its own version),
pinned as tools with templ, then `go tool templ generate` and
`go mod tidy`. With `--db=postgres` or `mysql`, create the database
before `migrate`.

| Path | Holds |
|---|---|
| `main.go` | `anetos.New`, `db.Connect`, `migrate.ForApp`, `web.NewServer`, `session.ForApp`, `routes.Register`, `app.Execute()`; `//go:generate` lines for templ and `anetos gen` |
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

Run anywhere in a project. Existing files are never overwritten.

| Command | Writes |
|---|---|
| `make:handler <Name>` | `app/handlers/<name>.go`: a handler type with an `Index` method |
| `make:model <Name> [--migration]` | `app/models/<name>.go`: a model embedding `db.Model`, then its typed columns (`anetos gen`); with `--migration`, also `create_<table>_table` |
| `make:migration <name>` | `database/migrations/<YYYY_MM_DD_HHMMSS>_<name>.go`: `create_posts_table` creates a table; `add_x_to_posts_table` (the last `to`, `from`, `in` or `on`) gets commented `Alter` code for that table; other names get empty functions. The timestamp is always after the newest migration's, so migrations made in the same second keep their order |
| `make:middleware <Name>` | `app/middleware/<name>.go`: a `func(http.Handler) http.Handler` |

Names may be `BlogPost`, `blog_post` or `blog-post`; files use snake case.

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
| `cache:clear` | `cache.ForApp` | Removes the app's cache items (keys with `CACHE_PREFIX`), locks included |
| `help [command]`, `-h`, `--help` | every app | The command list, or a command's usage (`<command> -h` too, as the first argument); doesn't boot the app |

| API | Does |
|---|---|
| `app.Command(name, description, run)` | Adds a command; panics if the name is invalid (lowercase words joined by `:` or `-`) or taken |
| `app.AddCommand(cmd.Command{Name, Usage, Description, Run, ManagesApp})` | The same, returning an error |
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

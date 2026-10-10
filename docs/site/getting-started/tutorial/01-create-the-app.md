---
title: "1. Create the app"
since: v0.3.0
weight: 1
---

# 1. Create the app

You create the project, run it, and look around.

## Create the project

```sh
anetos new tracker
cd tracker
```

`anetos new` writes a project, downloads its dependencies and generates
its views. The project is a Go module named `tracker`; its
packages import each other as `tracker/app/models` and so on. It pins
the `anetos` tool in `go.mod`: in the project, run it as `go tool
anetos`.

## Run it

Create the database tables the framework needs (sessions, the cache, the
queue's jobs), then start the app:

```sh
go run . migrate
go tool anetos dev
```

Open http://localhost:8080. `anetos dev` rebuilds and restarts the app
when you save a file, and reloads the page in the browser. Leave it
running in a terminal of its own for the rest of the tutorial.

## Look around

| Path | Holds |
|---|---|
| `main.go` | `setup`: connects the database, then adds the migrations, cache, queue, events, mailer, storage, scheduler, web server and routes. `main` calls it, and so do the tests |
| `.env` | The settings, for your machine: `APP_KEY` (a fresh key), `DB_NAME=database/app.db`… It stays out of git; `.env.example` lists the settings |
| `routes/web.go` | The routes: URLs to handlers |
| `app/handlers/home.go` | The home page's handler |
| `views/layout.templ`, `views/home.templ` | The page shell (a header with the app's links) and the home page, as [templ](https://templ.guide) components: typed Go functions that render HTML; `views/errors.templ` shows errors (a 404…) in the same shell |
| `views/ui/` | The components the pages are made of (`ui.Card`, `ui.Field`, `ui.Button`…): their markup and classes, so the pages need none |
| `public/static/app.css` | The styles: Anetos's starter theme, light and dark, with no build step ([Style your app](../../guides/styling.md)) |
| `locales/en/app.yaml` | The home page's text |
| `database/migrations/` | Migrations: changes to the database's tables, in Go |
| `main_test.go` | Tests that request the home page and a missing page |

`go run . help` lists the app's commands: `run` (the default: the web
server, the queue's workers and the scheduler), `migrate`, `routes:list`
and more. The [CLI reference](../../reference/cli.md) has the full list
of files and commands.

Run the test:

```sh
go test ./...
```

Tests boot the app with an in-memory database, so they don't touch
`database/app.db`.

Next: [2. Accounts](02-accounts.md).

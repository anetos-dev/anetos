---
title: Create a project
since: v0.3.0
group: "Your first app"
weight: 20
---

# Create a project

One command writes a working app: a home page in a styled layout,
sessions, CSRF protection, migrations, a queue, email, a test, and the
files to deploy it.

## Create it

```sh
anetos new blog
cd blog
```

`anetos new` writes the project, downloads its dependencies and
compiles its views. Its flags:

| Flag | Default | Meaning |
|---|---|---|
| `--db` | `sqlite` | `postgres` or `mysql` for a server database ([Choose a database](databases.md)) |
| `--stack` | `web` | `api` for an app that serves JSON only ([below](#an-api-instead)) |
| `--css` | `anetos` | The design kit: `anetos`, the starter theme; `none`, components without classes and an almost empty stylesheet, for your own CSS; `pico`, `bootstrap` or `bulma`, components in that framework's markup, with its files; `tailwind`, components with Tailwind CSS's classes, compiled by `anetos dev` ([Style your app](../guides/styling.md#7-or-start-with-another-kit)) |
| `--module` | the folder's name | The Go module path, such as `github.com/you/blog` |

## Run it

```sh
go run . migrate     # create the tables the framework uses
go tool anetos dev   # http://localhost:8080
```

`migrate` creates the database (for SQLite, `database/app.db`) and the
tables of sessions, the cache and the queue. `anetos dev` builds and
runs the app, and rebuilds it when you save a file: the page in your
browser reloads by itself. Keep it running in a terminal of its own.

Open http://localhost:8080: a welcome page, in light or dark like your
system. Edit `views/home.templ` and save to see it change; its text is
in `locales/en/app.yaml`, ready for other languages.

## The app's commands

The app is one program with commands. `go run . help` lists them:

```sh
go run . help          # every command
go run . routes:list   # the URLs and their handlers
go run .               # run: the web server, the queue's workers and the scheduler
```

In production, the built binary takes the same commands
(`./bin/blog migrate`).

## An API instead

Since v0.4, `--stack=api` writes an app that serves JSON only, for a
mobile app or a front end built separately:

```sh
anetos new shop --stack=api
cd shop
go run . migrate
go tool anetos dev   # http://localhost:8080/api/v1
```

It has the same database, queue, mail, storage, scheduler, plugins and
deploy files, but no views, static files, sessions or CSRF protection.
Its routes are in `routes/api.go`, under `/api/v1`; `GET /api/v1`
answers:

```json
{"name":"shop","message":"Welcome to the Shop API."}
```

Every error is JSON problem details, whatever the client accepts (a
missing URL's 404 too). Browsers on other origins may call the API once
`HTTP_CORS_ORIGINS` in `.env` lists them, such as
`http://localhost:5173`. `make:handler` writes JSON handlers in such a
project, `make:auth` accounts that log in with API tokens
([Add accounts to an API](../guides/api-accounts.md)), and `make:crud`
JSON endpoints for a model ([Add pages for a model](crud.md#in-an-api-project)).

`openapi.json` describes the API in OpenAPI 3.1, for clients and tools,
and the app serves it at `/api/v1/openapi.json`. `make:auth` and
`make:crud` update it; after changing a route or a handler yourself,
run `go run . openapi`: a test in `main_test.go` fails until you do
([Describe an API with OpenAPI](../guides/openapi.md)).

Next: [Project structure](project-structure.md).

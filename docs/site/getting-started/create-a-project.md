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
| `--css` | `anetos` | The starter theme; `none` for an almost empty stylesheet, for your own CSS or a CSS framework ([Style your app](../guides/styling.md)) |
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

Next: [Project structure](project-structure.md).

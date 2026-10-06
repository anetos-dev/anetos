---
title: Project structure
since: v0.3.0
group: "Your first app"
weight: 21
---

# Project structure

A project is a Go module. Its folders follow what each part of the app
does; the generators (`make:crud`, `make:auth`…) add their files in the
same places.

```text
blog/
├── main.go               setup: the database, cache, queue, mail, server, routes…
├── main_test.go          a test of the home page
├── plugins.go            the plugins (anetos add)
├── app/
│   ├── handlers/         HTTP handlers: a request in, a page or JSON out
│   └── models/           models: Go structs for tables, and their typed columns
├── database/
│   ├── migrations/       changes to the tables, in Go, in order
│   └── factories/        test data
├── routes/
│   └── web.go            URLs to handlers, with their middleware
├── views/
│   ├── layout.templ      the page shell: the header, the flash message
│   └── home.templ        the home page
├── locales/en/app.yaml   the pages' text
├── public/static/        CSS (app.css, the starter theme) and other files
├── deploy/               a systemd unit and the production settings
├── Dockerfile            a container image
├── .env                  settings for your machine (not in git)
└── .env.example          the settings, without secrets (in git)
```

## How a request flows

1. `routes/web.go` matches the URL to a handler and runs the route's
   middleware: sessions and CSRF protection for pages.
2. The handler, in `app/handlers`, reads the request (typed input,
   validated) and the database (the models of `app/models`).
3. It renders a view of `views/`, inside `Layout`, or answers JSON.

[HTTP request lifecycle](../concepts/http-request-lifecycle.md)
explains each step.

## Settings

Settings come from the environment, and in development from `.env`:
`APP_KEY` (made fresh for each project), `DB_*`, `MAIL_*` and the rest.
Tests read `.env.testing` instead (or nothing, for SQLite: an in-memory
database). The [configuration reference](../reference/configuration.md)
lists every key.

## Generated files

`*_templ.go` (from the `.templ` files) and `app/models/models_gen.go`
(the models' typed columns) are written by `templ generate` and
`anetos gen`; `anetos dev` and `anetos build` run both. Commit them, but
don't edit them.

The [CLI reference](../reference/cli.md#anetos-new-directory) describes
every file `anetos new` writes.

Next: [Add pages for a model](crud.md).

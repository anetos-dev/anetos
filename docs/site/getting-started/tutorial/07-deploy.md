---
title: "7. Deploy"
since: v0.3.0
weight: 7
---

# 7. Deploy

You build the app for production: one binary with everything in it, or
a container image.

## Build it

```sh
$ go tool anetos build
built bin/tracker (linux/amd64, 19.4 MB)
```

The binary holds the migrations, the views and the files of `public/`
and `locales/`. It needs its settings, from the environment:
`deploy/production.env.example` lists them. Try it locally with
production settings and a database of its own:

```sh
export APP_ENV=production APP_DEBUG=false APP_KEY=$(go tool anetos key:generate | cut -d= -f2-)
export DB_DATABASE=/tmp/tracker.db STORAGE_ROOT=/tmp/tracker-files HTTP_ADDR=:8081
./bin/tracker migrate
./bin/tracker run
```

`APP_DEBUG=false` overrides `.env`, which the app still reads in the
project's folder. Production cookies are HTTPS-only, so signing in needs
HTTPS: put a proxy in front, as the deploy guide shows.

## Or as an image

The project has a `Dockerfile`:

```sh
docker build -t tracker .
docker run --rm --env-file production.env tracker migrate
docker run -d -p 8080:8080 --env-file production.env -v tracker-data:/data tracker
```

`production.env` is your copy of `deploy/production.env.example`, filled
in; keep it out of git. With SQLite, the database is on the `/data`
volume.

## Where to run it

[Deploy](../../guides/deployment.md) covers a server with systemd,
Docker and Docker Compose, Fly.io and Render, HTTPS, and running the
queue's workers apart from the web server as traffic grows.

## What's next

You've built an app with accounts, pages, forms, htmx, search, events, a
queue and email, with its tests. From here:

- [`examples/tracker`](../../../../examples/tracker) grows this app into
  a team's tool: projects with roles, labels, files, history, a daily
  digest, an admin and a JSON API.
- The [guides](../../guides/) take one feature at a time.
- The [concepts](../../concepts/) explain how Anetos works underneath.

---
title: Test and build
since: v0.3.0
group: "Your first app"
weight: 24
---

# Test and build

## Test

```sh
go test ./...
```

Tests boot the app with test settings and a database of their own
(SQLite in memory, or `.env.testing`'s), and send requests to it like
a browser, through `anetostest`:

```go
// illustrative
func TestHome(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Get("/").AssertOK().AssertSee("Welcome")
}
```

Each test runs in a transaction that is rolled back, so tests don't see
each other's rows. `make:crud` and `make:auth` wrote tests for their
pages; add one for each page and form you write. [Test your
app](../guides/testing.md) shows forms, signed-in users, emails and
queue jobs.

## Build

```sh
go tool anetos build   # bin/blog
./bin/blog migrate
./bin/blog             # the web server, the queue's workers and the scheduler
```

`anetos build` compiles the views and the models' columns, then builds
one static binary that holds the migrations, the views and the files
of `public/` and `locales/`. A server needs only the binary and its
settings (the environment, or a file: `deploy/production.env.example`
lists them).

[Deploy](../guides/deployment.md) runs it on a server with systemd, in
a container (the project has a `Dockerfile`), or on Fly.io and Render.

Next: the [tutorial](tutorial/README.md) builds a whole app, step by
step.

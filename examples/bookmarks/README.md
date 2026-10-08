# Bookmarks

An [Anetos](https://github.com/anetos-dev/anetos) application: a JSON
API under `/api/v1`.

```sh
go run . migrate        # create the database tables
go tool anetos dev      # run with live reload on http://localhost:8080/api/v1
go test ./...
```

| Directory | Holds |
|---|---|
| `app/handlers` | HTTP handlers (`go tool anetos make:handler Posts`) |
| `app/models` | Models (`go tool anetos make:model Post --migration`) |
| `app/middleware` | Middleware (`go tool anetos make:middleware Admin`) |
| `database/migrations` | Migrations and seeders (`go tool anetos make:migration create_posts_table`) |
| `database/factories` | Model factories, for tests and seeders |
| `routes` | Routes: `api.go`, the API's under `/api/v1` |
| `locales` | The messages' translations (`locales/en/app.yaml`) |

Configuration is in `.env` (keep it out of git); `.env.example` lists the
settings.

Errors are JSON problem details ([RFC 9457](https://www.rfc-editor.org/rfc/rfc9457)),
whatever the client accepts. Browsers on other origins may call the API
once `HTTP_CORS_ORIGINS` lists them.

Add user accounts that sign in with API tokens (registration, login
with two-factor codes, email verification, password reset, token
management) with `go tool anetos make:auth`, then `go run . migrate`.

Add plugins with `go tool anetos add <module>` (and remove them with
`go tool anetos remove <module>`): `plugins.go` lists them, and `main.go`
loads them. See [Use plugins](https://github.com/anetos-dev/anetos/blob/main/docs/site/guides/plugins.md).

Tests use [anetostest](https://github.com/anetos-dev/anetos/blob/main/docs/site/guides/testing.md):
each gets the app with a migrated database, and leaves no rows behind.
It's an in-memory SQLite database, so there's nothing
to set up.

## Deploy

```sh
go tool anetos build    # bin/bookmarks: one binary, everything in it
```

Copy the binary to the server with its settings
(`deploy/production.env.example` lists them) and run `bookmarks migrate`,
then `bookmarks run`; `deploy/bookmarks.service` does that with systemd.
Or build the image with `docker build -t bookmarks .` (the
`Dockerfile`). See [Deploy](https://github.com/anetos-dev/anetos/blob/main/docs/site/guides/deployment.md).

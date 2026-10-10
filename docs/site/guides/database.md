---
title: Connect to a database
since: v0.1.0
group: "Data"
weight: 200
---

# Connect to a database

Open your app's database from configuration and use it from handlers, jobs
and background tasks.

## Before you start

Pick a driver module. Each is a separate Go module, so your binary only
contains the drivers you use:

| Database | Module | `DB_DRIVER` |
|---|---|---|
| SQLite (pure Go, no C compiler) | `anetos.dev/anetos/drivers/sqlite` | `sqlite` |
| PostgreSQL | `anetos.dev/anetos/drivers/postgres` | `postgres` |
| MySQL, MariaDB | `anetos.dev/anetos/drivers/mysql` | `mysql` |

```sh
go get anetos.dev/anetos/drivers/sqlite
```

## Steps

### 1. Configure the connection

```sh
# .env
DB_DRIVER=postgres
DB_HOST=127.0.0.1
DB_NAME=blog
DB_USER=blog
DB_PASSWORD=secret
```

For SQLite, `DB_NAME` is a file path (default `database/app.db`).
A database on another machine is reached over TLS that checks its
certificate (`DB_TLS=verify`, the default for any host but `localhost`
or a loopback address); `DB_TLS_CA` names a PEM file of your provider's
certificate authority when the system doesn't know it, and
`DB_TLS=none` turns TLS off for a private network you trust. Put other
driver options in `DB_URL`, which replaces the individual settings
(TLS included). Every key is in the
[configuration reference](../reference/configuration.md#database).

### 2. Connect at startup

```go
// DB_DRIVER (default sqlite) picks one of the drivers passed here.
if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
	return nil, err
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `connect`.)

Pass every driver the app may use: `DB_DRIVER` picks one, so you can
develop on SQLite and deploy on PostgreSQL with the same binary.

`db.Connect` pings the database when the app boots, so a wrong password
stops the app (or a command such as `migrate`) at startup instead of on
the first request, while `./app help` works without a database. It also:

- adds the connection to every context the app creates (HTTP requests,
  `app.Go` tasks, components, shutdown hooks);
- provides it as a `*db.DB` service (`anetos.Resolve[*db.DB](app)`);
- closes it in a shutdown hook, after the components stop.

### 3. Query with the request context

Any function with the request's context can query, with no database field
to pass around:

```go
// illustrative
func (h *Posts) Show(c *web.Ctx, in PostID) (Post, error) {
	return db.Find[Post](c, in.ID) // c is a context.Context
}
```

Code outside a request, such as a CLI command, gets the same values from
`app.Context(ctx)`.

### 4. Add more connections (optional)

Read another set of keys with a prefix and put that database in the
context when you need it:

```go
// illustrative
cfg, err := db.LoadConfig(app.Source(), "ANALYTICS_") // ANALYTICS_DB_DRIVER, ANALYTICS_DB_HOST, …
analytics, err := db.Open(postgres.Driver(), cfg, db.WithLogger(app.Logger()))
app.OnShutdown("analytics-db", func(context.Context) error { return analytics.Close() })

events, err := db.Query[Event](db.WithDB(ctx, analytics)).Get()
```

### 5. Switch a project to PostgreSQL or MySQL

A project made with `anetos new` uses SQLite unless you passed
`--db postgres` or `--db mysql`. To switch later:

1. Add the driver: `go get anetos.dev/anetos/drivers/postgres`,
   and pass `postgres.Driver()` to `db.Connect` in `main.go` (keep
   `sqlite.Driver()` too if you still want SQLite anywhere).
2. Set `DB_DRIVER=postgres` and the other `DB_*` settings in `.env`
   (and `.env.example`), as in step 1.
3. Create a test database and write `.env.testing` with **all** its
   settings: tests don't read `.env`, so nothing is inherited from it.

   ```sh
   # .env.testing
   DB_DRIVER=postgres
   DB_HOST=127.0.0.1
   DB_NAME=blog_test
   DB_USER=blog
   DB_PASSWORD=secret
   ```

   Without `DB_DRIVER` there (or without the file), tests would
   use SQLite; `anetostest` stops them with a message when `.env` has
   another `DB_DRIVER`.
4. `go run . migrate`, then `go test ./...`.
5. The deploy files still say SQLite: in `deploy/production.env.example`,
   put `DB_DRIVER` and `DB_URL` in place of SQLite's settings; in
   the `Dockerfile`, drop `DB_NAME` and `DB_MIGRATE_ON_START` (run
   `migrate` on each deploy instead); in `deploy/<name>.service`, drop
   `DB_NAME`; the README's "nothing to set up" is SQLite's too. A
   project made with `--db postgres` shows the server versions of these
   files.

Migrations written with the schema builder run on every database; raw
SQL in migrations may need changes.

## How it works

A `*db.DB` wraps a `database/sql` pool and the database's dialect. Queries
look it up in their context with `db.From`, and use a transaction instead
if the context carries one (see [Transactions](transactions.md)). The
[data layer concept](../concepts/data-layer.md) explains why.

In development, every query is logged at debug level with its duration.
Queries slower than `DB_SLOW_QUERY` (default 500ms) are logged as warnings
in every environment. In development and tests, a request (or job, …)
that runs the same query five times or more is logged as a warning: see
[Find N+1 queries](n-plus-one.md).

When the app starts, it also checks that the database can serve what the
app asks of it (the `DB_SEARCH_*` settings, features' requirements), and
stops with a clear message if it can't: see
[Add full-text search](search.md#choose-the-settings).

> **Coming from Laravel?** `DB_HOST`, `DB_PORT` and `DB_PASSWORD` are
> Laravel's; `DB_DRIVER`, `DB_NAME` and `DB_USER` are its
> `DB_CONNECTION`, `DB_DATABASE` and `DB_USERNAME` (those are read too,
> with a warning, until v0.6). There is no `config/database.php`: extra
> connections use prefixed keys.

## Testing it

`anetostest.New(t, setup)` boots the app with an in-memory SQLite
database (or, with `DB_*` set, your test database inside a transaction
rolled back at the end) and runs the migrations; pass `app.Context()` to
code that queries. See [Test your app](testing.md#5-check-the-database).

To test code without the app, open a database and put it in a context:

```go
// illustrative
d, err := db.Open(sqlite.Driver(), db.Config{Name: ":memory:"})
if err != nil {
	t.Fatal(err)
}
t.Cleanup(func() { d.Close() })
ctx := db.WithDB(t.Context(), d)
```

An in-memory database has a single connection, so a query that waits for
a second one (on the outer context inside `db.Tx`, or inside an `All()`
loop) blocks. A file in `t.TempDir()` behaves like production instead.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `db: DB_DRIVER is "postgres", but the drivers passed to Connect are [sqlite]` | The driver isn't passed to `Connect` | Import the driver module and pass its `Driver()` |
| `db: no database in context` | The context didn't come from the app | Use the request's `c`, a context from `app.Context`, or `db.WithDB` |
| `connect to postgres: … connection refused` at startup | Wrong host or port, or the server isn't running | Check `DB_HOST`/`DB_PORT`; the error comes from the ping in `Connect` |
| `… certificate signed by unknown authority`, `server does not support SSL`, `TLS requested but server does not support TLS` | A remote `DB_HOST` gets verified TLS by default | Set `DB_TLS_CA` to your provider's CA file, or `DB_TLS=none` on a private network (a Compose service, say) |
| `database is locked` on SQLite | A write took longer than the 5s busy timeout | Keep transactions short; SQLite allows one writer at a time |

## Next steps

- [Migrations](migrations.md)
- [Define models and save data](models.md)
- [Query data](queries.md)

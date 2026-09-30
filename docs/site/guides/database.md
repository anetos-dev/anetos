---
title: Connect to a database
since: v0.1.0
---

# Connect to a database

Open your app's database from configuration and use it from handlers, jobs
and background tasks.

## Before you start

Pick a driver module. Each is a separate Go module, so your binary only
contains the drivers you use:

| Database | Module | `DB_CONNECTION` |
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
DB_CONNECTION=postgres
DB_HOST=127.0.0.1
DB_DATABASE=blog
DB_USERNAME=blog
DB_PASSWORD=secret
```

For SQLite, `DB_DATABASE` is a file path (default `database/app.db`). Put
TLS and other driver options in `DB_URL`, which replaces the individual
settings. Every key is in the
[configuration reference](../reference/configuration.md#database).

### 2. Connect at startup

```go
// DB_CONNECTION (default sqlite) picks one of the drivers passed here.
if _, err := db.Connect(ctx, app, sqlite.Driver()); err != nil {
	return nil, nil, err
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `connect`.)

Pass every driver the app may use: `DB_CONNECTION` picks one, so you can
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
cfg, err := db.LoadConfig(app.Source(), "ANALYTICS_") // ANALYTICS_DB_CONNECTION, ANALYTICS_DB_HOST, …
analytics, err := db.Open(postgres.Driver(), cfg, db.WithLogger(app.Logger()))
app.OnShutdown("analytics-db", func(context.Context) error { return analytics.Close() })

events, err := db.Query[Event](db.WithDB(ctx, analytics)).Get()
```

## How it works

A `*db.DB` wraps a `database/sql` pool and the database's dialect. Queries
look it up in their context with `db.From`, and use a transaction instead
if the context carries one (see [Transactions](transactions.md)). The
[data layer concept](../concepts/data-layer.md) explains why.

In development, every query is logged at debug level with its duration.
Queries slower than `DB_SLOW_QUERY` (default 500ms) are logged as warnings
in every environment.

> **Coming from Laravel?** `DB_CONNECTION`, `DB_HOST` and friends mean what
> they mean in Laravel's `.env`. There is no `config/database.php`: extra
> connections use prefixed keys.

## Testing it

Use an in-memory SQLite database and put it in the test's context:

```go
// illustrative
func testDB(t *testing.T) context.Context {
	d, err := db.Open(sqlite.Driver(), db.Config{Database: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := db.WithDB(t.Context(), d)
	if _, err := db.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	return ctx
}
```

An in-memory database has a single connection, so a query that waits for
a second one (on the outer context inside `db.Tx`, or inside an `All()`
loop) blocks. A file in `t.TempDir()` behaves like production instead.
Per-test transaction rollback and an app test helper arrive with the
testing helpers (roadmap F12).

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `db: DB_CONNECTION is "postgres", but the drivers passed to Connect are [sqlite]` | The driver isn't passed to `Connect` | Import the driver module and pass its `Driver()` |
| `db: no database in context` | The context didn't come from the app | Use the request's `c`, a context from `app.Context`, or `db.WithDB` |
| `connect to postgres: … connection refused` at startup | Wrong host or port, or the server isn't running | Check `DB_HOST`/`DB_PORT`; the error comes from the ping in `Connect` |
| `database is locked` on SQLite | A write took longer than the 5s busy timeout | Keep transactions short; SQLite allows one writer at a time |

## Next steps

- [Migrations](migrations.md)
- [Define models and save data](models.md)
- [Query data](queries.md)

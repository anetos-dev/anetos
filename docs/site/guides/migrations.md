---
title: Migrations
since: v0.1.0
---

# Migrations

Create and change your database tables with versioned migrations that are
compiled into your app and run with one command.

## Before you start

[Connect to a database](database.md).

## Steps

### 1. Create a migration set

A set is the list of your app's migrations. Each migration has an ID that
starts with a timestamp, which decides the order:

```go
// Migrations is the app's migration set. In a larger app it lives in its
// own package (database/migrations), with one file per migration.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.Add("2026_10_01_120000_create_authors", createAuthors{})
	Migrations.Add("2026_10_01_120100_create_posts", createPosts{})
	Migrations.AddFunc("2026_10_02_090000_add_posts_views_index",
		func(s *migrate.Schema) error {
			return s.Alter("posts", func(t *migrate.Table) { t.Index("views") })
		},
		func(s *migrate.Schema) error {
			return s.Alter("posts", func(t *migrate.Table) { t.DropIndex("views") })
		})
}
```

(Copied from [`examples/database/migrations.go`](../../../examples/database/migrations.go), region `set`.)

In a larger app, put the set in its own package (`database/migrations`)
with one file per migration, each adding itself in `init`. The
`make:migration` generator (roadmap F11) will write those files for you.

### 2. Write the migration

`Up` makes the change and `Down` undoes it. The schema builder writes the
right SQL for PostgreSQL, MySQL and SQLite:

```go
type createPosts struct{}

func (createPosts) Up(s *migrate.Schema) error {
	return s.Create("posts", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("author_id").Constrained().CascadeOnDelete() // references authors(id)
		t.String("title", 200)
		t.Text("body")
		t.JSON("tags").Nullable()
		t.Integer("views").Default(0)
		t.Timestamp("published_at").Nullable()
		t.Timestamps()
		t.SoftDeletes()
		t.Index("author_id", "published_at")
	})
}

func (createPosts) Down(s *migrate.Schema) error { return s.Drop("posts") }
```

(Region `create-posts`.)

`t.ID()`, `t.Timestamps()` and `t.SoftDeletes()` create exactly the
columns `db.Model`, `db.Timestamps` and `db.SoftDeletes` expect.
`ForeignID(...).Constrained()` references the table named after the
column (`author_id` → `authors`). Every column type and option is in the
[migrations reference](../reference/migrations.md).

To change an existing table, use `s.Alter`:

```go
// illustrative
func (addSubtitle) Up(s *migrate.Schema) error {
	return s.Alter("posts", func(t *migrate.Table) {
		t.String("subtitle", 200).Nullable()
		t.RenameColumn("body", "content")
		t.Index("subtitle")
	})
}
```

For anything the builder doesn't cover, write SQL with `s.Exec`, and use
`s.Dialect()` if it differs per database. SQL without arguments is sent
as written (a `?` is just a `?`), statements separated by `;` run one by
one, and trigger and function bodies (`BEGIN … END`, `$$ … $$`) stay
whole.

A column added to an existing table must be `Nullable()` or have a
`Default`: PostgreSQL and SQLite refuse a NOT NULL column without one on
a table with rows, and MySQL would silently fill in zeros, so the builder
refuses on every database.

### 3. Connect the runner

```go
runner, err := migrate.ForApp(app, []*migrate.Set{Migrations}, migrate.WithSeeders(Seeders...))
if err != nil {
	return nil, nil, err
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `runner`.)

Pass one set per source: your app's, and one for each plugin that ships
migrations. They run in ID order across all sets.

### 4. Run the commands

Until the app binary's command framework arrives (roadmap F11), hand the
command-line arguments to the runner before starting the app:

```go
// go run . migrate | migrate:rollback | migrate:status | migrate:fresh --seed | db:seed
if handled, err := runner.Command(ctx, os.Args[1:], os.Stdout); handled {
	return errors.Join(err, app.Close())
}
```

(Region `commands`.)

| Command | Does |
|---|---|
| `migrate [--seed]` | Applies pending migrations, as one batch (then runs the seeders) |
| `migrate:status` | Lists migrations: `Ran` (with batch), `Pending`, or `Missing` (applied but no longer in any set) |
| `migrate:rollback [--step=N]` | Undoes the last N batches (default 1: the last `migrate` run) |
| `migrate:reset` | Undoes every migration |
| `migrate:fresh [--seed]` | Drops **every table** and migrates from scratch; development and testing only |
| `db:seed [--seeder=NAME]` | Runs seeders (see [Seed the database](seeders.md)) |

In production, `migrate:rollback`, `migrate:reset` and `db:seed` also
need `--force`. `migrate:fresh` refuses to run there at all. A flag a
command doesn't take is an error, and `-h` lists a command's flags.

### 5. Migrate when you deploy

Run `./app migrate` before the new version starts serving. Instances
starting at the same time are safe: on PostgreSQL and MySQL the runner
holds a lock in the database, so one instance migrates and the others
wait and find nothing left to do. The lock needs its own connection, so
the runner requires `DB_MAX_OPEN_CONNS` of at least 2 there.

## SQL migrations (optional)

If you prefer SQL files, embed them and add them to the set; `ID.up.sql`
applies and `ID.down.sql` (optional) undoes. They are sent as written and
split at `;` like `s.Exec`; a first line `-- anetos:no-transaction` runs
the file outside a transaction, and `-- anetos:no-split` sends it as one
statement:

```go
// illustrative
//go:embed sql/*.sql
var sqlFiles embed.FS

func init() {
	if err := Migrations.AddFS(sqlFiles, "sql"); err != nil {
		panic(err)
	}
}
```

## How it works

Applied migrations are recorded in the `migrations` table with their set,
their ID and the batch they ran in. On PostgreSQL and SQLite each
migration and its record run in one transaction, so a migration that
fails leaves nothing behind and can simply be fixed and re-run. MySQL
commits every schema change immediately: a migration that fails halfway
leaves its first changes in place, so keep MySQL migrations small.
Data changes in a MySQL migration aren't transactional either. For a
migration that must not run in a transaction (PostgreSQL's `CREATE INDEX
CONCURRENTLY`), implement `WithoutTransaction()`, or add it with
`set.Add(id, migrate.NoTransaction(migrate.Func(up, down)))`.

On SQLite, foreign key enforcement is off while a migration runs (and
`PRAGMA foreign_key_check` must pass before it commits). That makes the
usual way to change a column safe: create the new table, copy the rows,
drop the old table, rename the new one. The drop doesn't cascade to the
tables that reference it.

Migrations are Go code compiled into the binary, so a deploy ships exactly
the migrations its code expects, and no migration files need to be copied
to the server.

> **Coming from Laravel?** The commands, batches and `--step` work like
> Artisan's. `s.Create`/`s.Alter` are `Schema::create`/`Schema::table`,
> and `$table->foreignId('user_id')->constrained()` is
> `t.ForeignID("user_id").Constrained()`. Migrations are registered in a
> set instead of discovered from a directory.

## Testing it

Run the migrations against an in-memory SQLite database at the start of a
test:

```go
// illustrative
d, _ := db.Open(sqlite.Driver(), db.Config{Database: ":memory:"})
runner, _ := migrate.NewRunner(d, []*migrate.Set{migrations.All}, migrate.WithEnvironment(anetos.Testing))
if _, err := runner.Up(t.Context()); err != nil {
	t.Fatal(err)
}
ctx := db.WithDB(t.Context(), d)
```

Roll back in a test too (`runner.Reset`) to check that every `Down`
works.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `SQLite can't change columns` | SQLite's `ALTER TABLE` can't modify a column | Create the new table, copy the rows with `s.Exec`, drop the old one, rename (foreign keys are off during the migration) |
| `a column added to an existing table needs Nullable() or a Default` | NOT NULL without a default fails (or is silently zero-filled) on existing rows | Add `Nullable()` or `Default(…)`; backfill, then `Change()` it to NOT NULL |
| `the runner needs at least 2 connections` | `DB_MAX_OPEN_CONNS=1` on PostgreSQL or MySQL | Allow 2 or more |
| `cannot be cast automatically` from a PostgreSQL `Change()` | The type change needs a conversion | Add `.Using("code::integer")` |
| `relation … already exists` for an index | Two long index names were cut to the same prefix by an older tool | Name them: `t.IndexNamed(name, cols...)` |
| `SQLite can't add a column defaulting to the current time` | SQLite only allows constant defaults in `ADD COLUMN` | Add it `Nullable()` and fill it with `s.Exec("UPDATE …")` |
| `… is applied but not registered, so it can't be rolled back` | A migration was removed from the set after it ran | Put it back, or leave that batch alone |
| `fresh drops every table and only runs in development and testing` | `APP_ENV` is production or staging | Use `migrate:rollback`/`migrate` there |
| A MySQL migration failed and re-running it says a table exists | MySQL doesn't roll back schema changes | Undo the partial change by hand, then re-run |
| Migrations run in the wrong order | IDs don't start with a sortable timestamp | Name them `YYYY_MM_DD_HHMMSS_description` |

## Next steps

- [Seed the database](seeders.md)
- [Migrations reference](../reference/migrations.md)
- [Define models and save data](models.md)

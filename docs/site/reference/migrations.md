---
title: Migrations reference
since: v0.1.0
---

# Migrations reference

The schema builder, the migration runner and the commands in package
`anetos.dev/anetos/db/migrate`. See [Migrations](../guides/migrations.md)
and [Seed the database](../guides/seeders.md) for walkthroughs.

## Column types

| Method | PostgreSQL | MySQL / MariaDB | SQLite | Go field |
|---|---|---|---|---|
| `t.ID()` | `BIGSERIAL PRIMARY KEY` | `BIGINT AUTO_INCREMENT PRIMARY KEY` | `INTEGER PRIMARY KEY AUTOINCREMENT` | `int64` (`db.Model`) |
| `t.String(name, n)` | `VARCHAR(n)` | `VARCHAR(n)` | `VARCHAR(n)` | `string`; `n = 0` means 255 |
| `t.Text(name)` | `TEXT` | `TEXT` (64KB) | `TEXT` | `string` |
| `t.LongText(name)` | `TEXT` | `LONGTEXT` | `TEXT` | `string` |
| `t.Integer(name)` | `INTEGER` | `INT` | `INTEGER` | `int32`, `int` |
| `t.BigInteger(name)` | `BIGINT` | `BIGINT` | `BIGINT` | `int64` |
| `t.SmallInteger(name)` | `SMALLINT` | `SMALLINT` | `SMALLINT` | `int16` |
| `t.Boolean(name)` | `BOOLEAN` | `BOOLEAN` (TINYINT(1)) | `BOOLEAN` | `bool` |
| `t.Float(name)` | `DOUBLE PRECISION` | `DOUBLE` | `REAL` | `float64` |
| `t.Decimal(name, p, s)` | `NUMERIC(p, s)` | `DECIMAL(p, s)` | `TEXT` (exact, compares as text) | `string` or a decimal type (not `float64`) |
| `t.Date(name)` | `DATE` | `DATE` | `DATE` | `time.Time` (build dates in UTC) |
| `t.Timestamp(name)` | `TIMESTAMPTZ` | `DATETIME(6)` | `DATETIME` | `time.Time` |
| `t.JSON(name)` | `JSONB` | `JSON` | `TEXT` | any, with `db:"name,json"` |
| `t.Binary(name)` | `BYTEA` | `LONGBLOB` | `BLOB` | `[]byte` |
| `t.UUID(name)` | `UUID` | `CHAR(36)` | `TEXT` | `string` |
| `t.ForeignID(name)` | `BIGINT` | `BIGINT` | `BIGINT` | `int64` |
| `t.Vector(name, dims)` | `vector(dims)` (pgvector) | `VECTOR(dims)` (MariaDB 11.7+) | `BLOB` (little-endian float32s) | `db.Vector` |
| `t.Timestamps()` | `created_at`, `updated_at`: timestamps defaulting to now | | | `db.Timestamps` |
| `t.SoftDeletes()` | `deleted_at`: nullable timestamp | | | `db.SoftDeletes` |

## Column modifiers

| Modifier | Effect |
|---|---|
| `.Nullable()` | Allows NULL (columns are `NOT NULL` otherwise) |
| `.Default(v)` | Default value: string, number, bool or nil |
| `.DefaultRaw(sql)` | Default written in SQL, e.g. `"gen_random_uuid()"` |
| `.UseCurrent()` | Defaults a timestamp to the current time |
| `.Unique()`, `.Index()` | Index on this column (see names below) |
| `.Primary()` | This column is the primary key |
| `.References(table, col...)` | Foreign key to *table* (`id` if no column given) |
| `.Constrained()` | Foreign key to the table named after the column: `author_id` → `authors(id)` |
| `.Change()` | In `Alter`: change this existing column's type, NULL-ness and default (PostgreSQL, MySQL) |
| `.Using(sql)` | With `Change()` on PostgreSQL: the conversion, e.g. `"code::integer"` |

Foreign keys continue with `.OnDelete(migrate.Cascade)` (also `SetNull`,
`Restrict`, `NoAction`, `SetDefault`), `.OnUpdate(...)`,
`.CascadeOnDelete()`, `.NullOnDelete()` and `.Named(name)`. `References`
and `Constrained` return the foreign key, so put column modifiers such as
`Nullable()` before them.

A NOT NULL column can't `Default(nil)`, and a column added in `Alter`
must be `Nullable()` or have a default: the builder reports both.

## Table methods

| Method | In | Effect |
|---|---|---|
| `t.Index(cols...)`, `t.Unique(cols...)` | Create, Alter | Index or unique index |
| `t.Primary(cols...)` | Create | Composite primary key (not with `ID()`) |
| `t.Foreign(cols...).References(...)` | Create, Alter (not SQLite) | Foreign key on existing columns |
| `t.DropColumn(names...)` | Alter | Removes columns |
| `t.RenameColumn(from, to)` | Alter | Renames a column |
| `t.DropIndex(cols...)`, `t.DropUnique(cols...)` | Alter | Removes the index made by `Index`/`Unique` on those columns |
| `t.DropForeign(cols...)` | Alter (not SQLite) | Removes the foreign key on those columns |
| `t.IndexNamed(name, cols...)`, `t.UniqueNamed(name, cols...)` | Create, Alter | Index with your own name |
| `t.DropIndexNamed(name)`, `t.DropForeignNamed(name)` | Alter | Drop by name (after `Rename`, or for older schemas) |
| `t.SearchIndex(cols...)` | Create, Alter | Full-text search index over text columns, most important first ([search](#search-indexes)); one per table |
| `t.DropSearchIndex()` | Alter | Removes the table's search index |

Index names follow Laravel's: `posts_author_id_created_at_index`,
`posts_slug_unique`, `posts_author_id_foreign`. A name longer than 63
bytes is shortened to 54 bytes plus a hash of the full name, the same way
for create and drop. Renaming a table keeps its index names.

Table names can't be schema-qualified (`audit.logs`): connect to that
schema or database instead.

## Schema methods

| Method | Effect |
|---|---|
| `s.Create(table, fn)` | `CREATE TABLE` plus its indexes |
| `s.Alter(table, fn)` | Adds, changes, renames and drops columns, indexes and foreign keys |
| `s.Drop(table)`, `s.DropIfExists(table)` | Drops a table, and its search index |
| `s.Rename(from, to)` | Renames a table; refused when it has a search index (drop it first, add it again after) |
| `s.HasTable(table)`, `s.HasColumn(table, col)` | Whether they exist |
| `s.CreateEmbeddings(table, dims)`, `s.DropEmbeddings(table)` | The table of `table`'s embeddings: see [Embeddings tables](#embeddings-tables) |
| `s.Exec(sql, args...)` | Raw SQL. Without arguments: sent as written, split at `;` (trigger and function bodies stay whole). With arguments: one statement with `?` placeholders |
| `s.Context()` | The migration's context (database and transaction), for the db package |
| `s.Dialect()` | `"postgres"`, `"mysql"` or `"sqlite"` |

`migrate.NewSchema(ctx)` gives a Schema outside migrations (for tests).

## SQLite limits

SQLite's `ALTER TABLE` can add, rename and drop columns, but can't change
them, add or drop foreign keys, add primary keys, or add a column whose
default is the current time. The builder returns an error for those; the
usual fix is a new table, a copy of the rows, a drop and a rename.
Foreign key enforcement is off during SQLite migrations (checked with
`PRAGMA foreign_key_check` before commit), so the drop doesn't cascade.

## Sets and migrations

| API | Does |
|---|---|
| `migrate.NewSet(name)` | A set of migrations: `"app"`, or a plugin's name |
| `set.Add(id, m)` | Adds a `Migration` (`Up(*Schema) error`, `Down(*Schema) error`); panics on a bad or duplicate ID |
| `set.AddFunc(id, up, down)` | Adds two functions; a nil `down` makes it irreversible |
| `set.AddFS(fsys, dir)` | Adds `ID.up.sql` / `ID.down.sql` files; first-line directives `-- anetos:no-transaction`, `-- anetos:no-split` |
| `migrate.Func(up, down)` | A migration from two functions |
| `migrate.NoTransaction(m)`, or a `WithoutTransaction()` method | Runs that migration outside a transaction |
| `migrate.ErrIrreversible` | Returned by `Down` of a migration that can't be undone |

IDs are ASCII letters, digits, `_`, `-` and `.`, up to 255 characters, and
are sorted as strings: start them with `YYYY_MM_DD_HHMMSS`.

## Runner

| API | Does |
|---|---|
| `migrate.ForApp(app, sets, opts...)` | Runner on the app's database, environment and logger |
| `migrate.NewRunner(d, sets, opts...)` | Runner on any `*db.DB` |
| `WithSeeders(...)`, `WithTable(name)`, `WithEnvironment(env)`, `WithLogger(l)` | Options; the default table is `migrations`, the default environment production |
| `Up(ctx)` | Applies pending migrations as one batch; returns them |
| `Rollback(ctx, n)` | Undoes the last *n* batches |
| `Reset(ctx)` | Undoes everything |
| `Fresh(ctx)` | Drops every table and view (PostgreSQL: also materialized views and enum types; extension objects stay), then `Up`; `migrate.ErrNotAllowed` outside development and testing. Procedures and functions are not dropped |
| `Status(ctx)` | Every migration: applied (batch, time), pending, or missing |
| `Seed(ctx, names...)` | Runs seeders, each in a transaction |
| `Command(ctx, args, out)` | The commands below, for programs without `app.Execute`; `handled` is false for other arguments |
| `AppCommands()` | The commands below as app binary commands; `migrate.ForApp` registers them |

## Commands

Registered on the app by `migrate.ForApp`, so the binary runs them
(`./app migrate`). Bad flags exit with status 2.

| Command | Flags | Production |
|---|---|---|
| `migrate` | `--seed`, `--force` | Allowed; `--seed` needs `--force` |
| `migrate:status` | | Allowed |
| `migrate:rollback` | `--step=N` (default 1) | Needs `--force` |
| `migrate:reset` | | Needs `--force` |
| `migrate:fresh` | `--seed` | Refused (also in staging) |
| `db:seed` | `--seeder=NAME` | Needs `--force` |
| `search:reindex` | table names (default: all) | Allowed |

Every command but `db:seed` changes the schema (`cmd.Command.ChangesSchema`),
so it runs even when the search indexes don't match `SEARCH_LANGUAGE` and
`SEARCH_RANKING`, which stops the app and other commands at boot.

On PostgreSQL and MySQL, runs are serialized with an advisory or named
lock (per database) held on its own connection: the pool needs at least
2 connections.

## Search indexes

`t.SearchIndex("title", "body")` builds, for `SEARCH_LANGUAGE` and
`SEARCH_RANKING` (which the database must support, or the migration
fails):

| Database | Objects |
|---|---|
| PostgreSQL | `search_vector`, a stored generated `tsvector` column (columns weighted A, B, C, D in order), and its GIN index `<table>_search_index`; with `SEARCH_RANKING=bm25`, also `search_text`, a generated text column, and its `bm25` index `<table>_search_bm25` (pg_textsearch) |
| MySQL, MariaDB | `search_text`, a stored generated `LONGTEXT` column (the columns joined), and its `FULLTEXT` index `<table>_search_index` |
| SQLite | `<table>_search`, an FTS5 table on the table's rows (tokenizer `unicode61`, with `porter` for english; column weights 1, 0.4, 0.2, 0.1), and the triggers `<table>_search_insert`, `_delete` and `_update` that keep it current |

In `Alter`, rows already in the table are indexed. Each index is
recorded in the `search_indexes` table (`db.SearchIndexes(ctx)` reads
it): its columns, language and ranking. `search:reindex` rebuilds
indexes from those records for the current settings, after checking
that every indexed column still exists. On MySQL and MariaDB, whose
schema changes aren't transactional, a rebuild that fails midway (a lock
timeout, a full disk) leaves the table without its index: run
`search:reindex` again. Columns must hold
text. `Alter` refuses to drop or rename an indexed column unless the
same `Alter` drops the index (`t.DropSearchIndex()`, the change, then
`t.SearchIndex` with the new columns). Object names longer than 63
bytes are shortened like index names.

## Embeddings tables

`s.CreateEmbeddings("articles", 1536)` creates `articles_embeddings`,
which [package `ai`](../guides/semantic-search.md) fills, and needs the
database's vector search (`db.VectorSearch`): SQLite, PostgreSQL with
pgvector, MariaDB 11.7+. Dimensions run from 1 to 16000.

| Column | Type |
|---|---|
| `id` | `t.ID()` |
| `record_id` | the record's ID, a foreign key to `articles.id` (not on MariaDB); the chunks are deleted with the record |
| `chunk` | the chunk's position, from 0; unique with `record_id` |
| `content`, `content_hash` | the chunk's text, and its SHA-256 |
| `model` | the embedding model's name, up to 100 characters |
| `embedding` | `t.Vector("embedding", dims)` |
| `created_at`, `updated_at` | `t.Timestamps()` |

| Database | Also |
|---|---|
| PostgreSQL | `CREATE EXTENSION IF NOT EXISTS vector` (a superuser's: pgvector isn't trusted, so create it beforehand otherwise); an HNSW index with `vector_cosine_ops`, up to 2000 dimensions (above, searches compare every chunk); `ON DELETE CASCADE` |
| MariaDB | `VECTOR INDEX … DISTANCE=cosine`; no foreign key: an `AFTER DELETE` trigger on `articles`, `articles_embeddings_delete_trigger`, deletes a record's chunks, since InnoDB's cascading deletes skip the vector index, which then misses rows. A record deleted by another table's cascade doesn't fire it: its chunks stay until `db.PruneChunks` (`ai:embed`), and searches skip them (the candidates are chunks of existing records). Creating the trigger needs the `TRIGGER` privilege, and with binary logging `SUPER` or `log_bin_trust_function_creators`; if it fails, the table is dropped again |
| SQLite | no index (the driver's `anetos_vec_distance_cosine` compares every chunk); `ON DELETE CASCADE` |

`s.DropEmbeddings("articles")` drops the table (and the trigger).

## The migrations table

| Column | Content |
|---|---|
| `id` | Order applied |
| `source` | The set's name |
| `migration` | The migration's ID (unique per source) |
| `batch` | Which `migrate` run applied it |
| `applied_at` | When (UTC) |

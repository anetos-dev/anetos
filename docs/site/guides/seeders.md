---
title: Seed the database
since: v0.1.0
---

# Seed the database

Fill a database with sample data for development, or with the reference
data every environment needs.

## Before you start

[Create your tables with migrations](migrations.md).

## Steps

### 1. Write seeders

A seeder is a name and a function. Use the db package inside it:

```go
// Seeders fill a development database with sample data: go run . db:seed
var Seeders = []migrate.Seeder{
	{Name: "authors", Run: func(ctx context.Context) error {
		return db.CreateMany(ctx, []Author{
			{Name: "Ada", Email: "ada@example.com"},
			{Name: "Grace", Email: "grace@example.com"},
		})
	}},
	{Name: "posts", Run: func(ctx context.Context) error {
		ada, err := db.Query[Author](ctx).Where(AuthorCols.Email.Eq("ada@example.com")).First()
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		return db.CreateMany(ctx, []Post{
			{AuthorID: ada.ID, Title: "Hello, Anetos", Body: "The first post.", PublishedAt: &now},
			{AuthorID: ada.ID, Title: "A draft", Body: "Not published yet."},
		})
	}},
}
```

(Copied from [`examples/database/migrations.go`](../../../examples/database/migrations.go), region `seeders`.)

`AuthorCols` holds the model's typed columns, written by
[`anetos gen`](code-generation.md).

### 2. Give them to the runner

```go
// illustrative
runner, err := migrate.ForApp(app, []*migrate.Set{Migrations}, migrate.WithSeeders(Seeders...))
```

### 3. Run them

```sh
./app db:seed                 # every seeder, in order
./app db:seed --seeder=posts  # one of them
./app migrate:fresh --seed    # rebuild the database and seed it (development only)
```

In production, `db:seed` needs `--force`.

## How it works

Seeders run in the order they are listed, each in its own transaction:
if one fails, its changes are rolled back and the ones before it stay.
Order them so data exists before other data refers to it.

> **Coming from Laravel?** A seeder is a `Seeder` class's `run` method,
> and the list passed to the runner plays the role of `DatabaseSeeder`.
> Model factories for generating fake rows arrive with the testing helpers
> (roadmap F12).

## Testing it

`runner.Seed(ctx)` in a test fills a fresh in-memory database, which is a
quick way to test code against realistic data.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `no seeder named "users"` | Not in the list passed with `WithSeeders` | Add it |
| A seeder fails with a unique-constraint error on the second run | Seeders insert again each time | Use `db.Upsert`, or check with `Exists` first |

## Next steps

- [Migrations](migrations.md)

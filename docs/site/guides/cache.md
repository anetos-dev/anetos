---
title: Cache values
since: v0.2.0
---

# Cache values

Keep the results of slow work for a while, count events for rate limits,
and use locks so only one instance of the app does a job at a time.

## Before you start

Choose a store with `CACHE_STORE`:

| Store | `CACHE_STORE` | Shared between instances | Needs |
|---|---|---|---|
| Memory | `memory` (default) | No: each process has its own | Nothing |
| Database | `database` | Yes | `db.Connect`, and the table from `cache.Migrations` |
| Redis | `redis` | Yes | A Redis (or Valkey) server, and the `drivers/redis` module |

The memory store suits one instance and development. With several
instances, use the database or Redis store: otherwise each instance
caches its own copy, and locks only work within one process.

## Steps

### 1. Set up the cache

Call `cache.ForApp` while setting up the app, after `db.Connect`:

```go
// CACHE_STORE (default memory) picks the store; database uses the
// cache table from cache.Migrations.
if _, err := cache.ForApp(app); err != nil {
	return nil, err
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `cache`.)

It adds the cache to every context the app creates (handlers, commands,
components), closes the store at shutdown and adds the `cache:clear`
command.

For the database store, add the table's migration to the runner:

```go
if _, err := migrate.ForApp(app, []*migrate.Set{Migrations, cache.Migrations("")}, migrate.WithSeeders(Seeders...)); err != nil {
	return nil, err
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `runner`.)

`cache.Migrations("")` creates the table `cache`; pass the name if you
set `CACHE_TABLE`.

For Redis, add the driver module and pass its driver:

```sh
go get anetos.dev/anetos/drivers/redis
```

```go
// illustrative
if _, err := cache.ForApp(app, redis.CacheDriver()); err != nil { // CACHE_STORE=redis, REDIS_URL
	return nil, err
}
```

### 2. Remember a value

`cache.Remember` returns the cached value, or calls the function, stores
its result for the ttl and returns it:

```go
// Stats serves the totals from the cache, computing them at most once a
// minute (and after a post is created or deleted).
func (Blog) Stats(c *web.Ctx, _ struct{}) ([]AuthorStats, error) {
	return cache.Remember(c, "stats", time.Minute, authorStats)
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `remember`.)

Values are stored as JSON, so any type that encodes and decodes with
`encoding/json` works: structs (exported fields), slices, maps, numbers.

If several requests miss the cache at once, the function runs once in
each process and the others wait for its result. If it returns an error,
nothing is stored and the error is returned.

### 3. Forget a value when its data changes

```go
// GET /stats counts the new post. The post is saved either way: a
// cache failure only delays that, so log it rather than fail.
if err := cache.Forget(c, "stats"); err != nil {
	c.Logger().Warn("forget the cached stats", "error", err)
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `forget`.)

Inside a transaction, forget in `db.AfterCommit` instead: otherwise a
request between the forget and the commit caches the old data again (see
[Transactions](transactions.md#3-run-side-effects-after-commit)). Choose
the ttl as the staleness you accept if a write forgets to forget.

### 4. Read and write values directly

```go
// illustrative
err := cache.Set(ctx, "profile:7", profile, time.Hour) // cache.Forever: no expiry
profile, ok, err := cache.Get[Profile](ctx, "profile:7")
added, err := cache.Add(ctx, "welcome-mail:7", true, 24*time.Hour) // only if absent
ok, err := cache.Has(ctx, "profile:7")
err = cache.Forget(ctx, "profile:7")
err = cache.Flush(ctx) // every key of this app's cache
```

`Get` returns `ok == false` for a missing or expired key, and an error if
the stored value doesn't decode as the type you ask for.

Keys are UTF-8 text of up to 250 bytes, including the prefix
(`CACHE_PREFIX`, by default the app's name and `:cache:`), and compare
exactly (case, accents and spaces count). Build them from parts with a
separator, such as `"profile:" + id`.

### 5. Count events

`cache.Increment` adds to a counter atomically and returns the new
value. A new counter expires after the ttl, and increments don't extend
it, which makes a fixed window:

```go
// illustrative
n, err := cache.Increment(ctx, "logins:"+ip, 1, time.Minute)
if err != nil {
	return nil, err
}
if n > 5 {
	return nil, web.Error(http.StatusTooManyRequests, "Too many attempts; try again in a minute.")
}
```

For request limits, [Rate limiting](rate-limiting.md) builds on this.
Pass a negative delta to decrement. Read a counter with
`cache.Get[int64]`. Incrementing a key that holds something other than
an integer, or past the range of `int64`, is an error.

### 6. Run work under a lock

A lock is held by one owner at a time, across every instance that shares
the store. `cache.TryWithLock` runs the function only if the lock is
free, which suits work one instance should do:

```go
// illustrative
err := cache.TryWithLock(ctx, "reports:monthly", 10*time.Minute, func(ctx context.Context) error {
	return buildMonthlyReport(ctx)
})
if errors.Is(err, cache.ErrLockHeld) {
	return nil // another instance is on it
}
```

`cache.WithLock` waits for the lock instead (until the context ends).
The ttl is how long a crashed holder can keep the lock: make it longer
than the work, or extend it as you go with `cache.NewLock`:

```go
// illustrative
lock := cache.NewLock(ctx, "import:"+name, time.Minute)
if err := lock.Acquire(ctx); err != nil {
	return err
}
defer lock.Release(context.WithoutCancel(ctx))
for batch := range batches {
	if ok, err := lock.Extend(ctx, time.Minute); err != nil {
		return err
	} else if !ok {
		return errors.New("the import took too long and lost its lock")
	}
	// ...
}
```

Locks are released only by their owner: after the ttl has passed,
another owner may hold the lock, and `Release` and `Extend` return
false.

### 7. Test

`anetostest.New` gives each test app a cache prefix of its own and
removes its items when the test ends, so tests don't see each other's
cached values:

```go
func TestStatsCache(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, Authors.With(func(a *Author) { a.Name = "Ada" }))
	app.GetJSON("/stats").AssertJSONPath("0.posts", 0) // computed and cached

	// A post written behind the handlers' back isn't counted yet...
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID = ada.ID }))
	app.GetJSON("/stats").AssertJSONPath("0.posts", 0)

	// ...until a handler forgets the cached totals.
	app.PostJSON("/posts", map[string]any{"author_id": ada.ID, "title": "Hello", "body": "First post"}).AssertCreated()
	app.GetJSON("/stats").AssertJSONPath("0.posts", 2)
}
```

(Copied from [`examples/database/main_test.go`](../../../examples/database/main_test.go), region `test-cache`.)

## Clear the cache

```sh
go run . cache:clear
```

It removes the keys with the app's prefix, locks included, and leaves
other apps' keys in a shared store alone. Run it after a deploy that
changes the type of a cached value, or let `Remember` replace such values
as they are read (it logs a warning and recomputes them).

## Stores in detail

- **Memory**: expired items are removed when read, and the rest about
  once a minute. Items are gone when the process stops.
- **Database**: one row per key in the `cache` table, with the expiry
  in Unix milliseconds by the database server's clock, so instances
  whose clocks drift still agree. Expired rows are deleted every few
  minutes while the app writes to the cache. With PostgreSQL and MySQL,
  the store uses its own connections, not the request's transaction: a
  value cached or a lock taken inside a transaction stays if the
  transaction rolls back, and each cache call made inside a transaction
  needs a second connection from the pool, so keep `DB_MAX_OPEN_CONNS`
  above the number of transactions that run at once. SQLite allows one
  writer at a time, so there the store joins the transaction, and its
  writes roll back with it.
- **Redis**: items expire in Redis. `cache:clear` finds keys with
  `SCAN` and removes them with `UNLINK`, without blocking the server. The
  app shares one client, made by `redis.Connect`, between the cache and
  other Redis features. See [Redis settings](../reference/configuration.md#redis).

A cache made by hand with `cache.New(store, "")` has no prefix: its
`cache.Flush` empties the whole store, in Redis everything in the
database, sessions and queues included. `cache.ForApp` always sets a
prefix.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `cache: no cache in context` | `cache.ForApp` wasn't called, or the context didn't come from the app | Call it in setup; in code outside the app, use `cache.WithCache(ctx, c)` |
| `CACHE_STORE is "redis", but the drivers are [memory, database]` | The driver wasn't passed | `cache.ForApp(app, redis.CacheDriver())` |
| `the database store needs the app's database` | `cache.ForApp` ran before `db.Connect` | Connect the database first |
| `no such table: cache` / `relation "cache" does not exist` | The migration didn't run | Add `cache.Migrations("")` to the runner and run `migrate` |
| `holds something that isn't a …` | The cached value was stored as another type | `cache:clear`, or change the key when you change the type |
| Instances see different values; a lock doesn't exclude other instances | The memory store is per process | Use the database or Redis store |
| `redis: connect to …` at startup | The server isn't reachable at `REDIS_URL` | Check the URL, password and network |

## Next steps

- [Configuration reference: cache](../reference/configuration.md#cache)
- [Rate limiting](rate-limiting.md)
- [Background tasks](background-tasks.md)
- [Transactions](transactions.md)

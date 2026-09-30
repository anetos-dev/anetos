---
title: Transactions
since: v0.1.0
---

# Transactions

Make several writes succeed or fail together, and run follow-up work only
after they are saved.

## Before you start

[Connect to a database](database.md).

## Steps

### 1. Wrap the work in `db.Tx`

```go
// illustrative
err := db.Tx(ctx, func(ctx context.Context) error {
	if err := db.Create(ctx, &order); err != nil {
		return err // rolls back
	}
	return db.Create(ctx, &payment) // nil commits
})
```

Use the `ctx` passed to the function: the transaction travels in it, so
every query made with it (including in functions you call) joins the
transaction. An error or a panic rolls everything back.

### 2. Nest freely

Calling `db.Tx` inside a transaction starts a nested one with a
savepoint. If it fails, only its own work is undone, and the outer
function decides what to do:

```go
// illustrative
err := db.Tx(ctx, func(ctx context.Context) error {
	if err := db.Create(ctx, &order); err != nil {
		return err
	}
	if err := db.Tx(ctx, applyCoupon); err != nil {
		log.Warn("coupon not applied", "error", err) // the order still commits
	}
	return nil
})
```

### 3. Run side effects after commit

Emails, jobs and cache updates must not happen if the data is rolled
back:

```go
// illustrative
db.AfterCommit(ctx, func(ctx context.Context) {
	mailer.Send(ctx, OrderConfirmation{Order: order})
})
```

Outside a transaction, the function runs immediately. If the (nested)
transaction it was registered in rolls back, it never runs.

Forget cached values the same way, so no request caches the old data
again between the forget and the commit:

```go
// illustrative
db.AfterCommit(ctx, func(ctx context.Context) {
	_ = cache.Forget(ctx, "stats")
})
```

### 4. Lock rows you are about to change

```go
// illustrative
err := db.Tx(ctx, func(ctx context.Context) error {
	acct, err := db.Query[Account](ctx).Where(id.Eq(in.ID)).ForUpdate().First()
	if err != nil {
		return err
	}
	acct.Balance -= in.Amount
	return db.Update(ctx, &acct)
})
```

`ForUpdate` (and `ForShare`) lock the selected rows until the transaction
ends. SQLite has no row locks; its transactions take the database's write
lock when they start.

Use `db.TxWith(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable}, fn)`
for another isolation level or a read-only transaction.

### 5. Write outside the transaction

A record that must stay even if the transaction rolls back, such as an
audit entry for the attempt, is written with `db.WithoutTx(ctx)`, which
uses another connection:

```go
// illustrative
err := db.Tx(ctx, func(ctx context.Context) error {
	if err := db.Create(db.WithoutTx(ctx), &Audit{Action: "refund", OrderID: order.ID}); err != nil {
		return err
	}
	return refund(ctx, order)
})
```

With SQLite, whose transactions take the database's write lock when
they start, such a write waits for the transaction and fails after the
busy timeout: don't use `WithoutTx` there.

## Complete example

`ShowPost` in [`examples/database`](../../../examples/database/main.go)
counts a view and reads the post in one transaction.

## How it works

`db.Tx` begins a `database/sql` transaction and returns a context holding
it. If the function panics or calls `runtime.Goexit` (as `t.Fatal` does),
the transaction is rolled back and its connection released. If the
context is canceled before the commit, the returned error wraps
`context.Canceled`. Queries on that context's database use the transaction instead of the
pool. Nested calls issue `SAVEPOINT`, `ROLLBACK TO SAVEPOINT` and `RELEASE
SAVEPOINT`, which PostgreSQL, MySQL and SQLite all support. See the
[data layer concept](../concepts/data-layer.md).

> **Coming from Laravel?** `db.Tx` is `DB::transaction`, nested
> transactions use savepoints as Laravel's do, and `db.AfterCommit` is
> `DB::afterCommit`.

## Testing it

Call the function under test with a context from an in-memory database and
check the rows afterwards; `db.InTx(ctx)` reports whether code runs in a
transaction.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| Writes inside `Tx` are visible to other requests before commit | The code used the outer `ctx`, not the one passed to the function | Use the function's `ctx`; a query built earlier joins with `q.WithContext(ctx)` |
| A test hangs on an in-memory SQLite database | It has a single connection, and something waited for a second one (a query on the outer `ctx` inside `Tx`, or inside an `All()` loop) | Use the transaction's `ctx`; finish the loop first; or use a temporary database file |
| `conn busy` or a deadlock while iterating `All()` in a transaction | The transaction's single connection is still reading rows | Finish the loop (or use `Get`) before other queries |
| Queries from several goroutines in one transaction fail | A transaction is one connection | Run them one after another |

## Next steps

- [Query data](queries.md)
- [Raw SQL](raw-sql.md)

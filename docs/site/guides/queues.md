---
title: Queues
since: v0.2.0
---

# Queues

Run slow or failure-prone work in the background: charge a card, send an
email, build a report. A request dispatches a **job**, a typed struct, and
responds at once. **Workers** run the job later, retry it when it fails,
and keep a record of the jobs that failed for good. The complete app is
[`examples/queue`](../../../examples/queue).

## Before you start

Choose where jobs are kept with `QUEUE_DRIVER`:

| Driver | Jobs are kept | Use it for |
|---|---|---|
| `sync` (default) | Nowhere: each job runs when it is dispatched, and `Dispatch` returns its error | Development and tests |
| `memory` | In the process: lost when it stops | Development and tests with workers |
| `database` | In the app's database, tables `jobs` and `failed_jobs` | Production without another server |
| `redis` | In Redis 5 or later (`drivers/redis`, `REDIS_URL`) | Production with many jobs |

The database driver needs PostgreSQL, MySQL 8.0, MariaDB 10.6 or later, or
SQLite. Create its tables with the `queue.Migrations("", "")` migration
(new projects have it).

## Steps

### 1. Write a job

A job is a struct with a `Handle` method:

```go
// ChargeOrder charges an order. Its fields are what the worker gets: the
// order's ID, not the order.
type ChargeOrder struct {
	OrderID int64 `json:"order_id"`
}

// Handle charges the order. A job may run more than once, so it is
// idempotent: the job's ID is the gateway's idempotency key, and a paid
// order isn't charged again.
func (j ChargeOrder) Handle(ctx context.Context) error {
	o, err := db.Find[Order](ctx, j.OrderID)
	if errors.Is(err, db.ErrNotFound) {
		return queue.Permanent(err) // retrying won't make it appear
	}
	if err != nil {
		return err
	}
	if o.Status != "pending" {
		return nil
	}
	job, _ := queue.Current(ctx)
	switch err := gateway(ctx).Charge(ctx, job.ID, o.Cents); {
	case errors.Is(err, ErrDeclined):
		return queue.Permanent(err) // fail now: Failed marks the order
	case err != nil:
		return fmt.Errorf("charge order %d: %w", o.ID, err) // retried later
	}
	return setStatus(ctx, o.ID, "paid")
}

// Failed runs when the job fails for good: declined, or out of tries.
func (j ChargeOrder) Failed(ctx context.Context, err error) {
	slog.InfoContext(ctx, "order not charged", "order", j.OrderID, "error", err)
	if err := setStatus(ctx, j.OrderID, "failed"); err != nil {
		slog.ErrorContext(ctx, "mark the order failed", "order", j.OrderID, "error", err)
	}
}
```

(Copied from [`examples/queue/orders.go`](../../../examples/queue/orders.go), region `job`.)

The rules:

- **Fields are stored as JSON** when the job is dispatched. Keep them
  small: IDs, not records, which may have changed by the time the job
  runs.
- **`ctx` has what the app provides**: the database, the cache, the queue,
  and values you add with `app.AddContextValue` (the example's payment
  gateway). It also has the job's `queue.Info` (`queue.Current(ctx)`:
  its ID, attempt and tries) and its timeout.
- **Return an error to retry** the job later. Wrap it with
  `queue.Permanent` to fail the job at once, for errors that retrying
  won't fix.
- **A `Failed` method**, if the job has one, runs once when the job fails
  for good.

> **Warning:** A job can run more than once: delivery is
> **at-least-once**. A worker can die after the work but before it records
> it, and a job still running when its lease ends (the longest timeout of
> the job types, plus 30 seconds) can be picked up again. Make
> jobs idempotent: check whether the work is done, and give outside
> services an idempotency key (`queue.Current(ctx).ID` is the same for
> every attempt).

### 2. Set up the queue and its workers

In `setup`, after `db.Connect`, set up the queue, register each job
type, and add the workers:

```go
q, err := queue.ForApp(app, redis.QueueDriver()) // QUEUE_DRIVER: sync, memory, database or redis
if err != nil {
	return nil, err
}
if err := queue.Register[ChargeOrder](q, queue.Tries(5), queue.Timeout(30*time.Second)); err != nil {
	return nil, err
}
// Workers for the payments queue first, then the default one.
if err := q.Work(queue.Queues("payments", "default"), queue.Concurrency(4)); err != nil {
	return nil, err
}
```

(Copied from [`examples/queue`](../../../examples/queue/main.go), region `setup`.)

`queue.ForApp` reads the `QUEUE_*` settings (see the
[configuration reference](../reference/configuration.md#queue)). Pass
`redis.QueueDriver()` only if you use Redis; the other drivers are built
in.

`queue.Register` options set a job type's behavior:

| Option | Default | |
|---|---|---|
| `queue.Tries(n)` | `QUEUE_TRIES` (3) | Attempts before the job fails for good; 1 for no retries |
| `queue.Timeout(d)` | `QUEUE_TIMEOUT` (1m) | How long an attempt may run before its context is canceled |
| `queue.Backoff(d1, d2, …)` | `QUEUE_BACKOFF` (10s), doubling up to `QUEUE_BACKOFF_MAX` (10m) | Waits before the retries; the last repeats. Each varies by up to 20% |
| `queue.Name(s)` | The Go type, `main.ChargeOrder` | The name jobs are stored under: set it before renaming or moving the type |

`q.Work` adds the workers to the app, with the role `workers`:

| Option | Default | |
|---|---|---|
| `queue.Queues(names…)` | `QUEUE_DEFAULT` (`default`) | The queues to take jobs from, highest priority first |
| `queue.Concurrency(n)` | 1 | Jobs that run at once |
| `queue.ShutdownGrace(d)` | Half of `APP_SHUTDOWN_TIMEOUT` | How long running jobs may finish at shutdown |

`go run .` (the `run` command) runs the workers with the HTTP server. In
production, you can run them apart, from the same binary:

```bash
./app run --only=http      # web servers
./app run --only=workers   # workers, as many as you need
```

### 3. Dispatch jobs

Call `queue.Dispatch` with a request's context (or a job's):

```go
// PlaceOrder saves the order and dispatches the job that charges it.
func PlaceOrder(c *web.Ctx, in OrderInput) (web.Responder, error) {
	o := &Order{Item: in.Item, Cents: in.Cents, Email: in.Email, Status: "pending"}
	err := db.Tx(c, func(ctx context.Context) error {
		if err := db.Create(ctx, o); err != nil {
			return err
		}
		if err := events.Emit(ctx, OrderPlaced{OrderID: o.ID, Item: o.Item, Cents: o.Cents, Email: o.Email}); err != nil {
			return err // an On listener failed: no order
		}
		// AfterCommit: no charge for an order that isn't saved. (The
		// database driver writes the job in the transaction instead.)
		return queue.Dispatch(ctx, ChargeOrder{OrderID: o.ID}, queue.OnQueue("payments"), queue.AfterCommit())
	})
	if err != nil {
		return nil, err
	}
	return web.JSON(http.StatusAccepted, o), nil // 202: it is being charged
}
```

(Copied from [`examples/queue`](../../../examples/queue/main.go), region `dispatch`.)

| Option | |
|---|---|
| `queue.OnQueue(name)` | The queue (default `QUEUE_DEFAULT`). Names are lower-case letters, digits and `. _ : -` |
| `queue.Delay(d)` | Run the job after `d` |
| `queue.AfterCommit()` | Dispatch the job only once the transaction in the context commits; never if it rolls back |
| `queue.OnDispatched(fn)` | Call `fn(ctx, queue.Dispatched)` once the job is dispatched (after the commit with `AfterCommit`; not if the store refuses it or the transaction rolls back; with the sync driver, before it runs) |

Inside a transaction, a job must not run before the transaction commits,
or it may look for rows that aren't there yet:

- The **database** driver writes the job in the transaction, so the job is
  dispatched if and only if the transaction commits, with or without
  `queue.AfterCommit()`.
- With the other drivers, use `queue.AfterCommit()`. `Dispatch` then
  returns before the job is dispatched, so a failure to dispatch it is
  logged, not returned. (Outside a transaction, the job is dispatched at
  once, and `Dispatch` returns the error.)

Dispatching a type that isn't registered is an error, so a forgotten
`queue.Register` shows up at the first dispatch.

A job can also be a function, for work that needs dependencies a struct's
fields can't carry (the function can be a closure). Register it under a
name, with the type of its payload, and dispatch it by that name:

```go
// illustrative
err := queue.RegisterFunc(q, "reports.send", func(ctx context.Context, r ReportRequest) error {
	return reports.Send(ctx, r.Month)
}, queue.Tries(5))

err = queue.DispatchFunc(ctx, "reports.send", ReportRequest{Month: "2026-09"})
```

The name is stored with each job, like a struct job's type name, so it
must stay the same across deploys. [Queued event listeners](events.md)
are function jobs.

### 4. Handle failed jobs

A job that fails is retried after its backoff until it has used its
tries. Then it fails for good: its `Failed` method runs, and it is kept as
a failed job, with its last error. A job whose last try didn't record its
outcome (its worker died, or it ran past its lease) fails the same way,
without running again, so a job that crashes its worker can't crash
workers forever. Its work may have been done, so make `Failed`
idempotent too. Commands manage failed jobs:

| Command | |
|---|---|
| `queue:failed [--limit=N]` | List failed jobs, the latest first |
| `queue:retry <id>…` or `queue:retry all` | Put failed jobs back on their queues, with all their tries (`all`: those that had failed when it started) |
| `queue:forget <id>…` | Delete failed jobs |
| `queue:flush` | Delete every failed job |
| `queue:clear [queue]` | Delete every job waiting on a queue (default `QUEUE_DEFAULT`) |

```console
$ ./app queue:failed
ID                                    FAILED AT            QUEUE     JOB               ATTEMPTS  ERROR
01a0f614-9a28-7025-93a7-b8b88ecb37d0  2026-10-01 06:09:00  payments  main.ChargeOrder  1         card declined
$ ./app queue:retry 01a0f614-9a28-7025-93a7-b8b88ecb37d0
```

In production, `queue:flush` and `queue:clear` need `--force`.

### 5. Test

With `QUEUE_DRIVER=sync` (the default, so what tests get unless
`.env.testing` or the test says otherwise), jobs run when they are
dispatched, so a test sees their effects when the request returns:

```go
// With QUEUE_DRIVER=sync, jobs run when they are dispatched: a test sees
// their effects as soon as the request returns.
func TestChargeOrder(t *testing.T) {
	g := &FakeGateway{}
	fakeGateway(t, g)
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))

	paid := placeOrder(t, app, "Book", 1500)
	app.Get("/orders/"+strconv.FormatInt(paid, 10)).AssertOK().AssertJSONPath("status", "paid")

	declined := placeOrder(t, app, "Piano", 500_000)
	app.Get("/orders/"+strconv.FormatInt(declined, 10)).AssertOK().AssertJSONPath("status", "failed")

	if n := g.Charges(); n != 1 {
		t.Errorf("%d charges, want 1", n)
	}
}
```

(Copied from [`examples/queue/main_test.go`](../../../examples/queue/main_test.go), region `test-sync`.)

The sync driver runs each job once, without its delay, and `Dispatch`
returns its error. To test retries and backoff, run the workers:

```go
// With QUEUE_DRIVER=database, workers run the jobs: the test runs them
// with app.Run, and waits for the outcome.
func TestWorkersRetry(t *testing.T) {
	g := &FakeGateway{Flaky: true} // each charge fails once
	fakeGateway(t, g)
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{
		"QUEUE_DRIVER": "database", "QUEUE_POLL": "10ms", "QUEUE_BACKOFF": "10ms",
	}))
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, "workers") }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	paid := placeOrder(t, app, "Book", 1500)
	declined := placeOrder(t, app, "Piano", 500_000)
	waitFor(t, func() bool { return orderStatus(t, app, paid) == "paid" && orderStatus(t, app, declined) == "failed" })

	q := anetos.MustResolve[*queue.Queue](app.App)
	failed, err := q.Store().Failed(app.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].Error != "card declined" || failed[0].Attempts != 1 {
		t.Errorf("failed jobs = %+v", failed)
	}
	if n := g.Charges(); n != 1 {
		t.Errorf("%d charges, want 1", n)
	}
}
```

(Copied from [`examples/queue/main_test.go`](../../../examples/queue/main_test.go), region `test-workers`.)

Workers use their own database connections, so they don't see rows
written in the test's transaction. This test uses in-memory SQLite, which
has none; with a SQLite file, PostgreSQL or MySQL, pass
`anetostest.WithoutTransaction()` to such a test.

`anetostest` gives each test app its own `QUEUE_PREFIX`, and removes its
Redis jobs when the test ends. `queue.AfterCommit()` jobs are dispatched
when a `db.Tx` inside the test's transaction commits, as they would be
without it.

To check that a job was dispatched without running it, fake the queue
and assert on the recorded jobs:

```go
// illustrative
app := anetostest.New(t, setup, anetostest.FakeQueue())
id := placeOrder(t, app, "Book", 1500)
anetostest.AssertDispatched(app, func(j ChargeOrder) bool { return j.OrderID == id })
```

[Test your app](testing.md#6-check-jobs-events-emails-and-messages) has
the complete test and every assertion.

## How it works

**Leases.** A worker that takes a job **reserves** it: the job is hidden
from other workers for its lease, the longest timeout of the registered
job types plus 30 seconds. If the worker dies, the job becomes available
again when the lease ends, and counts that attempt. Each reservation has
its own token, and a worker records a job's outcome only with its token,
so a worker whose lease ran out can't delete the job from under the next.

**Order.** Workers take jobs from their queues in the order given, and
from each queue in the order the jobs became available. With concurrency
above 1, or several workers, jobs run in parallel and can finish in any
order.

**Shutdown.** Workers stop after the HTTP server, so they can still take
the jobs its last requests dispatched. They stop taking jobs,
then give running jobs the shutdown grace period (never more than the
shutdown budget leaves, less 2 seconds to put jobs back: keep
`APP_SHUTDOWN_TIMEOUT` well above that). Then they cancel the jobs' contexts: a job that
returns because of it goes back on its queue, without the attempt
counting, unless it returns a `queue.Permanent` error.

**Unknown jobs.** A worker that takes a job whose type its app doesn't
register counts a failed attempt. When you add a job type, deploy the
workers before the code that dispatches it.

**The drivers.** The database driver reserves jobs with
`SELECT … FOR UPDATE SKIP LOCKED`, so workers don't wait for each other.
The Redis driver keeps each queue in a sorted set and runs every change as
a Lua script; in a Redis Cluster, put a hash tag in the prefix
(`QUEUE_PREFIX={blog}:queue:`) so its keys share a slot. Both use the
server's clock for delays and leases.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `job type … isn't registered` | `queue.Register` wasn't called for it | Register every job type in `setup` |
| Jobs are never run | No workers: they run with `run` or `run --only=workers`, not with other commands | Run the app (`go run .`) or a worker process |
| `unknown job "…"` in failed jobs | The worker's app doesn't register that type (an older deploy, or a renamed type) | Deploy workers first; keep old names with `queue.Name` |
| A job runs twice | At-least-once delivery: a worker stopped, or the job ran past its timeout | Make the job idempotent |
| `no such table: jobs` | The queue's migration hasn't run | Add `queue.Migrations("", "")` to `migrate.ForApp`, then `migrate` |
| A job finds no row it should | It ran before the transaction that wrote the row committed | Use `queue.AfterCommit()` (or the database driver) |

## Next steps

- [Run background tasks](background-tasks.md) for work that isn't a job:
  pollers and heartbeats.
- [Scheduling](scheduling.md): dispatch a job every hour or night with
  `schedule.Dispatch`.
- [Send email](mail.md): `mailer.Queue` sends email from a queue job.
- [Runtime supervisor](../concepts/runtime-supervisor.md): roles, stages
  and shutdown.

> **Coming from Laravel?** A job is a `ShouldQueue` class, `Handle` is
> `handle()`, and `queue.Dispatch(ctx, job, queue.OnQueue("x"),
> queue.Delay(d))` is `Job::dispatch()->onQueue('x')->delay($d)`.
> `queue.Tries`, `queue.Backoff` and `queue.Timeout` are the `$tries`,
> `$backoff` and `$timeout` properties, `queue.Permanent(err)` is
> `$this->fail()`, and `Failed` is `failed()`. Instead of
> `php artisan queue:work`, workers run inside the app, supervised, and
> `run --only=workers` runs only them. `queue:failed`, `queue:retry`,
> `queue:forget`, `queue:flush` and `queue:clear` work as in Laravel.
> Dependencies come from the context, not from `handle()`'s parameters.

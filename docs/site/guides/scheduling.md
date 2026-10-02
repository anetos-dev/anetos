---
title: Scheduling
since: v0.2.0
---

# Scheduling

Run tasks on a schedule: prune old rows every night, send a report every
hour, sync with another service every five minutes. The scheduler runs
in your app's binary, as a supervised component, so there is no crontab
to install and nothing to run every minute. The complete app is
[`examples/queue`](../../../examples/queue).

## Before you start

You have an app created with `anetos.New()`. Tasks that use locks
(`WithoutOverlapping`, `OnOneServer`) need the [cache](cache.md) set up,
and tasks that dispatch jobs need the [queue](queues.md).

## Steps

### 1. Write a task

A task is a function of a context that returns an error:

```go
// pruneAuditLog deletes the audit log's entries older than 90 days. It
// runs every night (see setup) on one instance, and is safe to run again:
// it deletes what is old when it runs.
func pruneAuditLog(ctx context.Context) error {
	n, err := db.Query[AuditEntry](ctx).Where(colCreatedAt.Lt(anetos.Now(ctx).AddDate(0, 0, -90))).Delete()
	if err != nil {
		return err
	}
	anetos.Logger(ctx).InfoContext(ctx, "audit log pruned", "deleted", n)
	return nil
}
```

(Copied from [`examples/queue/schedule.go`](../../../examples/queue/schedule.go), region `task`.)

The rules for the function:

- **Watch `ctx`.** It has the app's values (the database, the cache, …)
  and is canceled at shutdown, after a grace period, or when the task's
  `schedule.Timeout` runs out.
- **Return an error to report a failure.** It is logged, and the task
  runs again at its next time: the scheduler doesn't retry. For work that
  must succeed, dispatch a queue job (step 2).
- Panics are recovered, logged with their stack, and count as failures.
- Make it safe to run twice, or late: a deploy, a clock change or a
  `schedule:run` can do either.

### 2. Add it to the scheduler

In `setup`, after `cache.ForApp` (and `queue.ForApp`, for jobs):

```go
// The scheduler's locks (WithoutOverlapping, OnOneServer) are in the
// cache: with several instances, use a store they share.
if _, err := cache.ForApp(app, redis.CacheDriver()); err != nil { // CACHE_STORE: memory, database or redis
	return nil, err
}
s, err := schedule.ForApp(app) // SCHEDULE_TIMEZONE, default UTC
if err != nil {
	return nil, err
}
if err := s.Add(schedule.DailyAt("03:00"), "prune-audit-log", pruneAuditLog,
	schedule.WithoutOverlapping(), schedule.OnOneServer(), schedule.Timeout(10*time.Minute)); err != nil {
	return nil, err
}
// Hourly, the scheduler dispatches a job; a worker runs it.
if err := queue.Register[SalesReport](q, queue.Tries(3)); err != nil {
	return nil, err
}
if err := s.Add(schedule.Hourly(), "sales-report", schedule.Dispatch(SalesReport{}), schedule.OnOneServer()); err != nil {
	return nil, err
}
```

(Copied from [`examples/queue`](../../../examples/queue/main.go), region `schedule-setup`.)

`schedule.Dispatch(job)` is a task that dispatches a queue job, so a
worker runs it with the queue's retries, timeouts and failed jobs:

```go
// SalesReport is a queue job that reports the orders paid in the hour
// before it runs. The scheduler dispatches it every hour; a worker runs
// it, with the queue's retries.
type SalesReport struct{}

// Handle counts the orders and sends the report.
func (SalesReport) Handle(ctx context.Context) error {
	n, err := db.Query[Order](ctx).Where(colStatus.Eq("paid"), colCreatedAt.Gte(anetos.Now(ctx).Add(-time.Hour))).Count()
	if err != nil {
		return err
	}
	salesReports.mu.Lock()
	defer salesReports.mu.Unlock()
	salesReports.sent = append(salesReports.sent, fmt.Sprintf("Sales report: orders paid in the last hour: %d", n))
	return nil
}
```

(Copied from [`examples/queue/schedule.go`](../../../examples/queue/schedule.go), region `report-job`.)

Task names are lower-case letters, digits and `. _ : -`; they name the
task in logs, locks and commands, so keep them the same across deploys.

**Schedules:**

| Schedule | Runs |
|---|---|
| `schedule.EveryMinute()` | Every minute |
| `schedule.Every(d)` | Every `d`, counted from midnight: whole minutes dividing an hour (`5*time.Minute` at :00, :05, …) or whole hours dividing a day (`6*time.Hour` at 00:00, 06:00, …) |
| `schedule.Hourly()`, `schedule.HourlyAt(m)` | Every hour, at minute 0 or `m` |
| `schedule.Daily()`, `schedule.DailyAt("15:04")` | Every day, at midnight or the time |
| `schedule.WeeklyOn(time.Monday, "08:00")` | Every week, on the day at the time |
| `schedule.MonthlyOn(1, "08:00")` | Every month, on the day (1 to 28) at the time |
| `schedule.Cron("*/15 9-17 * * MON-FRI")` | A cron expression: minute, hour, day of month, month, day of week; numbers, `*`, ranges, lists, steps and names; or `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly` |

As in cron, when a `Cron` expression restricts both the day of month
and the day of week, a day matching either runs: `0 0 1 * MON` is the 1st
of the month and every Monday. A field starting with `*` (`*/2` too)
isn't restricted, so `0 0 */2 * MON` is odd days that are Mondays.

**Options:**

| Option | Does |
|---|---|
| `schedule.WithoutOverlapping()` | Skips a run while the previous one is still going, on any instance sharing the cache |
| `schedule.OnOneServer()` | Runs each run on one instance only, when several run the scheduler |
| `schedule.Timeout(d)` | Cancels a run's context after `d` |

**Time zones.** Schedules are in `SCHEDULE_TIMEZONE` (default `UTC`), or
in the zone `.In("Asia/Dhaka")` gives one schedule. In a zone with
daylight saving time, a time that the clock change skips doesn't run that
day, and a time it repeats runs twice: schedule tasks that matter away
from the zone's clock changes (usually between midnight and 03:00; some
zones change at midnight), or in UTC.

### 3. Run the scheduler

The scheduler runs with the app's other components:

```bash
go run .                         # the web server, the workers and the scheduler
go run . run --only=scheduler    # only the scheduler
```

With several instances (several web servers, say), each one runs the
scheduler unless you split it out with `--only`. Either run it in one
process, or give the tasks `OnOneServer` and a cache store the instances
share (`CACHE_STORE=database` or `redis`): then the first instance to
take a run's lock runs it, and the others skip it. With the memory store,
the locks hold only within a process, and the scheduler warns at start.

Runs that should have happened while no scheduler was running (during a
deploy, say) are skipped, as with cron. A scheduler that wakes up late
runs each task it missed once, not once per missed time.

At shutdown, the scheduler stops starting runs before the listeners and
workers stop, and gives the runs still going half of
`APP_SHUTDOWN_TIMEOUT`; then their contexts are canceled. That time comes
out of the listeners' and workers' share, so put long work in a queue
job with `schedule.Dispatch`.

### 4. Check and run tasks

`schedule:list` shows the tasks and when they run next:

```text
$ go run . schedule:list
TASK             SCHEDULE   NEXT RUN                       OPTIONS
prune-audit-log  0 3 * * *  2026-10-02 03:00 +06 (in 11h)  without overlapping, on one server, timeout 10m
sales-report     0 * * * *  2026-10-01 17:00 +06 (in 1h)   on one server
```

`schedule:run <task>` runs a task now, whatever its schedule, and fails
with its error. `WithoutOverlapping` applies (it fails if the task is
running), but only sees runs in other processes with a shared cache
store; `OnOneServer` doesn't apply.

### 5. Test

Test a task by running it with `RunTask`, as `schedule:run` does:

```go
// RunTask runs a task now, as `go run . schedule:run <task>` does.
func TestScheduledTasks(t *testing.T) {
	fakeGateway(t, &FakeGateway{})
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	old := &AuditEntry{OrderID: 1, Message: "placed: Kettle"}
	if err := db.Create(app.Context(), old); err != nil {
		t.Fatal(err)
	}
	_, err := db.Query[AuditEntry](app.Context()).Where(colID.Eq(old.ID)).Update(colCreatedAt.Set(time.Now().AddDate(0, 0, -100)))
	if err != nil {
		t.Fatal(err)
	}
	recent := placeOrder(t, app, "Mug", 900)

	s := anetos.MustResolve[*schedule.Scheduler](app.App)
	if err := s.RunTask(app.Context(), "prune-audit-log"); err != nil {
		t.Fatal(err)
	}
	anetostest.AssertDatabaseMissing[AuditEntry](app, colID.Eq(old.ID))
	anetostest.AssertDatabaseHas[AuditEntry](app, db.Col[int64]("order_id").Eq(recent))

	if err := s.RunTask(app.Context(), "sales-report"); err != nil { // the sync queue runs the job at once
		t.Fatal(err)
	}
	if r := salesReports.Sent(); len(r) == 0 || r[len(r)-1] != "Sales report: orders paid in the last hour: 1" {
		t.Errorf("reports: %q", r)
	}
}
```

(Copied from [`examples/queue/main_test.go`](../../../examples/queue/main_test.go), region `test-schedule`.)

To check a schedule, `Next` returns when it runs after a time:

```go
// illustrative
next := schedule.WeeklyOn(time.Monday, "08:00").In("Asia/Dhaka").Next(time.Now())
```

## How it works

`schedule.ForApp` provides the `*schedule.Scheduler` to the app and, once
it has tasks, adds it as a component with the role `scheduler` and the
stage `StageScheduler`, which stops right after the HTTP server. Its loop
computes each task's next run, sleeps until the earliest, and starts the
runs that are due, each in its own goroutine, so a slow task doesn't
delay the others.

`OnOneServer` takes a cache lock named after the task and the run's
minute (`schedule:<task>:<UTC time>`), kept for an hour: instances whose
clocks differ by less than that agree on who runs it. `WithoutOverlapping`
takes the lock `schedule:<task>:running` while a run goes, released when
it ends: it lasts three minutes and the run extends it every minute, so
if the process dies, the next run after that can start. Both are cache
locks, so they need the store to be shared to work across instances.

Cron expressions are parsed when the task is added, and an expression
that never matches (`0 0 30 2 *`) is an error then.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `uses a lock … call cache.ForApp` | A task has `WithoutOverlapping` or `OnOneServer` but the app has no cache | Call `cache.ForApp` in `setup` |
| A task runs on every instance | `OnOneServer` is missing, or the cache store is `memory` | Add `OnOneServer` and use `CACHE_STORE=database` or `redis`, or run the scheduler in one process (`--only=scheduler`) |
| A task never runs | Its process doesn't run the `scheduler` role (`--only=http`), or it's running in another time zone than you think | Check `schedule:list`, and `SCHEDULE_TIMEZONE` |
| `skipped: the previous run is still going` | A `WithoutOverlapping` run takes longer than the time between runs | Run it less often, or make it faster |
| A failed run isn't retried | The scheduler doesn't retry | Dispatch a queue job with `schedule.Dispatch` |
| A task ran twice, or not at all, one night | A daylight saving time change | Schedule it away from the zone's clock changes, or in UTC |

## Next steps

- [Queues](queues.md): workers and retries, for the jobs tasks dispatch.
- [Cache](cache.md): stores and locks.
- [Runtime supervisor](../concepts/runtime-supervisor.md): roles and
  staged shutdown.

> **Coming from Laravel?** `s.Add(schedule.DailyAt("02:00"), "prune",
> prune)` is `Schedule::call(...)->dailyAt('02:00')` in
> `routes/console.php`, and `WithoutOverlapping`, `OnOneServer` and
> `schedule.Dispatch` are `withoutOverlapping()`, `onOneServer()` and
> `Schedule::job(...)`. There is no `schedule:work` or crontab entry
> running `schedule:run` every minute: the scheduler runs in your binary,
> and `schedule:run <task>` runs one task now (`schedule:test`). Every
> task needs a name.

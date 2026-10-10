---
title: Runtime supervisor
since: v0.1.0
group: "The app"
weight: 101
---

# Runtime supervisor

Every Anetos application runs its long-lived work (HTTP servers, queue
workers, pub/sub listeners, the scheduler, background tasks) as goroutines
inside one process, managed by a **supervisor**. It decides what runs, what
happens when something fails, and how everything stops.

## Components

Anything long-running is a **background component** ("component" for
short on this page; the pieces of a page in `views/ui` are UI
components, another thing):

```go
// illustrative (from supervisor/component.go)
type Component interface {
	Name() string
	Run(ctx context.Context) error
}
```

`Run` blocks until the work is done or `ctx` is canceled. On cancellation,
the component stops taking new work, finishes or hands back what's in
flight, and returns. `app.Go(name, fn)` wraps a function as a component;
`app.Component(c)` adds your own type.

## Process types: one binary, many shapes

Components declare **process types** (Heroku's word, from the
`Procfile`: `web`, `worker`). A process can run all of them or only
some:

```bash
./blog run                            # everything: dev and small deployments
./blog run --only=web                 # web machines
./blog run --only=worker              # background machines
./blog run --only=scheduler           # one machine, or several with OnOneServer
```

The built-in components with process types are the web server (`web`),
the queue's workers (`worker`, from `q.Work`; see
[Queues](../guides/queues.md)), pub/sub listeners (`listener`, from
`pubsub.Listen`; see [Pub/sub listeners](../guides/pubsub.md)) and the
scheduler (`scheduler`,
from `schedule.New` once it has tasks; see
[Scheduling](../guides/scheduling.md)). Your own components choose
theirs with `anetos.ProcessTypes("worker")`.

`run` is the binary's default command (`app.Execute`; see
[Commands](../guides/commands.md)); in code, pass process types to
`app.Run(ctx, "web")`. Before v0.5 they were "roles" named `http`,
`workers` and `listeners`; those names still work, with a warning,
until v0.6.

- Components **without** process types run in every process.
- Asking for a process type nothing declares is an error, so typos in `--only`
  fail loudly. `schedule.New` and `pubsub.New` declare theirs
  (`Supervisor.Declare`) before their components exist, so
  `run --only=worker,scheduler` works in an app that has no task yet
  and keeps working once it has one.

Split processes share work through shared stores: the database or Redis
for the queue, the cache (database or Redis) for the scheduler's
`OnOneServer` locks, and a broker (Redis, Google Pub/Sub) for pub/sub;
the `memory` drivers stay inside one process.
[`examples/saas`](../../../examples/saas) runs one binary as four
processes, `web`, `worker`, `listener` and `scheduler`, and its
`process_types_test.go` follows a sign-up from one to the other.

## Failure policies

| Policy | On error or panic | Use for |
|---|---|---|
| `RestartNever` (default) | Logged; component stays stopped; app keeps running | One-off background tasks |
| `RestartOnFailure` | Restarted after exponential backoff with jitter | Workers, listeners, pollers |
| `StopOnFailure` | Whole app shuts down; `Run` returns the error | Components the app can't live without, e.g. the HTTP server |

Panics are recovered and treated as failures, with the stack trace logged.

**Backoff** starts at `Initial` (default 1s), doubles each consecutive
failure up to `Max` (default 30s), and varies by ±20% so many instances
don't restart in lockstep. A component that ran longer than `Max` before
failing counts as healthy again, and its delay resets. If
`MaxRestarts` (default unlimited) is exceeded, the failure escalates like
`StopOnFailure`, so a crash-looping process exits and your orchestrator
(systemd, Kubernetes…) can take over.

## Staged shutdown

When the run context is canceled (usually by SIGINT or SIGTERM) or a
component escalates a failure, the supervisor stops components in **stages**.
Each stage is canceled only after the previous one has fully drained:

| Order | Stage | Why this order |
|---|---|---|
| 1 | `StageIngress` (HTTP) | Stop accepting requests; finish in-flight ones |
| 2 | `StageScheduler` | Stop starting scheduled runs, which produce jobs |
| 3 | `StageListeners` | Stop pulling external messages, which also produce jobs |
| 4 | `StageWorkers` | Finish in-flight jobs, including ones just produced above |
| 5 | `StageBackground` | Ad-hoc tasks (`app.Go`) |

The rule is **producers stop before consumers**, so work already accepted
gets done. All stages share one deadline (in an app, `APP_SHUTDOWN_TIMEOUT`
minus the part reserved for shutdown hooks). If it passes, the remaining
stages are canceled together with a short grace period, and `Run` returns an
error naming only the components that are still running, so the process
can exit anyway.

If every component finishes on its own, `Run` returns too. From that moment
new components are refused with `ErrStopping` rather than being started and
immediately canceled.

> **Warning:** A component that ignores `ctx` can't be stopped gracefully. It
> will be reported as stuck when the deadline passes.

## Health

- `app.Supervisor().Ready()` is true while running and not shutting down,
  and every component that implements `Ready() bool` is ready. Such a
  component that is waiting to restart or has failed counts as not ready.
  It backs the server's `GET /health/ready` and `health:check`;
  `migrate.New` adds one (`migrations`) that isn't ready while
  migrations are pending.
- `app.Supervisor().ShutdownDeadline()` is when the components' share of
  `APP_SHUTDOWN_TIMEOUT` runs out, once shutdown has begun: queue workers
  use it to stop their jobs in time.
- `app.Supervisor().Status()` lists each component's state (`pending`,
  `starting`, `running`, `backoff`, `done`, `failed`, `stopped`), restart
  count and last error.

> **Coming from Laravel?** This replaces running `php artisan queue:work`,
> `schedule:work` and Supervisor (the process manager) as separate programs.
> Everything runs in your app's own binary, and `--only` splits it apart
> when you need to scale. `./app queue:work` and `./app schedule:work`
> exist too: they are `run --only=worker` and `run --only=scheduler`.

## Related

- [Run background tasks](../guides/background-tasks.md)
- [Deploy](../guides/deployment.md): process types in processes and containers
- [Application lifecycle](application-lifecycle.md)
- Package docs: `supervisor`

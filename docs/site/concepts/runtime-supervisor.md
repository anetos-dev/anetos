---
title: Runtime supervisor
since: v0.1.0
---

# Runtime supervisor

Every Anetos application runs its long-lived work (HTTP servers, queue
workers, pub/sub listeners, the scheduler, background tasks) as goroutines
inside one process, managed by a **supervisor**. It decides what runs, what
happens when something fails, and how everything stops.

## Components

Anything long-running is a **component**:

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

## Roles: one binary, many shapes

Components declare **roles**. A process can run all of them or only some:

```bash
./blog run                            # everything: dev and small deployments
./blog run --only=http                # web machines
./blog run --only=workers            # background machines
```

In v0.1 the web server is the only built-in component with a role
(`http`); queue workers and listeners bring theirs in v0.2. Your own
components choose theirs with `anetos.Roles("workers")`.

`run` is the binary's default command (`app.Execute`; see
[Commands](../guides/commands.md)); in code, pass roles to
`app.Run(ctx, "http")`.

- Components **without** roles run in every process.
- Asking for a role no component declares is an error, so typos in `--only`
  fail loudly.

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
  It will back the planned readiness endpoint.
- `app.Supervisor().Status()` lists each component's state (`pending`,
  `starting`, `running`, `backoff`, `done`, `failed`, `stopped`), restart
  count and last error.

> **Coming from Laravel?** This replaces running `php artisan queue:work`,
> `schedule:run` and Supervisor (the process manager) as separate programs.
> Everything runs in your app's own binary, and `--only` splits it apart
> when you need to scale.

## Related

- [Run background tasks](../guides/background-tasks.md)
- [Application lifecycle](application-lifecycle.md)
- Package docs: `supervisor`

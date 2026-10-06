---
title: Run background tasks
since: v0.1.0
group: "Background work"
weight: 403
---

# Run background tasks

Run long-lived work, such as a poller, a cache warmer or a heartbeat, as a
supervised goroutine that restarts on failure and stops cleanly on deploy.

## Before you start

You have an app created with `anetos.New()`. See
[Configure your application](configuration.md).

## Steps

### 1. Start the task with `app.Go`

```go
	err = app.Go("heartbeat", func(ctx context.Context) error {
		counter := anetos.MustResolve[*Counter](app)
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				n := counter.n.Add(1)
				app.Logger().Info("beat", "n", n)
				if cfg.FailAt > 0 && n%int64(cfg.FailAt) == 0 {
					return errors.New("simulated failure")
				}
			}
		}
	},
		anetos.Roles("workers"),
		anetos.Restart(supervisor.RestartOnFailure),
		anetos.Backoff(supervisor.Backoff{Initial: time.Second, Max: 10 * time.Second}),
	)
	if err != nil {
		return err
	}
```

(Copied from [`examples/lifecycle`](../../../examples/lifecycle/main.go),
region `task`.)

The rules for the function:

- **Watch `ctx`.** Return soon after `ctx.Done()` closes. That's how
  shutdown reaches your task.
- **Return an error to report a failure.** The restart policy decides what
  happens next. Returning `nil` means "finished", and the task is not
  restarted.
- Panics are recovered and treated as failures.

### 2. Run the app with signal handling

```go
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
```

(Region `run`.)

### 3. Try it

```bash
HEARTBEAT_INTERVAL=200ms HEARTBEAT_FAIL_AT=3 APP_ENV=development go run ./examples/lifecycle
```

Every third beat the task fails and restarts after a growing delay. Press
Ctrl+C: the task stops, then the shutdown hook reports the total.

## Options

| Option | Default | Purpose |
|---|---|---|
| `anetos.Roles("workers")` | none (runs in every process) | Select with `app.Run(ctx, "workers")` / `--only` |
| `anetos.Restart(policy)` | `RestartNever` | `RestartOnFailure`, `StopOnFailure` |
| `anetos.Backoff(b)` | 1s initial, 30s max, unlimited | Restart delays and limit |
| `anetos.Stage(s)` | `StageBackground` | Shutdown order |

## Starting tasks while the app runs

`app.Go` also works after `Run` has started, for example from a request
handler. Task names must be unique, so include an ID:

```go
// illustrative
err := app.Go("export-"+job.ID, func(ctx context.Context) error {
	return exportReport(ctx, job)
})
```

> **Warning:** Tasks started this way are **not durable**. If the process
> crashes or is killed, they are lost. For work that must survive a restart,
> use a [queue job](queues.md).

## Testing it

Run the app with a cancelable context and check its status:

```go
// illustrative
ctx, cancel := context.WithCancel(context.Background())
go app.Run(ctx)
// … wait for the task to do something …
cancel()
for _, st := range app.Supervisor().Status() {
	t.Log(st.Name, st.State, st.Restarts, st.LastError)
}
```

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `shutdown timed out; still running: my-task` | The task ignores `ctx` | Select on `ctx.Done()` in loops; pass `ctx` to blocking calls |
| `duplicate component name` | Two tasks share a name | Make names unique, e.g. append an ID |
| `supervisor: shutting down` | `app.Go` called during shutdown, or after every other component finished | Expected; don't start new work while stopping |
| Task never restarts | Default policy is `RestartNever` | Add `anetos.Restart(supervisor.RestartOnFailure)` |

## Next steps

- [Scheduling](scheduling.md) for work that runs at set times (every
  night, every hour) rather than all the time.
- [Runtime supervisor](../concepts/runtime-supervisor.md): roles, policies,
  staged shutdown
- [Application lifecycle](../concepts/application-lifecycle.md)

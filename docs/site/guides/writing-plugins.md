---
title: Write a plugin
since: v0.2.0
group: "Extending"
weight: 901
---

# Write a plugin

Package a feature so any Anetos app can add it with `anetos add`: its
routes, tables, settings, commands, queue jobs, scheduled tasks and
event listeners. A plugin uses only the framework's public packages, as
an app does. The complete plugin is
[`plugins/postmark`](../../../plugins/postmark), which receives
Postmark's webhooks and keeps a list of the addresses Postmark stopped
sending to.

## Before you start

You know how to [use plugins](plugins.md). A plugin is a Go module with
a package that exports a constructor:

```go
// illustrative
func Plugin() ext.Plugin
```

`anetos add` lists `yourpkg.Plugin()` in the app's `plugins.go`, so the
function must have that name and signature, in the module's root
package (or the package path users add).

## Steps

### 1. Name it and declare what it supports

`ext.Plugin` has one method, `Name`. Everything else is optional
interfaces for what the plugin adds. The name namespaces all of it:
lower-case letters, digits and `-`, starting with a letter, up to 40
characters.

```go
// Settings are the plugin's settings.
type Settings struct {
	// WebhookUser is the user name of the basic auth Postmark sends with
	// webhooks (set in the webhook's URL). Without it and the password,
	// the webhook endpoint refuses every request.
	WebhookUser string `env:"POSTMARK_WEBHOOK_USER"`
	// WebhookPassword is the webhook's password.
	WebhookPassword anetos.Secret `env:"POSTMARK_WEBHOOK_PASSWORD"`
}

type plugin struct{ cfg Settings }

func (p *plugin) Name() string     { return "postmark" }
func (p *plugin) Requires() string { return ">= v0.2.0, < v0.6.0" }
func (p *plugin) Config() any      { return &p.cfg }
```

(Copied from [`plugins/postmark/plugin.go`](../../../plugins/postmark/plugin.go), region `plugin`.)

- `Requires` (`ext.Compat`) lists the Anetos versions the plugin works
  with: comparisons (`>=`, `>`, `<=`, `<`, `=`) of `vX.Y.Z` versions,
  joined by commas. Before v1, minor versions may break APIs, so
  allow one minor series. A pre-release (`v0.2.0-rc.1`) counts as its
  release, and an app built from an untagged commit or a local
  checkout of Anetos has the version that source is heading for
  (`v0.5.0-dev`).
- `Config` (`ext.HasConfig`) returns a pointer to the settings struct,
  with `env` tags as for [`config.Get`](configuration.md): defaults,
  `required`, typed fields, `anetos.Secret` for secrets. Every key must
  start with the name in capitals (`POSTMARK_`). `ext.Load` fills it
  before calling the other methods, so they can use it.

### 2. Add tables

`ext.HasMigrations` returns a migration set named after the plugin. Its
migrations run with the app's (`migrate`), and are tracked under that
name:

```go
// Migrations implements ext.HasMigrations.
func (p *plugin) Migrations() *migrate.Set {
	s := migrate.NewSet("postmark")
	s.AddFunc("2026_10_02_000000_create_postmark_suppressions",
		func(s *migrate.Schema) error {
			return s.Create("postmark_suppressions", func(t *migrate.Table) {
				t.ID()
				t.String("email", 254)
				t.String("record_type", 40)
				t.String("reason", 255)
				t.String("message_id", 64)
				t.Timestamps()
				t.Unique("email")
			})
		},
		func(s *migrate.Schema) error { return s.Drop("postmark_suppressions") })
	return s
}
```

(Region `migrations`.)

Prefix your tables with the plugin's name, so they don't collide with
the app's. Never change a released migration: add a new one.

### 3. Add routes

`ext.HasRoutes` gets a router under the plugin's prefix, `/<name>` by
default (the app can move it with `ext.Mount`), whose route names start
with `<name>.`. Use relative paths and route names, never the full path:

```go
// Routes implements ext.HasRoutes: POST /webhook under the plugin's
// prefix.
func (p *plugin) Routes(r *web.Router) error {
	r.Post("/webhook", p.webhook).Name("webhook")
	return nil
}

// webhook checks the credentials and queues the event: Postmark retries
// a webhook that doesn't get a 2xx quickly.
func (p *plugin) webhook(c *web.Ctx) error {
	if !p.authorized(c.Request()) {
		c.Writer().Header().Set("WWW-Authenticate", `Basic realm="postmark"`)
		return web.Error(http.StatusUnauthorized, "unauthorized")
	}
	var e event
	body := http.MaxBytesReader(c.Writer(), c.Request().Body, 1<<20) // events are a few KB
	if err := json.NewDecoder(body).Decode(&e); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return web.Error(http.StatusRequestEntityTooLarge, "too large")
		}
		return web.Error(http.StatusBadRequest, "invalid JSON")
	}
	switch e.RecordType {
	case "Bounce", "SpamComplaint", "SubscriptionChange":
		if a := e.address(); a != "" && len(a) <= 254 {
			if err := queue.DispatchFunc(c, "postmark:webhook", e); err != nil {
				return err
			}
		}
	}
	return c.NoContent() // other events, and events without an address: acknowledged, ignored
}
```

(Region `routes`.)

The routes get the app's global middleware, and middleware the app adds
to its root router with `Use` (CSRF there would refuse a webhook: apps
put it on a group). The router is the app's, so `UseGlobal` would wrap
every request of the app: don't. For pages, render templ components from
the plugin's package: they are Go code, compiled with it.

### 4. Add jobs, tasks, listeners and commands

Each capability gets the app's service to add to:

| Interface | Method | Name what it adds |
|---|---|---|
| `ext.HasJobs` | `Jobs(q *queue.Queue) error`: `queue.Register`, `queue.RegisterFunc`; `q.Work` for workers of its own | `<name>:…` |
| `ext.HasSchedule` | `Schedule(s *schedule.Scheduler) error`: `s.Add` | `<name>:…` |
| `ext.HasListeners` | `Listeners(bus *events.Bus) error` (`Listen` before v0.5, still called with a warning until v0.6): `events.On`, `OnAsync`, `OnQueued` | |
| `ext.HasCommands` | `Commands() []cmd.Command` | `<name>:…` (checked) |
| `ext.HasBoot` | `Boot(ctx, app) error`: runs when the app boots, after the app's own providers | |

```go
// Jobs implements ext.HasJobs: the job postmark:webhook, run by the
// app's workers.
func (p *plugin) Jobs(q *queue.Queue) error {
	return queue.RegisterFunc(q, "postmark:webhook", record)
}
```

(Region `jobs`.)

Jobs run on the app's workers, and use the app's database, cache and
mailer through their context, like the app's own jobs.

A value your plugin keeps in the context can follow the work it hands
off: add a carrier, and queue jobs and async event listeners get it from
the code that dispatched or emitted them (carried values are stored as
text with the job: no secrets):

```go
// illustrative
app.AddCarrier(anetos.Carrier{
	Name:    "acme.tenant",
	Capture: func(ctx context.Context) string { return tenantOf(ctx) },
	Restore: func(ctx context.Context, v string) context.Context { return withTenant(ctx, v) },
})
```

The audit log carries its actor this way.

### 5. Test it

Test the plugin in a small app: a `setup` that sets up what the plugin
needs, then loads it, run by [`anetostest`](testing.md):

```go
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	if _, err := migrate.New(app, nil); err != nil {
		return nil, err
	}
	if _, err := queue.New(app); err != nil {
		return nil, err
	}
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	return srv, ext.Load(app, []ext.Plugin{postmark.Plugin()})
}
```

(Copied from [`plugins/postmark/plugin_test.go`](../../../plugins/postmark/plugin_test.go), region `test-setup`.)

`anetostest.New(t, setup, anetostest.Env(…))` runs the plugin's
migrations on an in-memory database; requests go to `/postmark/…`.
Then try it in a real app: `go mod edit -replace` your module to its
directory, and `anetos add` it with any version.

### 6. Publish it

Tag the module (`v0.1.0`) and push it: `anetos add
example.com/you/yourplugin` gets it through the Go module proxy. Say in
its README what it adds (`plugin:list` shows it too), its settings,
and the Anetos versions it supports.

## Rules

`ext.Load` refuses a plugin that breaks them, naming the rule:

- The name is valid, isn't one the framework uses (`app`, `auth`,
  `cache`, `db`, `events`, `ext`, `health`, `help`, `http`, `log`, `mail`,
  `mailer`, `migrate`, `plugins`, `pubsub`, `queue`, `redis`, `routes`,
  `run`, `schedule`, `anetos`, `serve`, `session`, `social`, `storage`,
  `web`), and no other plugin has it.
- `Requires` is satisfied by the app's Anetos version
  (`anetos.Version()`).
- `Config` returns a pointer to a struct, whose settings start with
  `<NAME>_` (`-` becomes `_`), in upper-case letters, digits and `_`,
  and aren't in another plugin's namespace (`stripe` can't have
  `STRIPE_CONNECT_KEY` next to a `stripe-connect` plugin).
- The migration set is named after the plugin.
- Commands are named `<name>:…`.
- The services the plugin adds to are set up before `ext.Load`
  (`call queue.New before ext.Load`).
- `ext.Mount` names a loaded plugin with routes, and a clean path like
  `/billing/stripe` (no trailing `/`, `.` or `..` segments, or wildcards).

A plugin whose settings are missing or invalid isn't wired in, and the
app doesn't boot, with an error naming them; commands that don't boot
the app (`plugin:env`, `help`) still work.

## Next steps

- [Use plugins](plugins.md): `anetos add`, `ext.Load`, `ext.Mount`.
- [Migrations](migrations.md), [Queues](queues.md),
  [Scheduling](scheduling.md), [Events](events.md),
  [Commands](commands.md): what plugins add.

> **Coming from Laravel?** A plugin is a package with a service
> provider: `Name` and the `Has…` interfaces replace `register()` and
> `boot()`, `Config` replaces `mergeConfigFrom`, `Migrations` replaces
> `loadMigrationsFrom`, and `Commands` replaces `commands()`. There is no
> auto-discovery in `composer.json`: `anetos add` writes the app's
> `plugins.go`, and the app calls `ext.Load`.

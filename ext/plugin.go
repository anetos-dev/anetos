// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/web"
)

// Plugin is a package that adds to an app. It implements the interfaces
// below for what it adds; [Load] wires each into the app. A plugin's
// module exports a constructor, Plugin(), which `anetos add` lists in
// the app's plugins.go.
type Plugin interface {
	// Name is the plugin's name: lower-case letters, digits and -,
	// starting with a letter, up to 40 ("stripe"). It namespaces what
	// the plugin adds: routes under /<name> named "<name>.…", commands
	// "<name>:…", the migration set <name>, settings <NAME>_….
	Name() string
}

// Compat is implemented by plugins that work with some versions of
// Anetos only.
type Compat interface {
	// Requires returns the versions of Anetos the plugin works with:
	// comparisons joined by commas, such as ">= v0.2.0, < v0.4.0".
	// [Load] refuses the plugin if anetos.Version doesn't satisfy them.
	Requires() string
}

// HasConfig is implemented by plugins with settings.
type HasConfig interface {
	// Config returns a pointer to the plugin's settings struct, whose
	// fields have env tags (config.Get's), all starting with the
	// plugin's name in capitals: STRIPE_SECRET_KEY. [Load] fills it
	// first, so the other methods can use it.
	Config() any
}

// HasMigrations is implemented by plugins with database tables.
type HasMigrations interface {
	// Migrations returns the plugin's migrations, in a set named after
	// the plugin (migrate.NewSet(name)). They run with the app's
	// (`migrate`), never by themselves. Load needs migrate.ForApp.
	Migrations() *migrate.Set
}

// HasRoutes is implemented by plugins that serve requests.
type HasRoutes interface {
	// Routes adds the plugin's routes to r, a router under the plugin's
	// prefix (/<name>, or the app's choice: [Mount]) whose route names
	// start with "<name>.". Load needs web.NewServer.
	Routes(r *web.Router) error
}

// HasCommands is implemented by plugins that add commands to the app's
// binary.
type HasCommands interface {
	// Commands returns the plugin's commands, each named "<name>:…".
	Commands() []cmd.Command
}

// HasJobs is implemented by plugins with queue jobs.
type HasJobs interface {
	// Jobs registers the plugin's job types on q (queue.Register,
	// queue.RegisterFunc), and may start workers of its own (q.Work).
	// Name job types "<name>:…" (queue.Name). Load needs queue.ForApp.
	Jobs(q *queue.Queue) error
}

// HasSchedule is implemented by plugins with scheduled tasks.
type HasSchedule interface {
	// Schedule adds the plugin's tasks to s, each named "<name>:…".
	// Load needs schedule.ForApp.
	Schedule(s *schedule.Scheduler) error
}

// HasListeners is implemented by plugins that listen to the app's
// events.
type HasListeners interface {
	// Listen adds the plugin's listeners to bus (events.On, OnAsync,
	// OnQueued). Load needs events.ForApp.
	Listen(bus *events.Bus) error
}

// HasBoot is implemented by plugins with work to do when the app boots:
// checking a connection, warming a cache.
type HasBoot interface {
	// Boot runs when the app boots, after the app's providers, with the
	// app's context values.
	Boot(ctx context.Context, app *anetos.App) error
}

// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/web"
)

var (
	pluginName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	settingKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// reserved are names the framework uses for its migration sets, command
// prefixes and settings prefixes.
var reserved = []string{
	"app", "auth", "cache", "db", "events", "ext", "health", "help", "http", "lang",
	"locale", "log", "mail", "mailer", "migrate", "plugin", "plugins", "pubsub",
	"queue", "redis", "route", "routes", "run", "schedule", "anetos", "serve",
	"session", "social", "storage", "web",
}

// Option configures [Load].
type Option func(*options)

type options struct {
	mounts map[string]string
}

// Mount serves the routes of the plugin name under prefix instead of
// /<name>: ext.Mount("stripe", "/billing/stripe").
func Mount(name, prefix string) Option {
	return func(o *options) { o.mounts[name] = prefix }
}

// Info describes a loaded plugin, for the plugin:list command.
type Info struct {
	// Name is the plugin's name.
	Name string
	// Requires is its version constraint, if it has one.
	Requires string
	// Prefix is where its routes are mounted, if it has routes.
	Prefix string
	// Adds lists what it adds: "config", "migrations", "routes", …
	Adds []string
	// Keys are its settings.
	Keys []config.Key
}

// registry is the app's loaded plugins.
type registry struct{ plugins []Info }

// Load adds plugins to the app: for each one, in order, it checks its
// name (valid, not the framework's, not taken), its version requirement
// and its settings' names (in its namespace, not in another plugin's),
// fills its settings from the app's configuration, then adds its migrations, commands, jobs, routes,
// scheduled tasks and listeners, and its Boot as a provider. Call it at
// the end of setup, after the services the plugins use (web.NewServer,
// migrate.New, queue.New, schedule.New, events.New), before
// the app boots:
//
//	if err := ext.Load(app, plugins()); err != nil { // plugins.go, from anetos add
//		return nil, err
//	}
//
// A plugin whose settings are missing or invalid isn't wired in, and
// the app fails to boot with the error, so that the plugin:env and
// plugin:list commands, which Load adds and which don't boot the app,
// still run. Plugins are compiled into
// the app with its privileges: install only code you trust.
func Load(app *anetos.App, plugins []Plugin, opts ...Option) error {
	if app.Booted() {
		return errors.New("ext: Load after the app booted: call it in setup")
	}
	o := options{mounts: map[string]string{}}
	for _, opt := range opts {
		opt(&o)
	}
	reg, err := anetos.Resolve[*registry](app)
	if err != nil {
		reg = &registry{}
		for _, c := range reg.commands() {
			if err := app.AddCommand(c); err != nil {
				return err
			}
		}
		anetos.Provide(app, reg)
	}
	for name, prefix := range o.mounts {
		i := slices.IndexFunc(plugins, func(p Plugin) bool { return p != nil && p.Name() == name })
		if i < 0 {
			return fmt.Errorf("ext: Mount(%q): no such plugin", name)
		}
		if _, ok := plugins[i].(HasRoutes); !ok {
			return fmt.Errorf("ext: Mount(%q): the plugin has no routes", name)
		}
		if !strings.HasPrefix(prefix, "/") || prefix == "/" || path.Clean(prefix) != prefix ||
			strings.ContainsAny(prefix, " \t{}") {
			return fmt.Errorf("ext: Mount(%q, %q): the prefix must be a clean path such as /billing/stripe, without spaces or wildcards", name, prefix)
		}
	}
	for _, p := range plugins {
		if p == nil {
			return errors.New("ext: a nil plugin")
		}
		info, err := load(app, reg, p, o)
		if err != nil {
			return fmt.Errorf("ext: plugin %s: %w", p.Name(), err)
		}
		reg.plugins = append(reg.plugins, info)
	}
	return nil
}

// load wires one plugin into the app.
func load(app *anetos.App, reg *registry, p Plugin, o options) (Info, error) {
	name := p.Name()
	info := Info{Name: name}
	if !pluginName.MatchString(name) {
		return info, errors.New("invalid name: use up to 40 lower-case letters, digits and -, starting with a letter")
	}
	if slices.Contains(reserved, name) {
		return info, fmt.Errorf("the name %q is reserved for the framework", name)
	}
	if slices.ContainsFunc(reg.plugins, func(i Info) bool { return i.Name == name }) {
		return info, errors.New("loaded twice")
	}
	// The other way round: stripe-connect can't be loaded after a
	// stripe that sets STRIPE_CONNECT_….
	for _, other := range reg.plugins {
		for _, k := range other.Keys {
			if prefix := envPrefix(name); len(prefix) > len(envPrefix(other.Name)) && strings.HasPrefix(k.Name, prefix) {
				return info, fmt.Errorf("plugin %s's setting %s is in this plugin's namespace (%s)", other.Name, k.Name, prefix)
			}
		}
	}
	if c, ok := p.(Compat); ok && c.Requires() != "" {
		info.Requires = c.Requires()
		ok, err := Satisfies(anetos.Version(), info.Requires)
		if err != nil {
			return info, err
		}
		if !ok {
			return info, fmt.Errorf("requires Anetos %s, but this is %s: update the plugin or Anetos", info.Requires, anetos.Version())
		}
	}
	if c, ok := p.(HasConfig); ok {
		info.Adds = append(info.Adds, "config")
		dst := c.Config()
		if v := reflect.ValueOf(dst); v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
			return info, fmt.Errorf("its Config must return a non-nil pointer to a struct, not %T", dst)
		}
		keys, err := config.Keys(dst)
		if err != nil {
			return info, err
		}
		prefix := envPrefix(name)
		for _, k := range keys {
			if !strings.HasPrefix(k.Name, prefix) {
				return info, fmt.Errorf("setting %s doesn't start with %s", k.Name, prefix)
			}
			if !settingKey.MatchString(k.Name) {
				return info, fmt.Errorf("setting %q: use upper-case letters, digits and _", k.Name)
			}
			// Another plugin's namespace: stripe can't set STRIPE_CONNECT_…
			// when stripe-connect is loaded.
			for _, other := range reg.plugins {
				if op := envPrefix(other.Name); len(op) > len(prefix) && strings.HasPrefix(k.Name, op) {
					return info, fmt.Errorf("setting %s is in plugin %s's namespace (%s)", k.Name, other.Name, op)
				}
			}
		}
		info.Keys = keys
		if err := config.Bind(app.Source(), dst); err != nil {
			// Not an error yet: commands that don't boot the app
			// (plugin:env, help) still work. The rest of the plugin is
			// skipped, and the app doesn't boot.
			info.Adds = append(info.Adds, "(not loaded: settings missing or invalid)")
			app.Use(failed{name: name, err: err})
			return info, nil //nolint:nilerr // reported when the app boots
		}
	}
	if m, ok := p.(HasMigrations); ok {
		info.Adds = append(info.Adds, "migrations")
		set := m.Migrations()
		if set == nil || set.Source() != name {
			return info, fmt.Errorf("its migration set must be named %q (migrate.NewSet(%q))", name, name)
		}
		r, err := anetos.Resolve[*migrate.Runner](app)
		if err != nil {
			return info, errors.New("it has migrations: call migrate.New before ext.Load")
		}
		if err := r.Add(set); err != nil {
			return info, err
		}
	}
	if c, ok := p.(HasCommands); ok {
		info.Adds = append(info.Adds, "commands")
		for _, command := range c.Commands() {
			if !strings.HasPrefix(command.Name, name+":") {
				return info, fmt.Errorf("command %q must be named %s:…", command.Name, name)
			}
			if err := app.AddCommand(command); err != nil {
				return info, err
			}
		}
	}
	if j, ok := p.(HasJobs); ok {
		info.Adds = append(info.Adds, "jobs")
		q, err := anetos.Resolve[*queue.Queue](app)
		if err != nil {
			return info, errors.New("it has jobs: call queue.New before ext.Load")
		}
		if err := j.Jobs(q); err != nil {
			return info, err
		}
	}
	if r, ok := p.(HasRoutes); ok {
		info.Adds = append(info.Adds, "routes")
		srv, err := anetos.Resolve[*web.Server](app)
		if err != nil {
			return info, errors.New("it has routes: call web.NewServer before ext.Load")
		}
		prefix, ok := o.mounts[name]
		if !ok {
			prefix = "/" + name
		}
		info.Prefix = prefix
		if err := r.Routes(srv.Router().Group(prefix).As(name + ".")); err != nil {
			return info, err
		}
	}
	if s, ok := p.(HasSchedule); ok {
		info.Adds = append(info.Adds, "schedule")
		sched, err := anetos.Resolve[*schedule.Scheduler](app)
		if err != nil {
			return info, errors.New("it has scheduled tasks: call schedule.New before ext.Load")
		}
		if err := s.Schedule(sched); err != nil {
			return info, err
		}
	}
	if l, ok := p.(HasListeners); ok {
		info.Adds = append(info.Adds, "listeners")
		bus, err := anetos.Resolve[*events.Bus](app)
		if err != nil {
			return info, errors.New("it has listeners: call events.New before ext.Load")
		}
		if err := l.Listen(bus); err != nil {
			return info, err
		}
	}
	if b, ok := p.(HasBoot); ok {
		info.Adds = append(info.Adds, "boot")
		app.Use(booter{name: name, b: b})
	}
	return info, nil
}

// envPrefix is the prefix of the plugin name's settings: STRIPE_CONNECT_
// for stripe-connect.
func envPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}

// failed stops the app from booting with a plugin's settings error.
type failed struct {
	name string
	err  error
}

func (f failed) Name() string             { return "plugin " + f.name }
func (failed) Register(*anetos.App) error { return nil }
func (f failed) Boot(context.Context, *anetos.App) error {
	return fmt.Errorf("ext: plugin %s: settings: %w (go run . plugin:env %s lists them)", f.name, f.err, f.name)
}

// booter runs a plugin's Boot when the app boots.
type booter struct {
	name string
	b    HasBoot
}

func (b booter) Name() string             { return "plugin " + b.name }
func (booter) Register(*anetos.App) error { return nil }
func (b booter) Boot(ctx context.Context, app *anetos.App) error {
	return b.b.Boot(ctx, app)
}

// commands are plugin:list and plugin:env.
func (reg *registry) commands() []cmd.Command {
	return []cmd.Command{{
		Name:        "plugin:list",
		Former:      []string{"plugins:list"},
		Description: "List the plugins and what they add",
		ManagesApp:  true, // doesn't boot the app, so it works before the settings are set
		Run: func(_ context.Context, args *cmd.Args) error {
			if len(args.Args) > 0 {
				return cmd.Usagef("plugin:list takes no arguments")
			}
			if len(reg.plugins) == 0 {
				_, err := fmt.Fprintln(args.Stdout, "No plugins.")
				return err
			}
			tw := tabwriter.NewWriter(args.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "PLUGIN\tREQUIRES\tROUTES\tADDS")
			for _, p := range reg.plugins {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, dash(p.Requires), dash(p.Prefix), strings.Join(p.Adds, ", "))
			}
			return tw.Flush()
		},
	}, {
		Name:        "plugin:env",
		Former:      []string{"plugins:env"},
		Usage:       "[plugin]",
		Description: "Print the plugins' settings, as .env lines with their defaults",
		ManagesApp:  true, // doesn't boot the app, so it works before the settings are
		Run: func(_ context.Context, args *cmd.Args) error {
			if len(args.Args) > 1 {
				return cmd.Usagef("plugin:env takes at most a plugin's name")
			}
			found := false
			for _, p := range reg.plugins {
				if len(args.Args) == 1 && p.Name != args.Args[0] {
					continue
				}
				found = true
				if len(p.Keys) == 0 {
					continue
				}
				fmt.Fprintf(args.Stdout, "# %s\n", p.Name)
				for _, k := range p.Keys {
					line := k.Name + "=" + dotenvValue(k.Default)
					if k.Required {
						line += " # required"
					}
					fmt.Fprintln(args.Stdout, line)
				}
			}
			if len(args.Args) == 1 && !found {
				return fmt.Errorf("no plugin named %s", args.Args[0])
			}
			return nil
		},
	}}
}

// dotenvValue writes v for a .env file: as is when it is safe unquoted,
// otherwise double-quoted with the escapes config.ParseDotenv reads.
func dotenvValue(v string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.,:/@+%=", r)
	}
	if !strings.ContainsFunc(v, func(r rune) bool { return !safe(r) }) {
		return v
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(v) + `"`
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

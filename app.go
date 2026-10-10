// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"sync"
	"time"

	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/supervisor"
)

// Provider packages a piece of functionality (a database, a mailer, a set of
// routes, a plugin) and wires it into an [App].
//
// Providers run in two phases, in the order they were added with [App.Use]:
//
//   - Register: provide services and read configuration. No I/O, no
//     goroutines. Every provider's Register runs before any Boot, so Boot may
//     rely on services from providers added later.
//   - Boot: open connections, add components, register shutdown hooks.
//
// The public plugin API (roadmap B11) builds on this interface.
type Provider interface {
	// Name identifies the provider in logs and errors.
	Name() string
	// Register adds the provider's services and commands to a.
	Register(a *App) error
	// Boot starts what the provider needs, when the app boots.
	Boot(ctx context.Context, a *App) error
}

// App is an Anetos application: its configuration, logger, services,
// providers and supervised components.
//
// Create one with [New], add providers and components, then call [App.Run].
// An App is safe for concurrent use.
type App struct {
	cfg AppConfig
	src config.Source
	log *slog.Logger
	sup *supervisor.Supervisor

	svcMu    sync.RWMutex
	services map[reflect.Type]any

	mu        sync.Mutex
	state     appState
	providers []Provider
	hooks     []hook

	valMu  sync.RWMutex
	values []ctxValue

	cmdMu    sync.Mutex
	commands map[string]cmd.Command
	former   map[string]string // a command's former name → its name
	checks   []Check           // AddCheck, for doctor

	clock clock // Now

	unitFuncs // AroundUnits
	carriers  // AddCarrier
}

type ctxValue struct{ key, val any }

type appState int

const (
	appNew appState = iota
	appBooting
	appBooted
	appRunning
	appStopped
)

type hook struct {
	name string
	fn   func(ctx context.Context) error
}

// Option configures [New].
type Option func(*options)

type options struct {
	cfg       *AppConfig
	src       config.Source
	dir       string
	logger    *slog.Logger
	logOutput io.Writer
}

// WithSource makes New read configuration from src instead of calling
// config.Load. Tests use it with a config.Map for isolation.
func WithSource(src config.Source) Option {
	return func(o *options) { o.src = src }
}

// WithConfigDir sets the directory searched for .env files. Default ".".
func WithConfigDir(dir string) Option {
	return func(o *options) { o.dir = dir }
}

// WithAppConfig uses cfg as the application configuration instead of reading
// APP_* and LOG_* variables. It is still validated.
func WithAppConfig(cfg AppConfig) Option {
	return func(o *options) { o.cfg = &cfg }
}

// WithLogger replaces the default logger built from [LogConfig].
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithLogOutput sets where the default logger writes. Default os.Stderr.
func WithLogOutput(w io.Writer) Option {
	return func(o *options) { o.logOutput = w }
}

// New creates an App. Unless options say otherwise it loads configuration
// with config.Load (process environment, .env.<APP_ENV>, .env), binds and
// validates [AppConfig], and builds a structured logger.
//
// New returns an error listing every invalid or missing setting.
func New(opts ...Option) (*App, error) {
	o := options{dir: ".", logOutput: os.Stderr}
	for _, opt := range opts {
		opt(&o)
	}

	src := o.src
	if src == nil {
		var err error
		src, err = config.Load(config.LoadOptions{Dir: o.dir})
		if err != nil {
			return nil, err
		}
	}

	tracked := newTrackedSource(src)
	src = tracked

	var cfg AppConfig
	if o.cfg != nil {
		cfg = *o.cfg
		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("anetos: invalid app config: %w", err)
		}
	} else {
		var err error
		if cfg, err = config.Get[AppConfig](src); err != nil {
			return nil, err
		}
	}

	loc, err := appZone(cfg.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("anetos: APP_TIMEZONE: %w", err)
	}

	log := o.logger
	if log == nil {
		log = newLogger(cfg, o.logOutput)
	}

	a := &App{
		cfg:      cfg,
		src:      src,
		log:      log,
		sup:      supervisor.New(supervisor.Options{Logger: log, ShutdownTimeout: cfg.ShutdownTimeout - hookReserve(cfg.ShutdownTimeout)}),
		services: map[reflect.Type]any{},
		commands: map[string]cmd.Command{},
		former:   map[string]string{},
	}
	a.clock.loc = loc
	a.AddContextValue(clockKey{}, &a.clock)
	a.AddContextValue(loggerKey{}, log)
	tracked.setLogger(log)
	a.addBuiltins()
	a.AddCheck(Check{Name: "app", Run: a.appChecks})
	a.AddCheck(Check{Name: "settings", Run: tracked.settingsChecks})
	return a, nil
}

// hookReserve is the part of the shutdown budget kept for shutdown hooks.
func hookReserve(total time.Duration) time.Duration {
	return min(5*time.Second, total/5)
}

func newLogger(cfg AppConfig, w io.Writer) *slog.Logger {
	hopts := &slog.HandlerOptions{Level: cfg.Log.Level}
	var h slog.Handler
	format := cfg.Log.Format
	if format == "" {
		format = "text"
		if cfg.Env.IsProduction() {
			format = "json"
		}
	}
	if format == "json" {
		h = slog.NewJSONHandler(w, hopts)
	} else {
		h = slog.NewTextHandler(w, hopts)
	}
	return slog.New(h).With("app", cfg.Name, "env", string(cfg.Env))
}

// Config returns the application configuration.
func (a *App) Config() AppConfig { return a.cfg }

// Source returns the configuration source, for binding other config structs:
//
//	mailCfg, err := config.Get[MailConfig](app.Source())
func (a *App) Source() config.Source { return a.src }

// Logger returns the application logger.
func (a *App) Logger() *slog.Logger { return a.log }

type loggerKey struct{}

// Logger returns the logger of the app in ctx ([App.Logger]: LOG_LEVEL,
// LOG_FORMAT, with the app's name and environment), or slog.Default()
// when ctx has none. Jobs, listeners, scheduled tasks and handlers get
// contexts with the app in them, so they log with:
//
//	anetos.Logger(ctx).InfoContext(ctx, "invoice sent", "invoice", inv.ID)
//
// (In handlers, c.Logger() adds the request ID and route.)
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Supervisor returns the runtime supervisor, for health checks and status.
func (a *App) Supervisor() *supervisor.Supervisor { return a.sup }

// Use adds providers. Call it before [App.Boot] or [App.Run]; calling it
// later panics, because the providers would never be registered.
func (a *App) Use(providers ...Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != appNew {
		panic("anetos: App.Use called after Boot")
	}
	a.providers = append(a.providers, providers...)
}

// OnShutdown registers fn to run after all components have stopped, in
// reverse registration order (so resources opened first are closed last).
// Hooks share a context limited by what remains of APP_SHUTDOWN_TIMEOUT (see
// [AppConfig]); each hook runs even if an earlier one fails. A hook
// registered while hooks are running (for example, by another hook) runs
// after the current round.
//
// If components did not stop before the deadline, hooks still run so
// resources are released, even though a stuck component may still be using
// them.
func (a *App) OnShutdown(name string, fn func(ctx context.Context) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hooks = append(a.hooks, hook{name: name, fn: fn})
}

// Booted reports whether Boot has started (or the app has run or
// stopped): providers can no longer be added.
func (a *App) Booted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state != appNew
}

// Boot runs every provider's Register, then every provider's Boot. It is
// called by [App.Run] if needed, and may be called directly (for example in
// tests or one-off commands that don't run components).
//
// If any step fails, Boot runs the shutdown hooks registered so far, so
// resources opened by earlier providers are released, and returns the error.
func (a *App) Boot(ctx context.Context) error {
	a.mu.Lock()
	switch a.state {
	case appNew:
	case appBooting:
		a.mu.Unlock()
		return errors.New("anetos: Boot is already in progress")
	case appStopped:
		a.mu.Unlock()
		return errors.New("anetos: app has already stopped")
	default: // already booted or running
		a.mu.Unlock()
		return nil
	}
	a.state = appBooting
	providers := slices.Clone(a.providers)
	a.mu.Unlock()

	err := a.boot(ctx, providers)

	a.mu.Lock()
	if err != nil {
		a.state = appStopped
	} else {
		a.state = appBooted
	}
	a.mu.Unlock()

	if err != nil {
		return errors.Join(err, a.runHooks(a.cfg.ShutdownTimeout))
	}
	return nil
}

// Close runs the shutdown hooks of an app that was booted but not run, such
// as a one-off command that only needed its services. It returns an error if
// Run is in progress (cancel Run's context instead) and does nothing if the
// app has already stopped.
func (a *App) Close() error {
	a.mu.Lock()
	switch a.state {
	case appBooting, appRunning:
		a.mu.Unlock()
		return errors.New("anetos: Close called while booting or running; cancel Run's context instead")
	case appStopped:
		a.mu.Unlock()
		return nil
	}
	a.state = appStopped
	a.mu.Unlock()
	return a.runHooks(a.cfg.ShutdownTimeout)
}

// AddContextValue makes val available under key (as with
// context.WithValue) in every context the app hands out: providers' Boot,
// components and app.Go tasks, HTTP requests, shutdown hooks, and contexts
// passed through [App.Context]. Services use it so request and job code can
// reach them from a plain context.Context; the db package, for example,
// adds the database connection. Call it during Register; a value added in
// Boot reaches components, requests and hooks, but not the Boot contexts of
// providers that already ran.
func (a *App) AddContextValue(key, val any) {
	a.valMu.Lock()
	defer a.valMu.Unlock()
	a.values = append(a.values, ctxValue{key, val})
}

// Context returns parent with the values added by [App.AddContextValue],
// except those whose key parent already has a value for. Use it for
// contexts the app didn't create, such as in tests or one-off commands:
//
//	ctx := app.Context(context.Background())
func (a *App) Context(parent context.Context) context.Context {
	a.valMu.RLock()
	defer a.valMu.RUnlock()
	seen := make(map[any]bool, len(a.values))
	for _, v := range slices.Backward(a.values) { // the latest value per key wins
		if seen[v.key] {
			continue
		}
		seen[v.key] = true
		if parent.Value(v.key) == nil { // values already in parent win
			parent = context.WithValue(parent, v.key, v.val)
		}
	}
	return parent
}

func (a *App) boot(ctx context.Context, providers []Provider) error {
	seen := map[string]bool{}
	for _, p := range providers {
		if seen[p.Name()] {
			return fmt.Errorf("anetos: provider %q added twice", p.Name())
		}
		seen[p.Name()] = true
	}
	for _, p := range providers {
		if err := p.Register(a); err != nil {
			return fmt.Errorf("anetos: provider %q: register: %w", p.Name(), err)
		}
	}
	for _, p := range providers {
		if err := p.Boot(a.Context(ctx), a); err != nil {
			return fmt.Errorf("anetos: provider %q: boot: %w", p.Name(), err)
		}
	}
	return nil
}

// Run boots the app if necessary, then runs the components selected by roles
// (all of them if roles is empty) until ctx is canceled or a component
// escalates a failure. After the components stop, shutdown hooks run.
//
// A typical main function:
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	if err := app.Run(ctx); err != nil {
//		app.Logger().Error("app stopped with error", "error", err)
//		os.Exit(1)
//	}
//
// Run may be called only once.
func (a *App) Run(ctx context.Context, roles ...string) error {
	if err := a.Boot(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	if a.state != appBooted {
		a.mu.Unlock()
		return errors.New("anetos: Run called more than once")
	}
	a.state = appRunning
	a.mu.Unlock()

	runErr := a.sup.Run(a.Context(ctx), roles...)

	a.mu.Lock()
	a.state = appStopped
	a.mu.Unlock()

	budget := a.cfg.ShutdownTimeout
	if started := a.sup.ShutdownStarted(); !started.IsZero() {
		budget = max(a.cfg.ShutdownTimeout-time.Since(started), hookReserve(a.cfg.ShutdownTimeout))
	}
	return errors.Join(runErr, a.runHooks(budget))
}

func (a *App) runHooks(budget time.Duration) error {
	ctx, cancel := context.WithTimeout(a.Context(context.Background()), budget)
	defer cancel()

	var errs []error
	// Hooks may register more hooks; run those in later rounds.
	for range 10 {
		a.mu.Lock()
		hooks := a.hooks
		a.hooks = nil
		a.mu.Unlock()
		if len(hooks) == 0 {
			break
		}
		for _, h := range slices.Backward(hooks) {

			start := time.Now()
			if err := runHook(ctx, h); err != nil {
				a.log.Error("shutdown hook failed", "hook", h.name, "error", err)
				errs = append(errs, fmt.Errorf("anetos: shutdown hook %q: %w", h.name, err))
				continue
			}
			a.log.Debug("shutdown hook done", "hook", h.name, "took", time.Since(start))
		}
	}
	return errors.Join(errs...)
}

func runHook(ctx context.Context, h hook) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	return h.fn(ctx)
}

// ComponentOption customizes how [App.Go] and [App.Component] supervise a
// component.
type ComponentOption func(*supervisor.Spec)

// Roles assigns roles to the component, so `run --only=<role>` can select
// it. Components without roles run in every process.
func Roles(roles ...string) ComponentOption {
	return func(s *supervisor.Spec) { s.Roles = roles }
}

// Stage sets the shutdown stage. Default [supervisor.StageBackground].
func Stage(stage supervisor.Stage) ComponentOption {
	return func(s *supervisor.Spec) { s.Stage = stage }
}

// Restart sets the failure policy. Default [supervisor.RestartNever].
func Restart(policy supervisor.Restart) ComponentOption {
	return func(s *supervisor.Spec) { s.Restart = policy }
}

// Backoff configures restart delays for [supervisor.RestartOnFailure].
func Backoff(b supervisor.Backoff) ComponentOption {
	return func(s *supervisor.Spec) { s.Backoff = b }
}

// Go runs fn as a supervised background goroutine: panics are recovered and
// logged, ctx is canceled on shutdown (fn should return promptly then), and
// the task appears in [supervisor.Supervisor.Status]. Use it instead of a
// bare `go` statement so work is not silently lost on deploy.
//
// Go may be called before Run or while running (for example from a request
// handler). Names must be unique among components; returning an error from
// fn records a failure. By default a failed task is not restarted.
func (a *App) Go(name string, fn func(ctx context.Context) error, opts ...ComponentOption) error {
	return a.Component(supervisor.Func(name, fn), opts...)
}

// Component adds a long-running component, such as a custom server or
// consumer. Defaults: no roles, [supervisor.StageBackground],
// [supervisor.RestartNever].
func (a *App) Component(c supervisor.Component, opts ...ComponentOption) error {
	spec := supervisor.Spec{Component: c, Stage: supervisor.StageBackground}
	for _, opt := range opts {
		opt(&spec)
	}
	return a.sup.Add(spec)
}

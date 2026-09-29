// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/supervisor"
)

func quiet() anetos.Option { return anetos.WithLogger(slog.New(slog.DiscardHandler)) }

func newApp(t *testing.T, env config.Map, opts ...anetos.Option) *anetos.App {
	t.Helper()
	app, err := anetos.New(append([]anetos.Option{anetos.WithSource(env), quiet()}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return app
}

func TestNewDefaults(t *testing.T) {
	app := newApp(t, config.Map{})
	cfg := app.Config()
	want := anetos.DefaultAppConfig()
	if cfg != want {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
	if !cfg.Env.IsProduction() {
		t.Error("default env should be production")
	}
}

func TestNewReadsConfig(t *testing.T) {
	app := newApp(t, config.Map{
		"APP_NAME": "blog", "APP_ENV": "development", "APP_DEBUG": "true",
		"APP_SHUTDOWN_TIMEOUT": "5s", "LOG_LEVEL": "debug", "LOG_FORMAT": "json",
	})
	cfg := app.Config()
	if cfg.Name != "blog" || !cfg.Env.IsDevelopment() || !cfg.Debug || cfg.ShutdownTimeout != 5*time.Second ||
		cfg.Log.Level != slog.LevelDebug || cfg.Log.Format != "json" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestNewReportsAllConfigErrors(t *testing.T) {
	_, err := anetos.New(quiet(), anetos.WithSource(config.Map{
		"APP_ENV": "prod", "LOG_FORMAT": "xml", "APP_SHUTDOWN_TIMEOUT": "soon",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	// Type errors are reported first (Validate only runs when binding succeeds).
	if !strings.Contains(err.Error(), `APP_SHUTDOWN_TIMEOUT (AppConfig.ShutdownTimeout): invalid duration "soon"`) {
		t.Errorf("err = %v", err)
	}

	_, err = anetos.New(quiet(), anetos.WithSource(config.Map{"APP_ENV": "prod", "LOG_FORMAT": "xml"}))
	for _, want := range []string{`APP_ENV "prod" is not one of`, `LOG_FORMAT "xml" is not one of text, json`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
}

func TestDebugForbiddenInProduction(t *testing.T) {
	_, err := anetos.New(quiet(), anetos.WithSource(config.Map{"APP_DEBUG": "true"}))
	if err == nil || !strings.Contains(err.Error(), "APP_DEBUG=true is not allowed when APP_ENV=production") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithAppConfigIsValidated(t *testing.T) {
	cfg := anetos.DefaultAppConfig()
	cfg.ShutdownTimeout = 0
	if _, err := anetos.New(quiet(), anetos.WithSource(config.Map{}), anetos.WithAppConfig(cfg)); err == nil {
		t.Fatal("invalid AppConfig accepted")
	}
	cfg = anetos.DefaultAppConfig()
	cfg.Name = "direct"
	app, err := anetos.New(quiet(), anetos.WithSource(config.Map{"APP_NAME": "ignored"}), anetos.WithAppConfig(cfg))
	if err != nil || app.Config().Name != "direct" {
		t.Fatalf("app = %v, err = %v", app, err)
	}
}

func TestNewLoadsDotenvFromDir(t *testing.T) {
	dir := t.TempDir()
	content := "APP_NAME=from-file\nAPP_ENV=testing\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := anetos.New(quiet(), anetos.WithConfigDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if app.Config().Name != "from-file" || !app.Config().Env.IsTesting() {
		t.Errorf("config = %+v", app.Config())
	}
	if v, _ := app.Source().Lookup("APP_NAME"); v != "from-file" {
		t.Errorf("Source lookup = %q", v)
	}
}

func TestDefaultLoggerFormat(t *testing.T) {
	for env, want := range map[string]string{"production": `"app":"blog"`, "development": "app=blog"} {
		var buf bytes.Buffer
		app, err := anetos.New(anetos.WithSource(config.Map{"APP_NAME": "blog", "APP_ENV": env}), anetos.WithLogOutput(&buf))
		if err != nil {
			t.Fatal(err)
		}
		app.Logger().Info("hello")
		if !strings.Contains(buf.String(), want) {
			t.Errorf("%s log output = %q, want %q", env, buf.String(), want)
		}
	}
}

// --- container ---

type greeter interface{ Greet() string }
type english struct{}

func (english) Greet() string { return "hello" }

func TestContainer(t *testing.T) {
	app := newApp(t, config.Map{})

	if _, ok := anetos.Lookup[greeter](app); ok {
		t.Error("Lookup found unprovided service")
	}
	_, err := anetos.Resolve[greeter](app)
	if !errors.Is(err, anetos.ErrNotProvided) || !strings.Contains(err.Error(), "anetos_test.greeter") {
		t.Errorf("Resolve err = %v", err)
	}

	anetos.Provide[greeter](app, english{})
	if g := anetos.MustResolve[greeter](app); g.Greet() != "hello" {
		t.Error("wrong service")
	}
	// Keyed by static type: the concrete type is a separate entry.
	if _, ok := anetos.Lookup[english](app); ok {
		t.Error("concrete type should not be provided")
	}
	anetos.Provide(app, 42)
	anetos.Provide(app, 43) // replaces
	if n := anetos.MustResolve[int](app); n != 43 {
		t.Errorf("int = %d", n)
	}

	defer func() {
		if recover() == nil {
			t.Error("MustResolve did not panic")
		}
	}()
	anetos.MustResolve[string](app)
}

// --- providers & lifecycle ---

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) { r.mu.Lock(); r.events = append(r.events, e); r.mu.Unlock() }
func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.events, ",")
}

type testProvider struct {
	name        string
	rec         *recorder
	registerErr error
	bootErr     error
	boot        func(ctx context.Context, a *anetos.App) error
}

func (p *testProvider) Name() string { return p.name }
func (p *testProvider) Register(a *anetos.App) error {
	p.rec.add("register:" + p.name)
	return p.registerErr
}
func (p *testProvider) Boot(ctx context.Context, a *anetos.App) error {
	p.rec.add("boot:" + p.name)
	a.OnShutdown(p.name, func(context.Context) error { p.rec.add("close:" + p.name); return nil })
	if p.boot != nil {
		if err := p.boot(ctx, a); err != nil {
			return err
		}
	}
	return p.bootErr
}

func TestProviderOrder(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	// "cache" boots first but uses a service that "db" provides in Register.
	cache := &testProvider{name: "cache", rec: rec, boot: func(_ context.Context, a *anetos.App) error {
		_, err := anetos.Resolve[*recorder](a)
		return err
	}}
	db := &testProvider{name: "db", rec: rec}
	app.Use(cache, db)
	anetos.Provide(app, rec)

	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("second Boot should be a no-op: %v", err)
	}
	if got := rec.String(); got != "register:cache,register:db,boot:cache,boot:db" {
		t.Errorf("order = %s", got)
	}
}

func TestBootFailureRunsHooks(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	app.Use(
		&testProvider{name: "db", rec: rec},
		&testProvider{name: "queue", rec: rec},
		&testProvider{name: "broken", rec: rec, bootErr: errors.New("connection refused")},
	)
	err := app.Boot(context.Background())
	if err == nil || err.Error() != `anetos: provider "broken": boot: connection refused` {
		t.Fatalf("err = %v", err)
	}
	// Hooks registered before the failure run in reverse order.
	if got := rec.String(); !strings.HasSuffix(got, "close:broken,close:queue,close:db") {
		t.Errorf("events = %s", got)
	}
	if err := app.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "already stopped") {
		t.Errorf("Run after failed boot = %v", err)
	}
}

func TestRegisterFailure(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	app.Use(&testProvider{name: "bad", rec: rec, registerErr: errors.New("missing MAIL_HOST")}, &testProvider{name: "never", rec: rec})
	err := app.Boot(context.Background())
	if err == nil || err.Error() != `anetos: provider "bad": register: missing MAIL_HOST` {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(rec.String(), "boot:") {
		t.Errorf("boot ran after register failure: %s", rec)
	}
}

func TestDuplicateProvider(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	app.Use(&testProvider{name: "db", rec: rec}, &testProvider{name: "db", rec: rec})
	if err := app.Boot(context.Background()); err == nil || !strings.Contains(err.Error(), `provider "db" added twice`) {
		t.Fatalf("err = %v", err)
	}
}

func TestUseAfterBootPanics(t *testing.T) {
	app := newApp(t, config.Map{})
	if err := app.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("Use after Boot did not panic")
		}
	}()
	app.Use(&testProvider{name: "late", rec: &recorder{}})
}

func TestRunLifecycle(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	app.Use(&testProvider{name: "db", rec: rec}, &testProvider{name: "cache", rec: rec})

	started := make(chan struct{})
	if err := app.Go("worker", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		rec.add("worker-stopped")
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "register:db,register:cache,boot:db,boot:cache,worker-stopped,close:cache,close:db"
	if got := rec.String(); got != want {
		t.Errorf("events:\n got %s\nwant %s", got, want)
	}
	if err := app.Run(context.Background()); err == nil {
		t.Error("second Run succeeded")
	}
}

func TestRunRolesAndOptions(t *testing.T) {
	app := newApp(t, config.Map{})
	ran := make(chan string, 3)
	task := func(name string) func(context.Context) error {
		return func(ctx context.Context) error { ran <- name; <-ctx.Done(); return nil }
	}
	must(t, app.Go("http", task("http"), anetos.Roles("http"), anetos.Stage(supervisor.StageIngress)))
	must(t, app.Go("jobs", task("jobs"), anetos.Roles("workers"),
		anetos.Restart(supervisor.RestartOnFailure), anetos.Backoff(supervisor.Backoff{Initial: time.Millisecond})))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, "workers") }()
	if got := <-ran; got != "jobs" {
		t.Errorf("started %s, want jobs", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, st := range app.Supervisor().Status() {
		if st.Name == "http" && st.State != supervisor.StatePending {
			t.Errorf("http state = %v", st.State)
		}
		if st.Name == "jobs" && (st.Restart != supervisor.RestartOnFailure || st.Stage != supervisor.StageBackground) {
			t.Errorf("jobs spec = %+v", st)
		}
	}
}

func TestRunFailureStillRunsHooks(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	app.OnShutdown("flush", func(context.Context) error { rec.add("flush"); return nil })
	app.OnShutdown("bad-hook", func(context.Context) error { return errors.New("disk full") })
	app.OnShutdown("panicky-hook", func(context.Context) error { panic("oops") })
	must(t, app.Go("http", func(context.Context) error { return errors.New("port in use") },
		anetos.Restart(supervisor.StopOnFailure)))

	err := app.Run(context.Background())
	for _, want := range []string{"port in use", `shutdown hook "bad-hook": disk full`, `shutdown hook "panicky-hook": panic: oops`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
	if rec.String() != "flush" {
		t.Errorf("flush hook did not run: %s", rec)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Example_lifecycle shows the smallest complete application: configuration,
// a supervised background task, and a graceful shutdown.
func Example_lifecycle() {
	app, err := anetos.New(
		anetos.WithSource(config.Map{"APP_NAME": "demo", "APP_ENV": "development"}),
		anetos.WithLogOutput(io.Discard),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	app.OnShutdown("goodbye", func(context.Context) error {
		fmt.Println("shutdown hook ran")
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	_ = app.Go("ticker", func(taskCtx context.Context) error {
		fmt.Println("task started in", app.Config().Env)
		cancel() // in a real app, SIGTERM would do this
		<-taskCtx.Done()
		fmt.Println("task stopped")
		return nil
	})

	if err := app.Run(ctx); err != nil {
		fmt.Println(err)
	}
	// Output:
	// task started in development
	// task stopped
	// shutdown hook ran
}

// --- regression tests from the F2–F4 code review ---

func TestProvideNilInterface(t *testing.T) {
	app := newApp(t, config.Map{})
	anetos.Provide[io.Reader](app, nil)
	r, ok := anetos.Lookup[io.Reader](app)
	if !ok || r != nil {
		t.Errorf("Lookup = %v, %v; want nil, true", r, ok)
	}
}

func TestCloseAfterBoot(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	app.Use(&testProvider{name: "db", rec: rec})
	must(t, app.Boot(context.Background()))
	must(t, app.Close())
	if !strings.HasSuffix(rec.String(), "close:db") {
		t.Errorf("events = %s", rec)
	}
	must(t, app.Close()) // idempotent
	if err := app.Run(context.Background()); err == nil {
		t.Error("Run after Close succeeded")
	}
}

func TestCloseWhileRunning(t *testing.T) {
	app := newApp(t, config.Map{})
	started := make(chan struct{})
	must(t, app.Go("x", func(ctx context.Context) error { close(started); <-ctx.Done(); return nil }))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	<-started
	if err := app.Close(); err == nil {
		t.Error("Close while running succeeded")
	}
	cancel()
	must(t, <-done)
}

func TestHookRegisteredByHookRuns(t *testing.T) {
	rec := &recorder{}
	app := newApp(t, config.Map{})
	app.OnShutdown("outer", func(context.Context) error {
		rec.add("outer")
		app.OnShutdown("inner", func(context.Context) error { rec.add("inner"); return nil })
		return nil
	})
	must(t, app.Run(context.Background()))
	if rec.String() != "outer,inner" {
		t.Errorf("events = %s", rec)
	}
}

func TestHooksGetReservedBudget(t *testing.T) {
	cfg := anetos.DefaultAppConfig()
	cfg.ShutdownTimeout = 500 * time.Millisecond // components 400ms, hooks >= 100ms
	app, err := anetos.New(quiet(), anetos.WithSource(config.Map{}), anetos.WithAppConfig(cfg))
	must(t, err)

	release := make(chan struct{})
	defer close(release)
	must(t, app.Go("stuck", func(context.Context) error { <-release; return nil }))
	var hookBudget time.Duration
	app.OnShutdown("measure", func(ctx context.Context) error {
		dl, _ := ctx.Deadline()
		hookBudget = time.Until(dl)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err = app.Run(ctx)
	if !errors.Is(err, supervisor.ErrShutdownTimeout) {
		t.Fatalf("Run = %v", err)
	}
	if hookBudget < 50*time.Millisecond || hookBudget > 150*time.Millisecond {
		t.Errorf("hook budget = %v, want about 100ms", hookBudget)
	}
	if total := time.Since(start); total > 700*time.Millisecond {
		t.Errorf("shutdown took %v, want within the 500ms budget (+ grace)", total)
	}
}

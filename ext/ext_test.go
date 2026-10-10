// SPDX-License-Identifier: Apache-2.0

package ext_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/ext"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/web"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// greeter is a plugin with every capability but migrations.
type greeter struct {
	cfg struct {
		Greeting string `env:"GREETER_GREETING" default:"Hello"`
		Token    string `env:"GREETER_TOKEN,required"`
	}
	booted, jobs atomic.Int32
	requires     string
}

type greeted struct{ Name string }

func (g *greeter) Name() string     { return "greeter" }
func (g *greeter) Requires() string { return g.requires }
func (g *greeter) Config() any      { return &g.cfg }
func (g *greeter) Routes(r *web.Router) error {
	r.Get("/hello/{name}", func(c *web.Ctx) error {
		if err := events.Emit(c, greeted{c.Param("name")}); err != nil {
			return err
		}
		return c.Text(http.StatusOK, g.cfg.Greeting+", "+c.Param("name"))
	}).Name("hello")
	return nil
}
func (g *greeter) Commands() []cmd.Command {
	return []cmd.Command{{Name: "greeter:greet", Description: "Greet", Run: func(_ context.Context, a *cmd.Args) error {
		_, err := io.WriteString(a.Stdout, g.cfg.Greeting)
		return err
	}}}
}
func (g *greeter) Jobs(q *queue.Queue) error {
	return queue.RegisterFunc(q, "greeter:count", func(context.Context, struct{}) error { g.jobs.Add(1); return nil })
}
func (g *greeter) Schedule(s *schedule.Scheduler) error {
	return s.Add(schedule.Daily(), "greeter:daily", func(context.Context) error { return nil })
}
func (g *greeter) Listen(bus *events.Bus) error {
	return events.On(bus, func(ctx context.Context, e greeted) error {
		return queue.DispatchFunc(ctx, "greeter:count", struct{}{})
	})
}
func (g *greeter) Boot(context.Context, *anetos.App) error { g.booted.Add(1); return nil }

func newApp(t *testing.T, env config.Map) (*anetos.App, *web.Server) {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "QUEUE_DRIVER": "sync", "GREETER_TOKEN": "t"}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	_, err = queue.New(app)
	check(t, err)
	_, err = events.New(app)
	check(t, err)
	_, err = schedule.New(app)
	check(t, err)
	srv, err := web.NewServer(app)
	check(t, err)
	return app, srv
}

// run runs a command directly, as app.Execute would after booting.
func run(t *testing.T, app *anetos.App, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	for _, c := range app.Commands() {
		if c.Name == args[0] {
			if err := c.Run(context.Background(), &cmd.Args{Name: c.Name, Args: args[1:], Stdout: &out, Stderr: &out}); err != nil {
				return out.String() + err.Error(), 1
			}
			return out.String(), 0
		}
	}
	t.Fatalf("no command %s", args[0])
	return "", 0
}

func TestLoad(t *testing.T) {
	app, srv := newApp(t, config.Map{"GREETER_GREETING": "Hi"})
	g := &greeter{requires: ">= v0.2.0, < v1.0.0"}
	check(t, ext.Load(app, []ext.Plugin{g}, ext.Mount("greeter", "/greet")))
	check(t, app.Boot(context.Background()))
	if g.booted.Load() != 1 {
		t.Error("Boot didn't run")
	}

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/greet/hello/ada", nil).WithContext(app.Context(context.Background())))
	if w.Code != http.StatusOK || w.Body.String() != "Hi, ada" {
		t.Errorf("route: %d %q", w.Code, w.Body.String()[:min(200, w.Body.Len())])
	}
	if u, err := srv.Router().URL("greeter.hello", "ada"); err != nil || u != "/greet/hello/ada" {
		t.Errorf("route name: %q, %v", u, err)
	}
	if g.jobs.Load() != 1 {
		t.Errorf("the listener's job ran %d times", g.jobs.Load())
	}
	if tasks := anetos.MustResolve[*schedule.Scheduler](app).Tasks(); len(tasks) != 1 || tasks[0].Name != "greeter:daily" {
		t.Errorf("tasks %+v", tasks)
	}
	if out, code := run(t, app, "greeter:greet"); code != 0 || out != "Hi" {
		t.Errorf("greeter:greet: %d %q", code, out)
	}
	out, code := run(t, app, "plugins:list")
	if code != 0 || !strings.Contains(out, "greeter") || !strings.Contains(out, "/greet") ||
		!strings.Contains(out, "config, commands, jobs, routes, schedule, listeners, boot") {
		t.Errorf("plugins:list: %d\n%s", code, out)
	}
	out, code = run(t, app, "plugins:env")
	if code != 0 || out != "# greeter\nGREETER_GREETING=Hello\nGREETER_TOKEN= # required\n" {
		t.Errorf("plugins:env: %d %q", code, out)
	}
	if _, code := run(t, app, "plugins:env", "nope"); code == 0 {
		t.Error("plugins:env nope: exit 0")
	}
	if err := ext.Load(app, []ext.Plugin{&greeter{}}); err == nil {
		t.Error("Load after boot: no error")
	}
}

// quoted has defaults that need quoting in a .env file.
type quoted struct {
	named
	cfg struct {
		Plain string `env:"Q_PLAIN" default:"a-b.c:d/e@f,g+h=i%j"`
		Space string `env:"Q_SPACE" default:"hello world # not a comment"`
		Ref   string `env:"Q_REF" default:"${HOME} \"x\" \\"`
		Line  string `env:"Q_LINE" default:"a\nQ_INJECTED=1"`
		Empty string `env:"Q_EMPTY"`
	}
}

func (q *quoted) Config() any { return &q.cfg }

// plugins:env's lines read back as the defaults.
func TestEnvQuoting(t *testing.T) {
	app, _ := newApp(t, nil)
	q := &quoted{named: "q"}
	check(t, ext.Load(app, []ext.Plugin{q}))
	out, code := run(t, app, "plugins:env")
	if code != 0 {
		t.Fatal(out)
	}
	got, err := config.ParseDotenv(strings.NewReader(out), nil)
	check(t, err)
	want := config.Map{"Q_PLAIN": q.cfg.Plain, "Q_SPACE": q.cfg.Space, "Q_REF": q.cfg.Ref, "Q_LINE": q.cfg.Line, "Q_EMPTY": ""}
	if !maps.Equal(got, want) {
		t.Errorf("plugins:env:\n%s\nreads back as %q, want %q", out, got, want)
	}
	if !strings.Contains(out, "Q_PLAIN=a-b.c:d/e@f,g+h=i%j\n") {
		t.Errorf("a safe value was quoted:\n%s", out)
	}
}

// Missing settings stop the app from booting, but not plugins:env.
func TestMissingSettings(t *testing.T) {
	app, _ := newApp(t, config.Map{"GREETER_TOKEN": ""})
	g := &greeter{}
	check(t, ext.Load(app, []ext.Plugin{g}))
	if out, code := run(t, app, "plugins:env", "greeter"); code != 0 || !strings.Contains(out, "GREETER_TOKEN= # required") {
		t.Errorf("plugins:env: %d %q", code, out)
	}
	if err := app.Boot(context.Background()); err == nil || !strings.Contains(err.Error(), "GREETER_TOKEN") {
		t.Errorf("Boot = %v", err)
	}
	if g.booted.Load() != 0 {
		t.Error("a plugin without its settings booted")
	}
	if out, _ := run(t, app, "plugins:list"); !strings.Contains(out, "not loaded: settings missing or invalid") {
		t.Errorf("plugins:list:\n%s", out)
	}
	for _, c := range app.Commands() {
		if strings.HasPrefix(c.Name, "plugins:") && !c.ManagesApp {
			t.Errorf("%s boots the app", c.Name)
		}
	}
}

type named string

func (n named) Name() string { return string(n) }

type badConfig struct{ named }

func (badConfig) Config() any {
	return &struct {
		X string `env:"OTHER_X"`
	}{}
}

type badCommand struct{ named }

func (badCommand) Commands() []cmd.Command {
	return []cmd.Command{{Name: "other:x", Run: func(context.Context, *cmd.Args) error { return nil }}}
}

type badSet struct{ named }

func (badSet) Migrations() *migrate.Set { return migrate.NewSet("other") }

type goodSet struct{ named }

func (g goodSet) Migrations() *migrate.Set { return migrate.NewSet(string(g.named)) }

type valueConfig struct{ named }

func (valueConfig) Config() any {
	return struct {
		X string `env:"VAL_X"`
	}{}
}

type lowerKey struct{ named }

func (lowerKey) Config() any {
	return &struct {
		X string `env:"LOW_x"`
	}{}
}

type stripe struct{ named }

func (stripe) Config() any {
	return &struct {
		X string `env:"STRIPE_CONNECT_KEY"`
	}{}
}

type stripeConnect struct{ named }

func (stripeConnect) Config() any {
	return &struct {
		X string `env:"STRIPE_CONNECT_X"`
	}{}
}

type failingRoutes struct{ named }

func (failingRoutes) Routes(*web.Router) error { return errors.New("no routes today") }

func TestLoadErrors(t *testing.T) {
	for name, tt := range map[string]struct {
		plugins []ext.Plugin
		opts    []ext.Option
		want    string
	}{
		"bad name":        {[]ext.Plugin{named("Bad Name")}, nil, "invalid name"},
		"twice":           {[]ext.Plugin{named("a"), named("a")}, nil, "loaded twice"},
		"nil":             {[]ext.Plugin{nil}, nil, "nil plugin"},
		"too new":         {[]ext.Plugin{&greeter{requires: ">= v9.0.0"}}, nil, "requires Anetos >= v9.0.0"},
		"bad constraint":  {[]ext.Plugin{&greeter{requires: "~> 1"}}, nil, "constraint"},
		"setting prefix":  {[]ext.Plugin{badConfig{"bad"}}, nil, "OTHER_X doesn't start with BAD_"},
		"command prefix":  {[]ext.Plugin{badCommand{"bad"}}, nil, "must be named bad:"},
		"set name":        {[]ext.Plugin{badSet{"bad"}}, nil, `named "bad"`},
		"no migrate":      {[]ext.Plugin{goodSet{"good"}}, nil, "migrate.New"},
		"routes error":    {[]ext.Plugin{failingRoutes{"r"}}, nil, "no routes today"},
		"mount of no one": {[]ext.Plugin{named("a")}, []ext.Option{ext.Mount("b", "/b")}, `Mount("b")`},
		"mount no routes": {[]ext.Plugin{named("a")}, []ext.Option{ext.Mount("a", "/b")}, "has no routes"},
		"mount bad path":  {[]ext.Plugin{&greeter{}}, []ext.Option{ext.Mount("greeter", "nope")}, "clean path"},
		"mount trailing":  {[]ext.Plugin{&greeter{}}, []ext.Option{ext.Mount("greeter", "/g/")}, "clean path"},
		"mount dots":      {[]ext.Plugin{&greeter{}}, []ext.Option{ext.Mount("greeter", "/x/../y")}, "clean path"},
		"mount root":      {[]ext.Plugin{&greeter{}}, []ext.Option{ext.Mount("greeter", "/")}, "clean path"},
		"mount wildcard":  {[]ext.Plugin{&greeter{}}, []ext.Option{ext.Mount("greeter", "/{x}")}, "wildcards"},
		"health":          {[]ext.Plugin{named("health")}, nil, "reserved"},
		"keys of other 2": {[]ext.Plugin{stripe{"stripe"}, named("stripe-connect")}, nil, "is in this plugin's namespace"},
		"reserved":        {[]ext.Plugin{named("queue")}, nil, "reserved"},
		"value config":    {[]ext.Plugin{valueConfig{"val"}}, nil, "pointer to a struct"},
		"lower key":       {[]ext.Plugin{lowerKey{"low"}}, nil, "upper-case"},
		"other's keys":    {[]ext.Plugin{stripeConnect{"stripe-connect"}, stripe{"stripe"}}, nil, "in plugin stripe-connect's namespace"},
		"keys of other":   {[]ext.Plugin{stripe{"stripe"}, stripeConnect{"stripe-connect"}}, nil, "is in this plugin's namespace"},
	} {
		app, _ := newApp(t, nil)
		err := ext.Load(app, tt.plugins, tt.opts...)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want %q", name, err, tt.want)
		}
	}
	// What a plugin needs must be set up first.
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "GREETER_TOKEN": "t"}
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	if err := ext.Load(app, []ext.Plugin{&greeter{}}); err == nil || !strings.Contains(err.Error(), "queue.New") {
		t.Errorf("without a queue: %v", err)
	}
	// No plugins.
	app2, _ := newApp(t, nil)
	check(t, ext.Load(app2, nil))
	if out, _ := run(t, app2, "plugins:list"); out != "No plugins.\n" {
		t.Errorf("plugins:list = %q", out)
	}
}

func TestSatisfies(t *testing.T) {
	for _, tt := range []struct {
		version, constraint string
		want                bool
	}{
		{"v0.2.0", ">= v0.2.0", true},
		{"v0.2.0-dev", ">= v0.2.0, < v0.4.0", true},
		{"v0.3.9", ">= v0.2.0, < v0.4.0", true},
		{"v0.4.0", ">= v0.2.0, < v0.4.0", false},
		{"v0.1.9", "> v0.1.9", false},
		{"v1.2.3", "= v1.2.3", true},
		{"v1.2.3", "<= v1.2.2", false},
		{"v10.0.0", "> v9.99.99", true},
	} {
		got, err := ext.Satisfies(tt.version, tt.constraint)
		if err != nil || got != tt.want {
			t.Errorf("Satisfies(%s, %q) = %v, %v", tt.version, tt.constraint, got, err)
		}
	}
	for _, c := range []string{"", "1.0.0", ">= 1.0.0", ">= v1.0", ">= v1.0.x", "~ v1.0.0", ">= v01.0.0"} {
		if _, err := ext.Satisfies("v1.0.0", c); err == nil {
			t.Errorf("Satisfies(%q): no error", c)
		}
	}
	if _, err := ext.Satisfies("1.0.0", ">= v1.0.0"); err == nil {
		t.Error("a version without v: no error")
	}
	if v := anetos.Version(); !strings.HasPrefix(v, "v0.") {
		t.Errorf("Version = %s", v)
	}
}

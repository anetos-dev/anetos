// SPDX-License-Identifier: Apache-2.0

// Package anetostest tests Anetos applications: it boots the app with test
// settings and a clean database, sends requests through the router like a
// browser (cookies, CSRF tokens, the Referer), and asserts on responses,
// sessions and database rows.
//
//	func TestCreatePost(t *testing.T) {
//		app := anetostest.New(t, setup) // setup: the app's own wiring
//		author := anetostest.Create(app, factories.Authors)
//
//		app.Get("/posts/new")
//		app.PostForm("/posts", url.Values{"title": {"Hello"}, "author_id": {fmt.Sprint(author.ID)}}).
//			AssertRedirect("/posts").
//			AssertSessionHas("status", "Post created.")
//
//		anetostest.AssertDatabaseHas[models.Post](app, models.PostCols.Title.Eq("Hello"))
//	}
//
// Make one App per test or subtest: it reports to the t it was made with.
// With SQLite and no DB_DATABASE, each App gets its own in-memory database;
// with PostgreSQL or MySQL (DB_* in the environment or .env.testing) or a
// SQLite file, migrations run and everything the test does happens in a
// transaction that is rolled back at the end.
package anetostest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
)

// App is an application under test. It embeds the *anetos.App, and sends
// requests to the app's router with the methods of this type.
// Requests run one at a time; an App isn't safe for concurrent use (the
// database transaction isn't), but tests with their own App may run in
// parallel.
type App struct {
	*anetos.App
	t        testing.TB
	ctx      context.Context
	router   *web.Router
	sessions *session.Manager
	jar      *jar
	headers  http.Header
	referer  string
	tx       bool // the test runs in a transaction
}

// Option configures [New].
type Option func(*options)

type options struct {
	env         config.Map
	migrate     bool
	transaction bool
	level       slog.Level
}

// Env sets configuration values, over the test defaults and the
// environment.
func Env(values map[string]string) Option {
	return func(o *options) {
		maps.Copy(o.env, values)
	}
}

// WithoutMigrations skips running the app's migrations.
func WithoutMigrations() Option { return func(o *options) { o.migrate = false } }

// WithoutTransaction lets the test's writes be committed (with PostgreSQL,
// MySQL or a SQLite file), for code that needs its own connections or
// AfterCommit callbacks. The test must clean up after itself.
func WithoutTransaction() Option { return func(o *options) { o.transaction = false } }

// LogLevel sets the minimum level of the app's logs, which go to the
// test's log (shown for failed tests and with -v). Default Info.
func LogLevel(l slog.Level) Option { return func(o *options) { o.level = l } }

// New builds the app with setup (the function main uses to connect the
// database and add the server and routes; nil for none), boots it and
// prepares its database. It closes the app when the test ends.
//
// Settings, from highest priority: [Env] options; APP_ENV=testing and a
// random APP_KEY; the process environment; the .env.testing file next to
// go.mod, if there is one (say, DB_DATABASE=blog_test); then
// HTTP_ACCESS_LOG=false. The .env file is not read. With SQLite and
// neither DB_DATABASE nor DB_URL set (or set to ""), the database is in
// memory, not database/app.db.
func New(t testing.TB, setup func(app *anetos.App) (*web.Server, error), opts ...Option) *App {
	t.Helper()
	o := &options{env: config.Map{}, migrate: true, transaction: true, level: slog.LevelInfo}
	for _, opt := range opts {
		opt(o)
	}
	forced := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}
	defaults := config.Map{"HTTP_ACCESS_LOG": "false"}
	file, err := testingEnv()
	if err != nil {
		t.Fatalf("anetostest: %v", err)
	}
	explicit := config.Layers(o.env, config.Env(), file)
	conn, _ := explicit.Lookup("DB_CONNECTION")
	database, _ := explicit.Lookup("DB_DATABASE")
	dbURL, _ := explicit.Lookup("DB_URL")
	memory := config.Map{}
	if (conn == "" || conn == "sqlite") && database == "" && dbURL == "" {
		// Not the default database/app.db, even when DB_DATABASE is set
		// to "".
		memory["DB_DATABASE"] = ":memory:"
	}
	src := config.Layers(memory, o.env, forced, config.Env(), file, defaults)

	logs := &testLog{t: t}
	t.Cleanup(logs.stop) // runs last: nothing logs into a finished test
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: o.level}))))
	if err != nil {
		t.Fatalf("anetostest: %v", err)
	}
	t.Cleanup(func() { // also releases what setup opened if boot fails
		if err := app.Close(); err != nil {
			t.Errorf("anetostest: close: %v", err)
		}
	})
	a := &App{App: app, t: t, headers: http.Header{}}
	a.jar = newJar()

	if setup != nil {
		srv, err := setup(app)
		if err != nil {
			t.Fatalf("anetostest: setup: %v", err)
		}
		if srv != nil {
			a.router = srv.Router()
		}
	}
	ctx := context.Background()
	if err := app.Boot(ctx); err != nil {
		t.Fatalf("anetostest: boot: %v", err)
	}
	a.ctx = app.Context(ctx)
	a.sessions, _ = anetos.Resolve[*session.Manager](app)

	if o.migrate {
		if runner, err := anetos.Resolve[*migrate.Runner](app); err == nil {
			if _, err := runner.Up(a.ctx); err != nil {
				t.Fatalf("anetostest: migrate: %v", err)
			}
		}
	}
	if d, err := anetos.Resolve[*db.DB](app); err == nil && o.transaction && !inMemory(a.ctx, d) {
		tx, err := d.SQL().BeginTx(a.ctx, nil)
		if err != nil {
			t.Fatalf("anetostest: begin the test's transaction: %v", err)
		}
		t.Cleanup(func() {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Errorf("anetostest: roll back the test's transaction: %v", err)
			}
		})
		if a.ctx, err = db.WithTx(a.ctx, tx); err != nil {
			t.Fatalf("anetostest: %v", err)
		}
		a.tx = true
	}
	return a
}

// inMemory reports whether d is an in-memory SQLite database: one
// connection, which a transaction would hold, and gone with the test
// anyway.
func inMemory(ctx context.Context, d *db.DB) bool {
	if d.Dialect().Name() != "sqlite" {
		return false
	}
	var file string
	err := d.SQL().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file)
	return err == nil && file == ""
}

// Context returns the context the test's requests run with: it carries
// what the app provides (the database) and the test's transaction. Use it
// to call the app's code and the db package directly.
func (a *App) Context() context.Context { return a.ctx }

// Router returns the app's router, or nil if setup returned no server.
func (a *App) Router() *web.Router { return a.router }

// WithHeader sets a header on every later request.
func (a *App) WithHeader(name, value string) *App {
	a.headers.Set(name, value)
	return a
}

// WithSession changes the session later requests carry, as if earlier
// requests had stored the values: a signed-in user, a cart. It needs the
// session middleware's manager (session.ForApp).
func (a *App) WithSession(fn func(s *session.Session)) *App {
	a.t.Helper()
	if a.sessions == nil {
		a.t.Fatalf("anetostest: WithSession needs sessions (session.ForApp in setup)")
	}
	a.editSession(fn)
	return a
}

// Session returns the session the next request will carry, to inspect.
// Changes to it are not saved; use [App.WithSession].
func (a *App) Session() *session.Session {
	a.t.Helper()
	if a.sessions == nil {
		a.t.Fatalf("anetostest: Session needs sessions (session.ForApp in setup)")
	}
	return a.sessions.Load(a.cookieRequest())
}

// testingEnv reads the .env.testing file of the module the test is in.
func testingEnv() (config.Map, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil // not in a module
		}
		dir = parent
	}
	name := filepath.Join(dir, ".env.testing")
	f, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := config.ParseDotenv(f, config.Env())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}

var base = &url.URL{Scheme: "http", Host: "example.test", Path: "/"}

// cookieRequest is a request carrying the jar's cookies.
func (a *App) cookieRequest() *http.Request {
	r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base.String(), nil)
	for _, c := range a.jar.forPath("") { // whatever the session cookie's path
		r.AddCookie(c)
	}
	return r
}

func (a *App) editSession(fn func(s *session.Session)) {
	a.t.Helper()
	c, err := a.sessions.Edit(a.cookieRequest(), fn)
	if err != nil {
		a.t.Fatalf("anetostest: %v", err)
	}
	a.jar.set([]*http.Cookie{c})
}

// testLog writes the app's logs to the test's log until the test ends.
type testLog struct {
	mu   sync.Mutex
	t    testing.TB
	done bool
}

func (l *testLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.done {
		l.t.Log(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func (l *testLog) stop() {
	l.mu.Lock()
	l.done = true
	l.mu.Unlock()
}

var _ io.Writer = (*testLog)(nil)

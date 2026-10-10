// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/web"
)

type Note struct {
	db.Model
	Title string `db:"title" json:"title"`
}

func newApp(t *testing.T, env config.Map, logs io.Writer) *anetos.App {
	t.Helper()
	app, err := anetos.New(anetos.WithSource(env), anetos.WithLogOutput(logs))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// TestConnect checks the whole path: DB_* config, the DB in request
// contexts, query logging in development, and closing at shutdown.
func TestConnect(t *testing.T) {
	var logs bytes.Buffer
	app := newApp(t, config.Map{
		"APP_ENV":   "development",
		"LOG_LEVEL": "debug",
		"DB_DRIVER": "sqlite",
		"DB_NAME":   filepath.Join(t.TempDir(), "app.db"),
		"HTTP_ADDR": "127.0.0.1:0",
	}, &logs)
	d, err := db.Connect(t.Context(), app, sqlite.Driver())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := anetos.Resolve[*db.DB](app); got != d {
		t.Error("DB not provided to the container")
	}
	ctx := app.Context(context.Background())
	if _, err := db.Exec(ctx, "CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)"); err != nil {
		t.Fatal(err)
	}

	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().Post("/notes", web.H(func(c *web.Ctx, in struct {
		Title string `json:"title" validate:"required"`
	}) (web.Responder, error) {
		n := Note{Title: in.Title}
		if err := db.Create(c, &n); err != nil { // the request context carries the DB
			return nil, err
		}
		return web.Created(n), nil
	}))
	srv.Router().Get("/notes/{id}", web.H(func(c *web.Ctx, in struct {
		ID int64 `path:"id"`
	}) (Note, error) {
		return db.Find[Note](c, in.ID)
	}))

	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(runCtx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !srv.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	base := "http://" + srv.Addr()
	res, err := http.Post(base+"/notes", "application/json", strings.NewReader(`{"title":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Errorf("create: %d", res.StatusCode)
	}
	res, err = http.Get(base + "/notes/999")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("missing note: %d", res.StatusCode)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := d.Ping(context.Background()); err == nil {
		t.Error("DB still open after shutdown")
	}
	if !strings.Contains(logs.String(), `msg=query`) || !strings.Contains(logs.String(), `INSERT INTO`) {
		t.Errorf("queries not logged in development:\n%s", logs.String())
	}
}

func TestConnectErrors(t *testing.T) {
	app := newApp(t, config.Map{"DB_DRIVER": "postgres"}, io.Discard)
	if _, err := db.Connect(t.Context(), app, sqlite.Driver()); err == nil || !strings.Contains(err.Error(), "import the driver module") {
		t.Errorf("unknown driver: %v", err)
	}
	app = newApp(t, config.Map{"DB_NAME": t.TempDir()}, io.Discard) // a directory
	if _, err := db.Connect(t.Context(), app, sqlite.Driver()); err == nil {
		if err := app.Boot(t.Context()); err == nil || !strings.Contains(err.Error(), "db: connect to sqlite") {
			t.Errorf("unusable database accepted: %v", err)
		}
	}
	// After boot, Connect checks right away.
	app = newApp(t, config.Map{"DB_NAME": t.TempDir()}, io.Discard)
	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Connect(t.Context(), app, sqlite.Driver()); err == nil {
		t.Error("unusable database accepted after boot")
	}
	app = newApp(t, config.Map{"DB_MAX_OPEN_CONNS": "-1"}, io.Discard)
	if _, err := db.Connect(t.Context(), app, sqlite.Driver()); err == nil {
		t.Error("invalid config accepted")
	}
}

func TestLoadConfigPrefix(t *testing.T) {
	cfg, err := db.LoadConfig(config.Map{"ANALYTICS_DB_DRIVER": "postgres", "ANALYTICS_DB_PORT": "6543", "DB_PORT": "1"}, "ANALYTICS_")
	if err != nil || cfg.Driver != "postgres" || cfg.Port != 6543 || cfg.MaxOpenConns != 25 {
		t.Errorf("cfg = %+v, %v", cfg, err)
	}
}

func TestSlowQueryLog(t *testing.T) {
	var logs bytes.Buffer
	d := open(t, cfgFor(":memory:"))
	d2 := db.New(d.SQL(), d.Dialect(), db.WithLogger(slogTo(&logs)), db.WithSlowQuery(time.Nanosecond))
	if _, err := db.Exec(db.WithDB(t.Context(), d2), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "slow query") || strings.Contains(logs.String(), "args") {
		t.Errorf("logs: %s", logs.String())
	}
}

type Counter struct {
	ID int64 `db:"id,pk"`
}

func TestInsertDefaultValues(t *testing.T) {
	ctx := db.WithDB(t.Context(), open(t, cfgFor(":memory:")))
	if _, err := db.Exec(ctx, "CREATE TABLE counters (id INTEGER PRIMARY KEY AUTOINCREMENT)"); err != nil {
		t.Fatal(err)
	}
	var c Counter
	if err := db.Create(ctx, &c); err != nil || c.ID != 1 {
		t.Errorf("create: %+v %v", c, err)
	}
}

// TestConnectTwice: a second Connect, or migrate.New, for one app is an
// error, not a second pool or runner.
func TestConnectTwice(t *testing.T) {
	app := newApp(t, config.Map{
		"APP_ENV":   "testing",
		"DB_DRIVER": "sqlite",
		"DB_NAME":   filepath.Join(t.TempDir(), "app.db"),
	}, io.Discard)
	defer app.Close()
	if _, err := db.Connect(t.Context(), app, sqlite.Driver()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Connect(t.Context(), app, sqlite.Driver()); err == nil || !strings.Contains(err.Error(), "called twice") {
		t.Errorf("Connect twice: %v", err)
	}
	if _, err := migrate.New(app, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.New(app, nil); err == nil || !strings.Contains(err.Error(), "called twice") {
		t.Errorf("migrate.New twice: %v", err)
	}
}

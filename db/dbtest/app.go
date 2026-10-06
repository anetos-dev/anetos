// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/factory"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/web"
)

// stAppItem is the model of [RunApp].
type stAppItem struct {
	db.Model
	Name string `db:"name" json:"name"`
}

// TableName names the table: all dbtest tables start with st_.
func (stAppItem) TableName() string { return "st_app_items" }

type newAppItem struct {
	Name string `json:"name" validate:"required|unique:st_app_items,name"`
}

var appItems = factory.New(func(n int) stAppItem { return stAppItem{Name: fmt.Sprintf("item %d", n)} })

// RunApp tests an app using the driver with anetostest: migrations, the
// test's transaction (seen by requests and validation rules) and its
// rollback. env holds the DB_* settings for a database that isn't
// in-memory.
func RunApp(t *testing.T, drv db.Driver, env map[string]string) {
	t.Helper()
	cfg, err := db.LoadConfig(config.Map(env), "")
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(drv, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := db.WithDB(context.Background(), d)
	drop := func() {
		for _, table := range []string{"st_app_items", "st_app_migrations"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
		}
	}
	drop()
	t.Cleanup(func() {
		drop()
		_ = d.Close()
	})

	set := migrate.NewSet("dbtest")
	set.Add("2026_01_01_000000_create_st_app_items", migrate.Func(
		func(s *migrate.Schema) error {
			return s.Create("st_app_items", func(t *migrate.Table) {
				t.ID()
				t.String("name", 100).Unique()
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.Drop("st_app_items") },
	))
	setup := func(app *anetos.App) (*web.Server, error) {
		if _, err := db.Connect(context.Background(), app, drv); err != nil {
			return nil, err
		}
		if _, err := migrate.ForApp(app, []*migrate.Set{set}, migrate.WithTable("st_app_migrations")); err != nil {
			return nil, err
		}
		srv, err := web.NewServer(app)
		if err != nil {
			return nil, err
		}
		srv.Router().Post("/items", web.H(func(c *web.Ctx, in newAppItem) (web.Responder, error) {
			it := stAppItem{Name: in.Name}
			if err := db.Create(c, &it); err != nil {
				return nil, err
			}
			return web.Created(it), nil
		}))
		srv.Router().Post("/items/unchecked", web.H(func(c *web.Ctx, in stAppItem) (web.Responder, error) {
			if err := db.Create(c, &in); err != nil {
				return nil, err // a duplicate: the database refuses it
			}
			return web.Created(in), nil
		}))
		srv.Router().Get("/items/count", func(c *web.Ctx) error {
			n, err := db.Query[stAppItem](c).Count()
			if err != nil {
				return err
			}
			return c.JSON(http.StatusOK, n)
		})
		return srv, nil
	}

	for i := range 2 { // the second run sees nothing of the first
		t.Run(fmt.Sprint("run", i+1), func(t *testing.T) {
			app := anetostest.New(t, setup, anetostest.Env(env))
			app.PostJSON("/items", newAppItem{Name: "tea"}).AssertCreated().AssertJSONPath("name", "tea")
			app.PostJSON("/items", newAppItem{Name: "tea"}).AssertValidationErrors("name")
			// A failed statement doesn't break the test's transaction
			// (PostgreSQL aborts it): requests run in savepoints.
			if res := app.PostJSON("/items/unchecked", stAppItem{Name: "tea"}); res.StatusCode < 400 {
				t.Errorf("duplicate: status %d", res.StatusCode)
			}
			anetostest.CreateMany(app, appItems, 2)
			app.GetJSON("/items/count").AssertOK().AssertJSON(3)
			anetostest.AssertDatabaseHas[stAppItem](app, db.Col[string]("name").Eq("tea"))
			anetostest.AssertDatabaseCount[stAppItem](app, 3)
		})
	}
	if n, err := db.Query[stAppItem](ctx).Count(); err != nil || n != 0 {
		t.Errorf("after the tests: %d rows, %v", n, err)
	}
	t.Run("doctor", func(t *testing.T) {
		drop()
		doctor := func(args ...string) string {
			t.Helper()
			src := config.Map{"APP_ENV": "development"}
			maps.Copy(src, env)
			app, err := anetos.New(anetos.WithSource(src), anetos.WithLogger(slog.New(slog.DiscardHandler)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Connect(context.Background(), app, drv); err != nil {
				t.Fatal(err)
			}
			if _, err := migrate.ForApp(app, []*migrate.Set{set}, migrate.WithTable("st_app_migrations")); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			app.ExecuteArgs(context.Background(), args, &out, &out)
			return strings.Join(strings.Fields(out.String()), " ")
		}
		if out := doctor("doctor"); !strings.Contains(out, "warning migrations: 1 migration(s) haven't run (2026_01_01_000000_create_st_app_items): run migrate") ||
			!strings.Contains(out, "ok db") {
			t.Errorf("before migrate:\n%s", out)
		}
		if out := doctor("migrate"); !strings.Contains(out, "create_st_app_items") {
			t.Fatalf("migrate:\n%s", out)
		}
		if out := doctor("doctor"); !strings.Contains(out, "ok migrations") || !strings.Contains(out, "0 problems, 0 warnings") {
			t.Errorf("after migrate:\n%s", out)
		}
		drop()
	})
	t.Run("search settings", func(t *testing.T) { runSearchApp(t, drv, env, d) })
}

// runSearchApp checks the search settings when the app boots: a search
// index built for another SEARCH_LANGUAGE stops the app, but not the
// commands that change the schema, and search:reindex rebuilds it.
func runSearchApp(t *testing.T, drv db.Driver, env map[string]string, d *db.DB) {
	set := migrate.NewSet("dbtest")
	set.Add("2026_01_01_000000_create_st_app_notes", migrate.Func(
		func(s *migrate.Schema) error {
			return s.Create("st_app_notes", func(t *migrate.Table) {
				t.ID()
				t.Text("text")
				t.SearchIndex("text")
			})
		},
		func(s *migrate.Schema) error { return s.Drop("st_app_notes") },
	))
	run := func(lang string, args ...string) (int, string) {
		t.Helper()
		src := config.Map{"APP_ENV": "testing", "SEARCH_LANGUAGE": lang}
		maps.Copy(src, env)
		app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Connect(context.Background(), app, drv); err != nil {
			t.Fatal(err)
		}
		if _, err := migrate.ForApp(app, []*migrate.Set{set}, migrate.WithTable("st_app_search_migrations")); err != nil {
			t.Fatal(err)
		}
		app.Command("notes:count", "Count the notes about tea", func(ctx context.Context, args *cmd.Args) error {
			n, err := db.Query[stAppNote](ctx).Search("tea").Count()
			fmt.Fprint(args.Stdout, n)
			return err
		})
		var out bytes.Buffer
		code := app.ExecuteArgs(context.Background(), args, &out, &out)
		return code, out.String()
	}
	ctx := db.WithDB(context.Background(), d)
	t.Cleanup(func() {
		for _, table := range []string{"st_app_notes", "st_app_notes_search", "st_app_search_migrations", db.SearchIndexesTable} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
		}
	})
	if code, out := run("simple", "migrate"); code != 0 {
		t.Fatalf("migrate: %d %s", code, out)
	}
	if _, err := db.Exec(ctx, "INSERT INTO st_app_notes (text) VALUES (?), (?)", "Green teas", "Coffee"); err != nil {
		t.Fatal(err)
	}
	if code, out := run("simple", "notes:count"); code != 0 || out != "1" {
		t.Fatalf("notes:count: %d %s", code, out)
	}
	if d.Dialect().Name() == "mysql" {
		// MySQL has no english: refused, whatever the command.
		for _, c := range []string{"notes:count", "search:reindex"} {
			if code, out := run("english", c); code != 1 || !strings.Contains(out, "isn't available on MySQL") {
				t.Errorf("english %s on MySQL: %d %s", c, code, out)
			}
		}
	} else {
		if code, out := run("english", "notes:count"); code != 1 || !strings.Contains(out, "search:reindex") {
			t.Errorf("a stale index: %d %s", code, out)
		}
		if code, out := run("english", "search:reindex"); code != 0 || !strings.Contains(out, "Reindexed:   st_app_notes") {
			t.Errorf("search:reindex: %d %s", code, out)
		}
		if code, out := run("english", "notes:count"); code != 0 || out != "1" {
			t.Errorf("after search:reindex: %d %s", code, out)
		}
		if code, out := run("simple", "migrate:status"); code != 0 {
			t.Errorf("migrate:status with a stale index: %d %s", code, out)
		}
		if code, out := run("simple", "search:reindex", "st_app_notes"); code != 0 {
			t.Errorf("search:reindex back: %d %s", code, out)
		}
	}
	// The tables and the search index go with a rollback, and come back.
	if code, out := run("simple", "migrate:reset"); code != 0 {
		t.Errorf("migrate:reset: %d %s", code, out)
	}
	if idx, err := db.SearchIndexes(ctx); err != nil || len(idx) != 0 {
		t.Errorf("after migrate:reset: %+v, %v", idx, err)
	}
	if code, out := run("simple", "migrate"); code != 0 {
		t.Errorf("migrate again: %d %s", code, out)
	}
	if code, out := run("simple", "search:reindex"); code != 0 || !strings.Contains(out, "st_app_notes") {
		t.Errorf("search:reindex: %d %s", code, out)
	}
	// migrate:fresh drops the search objects too (SQLite's FTS5 tables).
	if code, out := run("simple", "migrate:fresh"); code != 0 {
		t.Errorf("migrate:fresh: %d %s", code, out)
	}
	if code, out := run("simple", "notes:count"); code != 0 || out != "0" {
		t.Errorf("after migrate:fresh: %d %s", code, out)
	}
}

// stAppNote is the searchable model of [runSearchApp].
type stAppNote struct {
	ID   int64  `db:"id,pk"`
	Text string `db:"text"`
}

// TableName names the table.
func (stAppNote) TableName() string { return "st_app_notes" }

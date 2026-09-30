// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/factory"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/anetostest"
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
}

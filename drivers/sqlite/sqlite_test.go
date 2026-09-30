// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/dbtest"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/web"
)

func open(t *testing.T, cfg db.Config) *db.DB {
	t.Helper()
	d, err := db.Open(sqlite.Driver(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	return d
}

func cfgFor(database string) db.Config {
	return db.Config{Connection: "sqlite", Database: database, MaxOpenConns: 10, MaxIdleConns: 10}
}

func TestConformanceFile(t *testing.T) {
	dbtest.Run(t, open(t, cfgFor(filepath.Join(t.TempDir(), "sub", "app.db"))))
}

func TestApp(t *testing.T) {
	dbtest.RunApp(t, sqlite.Driver(), map[string]string{"DB_DATABASE": filepath.Join(t.TempDir(), "app.db")})
}

func TestConformanceMemory(t *testing.T) {
	d := open(t, cfgFor(":memory:"))
	if n := d.SQL().Stats().MaxOpenConnections; n != 1 {
		t.Fatalf("in-memory database uses %d connections", n)
	}
	dbtest.Run(t, d)
}

func TestOddFileNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"v1?.db", "a#b.db", "pct%41.db", "sp ace.db"} {
		d := open(t, cfgFor(filepath.Join(dir, name)))
		if _, err := d.SQL().Exec("CREATE TABLE t (x INTEGER)"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		var timeout int
		if err := d.SQL().QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
			t.Errorf("%s: busy_timeout %d %v", name, timeout, err)
		}
	}
}

// TestAnetostestDatabases checks which SQLite databases anetostest isolates
// how: never the development database/app.db, a transaction around a file
// wherever its name comes from.
func TestAnetostestDatabases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	file := filepath.Join(dir, "test.db")
	for name, env := range map[string]map[string]string{
		"empty DB_DATABASE": {"DB_DATABASE": ""},
		"explicit memory":   {"DB_DATABASE": ":memory:"},
		"file":              {"DB_DATABASE": file},
		"file URL":          {"DB_URL": "file:" + file},
	} {
		t.Run(name, func(t *testing.T) {
			for range 2 { // the second app sees nothing of the first
				t.Run("run", func(t *testing.T) {
					app := anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
						_, err := db.Connect(context.Background(), app, sqlite.Driver())
						return nil, err
					}, anetostest.Env(env))
					if _, err := db.Exec(app.Context(), "CREATE TABLE IF NOT EXISTS st_anetostest (name TEXT UNIQUE)"); err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(app.Context(), "INSERT INTO st_anetostest VALUES ('x')"); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "database", "app.db")); err == nil {
		t.Error("the development database was created")
	}
}

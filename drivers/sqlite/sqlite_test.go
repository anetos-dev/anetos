// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"os"
	"path/filepath"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/dbtest"
	"anetos.dev/anetos/drivers/sqlite"
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

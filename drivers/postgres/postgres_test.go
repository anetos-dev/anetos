// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"os"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/dbtest"
	"anetos.dev/anetos/drivers/postgres"
)

// TestConformance runs against the database in ANETOS_TEST_POSTGRES_URL,
// e.g. postgres://postgres@127.0.0.1:5432/anetos_test.
func TestConformance(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_POSTGRES_URL not set")
	}
	d, err := db.Open(postgres.Driver(), db.Config{Connection: "postgres", URL: url, MaxOpenConns: 10, MaxIdleConns: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	dbtest.Run(t, d)
}

// TestApp tests an app on the database with anetostest.
func TestApp(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_POSTGRES_URL not set")
	}
	dbtest.RunApp(t, postgres.Driver(), map[string]string{"DB_CONNECTION": "postgres", "DB_URL": url})
}

func TestDSN(t *testing.T) {
	cases := []struct {
		cfg  db.Config
		want string
	}{
		{db.Config{Host: "db", Database: "app", Username: "u", Password: "p@ss/word"}, "postgres://u:p%40ss%2Fword@db:5432/app?timezone=UTC"},
		{db.Config{Host: "::1", Port: 6543, Database: "app"}, "postgres://[::1]:6543/app?timezone=UTC"},
		{db.Config{Host: "db", Database: "app", Username: "u"}, "postgres://u@db:5432/app?timezone=UTC"},
		{db.Config{URL: "postgres://x/y?sslmode=require"}, "postgres://x/y?sslmode=require&timezone=UTC"},
		{db.Config{URL: "postgres://x/y?TimeZone=Asia/Dhaka"}, "postgres://x/y?TimeZone=Asia/Dhaka"},
		{db.Config{URL: "host=x dbname=y"}, "host=x dbname=y timezone=UTC"},
		{db.Config{URL: "host=x timezone=UTC"}, "host=x timezone=UTC"},
		{db.Config{Host: "/var/run/postgresql", Database: "my db", Username: "u"}, "postgres://u@/my%20db?host=%2Fvar%2Frun%2Fpostgresql&port=5432&timezone=UTC"},
	}
	for _, c := range cases {
		if got := postgres.DSN(c.cfg); got != c.want {
			t.Errorf("DSN = %s, want %s", got, c.want)
		}
	}
}

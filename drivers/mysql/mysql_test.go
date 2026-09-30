// SPDX-License-Identifier: Apache-2.0

package mysql_test

import (
	"os"
	"strings"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/dbtest"
	"anetos.dev/anetos/drivers/mysql"
)

// TestConformance runs against the database in ANETOS_TEST_MYSQL_URL, e.g.
// anetos:secret@tcp(127.0.0.1:3306)/anetos_test.
func TestConformance(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_MYSQL_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_MYSQL_URL not set")
	}
	d, err := db.Open(mysql.Driver(), db.Config{Connection: "mysql", URL: url, MaxOpenConns: 10, MaxIdleConns: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	dbtest.Run(t, d)
}

func TestDSN(t *testing.T) {
	got, err := mysql.DSN(db.Config{Host: "db", Database: "app", Username: "u", Password: "p@ss"})
	if err != nil || !strings.HasPrefix(got, "u:p@ss@tcp(db:3306)/app?") || !strings.Contains(got, "parseTime=true") || !strings.Contains(got, "clientFoundRows=true") {
		t.Errorf("DSN = %s, %v", got, err)
	}
	got, err = mysql.DSN(db.Config{URL: "u:p@tcp(h:1)/x?loc=Local"})
	if err != nil || !strings.Contains(got, "parseTime=true") || strings.Contains(got, "Local") {
		t.Errorf("DSN from URL = %s, %v", got, err)
	}
	if _, err := mysql.DSN(db.Config{URL: "not a dsn"}); err == nil {
		t.Error("bad DSN accepted")
	}
}

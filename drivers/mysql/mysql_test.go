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

// TestApp tests an app on the database with anetostest.
func TestApp(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_MYSQL_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_MYSQL_URL not set")
	}
	dbtest.RunApp(t, mysql.Driver(), map[string]string{"DB_CONNECTION": "mysql", "DB_URL": url})
}

// TestLocalTimeZone checks that a session time zone set in DB_URL
// stops the app at boot.
func TestLocalTimeZone(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_MYSQL_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_MYSQL_URL not set")
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	dbtest.RunLocalTimeZone(t, mysql.Driver(), db.Config{Connection: "mysql", URL: url + sep + "time_zone=%27%2B06:00%27"})

	// The server's own zone (SYSTEM) is accepted only if it is UTC.
	d, err := db.Open(mysql.Driver(), db.Config{Connection: "mysql", URL: url + sep + "time_zone=%27SYSTEM%27"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var utc bool
	if err := d.SQL().QueryRowContext(t.Context(), `SELECT FROM_UNIXTIME(1768478400) = '2026-01-15 12:00:00' AND FROM_UNIXTIME(1784116800) = '2026-07-15 12:00:00'`).Scan(&utc); err != nil {
		t.Fatal(err)
	}
	err = d.CheckTimeZone(t.Context())
	if utc != (err == nil) || (err != nil && !strings.Contains(err.Error(), "the server's")) {
		t.Errorf("SYSTEM zone (UTC: %v): %v", utc, err)
	}
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

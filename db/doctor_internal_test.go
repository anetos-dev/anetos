// SPDX-License-Identifier: Apache-2.0

package db

import (
	"errors"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

func TestDoctorChecks(t *testing.T) {
	yes := true
	inspect := Driver{InspectURL: func(url string) (string, string, error) {
		if url == "bad" {
			return "", "", errors.New("bad DSN")
		}
		host, mode, _ := strings.Cut(url, "|")
		return host, mode, nil
	}}
	tests := []struct {
		cfg  Config
		d    Driver
		env  anetos.Environment
		want string // "" for no finding
	}{
		{Config{Driver: "postgres", Host: "db.example.com"}, Driver{}, anetos.Production, ""}, // verify by default
		{Config{Driver: "postgres", Host: "db.example.com", TLS: TLSNone}, Driver{}, anetos.Production, "warning:DB_TLS=none for db.example.com: connections may go in plain text"},
		{Config{Driver: "mysql", Host: "10.0.0.5", TLS: TLSSkipVerify}, Driver{}, anetos.Staging, "warning:DB_TLS=skip-verify for 10.0.0.5: connections are encrypted, but"},
		{Config{Driver: "postgres", Host: "127.0.0.1", TLS: TLSNone}, Driver{}, anetos.Production, ""},
		{Config{Driver: "postgres", Host: "db.example.com", TLS: TLSNone}, Driver{}, anetos.Development, ""},
		{Config{Driver: "sqlite", LogQueries: &yes}, Driver{}, anetos.Production, "warning:DB_LOG_QUERIES=true in production"},
		{Config{Driver: "sqlite", TLS: TLSNone}, Driver{}, anetos.Production, ""},
		{Config{Driver: "postgres", Host: "localhost", LogQueries: &yes}, Driver{}, anetos.Production, "warning:DB_LOG_QUERIES=true in production"},
		{Config{Driver: "postgres", URL: "db.example.com|none"}, inspect, anetos.Production, "warning:DB_URL for db.example.com: connections may go in plain text"},
		{Config{Driver: "postgres", URL: "db.example.com|verify"}, inspect, anetos.Production, ""},
		{Config{Driver: "postgres", URL: "/var/run/postgresql|none"}, inspect, anetos.Production, ""},
		{Config{Driver: "postgres", URL: "bad"}, inspect, anetos.Production, "warning:DB_URL can't be read to check its TLS settings: bad DSN"},
		{Config{Driver: "postgres", URL: "db.example.com|none"}, Driver{}, anetos.Production, ""}, // the driver can't tell
	}
	for _, tt := range tests {
		found := checks(tt.cfg, tt.d, tt.env)
		var got string
		if len(found) > 0 {
			got = found[0].Severity.String() + ":" + found[0].Message
		}
		if len(found) > 1 || tt.want == "" && got != "" || !strings.HasPrefix(got, tt.want) {
			t.Errorf("%v in %s: got %v, want %q", tt.cfg, tt.env, found, tt.want)
		}
	}
}

// TestPlatformURL: a postgres:// DATABASE_URL when nothing else says
// where the database is.
func TestPlatformURL(t *testing.T) {
	for _, c := range []struct {
		src         config.Map
		used        bool
		url, driver string
	}{
		{config.Map{}, false, "", "sqlite"},
		{config.Map{"DATABASE_URL": "postgres://u@h/db"}, true, "postgres://u@h/db", "postgres"},
		{config.Map{"DATABASE_URL": "postgresql://u@h/db", "DB_DRIVER": "postgres"}, true, "postgresql://u@h/db", "postgres"},
		{config.Map{"DATABASE_URL": "postgres://u@h/db", "DB_DRIVER": "sqlite"}, false, "", "sqlite"},   // a dev shell's
		{config.Map{"DATABASE_URL": "postgres://u@h/db", "DB_CONNECTION": "mysql"}, false, "", "mysql"}, // the former name counts
		{config.Map{"DATABASE_URL": "postgres://u@h/db", "DB_URL": "file.db"}, false, "file.db", "sqlite"},
		{config.Map{"DATABASE_URL": "postgres://u@h/db", "DB_HOST": "db"}, false, "", "sqlite"},
		{config.Map{"DATABASE_URL": "postgres://u@h/db", "DB_NAME": "app"}, false, "", "sqlite"},
		{config.Map{"DATABASE_URL": "mysql://u@h/db"}, false, "", "sqlite"},
	} {
		cfg, err := config.Get[Config](c.src)
		if err != nil {
			t.Fatal(err)
		}
		used := platformURL(c.src, &cfg)
		if used != c.used || cfg.URL != c.url || cfg.Driver != c.driver || used && urlKey(cfg) != "DATABASE_URL" {
			t.Errorf("%v: %v %q %q", c.src, used, cfg.URL, cfg.Driver)
		}
	}
}

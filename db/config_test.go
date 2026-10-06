// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
)

func TestLoadConfigPrefixedErrors(t *testing.T) {
	_, err := db.LoadConfig(config.Map{"ANALYTICS_DB_PORT": "x"}, "ANALYTICS_")
	if err == nil || !strings.Contains(err.Error(), "ANALYTICS_DB_PORT") {
		t.Errorf("bind error = %v", err)
	}
	_, err = db.LoadConfig(config.Map{"ANALYTICS_DB_PORT": "70000"}, "ANALYTICS_")
	if err == nil || !strings.Contains(err.Error(), "ANALYTICS_DB_PORT 70000 is out of range") {
		t.Errorf("validation error = %v", err)
	}
	_, err = db.LoadConfig(config.Map{"DB_PORT": "x"}, "")
	if err == nil || !strings.Contains(err.Error(), "config: DB_PORT") {
		t.Errorf("unprefixed = %v", err)
	}
}

func TestConfigStringMasksSecrets(t *testing.T) {
	for _, c := range []db.Config{
		{Password: "s3cret"},
		{URL: "postgres://app:s3cret@db/app?sslmode=require"},
		{URL: "postgres://app@db/app?password=s3cret"},
		{URL: "postgres://db/app?user=app&password=s3cret&sslmode=disable"},
		{URL: "postgres:///app?host=/tmp&password=s3cret"},
		{URL: "app:s3cret@tcp(db:3306)/app"},
		{URL: "app:my s3cret@tcp(db:3306)/app?parseTime=true"},
		{URL: "app:p@s3cret@tcp(db:3306)/app"},
		{URL: "host=db user=app password=s3cret dbname=app"},
		{URL: "host=db password = s3cret"},
		{URL: "host=db password='my s3cret' dbname=app"},
		{URL: "password=p@s3cret"},
		{URL: "file:app.db?_auth&_auth_user=a&_auth_pass=s3cret"},
		{URL: "Server=db;Password=s3cret;"},
	} {
		for _, s := range []string{c.String(), fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
			if strings.Contains(s, "s3cret") {
				t.Errorf("secret shown: %s", s)
			}
		}
	}
	for dsn, want := range map[string]string{
		"postgres://app:s3cret@db/app?sslmode=require":   "postgres://app:xxxxx@db/app?sslmode=require",
		"app:s3cret@tcp(db:3306)/app?parseTime=true":     "app:xxxxx@tcp(db:3306)/app?parseTime=true",
		"host=db user=app password='my s3cret' dbname=a": "host=db user=app password=xxxxx dbname=a",
		"database/app.db": "database/app.db",
	} {
		if got := (db.Config{URL: dsn}).String(); !strings.Contains(got, "URL:"+want+" ") {
			t.Errorf("%s → %s", dsn, got)
		}
	}
	if s := fmt.Sprint(db.Config{}); !strings.Contains(s, "LogQueries:unset") {
		t.Errorf("LogQueries: %s", s)
	}
	var buf strings.Builder
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("connect", "cfg", db.Config{Password: "s3cret"})
	if strings.Contains(buf.String(), "s3cret") {
		t.Errorf("JSON log shows the secret: %s", buf.String())
	}
	if s := (db.Config{URL: "postgres://app@db/app", Host: "db"}).String(); !strings.Contains(s, "postgres://app@db/app") {
		t.Errorf("URL without a password changed: %s", s)
	}
}

func TestTLSMode(t *testing.T) {
	for _, c := range []struct {
		cfg  db.Config
		want string
	}{
		{db.Config{Host: "db.example.com"}, db.TLSVerify},
		{db.Config{Host: "127.0.0.1"}, db.TLSNone},
		{db.Config{Host: "/var/run/postgresql"}, db.TLSNone},
		{db.Config{Host: "127.0.0.1", TLSCA: "/etc/ca.pem"}, db.TLSVerify},
		{db.Config{Host: "db.example.com", TLS: db.TLSSkipVerify}, db.TLSSkipVerify},
		{db.Config{URL: "postgres://x/y"}, ""},
	} {
		if got := c.cfg.TLSMode(); got != c.want {
			t.Errorf("%v: %q, want %q", c.cfg, got, c.want)
		}
	}
	_, err := db.LoadConfig(config.Map{"DB_TLS": "none", "DB_TLS_CA": "/etc/ca.pem"}, "")
	if err == nil || !strings.Contains(err.Error(), "DB_TLS_CA is for checking the server's certificate") {
		t.Errorf("DB_TLS_CA with DB_TLS=none: %v", err)
	}
}

// SPDX-License-Identifier: Apache-2.0

// Package postgres is the PostgreSQL driver for Anetos's db package, using
// github.com/jackc/pgx.
//
//	database, err := db.Connect(ctx, app, postgres.Driver())
//
// The connection is built from DB_HOST, DB_PORT (default 5432),
// DB_DATABASE, DB_USERNAME and DB_PASSWORD, or taken whole from DB_URL
// (postgres://user:password@host:5432/app?sslmode=require); use DB_URL for
// TLS and other connection parameters. Sessions use the UTC time zone
// unless the URL sets timezone.
package postgres

import (
	"database/sql"
	"net"
	"net/url"
	"strconv"
	"strings"

	"anetos.dev/anetos/db"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// Driver returns the PostgreSQL driver, selected by DB_CONNECTION=postgres.
func Driver() db.Driver {
	return db.Driver{Name: "postgres", Dialect: db.Postgres(), Open: open}
}

func open(cfg db.Config) (*sql.DB, error) {
	return sql.Open("pgx", DSN(cfg))
}

// withUTC adds timezone=UTC to a connection string that doesn't choose a
// time zone, so CURRENT_TIMESTAMP and timestamp columns agree with the UTC
// times the db package writes. It handles URLs and key=value strings.
func withUTC(dsn string) string {
	if strings.Contains(strings.ToLower(dsn), "timezone=") {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "timezone=UTC"
	}
	return strings.TrimSpace(dsn) + " timezone=UTC"
}

// DSN returns the connection URL for cfg.
func DSN(cfg db.Config) string {
	if cfg.URL != "" {
		return withUTC(cfg.URL)
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{Scheme: "postgres", Path: "/" + cfg.Database}
	q := url.Values{}
	if strings.HasPrefix(cfg.Host, "/") {
		// A Unix socket directory goes in the host parameter.
		q.Set("host", cfg.Host)
		q.Set("port", strconv.Itoa(port))
	} else {
		u.Host = net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	}
	if cfg.Username != "" {
		u.User = url.UserPassword(cfg.Username, cfg.Password)
		if cfg.Password == "" {
			u.User = url.User(cfg.Username)
		}
	}
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	return u.String()
}

// SPDX-License-Identifier: Apache-2.0

// Package postgres is the PostgreSQL driver for Anetos's db package, using
// github.com/jackc/pgx.
//
//	database, err := db.Connect(ctx, app, postgres.Driver())
//
// The connection is built from DB_HOST, DB_PORT (default 5432),
// DB_NAME, DB_USER and DB_PASSWORD, or taken whole from DB_URL
// (postgres://user:password@host:5432/app?sslmode=verify-full). Built
// from DB_HOST, the connection uses TLS as DB_TLS says
// (db.Config.TLSMode): verified (sslmode=verify-full, with DB_TLS_CA's
// CAs if set) for any host but this machine's, unless DB_TLS=skip-verify
// or none. Sessions use the UTC time zone unless the URL sets timezone.
//
// For vector search (pgvector), each session sets hnsw.ef_search to
// db.SimilarCandidates and, with pgvector 0.8+, hnsw.iterative_scan to
// strict_order, so an HNSW index returns as many candidates as the query
// builder asks for, conditions included (by default it stops at 40). They
// apply to the app's own pgvector queries too.
package postgres

import (
	"context"
	"crypto/tls"
	"database/sql"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"anetos.dev/anetos/db"
)

// Driver returns the PostgreSQL driver, selected by DB_DRIVER=postgres.
func Driver() db.Driver {
	return db.Driver{Name: "postgres", Dialect: db.Postgres(), Open: open, InspectURL: inspectURL}
}

// inspectURL reads a DB_URL's host and TLS mode, for the doctor command:
// sslmode verify-full verifies; verify-ca verifies with sslrootcert
// (without it, any certificate of a public authority passes, its name
// unchecked); require skips verification (without sslrootcert); and
// disable, allow and prefer (the default) may go in plain text. With
// several hosts, it reports the worst of those not on this machine.
func inspectURL(dsn string) (host, mode string, err error) {
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", "", err
	}
	rank := map[string]int{db.TLSVerify: 0, db.TLSSkipVerify: 1, db.TLSNone: 2}
	host, mode = cc.Host, tlsMode(cc.TLSConfig)
	worst := -1
	consider := func(h string, tc *tls.Config) {
		m := tlsMode(tc)
		if db.LocalHost(h) || rank[m] <= worst {
			return
		}
		host, mode, worst = h, m, rank[m]
	}
	consider(cc.Host, cc.TLSConfig)
	for _, f := range cc.Fallbacks {
		consider(f.Host, f.TLSConfig)
	}
	return host, mode, nil
}

// tlsMode is the mode of one of pgx's connection attempts.
func tlsMode(tc *tls.Config) string {
	switch {
	case tc == nil:
		return db.TLSNone
	case !tc.InsecureSkipVerify:
		return db.TLSVerify // verify-full
	case tc.VerifyPeerCertificate != nil && tc.RootCAs != nil:
		return db.TLSVerify // verify-ca with the server's own authority
	}
	return db.TLSSkipVerify
}

func open(cfg db.Config) (*sql.DB, error) {
	cc, err := pgx.ParseConfig(DSN(cfg))
	if err != nil {
		return nil, err
	}
	return stdlib.OpenDB(*cc, stdlib.OptionAfterConnect(vectorSettings)), nil
}

// vectorSettings sets pgvector's HNSW search settings for the session.
// Before the extension's library loads, they are placeholders, which it
// adopts; an older pgvector without iterative scans drops that one.
// They are best effort: a server that refuses them (a pooler, say)
// keeps pgvector's defaults.
func vectorSettings(ctx context.Context, c *pgx.Conn) error {
	_, _ = c.Exec(ctx, "SET hnsw.ef_search = "+strconv.Itoa(db.SimilarCandidates))
	_, _ = c.Exec(ctx, "SET hnsw.iterative_scan = strict_order") // pgvector 0.8+
	return nil
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

// DSN returns the connection URL for cfg. Built from DB_HOST and the
// others, its sslmode follows cfg.TLSMode: verify-full (with sslrootcert
// from DB_TLS_CA), require, or disable.
func DSN(cfg db.Config) string {
	if cfg.URL != "" {
		return withUTC(cfg.URL)
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{Scheme: "postgres", Path: "/" + cfg.Name}
	q := url.Values{}
	if strings.HasPrefix(cfg.Host, "/") {
		// A Unix socket directory goes in the host parameter.
		q.Set("host", cfg.Host)
		q.Set("port", strconv.Itoa(port))
	} else {
		u.Host = net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
		if cfg.Password == "" {
			u.User = url.User(cfg.User)
		}
	}
	switch cfg.TLSMode() {
	case db.TLSVerify:
		q.Set("sslmode", "verify-full")
		if cfg.TLSCA != "" {
			q.Set("sslrootcert", cfg.TLSCA)
		}
	case db.TLSSkipVerify:
		q.Set("sslmode", "require")
	default:
		q.Set("sslmode", "disable")
	}
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	return u.String()
}

// SPDX-License-Identifier: Apache-2.0

// Package mysql is the MySQL and MariaDB driver for Anetos's db package,
// using github.com/go-sql-driver/mysql.
//
//	database, err := db.Connect(ctx, app, mysql.Driver())
//
// The connection is built from DB_HOST, DB_PORT (default 3306),
// DB_DATABASE, DB_USERNAME and DB_PASSWORD, or taken from DB_URL in the
// driver's DSN format (user:password@tcp(host:3306)/app?tls=true). Either
// way, times are read as time.Time in UTC (parseTime=true, loc=UTC), and
// updates report the rows they matched (clientFoundRows=true), which the db
// package relies on. Sessions use UTC (time_zone='+00:00') unless the DSN
// sets time_zone. Built from DB_HOST, the connection uses TLS as DB_TLS
// says (db.Config.TLSMode): verified for any host but this machine's,
// unless DB_TLS=skip-verify or none.
//
// For vector search on MariaDB 11.7+, each session sets mhnsw_ef_search
// to 1000 (MariaDB's default is 20), so a vector index returns the
// candidates the query builder asks for (db.SimilarCandidates) even when
// the query's conditions keep few of the chunks it visits. It applies to
// the app's own vector queries too; MySQL and older MariaDB ignore it.
package mysql

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"anetos.dev/anetos/db"
	"github.com/go-sql-driver/mysql"
)

// Driver returns the MySQL/MariaDB driver, selected by
// DB_CONNECTION=mysql.
func Driver() db.Driver {
	return db.Driver{Name: "mysql", Dialect: db.MySQL(), Open: open, InspectURL: inspectURL}
}

// inspectURL reads a DB_URL's host and TLS mode, for the doctor command,
// from the configuration the DSN selects: tls=true, or a registered
// configuration that checks certificates, verifies; tls=skip-verify (or a
// configuration with InsecureSkipVerify) doesn't; and tls=preferred,
// tls=false, no TLS or allowFallbackToPlaintext may go in plain text. A
// Unix socket is local.
func inspectURL(dsn string) (host, mode string, err error) {
	c, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", "", err
	}
	if c.Net == "unix" {
		return c.Addr, db.TLSNone, nil
	}
	host = c.Addr
	if h, _, err := net.SplitHostPort(c.Addr); err == nil {
		host = h
	}
	switch {
	case c.TLS == nil || c.AllowFallbackToPlaintext:
		mode = db.TLSNone
	case c.TLS.InsecureSkipVerify:
		mode = db.TLSSkipVerify
	default:
		mode = db.TLSVerify
	}
	return host, mode, nil
}

func open(cfg db.Config) (*sql.DB, error) {
	dsn, err := DSN(cfg)
	if err != nil {
		return nil, err
	}
	c, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := mysql.NewConnector(c)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(vectorConnector{conn}), nil
}

// vectorEFSearch is the mhnsw_ef_search sessions set: candidates a
// MariaDB vector index visits before the query's conditions.
const vectorEFSearch = 1000

// vectorConnector sets MariaDB's vector search setting on each new
// connection, best effort (MySQL has no such variable).
type vectorConnector struct{ driver.Connector }

func (v vectorConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c, err := v.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if ex, ok := c.(driver.ExecerContext); ok {
		_, _ = ex.ExecContext(ctx, "SET SESSION mhnsw_ef_search = "+strconv.Itoa(vectorEFSearch), nil)
	}
	return c, nil
}

// registerCA registers a TLS configuration that trusts the CAs in the PEM
// file at path and checks the server is host, and returns its name for
// the DSN's tls parameter.
func registerCA(path, host string) (string, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("mysql: DB_TLS_CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return "", fmt.Errorf("mysql: DB_TLS_CA %s has no PEM certificates", path)
	}
	sum := sha256.Sum256(append([]byte(host+"\x00"), pem...))
	name := "anetos-" + hex.EncodeToString(sum[:8])
	err = mysql.RegisterTLSConfig(name, &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12})
	return name, err
}

// DSN returns the go-sql-driver/mysql DSN for cfg. Built from DB_HOST and
// the others, its tls follows cfg.TLSMode: true (or the CAs of
// DB_TLS_CA), skip-verify, or false.
func DSN(cfg db.Config) (string, error) {
	var c *mysql.Config
	if cfg.URL != "" {
		var err error
		if c, err = mysql.ParseDSN(cfg.URL); err != nil {
			return "", err
		}
	} else {
		port := cfg.Port
		if port == 0 {
			port = 3306
		}
		c = mysql.NewConfig()
		c.Net = "tcp"
		c.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(port))
		c.DBName = cfg.Database
		c.User = cfg.Username
		c.Passwd = cfg.Password
		switch cfg.TLSMode() {
		case db.TLSVerify:
			c.TLSConfig = "true"
			if cfg.TLSCA != "" {
				name, err := registerCA(cfg.TLSCA, cfg.Host)
				if err != nil {
					return "", err
				}
				c.TLSConfig = name
			}
		case db.TLSSkipVerify:
			c.TLSConfig = "skip-verify"
		default:
			c.TLSConfig = "false"
		}
	}
	c.ParseTime = true
	c.Loc = time.UTC
	// Report matched rather than changed rows, as PostgreSQL and SQLite
	// do, so an update that changes nothing still finds its row.
	c.ClientFoundRows = true
	// Session time zone UTC, so CURRENT_TIMESTAMP matches the UTC times the
	// db package writes, unless the DSN chose one.
	if _, set := c.Params["time_zone"]; !set {
		if c.Params == nil {
			c.Params = map[string]string{}
		}
		c.Params["time_zone"] = "'+00:00'"
	}
	return c.FormatDSN(), nil
}

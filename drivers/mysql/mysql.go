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
// sets time_zone.
package mysql

import (
	"database/sql"
	"net"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"anetos.dev/anetos/db"
)

// Driver returns the MySQL/MariaDB driver, selected by
// DB_CONNECTION=mysql.
func Driver() db.Driver {
	return db.Driver{Name: "mysql", Dialect: db.MySQL(), Open: open}
}

func open(cfg db.Config) (*sql.DB, error) {
	dsn, err := DSN(cfg)
	if err != nil {
		return nil, err
	}
	return sql.Open("mysql", dsn)
}

// DSN returns the go-sql-driver/mysql DSN for cfg.
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

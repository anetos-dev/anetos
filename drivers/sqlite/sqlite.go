// SPDX-License-Identifier: Apache-2.0

// Package sqlite is the SQLite driver for Anetos's db package. It uses
// modernc.org/sqlite, a pure-Go SQLite, so builds need no C compiler and
// cross-compile to a single binary.
//
//	database, err := db.Connect(ctx, app, sqlite.Driver())
//
// DB_NAME is the database file (default database/app.db; its directory
// is created if missing), or :memory: for a private in-memory database.
// Connections use WAL journaling, a 5 second busy timeout, foreign keys,
// and transactions that take the write lock when they begin, so concurrent
// writers wait instead of failing. The driver adds the SQL function vector
// search compares embeddings with (db.SQLiteCosineFunction). DB_URL replaces all of this with a
// modernc.org/sqlite DSN of your own.
package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"anetos.dev/anetos/db"
	"modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

func init() {
	// Vector search (db.Q.Similar) compares embeddings with this function:
	// the pure-Go SQLite can't load extensions such as sqlite-vec.
	if err := sqlite.RegisterDeterministicScalarFunction(db.SQLiteCosineFunction, 2, cosine); err != nil {
		panic(err)
	}
}

// cosine is db.CosineDistanceBlobs as an SQL function; NULL for a NULL
// argument.
func cosine(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	a, aok := args[0].([]byte)
	b, bok := args[1].([]byte)
	if !aok || !bok {
		if args[0] == nil || args[1] == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: arguments must be vectors (blobs), not %T and %T", db.SQLiteCosineFunction, args[0], args[1])
	}
	return db.CosineDistanceBlobs(a, b)
}

// DefaultPath is the database file used when DB_NAME is empty.
const DefaultPath = "database/app.db"

// params are the connection's settings. _dqs=0 turns off SQLite's
// double-quoted string literals: "name" is always an identifier, so a
// misspelled column is an error rather than a string that matches
// everything (a unique rule on "emial" would always pass).
const params = "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_time_format=sqlite&_txlock=immediate&_dqs=0"

// Driver returns the SQLite driver, selected by DB_DRIVER=sqlite.
func Driver() db.Driver {
	return db.Driver{Name: "sqlite", Dialect: db.SQLite(), Tune: tune, Open: open}
}

func tune(cfg *db.Config) {
	if cfg.URL == "" && cfg.Name == ":memory:" {
		// Each connection to :memory: is a separate database, and closing
		// the last one discards it: keep exactly one, forever.
		cfg.MaxOpenConns, cfg.MaxIdleConns = 1, 1
		cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime = 0, 0
	}
}

func open(cfg db.Config) (*sql.DB, error) {
	return sql.Open("sqlite", DSN(cfg))
}

// DSN returns the modernc.org/sqlite connection string for cfg.
func DSN(cfg db.Config) string {
	if cfg.URL != "" {
		return cfg.URL
	}
	path := cfg.Name
	switch path {
	case ":memory:":
		return "file::memory:" + params
	case "":
		path = DefaultPath
	}
	if dir := filepath.Dir(path); dir != "." {
		// Best effort: sql.Open reports the real problem if this fails.
		_ = os.MkdirAll(dir, 0o755)
	}
	// The DSN is a URI: characters with a meaning there must be escaped
	// in the file name.
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	return fmt.Sprintf("file:%s%s", escaped, params)
}

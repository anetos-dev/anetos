// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql/driver"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Dialect is the SQL flavor of a database: placeholders, identifier quoting
// and the few statements that differ between databases. The db package
// ships dialects for PostgreSQL, MySQL/MariaDB and SQLite; driver modules
// pair one with a database/sql driver in a [Driver].
//
// The interface may grow before v1.0; implement it outside this package
// only if you are prepared to follow those changes.
type Dialect interface {
	// Name identifies the dialect: "postgres", "mysql" or "sqlite".
	Name() string
	// Placeholder returns the n-th (1-based) parameter placeholder.
	Placeholder(n int) string
	// QuoteIdent quotes one identifier (no dots).
	QuoteIdent(name string) string
	// Returning reports whether INSERT … RETURNING is supported.
	Returning() bool
	// DefaultValues is the INSERT clause for a row with no explicit columns.
	DefaultValues() string
	// Upsert returns the clause that follows INSERT … VALUES to update the
	// columns in update (or do nothing if it is empty) when a row conflicts
	// on the conflict columns. Column names are already quoted.
	Upsert(conflict, update []string) string
	// LockClause returns the row-locking suffix for SELECT … FOR UPDATE
	// (share=false) or FOR SHARE, or "" if the database has no row locks.
	LockClause(share bool) string
	// Arg converts a query argument before it is sent to the driver.
	Arg(v any) any
}

// Postgres returns the PostgreSQL dialect.
func Postgres() Dialect { return postgres{} }

// MySQL returns the MySQL and MariaDB dialect.
func MySQL() Dialect { return mysql{} }

// SQLite returns the SQLite dialect.
func SQLite() Dialect { return sqlite{} }

type postgres struct{}

func (postgres) Name() string               { return "postgres" }
func (postgres) Placeholder(n int) string   { return "$" + strconv.Itoa(n) }
func (postgres) QuoteIdent(s string) string { return quote(s, '"') }
func (postgres) Returning() bool            { return true }
func (postgres) DefaultValues() string      { return "DEFAULT VALUES" }
func (postgres) Upsert(conflict, update []string) string {
	return onConflict(conflict, update, "EXCLUDED")
}
func (postgres) LockClause(share bool) string {
	if share {
		return "FOR SHARE"
	}
	return "FOR UPDATE"
}

// Arg sends times in UTC, so timestamp (without time zone) columns hold
// UTC like timestamptz ones.
func (postgres) Arg(v any) any {
	if t, ok := timeArg(v); ok {
		return t
	}
	return v
}

type mysql struct{}

func (mysql) Name() string               { return "mysql" }
func (mysql) Placeholder(int) string     { return "?" }
func (mysql) QuoteIdent(s string) string { return quote(s, '`') }
func (mysql) Returning() bool            { return false } // MariaDB has it, MySQL doesn't
func (mysql) DefaultValues() string      { return "() VALUES ()" }
func (mysql) Upsert(conflict, update []string) string {
	var b strings.Builder
	b.WriteString("ON DUPLICATE KEY UPDATE ")
	if len(update) == 0 {
		// No update: keep the row as it is.
		b.WriteString(conflict[0] + " = " + conflict[0])
		return b.String()
	}
	for i, c := range update {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c + " = VALUES(" + c + ")")
	}
	return b.String()
}
func (mysql) LockClause(share bool) string {
	if share {
		return "LOCK IN SHARE MODE" // understood by MySQL and MariaDB
	}
	return "FOR UPDATE"
}
func (mysql) Arg(v any) any { return v }

type sqlite struct{}

func (sqlite) Name() string               { return "sqlite" }
func (sqlite) Placeholder(int) string     { return "?" }
func (sqlite) QuoteIdent(s string) string { return quote(s, '"') }
func (sqlite) Returning() bool            { return true } // SQLite 3.35+
func (sqlite) DefaultValues() string      { return "DEFAULT VALUES" }
func (sqlite) Upsert(conflict, update []string) string {
	return onConflict(conflict, update, "excluded")
}

// LockClause is empty: SQLite locks the whole database, and transactions
// opened by the SQLite driver take the write lock up front.
func (sqlite) LockClause(bool) string { return "" }

// SQLiteTimeFormat is how times are stored in SQLite: UTC text in the
// format of SQLite's own CURRENT_TIMESTAMP and datetime(), with fractional
// seconds when present. Using one format makes text comparison and sorting
// match time order, also against values written by SQL defaults.
const SQLiteTimeFormat = "2006-01-02 15:04:05.999999999"

// Arg writes times as UTC text in [SQLiteTimeFormat].
func (sqlite) Arg(v any) any {
	if t, ok := timeArg(v); ok {
		if tt, isTime := t.(time.Time); isTime {
			return tt.Format(SQLiteTimeFormat)
		}
		return t
	}
	return v
}

// timeArg converts a time argument (time.Time, *time.Time, or a
// driver.Valuer producing a time, like sql.Null[time.Time]) to a UTC
// time.Time, or nil for a nil pointer or NULL. ok is false for other values.
func timeArg(v any) (any, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), true
	case *time.Time:
		if t == nil {
			return nil, true
		}
		return t.UTC(), true
	case driver.Valuer:
		if isNilValuer(t) {
			return nil, false
		}
		dv, err := t.Value()
		if err != nil {
			return nil, false // let database/sql report it
		}
		if tt, ok := dv.(time.Time); ok {
			return tt.UTC(), true
		}
	}
	return nil, false
}

func isNilValuer(v driver.Valuer) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

func onConflict(conflict, update []string, excluded string) string {
	var b strings.Builder
	b.WriteString("ON CONFLICT (" + strings.Join(conflict, ", ") + ") DO ")
	if len(update) == 0 {
		b.WriteString("NOTHING")
		return b.String()
	}
	b.WriteString("UPDATE SET ")
	for i, c := range update {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c + " = " + excluded + "." + c)
	}
	return b.String()
}

// quote wraps an identifier in q, doubling any q inside it.
func quote(s string, q byte) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte(q)
	for i := range len(s) {
		if s[i] == q {
			b.WriteByte(q)
		}
		b.WriteByte(s[i])
	}
	b.WriteByte(q)
	return b.String()
}

// quoteName quotes a possibly qualified name such as posts.title, leaving
// a bare * alone.
func quoteName(d Dialect, name string) string {
	if !strings.Contains(name, ".") {
		if name == "*" {
			return name
		}
		return d.QuoteIdent(name)
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		if p != "*" {
			parts[i] = d.QuoteIdent(p)
		}
	}
	return strings.Join(parts, ".")
}

// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// conn is what queries run on: the pool or a transaction.
type conn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// handle returns the DB in ctx and the connection to use: the DB's
// transaction in ctx if there is one, else the pool.
func handle(ctx context.Context) (*DB, conn, error) {
	d, err := From(ctx)
	if err != nil {
		return nil, nil, err
	}
	if st := d.txIn(ctx); st != nil {
		return d, st.tx, nil
	}
	return d, d.sql, nil
}

func (d *DB) exec(ctx context.Context, c conn, query string, args []any) (sql.Result, error) {
	args = d.convertArgs(args)
	start := time.Now()
	res, err := c.ExecContext(ctx, query, args...)
	d.logQuery(ctx, query, args, start, err)
	return res, err
}

func (d *DB) query(ctx context.Context, c conn, query string, args []any) (*sql.Rows, error) {
	args = d.convertArgs(args)
	start := time.Now()
	rows, err := c.QueryContext(ctx, query, args...)
	d.logQuery(ctx, query, args, start, err)
	return rows, err
}

// convertArgs returns args converted by the dialect, in a new slice: the
// caller's slice may be shared.
func (d *DB) convertArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = d.dialect.Arg(a)
	}
	return out
}

func (d *DB) logQuery(ctx context.Context, query string, args []any, start time.Time, err error) {
	took := time.Since(start)
	if d.slow > 0 && took >= d.slow {
		d.log.WarnContext(ctx, "slow query", "sql", query, "took", took)
	} else if d.logAll {
		attrs := []any{"sql", query, "args", args, "took", took}
		if err != nil {
			attrs = append(attrs, "error", err)
		}
		d.log.DebugContext(ctx, "query", attrs...)
	}
}

// Named holds named arguments for raw SQL written with :name parameters:
//
//	db.Raw[User](ctx, "SELECT * FROM users WHERE email = :email", db.Named{"email": e})
type Named map[string]any

// rebind rewrites raw SQL written with ? (or :name with a single Named
// argument) into the dialect's placeholders. ?? stands for a literal ?
// (for PostgreSQL's JSON operators), and :: is left alone. SQL without
// arguments is returned unchanged. Quoted strings,
// quoted identifiers and comments are skipped. SQL without ? or :name is
// returned unchanged, so native placeholders like $1 still work.
func rebind(d Dialect, query string, args []any) (string, []any, error) {
	q, out, _, err := rebindCount(d, query, args)
	return q, out, err
}

// rebindCount is rebind that also reports how many placeholders it found.
func rebindCount(d Dialect, query string, args []any) (string, []any, int, error) {
	if len(args) == 0 {
		// Nothing to bind: send the SQL as written, so operators like
		// PostgreSQL's jsonb ? need no escaping.
		return query, args, 0, nil
	}
	var named Named
	if len(args) == 1 {
		named, _ = args[0].(Named)
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	var out []any
	n := 0
	changed := false
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case (c == 'E' || c == 'e') && d.Name() == "postgres" && strings.HasPrefix(query[i+1:], "'") &&
			(i == 0 || !isIdentByte(query[i-1])):
			// E'…' strings take backslash escapes.
			j := skipQuoted(query, i+1, '\'', true)
			b.WriteString(query[i:j])
			i = j
		case c == '\'' || c == '"' || c == '`':
			j := skipQuoted(query, i, c, d.Name() == "mysql")
			b.WriteString(query[i:j])
			i = j
		case c == '#' && d.Name() == "mysql":
			j := strings.IndexByte(query[i:], '\n')
			if j < 0 {
				j = len(query) - i
			}
			b.WriteString(query[i : i+j])
			i += j
		case c == '-' && strings.HasPrefix(query[i:], "--"):
			j := strings.IndexByte(query[i:], '\n')
			if j < 0 {
				j = len(query) - i
			}
			b.WriteString(query[i : i+j])
			i += j
		case c == '/' && strings.HasPrefix(query[i:], "/*"):
			end := skipBlockComment(query, i, d.Name() == "postgres")
			b.WriteString(query[i:end])
			i = end
		case c == '$' && d.Name() == "postgres" && dollarTag(query[i:]) != "":
			tag := dollarTag(query[i:])
			j := strings.Index(query[i+len(tag):], tag)
			end := len(query)
			if j >= 0 {
				end = i + len(tag) + j + len(tag)
			}
			b.WriteString(query[i:end])
			i = end
		case c == '?' && strings.HasPrefix(query[i:], "??"):
			b.WriteByte('?')
			i += 2
			changed = true
		case c == '?':
			if named != nil {
				return "", nil, 0, errors.New("db: mix of ? and named parameters")
			}
			n++
			b.WriteString(d.Placeholder(n))
			i++
			changed = true
		case c == ':' && strings.HasPrefix(query[i:], "::"):
			b.WriteString("::")
			i += 2
		case c == ':' && named != nil && i+1 < len(query) && isIdentStart(query[i+1:]):
			j := i + 1
			for j < len(query) {
				r, size := utf8.DecodeRuneInString(query[j:])
				if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
					break
				}
				j += size
			}
			name := query[i+1 : j]
			v, ok := named[name]
			if !ok {
				return "", nil, 0, fmt.Errorf("db: no value for :%s", name)
			}
			n++
			out = append(out, v)
			b.WriteString(d.Placeholder(n))
			i = j
			changed = true
		default:
			b.WriteByte(c)
			i++
		}
	}
	if named != nil {
		if !changed {
			return "", nil, 0, errors.New("db: db.Named given but the SQL has no :name parameters")
		}
		return b.String(), out, n, nil
	}
	if !changed {
		return query, args, 0, nil
	}
	if n != len(args) {
		return "", nil, 0, fmt.Errorf("db: the SQL has %d ? placeholders but %d arguments were given", n, len(args))
	}
	return b.String(), args, n, nil
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// skipBlockComment returns the index after the /* comment at i. PostgreSQL
// comments nest.
func skipBlockComment(s string, i int, nested bool) int {
	depth := 0
	for j := i; j < len(s)-1; j++ {
		switch {
		case s[j] == '/' && s[j+1] == '*':
			if depth == 0 || nested {
				depth++
			}
			j++
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(s)
}

func isIdentStart(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r == '_' || unicode.IsLetter(r)
}

// skipQuoted returns the index after the quoted section starting at i;
// a doubled quote character stays inside it, and so does a
// backslash-escaped one when backslash escapes are on (MySQL).
func skipQuoted(s string, i int, q byte, backslash bool) int {
	for j := i + 1; j < len(s); j++ {
		if backslash && s[j] == '\\' && j+1 < len(s) {
			j++
			continue
		}
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(s)
}

// dollarTag returns the PostgreSQL dollar-quote tag at the start of s
// ($$ or $name$), or "".
func dollarTag(s string) string {
	for j := 1; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '$':
			return s[:j+1]
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || (j > 1 && c >= '0' && c <= '9'):
		default:
			return ""
		}
	}
	return ""
}

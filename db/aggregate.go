// SPDX-License-Identifier: Apache-2.0

package db

import (
	"fmt"
	"strings"
)

// Sum returns the sum of col over the matching rows (zero if none),
// respecting Limit and Offset. Distinct is ignored (use [Select] with
// "SUM(DISTINCT col)"), and GroupBy is an error (use [Select] for
// per-group totals). The same holds for [Avg], [Min] and [Max].
//
//	total, err := db.Sum(db.Query[Order](ctx).Where(paid), db.Col[int64]("amount"))
func Sum[V, T any](q *Q[T], col Column[V]) (V, error) { return aggregate[V](q, "SUM", col.name) }

// Avg returns the average of col over the matching rows (zero if none).
func Avg[V, T any](q *Q[T], col Column[V]) (float64, error) {
	return aggregate[float64](q, "AVG", col.name)
}

// Min returns the smallest value of col (the zero value if no rows match).
func Min[V, T any](q *Q[T], col Column[V]) (V, error) { return aggregate[V](q, "MIN", col.name) }

// Max returns the largest value of col (the zero value if no rows match).
func Max[V, T any](q *Q[T], col Column[V]) (V, error) { return aggregate[V](q, "MAX", col.name) }

func aggregate[V, T any](q *Q[T], fn, col string) (V, error) {
	var zero V
	d, c, err := q.prepare()
	if err != nil {
		return zero, err
	}
	if len(q.groups) > 0 || len(q.havings) > 0 {
		return zero, fmt.Errorf("db: %s over groups needs db.Select, e.g. %q", fn, fn+"("+col+") AS total")
	}
	sub := q.clone()
	sub.distinct = false // DISTINCT would apply to the aggregated column alone
	var b *sqlBuilder
	if q.hasLimit || q.offset > 0 {
		// Aggregate over the limited rows, not over the table.
		inner := sub.selectSQL(d.dialect, []string{quoteName(d.dialect, col)})
		bare := col
		if i := strings.LastIndexByte(col, '.'); i >= 0 {
			bare = col[i+1:]
		}
		b = &sqlBuilder{d: d.dialect, args: inner.args, err: inner.err}
		b.write("SELECT " + fn + "(" + quoteName(d.dialect, bare) + ") FROM (" + inner.String() + ") AS anetos_agg")
	} else {
		sub.orders = nil
		b = sub.selectSQL(d.dialect, []string{fn + "(" + quoteName(d.dialect, col) + ")"})
	}
	if b.err != nil {
		return zero, b.err
	}
	return scalar[V](q.ctx, d, c, b)
}

// Pluck returns the values of one column of the matching rows:
//
//	emails, err := db.Pluck(db.Query[User](ctx).Where(active), db.Col[string]("email"))
func Pluck[V, T any](q *Q[T], col Column[V]) ([]V, error) {
	d, c, err := q.prepare()
	if err != nil {
		return nil, err
	}
	b := q.selectSQL(d.dialect, []string{quoteName(d.dialect, col.name)})
	if b.err != nil {
		return nil, b.err
	}
	rows, err := d.query(q.ctx, c, b.String(), b.args)
	return collect[V](d, rows, err)
}

// Select runs the query with the given SELECT terms (written in SQL) and
// scans the rows into R, matching columns to R's db tags (or snake_case
// field names). Use it for aggregates per group:
//
//	type authorStats struct {
//		AuthorID int64 `db:"author_id"`
//		Posts    int64 `db:"posts"`
//	}
//	stats, err := db.Select[authorStats](
//		db.Query[Post](ctx).GroupBy("author_id"),
//		"author_id", "COUNT(*) AS posts")
func Select[R, T any](q *Q[T], terms ...string) ([]R, error) {
	d, c, err := q.prepare()
	if err != nil {
		return nil, err
	}
	if len(terms) == 0 {
		terms = []string{"*"}
	}
	b := q.selectSQL(d.dialect, terms)
	if b.err != nil {
		return nil, b.err
	}
	rows, err := d.query(q.ctx, c, b.String(), b.args)
	return collect[R](d, rows, err)
}

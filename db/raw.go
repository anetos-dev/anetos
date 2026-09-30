// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
)

// Raw runs a SQL query and scans every row into T: a struct (columns match
// db tags or snake_case field names; extra columns are ignored) or a single
// value such as int64 or string for one-column results.
//
//	type row struct {
//		Name  string `db:"name"`
//		Total int64  `db:"total"`
//	}
//	rows, err := db.Raw[row](ctx, `
//		SELECT u.name, COUNT(p.id) AS total
//		FROM users u JOIN posts p ON p.author_id = u.id
//		WHERE p.created_at > ?
//		GROUP BY u.name`, since)
//
// Write parameters as ? on every database (?? for a literal ?), or as
// :name with a single [Named] argument. Native placeholders ($1) also work
// when the query has no ?.
func Raw[T any](ctx context.Context, query string, args ...any) ([]T, error) {
	d, c, err := handle(ctx)
	if err != nil {
		return nil, err
	}
	q, a, err := rebind(d.dialect, query, args)
	if err != nil {
		return nil, err
	}
	rows, err := d.query(ctx, c, q, a)
	return collect[T](d, rows, err)
}

// RawFirst is like [Raw] but returns only the first row, or [ErrNotFound].
func RawFirst[T any](ctx context.Context, query string, args ...any) (T, error) {
	var zero T
	rows, err := Raw[T](ctx, query, args...)
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, ErrNotFound
	}
	return rows[0], nil
}

// Exec runs a SQL statement that returns no rows, with the same parameter
// forms as [Raw].
func Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	d, c, err := handle(ctx)
	if err != nil {
		return nil, err
	}
	q, a, err := rebind(d.dialect, query, args)
	if err != nil {
		return nil, err
	}
	return d.exec(ctx, c, q, a)
}

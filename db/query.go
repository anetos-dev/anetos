// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Q is a query on the table of model T. Build one with [Query]; each method
// returns a new Q, so a base query can be reused:
//
//	published := db.Query[Post](ctx).Where(db.C("published").Eq(true))
//	recent, err := published.Latest().Limit(10).Get()
//	total, err := published.Count()
//
// Models with [SoftDeletes] exclude deleted rows unless [Q.WithTrashed] or
// [Q.OnlyTrashed] is used.
type Q[T any] struct {
	ctx      context.Context
	m        *meta
	err      error
	wheres   []Expr
	joins    []Expr
	orders   []Order
	groups   []string
	havings  []Expr
	limit    int
	offset   int
	hasLimit bool
	trashed  trashed
	distinct bool
	lock     lock
	with     []relSpec // relations to load
}

type trashed int

const (
	withoutTrashed trashed = iota
	withTrashed
	onlyTrashed
)

type lock int

const (
	noLock lock = iota
	lockUpdate
	lockShare
)

// Query starts a query on the table of model T, using the database (and
// transaction) in ctx.
func Query[T any](ctx context.Context) *Q[T] {
	m, err := metaOf(reflect.TypeFor[T]())
	return &Q[T]{ctx: ctx, m: m, err: err}
}

// Find returns the row of model T whose primary key is id, or
// [ErrNotFound].
func Find[T any](ctx context.Context, id any) (T, error) {
	return Query[T](ctx).Find(id)
}

func (q *Q[T]) clone() *Q[T] {
	c := *q
	c.wheres = slices.Clip(c.wheres)
	c.joins = slices.Clip(c.joins)
	c.orders = slices.Clip(c.orders)
	c.groups = slices.Clip(c.groups)
	c.havings = slices.Clip(c.havings)
	c.with = slices.Clip(c.with)
	return &c
}

// Where adds conditions, all of which must hold (AND). Build them with
// [Column] methods, [And], [Or], [Not] and [SQL].
func (q *Q[T]) Where(conds ...Expr) *Q[T] {
	c := q.clone()
	c.wheres = append(c.wheres, conds...)
	return c
}

// WhereRaw adds a condition written in SQL with ? placeholders.
func (q *Q[T]) WhereRaw(sql string, args ...any) *Q[T] { return q.Where(SQL(sql, args...)) }

// Join adds a join clause written in SQL:
//
//	q.Join("JOIN users ON users.id = posts.author_id").Where(db.C("users.active").Eq(true))
//
// The query still selects only T's columns; qualify column names that
// exist in both tables.
func (q *Q[T]) Join(sql string, args ...any) *Q[T] {
	c := q.clone()
	c.joins = append(c.joins, SQL(sql, args...))
	return c
}

// OrderBy adds ORDER BY terms.
func (q *Q[T]) OrderBy(orders ...Order) *Q[T] {
	c := q.clone()
	c.orders = append(c.orders, orders...)
	return c
}

// Latest orders by created_at, newest first.
func (q *Q[T]) Latest() *Q[T] { return q.OrderBy(C(q.qualified("created_at")).Desc()) }

// Oldest orders by created_at, oldest first.
func (q *Q[T]) Oldest() *Q[T] { return q.OrderBy(C(q.qualified("created_at")).Asc()) }

// Limit returns at most n rows.
func (q *Q[T]) Limit(n int) *Q[T] {
	c := q.clone()
	c.limit, c.hasLimit = max(n, 0), true
	return c
}

// Offset skips the first n rows.
func (q *Q[T]) Offset(n int) *Q[T] {
	c := q.clone()
	c.offset = max(n, 0)
	return c
}

// GroupBy adds GROUP BY columns. Use it with [Select] to read the groups.
func (q *Q[T]) GroupBy(cols ...string) *Q[T] {
	c := q.clone()
	c.groups = append(c.groups, cols...)
	return c
}

// Having adds HAVING conditions.
func (q *Q[T]) Having(conds ...Expr) *Q[T] {
	c := q.clone()
	c.havings = append(c.havings, conds...)
	return c
}

// Distinct selects distinct rows.
func (q *Q[T]) Distinct() *Q[T] {
	c := q.clone()
	c.distinct = true
	return c
}

// Scope applies reusable query modifiers:
//
//	func Published(q *db.Q[Post]) *db.Q[Post] { return q.Where(db.C("published").Eq(true)) }
//	posts, err := db.Query[Post](ctx).Scope(Published).Get()
func (q *Q[T]) Scope(scopes ...func(*Q[T]) *Q[T]) *Q[T] {
	for _, s := range scopes {
		q = s(q)
	}
	return q
}

// WithTrashed includes soft-deleted rows.
func (q *Q[T]) WithTrashed() *Q[T] {
	c := q.clone()
	c.trashed = withTrashed
	return c
}

// OnlyTrashed returns only soft-deleted rows.
func (q *Q[T]) OnlyTrashed() *Q[T] {
	c := q.clone()
	c.trashed = onlyTrashed
	return c
}

// ForUpdate locks the selected rows until the transaction ends (SELECT …
// FOR UPDATE). Use it inside [Tx]. On SQLite it does nothing: SQLite
// transactions lock the whole database.
func (q *Q[T]) ForUpdate() *Q[T] {
	c := q.clone()
	c.lock = lockUpdate
	return c
}

// ForShare locks the selected rows against changes until the transaction
// ends.
func (q *Q[T]) ForShare() *Q[T] {
	c := q.clone()
	c.lock = lockShare
	return c
}

// WithContext returns the query with another context, for example a base
// query built outside a transaction and run inside one:
//
//	err := db.Tx(ctx, func(ctx context.Context) error {
//		rows, err := published.WithContext(ctx).ForUpdate().Get()
//		…
//	})
func (q *Q[T]) WithContext(ctx context.Context) *Q[T] {
	c := q.clone()
	c.ctx = ctx
	return c
}

func (q *Q[T]) qualified(col string) string {
	if q.m == nil {
		return col
	}
	return q.m.table + "." + col
}

// ---- SQL generation ----

// where writes the WHERE clause, including the soft-delete scope.
func (q *Q[T]) where(b *sqlBuilder) {
	conds := q.wheres
	if q.m.deletedAt >= 0 {
		col := C(q.qualified(q.m.cols[q.m.deletedAt].name))
		switch q.trashed {
		case withoutTrashed:
			conds = append(slices.Clip(conds), col.IsNull())
		case onlyTrashed:
			conds = append(slices.Clip(conds), col.NotNull())
		}
	}
	if len(conds) == 0 {
		return
	}
	b.write(" WHERE ")
	And(conds...).build(b)
}

func (q *Q[T]) from(b *sqlBuilder) {
	b.write(" FROM ")
	b.name(q.m.table)
	for _, j := range q.joins {
		b.write(" ")
		j.build(b)
	}
}

func (q *Q[T]) tail(b *sqlBuilder, withOrder bool) {
	if len(q.groups) > 0 {
		b.write(" GROUP BY ")
		for i, g := range q.groups {
			if i > 0 {
				b.write(", ")
			}
			b.name(g)
		}
	}
	if len(q.havings) > 0 {
		b.write(" HAVING ")
		And(q.havings...).build(b)
	}
	if withOrder && len(q.orders) > 0 {
		b.write(" ORDER BY ")
		for i, o := range q.orders {
			if i > 0 {
				b.write(", ")
			}
			o.build(b)
		}
	}
	switch {
	case q.hasLimit:
		b.write(" LIMIT " + strconv.Itoa(q.limit))
	case q.offset > 0 && b.d.Name() == "mysql":
		b.write(" LIMIT 18446744073709551615")
	case q.offset > 0 && b.d.Name() == "sqlite":
		b.write(" LIMIT -1")
	}
	if q.offset > 0 {
		b.write(" OFFSET " + strconv.Itoa(q.offset))
	}
	if q.lock != noLock {
		if l := b.d.LockClause(q.lock == lockShare); l != "" {
			b.write(" " + l)
		}
	}
}

// selectSQL builds the SELECT for the model's columns, or for cols if given.
func (q *Q[T]) selectSQL(d Dialect, cols []string) *sqlBuilder {
	b := &sqlBuilder{d: d}
	b.write("SELECT ")
	if q.distinct {
		b.write("DISTINCT ")
	}
	if cols == nil {
		b.write(q.m.selectColumns(d))
	} else {
		for i, c := range cols {
			if i > 0 {
				b.write(", ")
			}
			b.write(c)
		}
	}
	q.from(b)
	q.where(b)
	q.tail(b, true)
	return b
}

// countSQL counts the rows the query would return.
func (q *Q[T]) countSQL(d Dialect) *sqlBuilder {
	if q.distinct || len(q.groups) > 0 || len(q.havings) > 0 || q.hasLimit || q.offset > 0 {
		var cols []string // the model's columns
		if len(q.groups) > 0 {
			cols = make([]string, len(q.groups)) // one row per group
			for i, g := range q.groups {
				cols[i] = quoteName(d, g)
			}
		}
		inner := q.selectSQL(d, cols)
		b := &sqlBuilder{d: d, args: inner.args, err: inner.err}
		b.write("SELECT COUNT(*) FROM (" + inner.String() + ") AS anetos_count")
		return b
	}
	b := &sqlBuilder{d: d}
	b.write("SELECT COUNT(*)")
	q.from(b)
	q.where(b)
	return b
}

// ---- execution ----

func (q *Q[T]) prepare() (*DB, conn, error) {
	if q.err != nil {
		return nil, nil, q.err
	}
	return handle(q.ctx)
}

func (q *Q[T]) rows(cols []string) (*DB, *sql.Rows, error) {
	d, c, err := q.prepare()
	if err != nil {
		return nil, nil, err
	}
	b := q.selectSQL(d.dialect, cols)
	if b.err != nil {
		return nil, nil, b.err
	}
	rows, err := d.query(q.ctx, c, b.String(), b.args)
	return d, rows, err
}

// Get returns every matching row. It returns an empty (non-nil) slice if
// there are none.
func (q *Q[T]) Get() ([]T, error) {
	if len(q.with) > 0 {
		if err := checkSpecs(q.with...); err != nil {
			return nil, err
		}
	}
	d, rows, err := q.rows(nil)
	out, err := collect[T](d, rows, err)
	if err == nil && len(q.with) > 0 && len(out) > 0 {
		err = loadRelations(q.ctx, q.m, reflect.ValueOf(out), q.with)
	}
	return out, err
}

// All streams the matching rows, for results too large to hold at once:
//
//	for post, err := range db.Query[Post](ctx).All() {
//		if err != nil {
//			return err
//		}
//		…
//	}
//
// The connection stays busy until the loop ends, so don't run other
// queries in the same transaction inside the loop.
func (q *Q[T]) All() iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		if len(q.with) > 0 {
			yield(zero, errors.New("db: All can't load relations (With); use Get or Paginate, or LoadMany on batches"))
			return
		}
		d, rows, err := q.rows(nil)
		if err != nil {
			yield(zero, err)
			return
		}
		defer rows.Close()
		s, err := newRowScanner(d, reflect.TypeFor[T](), rows)
		if err != nil {
			yield(zero, err)
			return
		}
		for rows.Next() {
			var v T
			if err := s.scan(rows, reflect.ValueOf(&v).Elem()); err != nil {
				yield(zero, err)
				return
			}
			if !yield(v, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(zero, err)
		}
	}
}

// First returns the first matching row, or [ErrNotFound]. Without
// OrderBy, which row is first is up to the database.
func (q *Q[T]) First() (T, error) {
	var zero T
	rows, err := q.Limit(1).Get()
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, ErrNotFound
	}
	return rows[0], nil
}

// Find returns the row whose primary key is id, or [ErrNotFound].
func (q *Q[T]) Find(id any) (T, error) {
	var zero T
	if q.err != nil {
		return zero, q.err
	}
	if q.m.pk < 0 {
		return zero, fmt.Errorf("db: %s has no primary key", q.m.typ)
	}
	return q.Where(cmpExpr{q.qualified(q.m.cols[q.m.pk].name), "=", id}).First()
}

// Count returns the number of matching rows.
func (q *Q[T]) Count() (int64, error) {
	d, c, err := q.prepare()
	if err != nil {
		return 0, err
	}
	b := q.countSQL(d.dialect)
	if b.err != nil {
		return 0, b.err
	}
	return scalar[int64](q.ctx, d, c, b)
}

// Exists reports whether any row matches.
func (q *Q[T]) Exists() (bool, error) {
	d, c, err := q.prepare()
	if err != nil {
		return false, err
	}
	b := q.Limit(1).selectSQL(d.dialect, []string{"1"})
	if b.err != nil {
		return false, b.err
	}
	rows, err := d.query(q.ctx, c, b.String(), b.args)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	found := rows.Next()
	return found, errors.Join(rows.Err(), rows.Close())
}

// scalar runs a query returning one value (NULL becomes the zero value).
func scalar[V any](ctx context.Context, d *DB, c conn, b *sqlBuilder) (V, error) {
	var out sql.Null[V]
	rows, err := d.query(ctx, c, b.String(), b.args)
	if err != nil {
		return out.V, err
	}
	defer rows.Close()
	if rows.Next() {
		var dest any = &out
		if vt := reflect.TypeFor[V](); (vt == timeType || vt == timePtrType) && needsTimeFix(d.dialect) {
			dest = &timeScanner{reflect.ValueOf(&out.V).Elem()}
		}
		if err := rows.Scan(dest); err != nil {
			return out.V, fmt.Errorf("db: scan: %w", err)
		}
	}
	return out.V, errors.Join(rows.Err(), rows.Close())
}

// ---- writes ----

// Update sets columns on every matching row and returns how many rows
// matched. Columns may be qualified with the model's own table. Models with [Timestamps] also get updated_at set. Model hooks
// don't run for mass updates.
//
//	n, err := db.Query[Post](ctx).Where(author.Eq(id)).Update(published.Set(false))
func (q *Q[T]) Update(assignments ...Assignment) (int64, error) {
	if len(assignments) == 0 {
		return 0, errors.New("db: Update needs at least one assignment")
	}
	if q.err != nil {
		return 0, q.err
	}
	cols := make([]string, len(assignments))
	for i, a := range assignments {
		// SET takes bare column names on PostgreSQL and SQLite.
		table, col, qualified := strings.Cut(a.col, ".")
		if qualified && table != q.m.table {
			return 0, fmt.Errorf("db: Update can only set columns of %s, not %s", q.m.table, a.col)
		}
		if !qualified {
			col = a.col
		}
		cols[i] = col
	}
	if q.m.updatedAt >= 0 {
		name := q.m.cols[q.m.updatedAt].name
		if !slices.Contains(cols, name) {
			assignments = append(slices.Clip(assignments), Assignment{name, valueExpr{now()}})
			cols = append(cols, name)
		}
	}
	return q.write(func(b *sqlBuilder) {
		b.write("UPDATE ")
		b.name(q.m.table)
		b.write(" SET ")
		for i, a := range assignments {
			if i > 0 {
				b.write(", ")
			}
			b.name(cols[i])
			b.write(" = ")
			a.expr.build(b)
		}
	})
}

// Delete deletes every matching row and returns how many. Models with
// [SoftDeletes] are soft-deleted: deleted_at is set on matching rows that
// aren't already deleted. Model hooks don't run for mass deletes.
func (q *Q[T]) Delete() (int64, error) {
	if q.m != nil && q.m.deletedAt >= 0 {
		t := now()
		as := []Assignment{{q.m.cols[q.m.deletedAt].name, valueExpr{t}}}
		if q.m.updatedAt >= 0 {
			as = append(as, Assignment{q.m.cols[q.m.updatedAt].name, valueExpr{t}})
		}
		if q.trashed == onlyTrashed {
			return 0, nil // already deleted: nothing to soft-delete
		}
		live := q.clone()
		live.trashed = withoutTrashed // keep the original deleted_at of trashed rows
		return live.Update(as...)
	}
	return q.ForceDelete()
}

// ForceDelete deletes every matching row, even for models with
// [SoftDeletes]. Soft-deleted rows only match with WithTrashed or
// OnlyTrashed.
func (q *Q[T]) ForceDelete() (int64, error) {
	return q.write(func(b *sqlBuilder) {
		b.write("DELETE")
		q.from(b)
	})
}

// Restore undeletes the matching soft-deleted rows (live rows are left
// alone, whatever WithTrashed says).
func (q *Q[T]) Restore() (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	if q.m.deletedAt < 0 {
		return 0, fmt.Errorf("db: %s doesn't use SoftDeletes", q.m.typ)
	}
	return q.OnlyTrashed().Update(Assignment{q.m.cols[q.m.deletedAt].name, valueExpr{nil}})
}

// write runs an UPDATE or DELETE whose head is written by head.
func (q *Q[T]) write(head func(b *sqlBuilder)) (int64, error) {
	if len(q.joins) > 0 || len(q.orders) > 0 || q.hasLimit || q.offset > 0 || len(q.groups) > 0 ||
		len(q.havings) > 0 || q.distinct || q.lock != noLock {
		return 0, errors.New("db: Update and Delete only support Where conditions (not Join, OrderBy, Limit, Offset, GroupBy, Having, Distinct or locks); for more, use db.Exec with your database's syntax")
	}
	d, c, err := q.prepare()
	if err != nil {
		return 0, err
	}
	b := &sqlBuilder{d: d.dialect}
	head(b)
	q.where(b)
	if b.err != nil {
		return 0, b.err
	}
	res, err := d.exec(q.ctx, c, b.String(), b.args)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// now is the time written to timestamp columns: UTC, with microsecond
// precision (what PostgreSQL and MySQL's DATETIME(6) store), so the value
// in memory matches the one read back.
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

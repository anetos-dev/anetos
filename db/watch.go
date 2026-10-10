// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Watched writes: a package such as audit watches the writes to a table
// with [DB.Watch], and is told about each one in its transaction.

// Op is the kind of a watched write.
type Op string

// The kinds of watched writes.
const (
	// OpCreate inserts rows: Create, CreateMany.
	OpCreate Op = "create"
	// OpUpdate changes rows: Update, Save, and Update on a query.
	OpUpdate Op = "update"
	// OpDelete soft-deletes rows (models with SoftDeletes): the rows stay.
	OpDelete Op = "delete"
	// OpRestore undeletes soft-deleted rows.
	OpRestore Op = "restore"
	// OpForceDelete removes rows from the database: ForceDelete, and
	// Delete on a model without SoftDeletes.
	OpForceDelete Op = "force_delete"
	// OpUpsert inserts rows or updates those that conflict (Upsert).
	OpUpsert Op = "upsert"
)

// Values are a row's column values, by column name: nil, the field's
// value (ints, floats, strings, bools, []byte, the value of a
// driver.Valuer), a time.Time (UTC, to the microsecond), or a
// json.RawMessage for JSON columns.
type Values map[string]any

// RowValues are the values of the row with Key.
type RowValues struct {
	// Key is the row's primary key.
	Key any
	// Values are the row's column values.
	Values Values
}

// SetExpr is an assignment of a bulk update that isn't a plain value
// ([Column.SetRaw]), as SQL with its arguments.
type SetExpr struct {
	// SQL is the expression, in the database's SQL.
	SQL string `json:"sql"`
	// Args are its arguments.
	Args []any `json:"args,omitempty"`
}

// Bulk describes a write to many rows at once: a query's Update, Delete,
// ForceDelete or Restore, CreateMany or Upsert.
type Bulk struct {
	// Where is the condition the rows were chosen by, in the database's
	// SQL ("" for CreateMany and Upsert), and Args its arguments.
	Where string
	// Args are the arguments of Where.
	Args []any
	// Set are a bulk update's assignments by column: values, or SetExpr
	// for expressions. updated_at is left out.
	Set map[string]any
	// Keys are the primary keys of every row written.
	Keys []any
	// Before are rows' values before the write, for as many rows as the
	// watchers asked for ([DB.Watch]): the assigned columns of an update,
	// every column of a force delete, the updated columns of rows an
	// upsert updated. Empty for creates, deletes and restores.
	Before []RowValues
	// After are rows' values after an upsert or a create, within the same
	// limit.
	After []RowValues
	// Complete reports whether Before and After hold every row's values.
	Complete bool
}

// Write describes a write to a watched table. Single-row writes set Key
// and the values; bulk writes set Bulk.
type Write struct {
	// Op is the kind of write.
	Op Op
	// Table is the table written.
	Table string
	// Key is the primary key of the row (single-row writes).
	Key any
	// Before are the row's values before an update or a force delete,
	// read in the write's transaction, so they are the database's.
	Before Values
	// After are the row's values after a create (without readonly
	// columns, which the database sets) or an update (the values before,
	// with the columns the update wrote).
	After Values
	// Bulk describes a bulk write; nil for single-row writes.
	Bulk *Bulk
}

// A Watcher is told about the writes to the tables it watches
// ([DB.Watch]).
type Watcher interface {
	// Written runs after the write, in its transaction, with its context:
	// queries made with ctx are part of the transaction. An error rolls
	// the write back and is returned by the call that made it.
	Written(ctx context.Context, w *Write) error
}

type watch struct {
	w          Watcher
	bulkValues int
}

// WatchOption configures a watcher ([DB.Watch]).
type WatchOption func(*watchOptions)

type watchOptions struct{ bulkValues int }

// WatchBulkValues has a bulk write (a query's Update, Delete…, an
// upsert, CreateMany) give the watcher the values of up to n of the rows
// it writes ([Bulk].Before and After); default 0, the keys only.
func WatchBulkValues(n int) WatchOption {
	return func(o *watchOptions) { o.bulkValues = max(n, 0) }
}

// Watch makes w watch the writes the db package makes to table: Create,
// CreateMany, Upsert, Update, Save, Delete, ForceDelete, Restore, and a
// query's Update, Delete, ForceDelete and Restore. For a watched table,
// each of these runs in a transaction (a savepoint inside one already
// open, so model hooks run inside it too) and calls w after writing:
//
//   - an update first reads the row's values FOR UPDATE, so the watcher
//     gets the values before and after;
//   - a force delete (or a delete without SoftDeletes) reads the values
//     the row had;
//   - a bulk write first selects the matching rows' keys (and the values
//     of up to [WatchBulkValues] rows) FOR UPDATE, then writes in chunks of
//     1,000 keys, with the condition and the keys, and fails if a chunk
//     changes fewer rows than it selected, so what the watcher is told is
//     exactly what was written;
//   - CreateMany on a database that can't report a multi-row insert's
//     keys (MySQL) inserts the rows one by one.
//
// A watched table's model needs a primary key. Raw SQL ([Exec]), pivot
// writes ([Attach], [Sync]…) and the database's own cascades aren't seen.
// Call Watch before the app writes, typically at setup.
func (d *DB) Watch(table string, w Watcher, opts ...WatchOption) error {
	if table == "" || w == nil {
		return errors.New("db: Watch needs a table and a watcher")
	}
	var o watchOptions
	for _, opt := range opts {
		opt(&o)
	}
	d.watchMu.Lock()
	defer d.watchMu.Unlock()
	if d.watches == nil {
		d.watches = map[string][]watch{}
	}
	d.watches[table] = append(d.watches[table], watch{w, o.bulkValues})
	d.watching.Store(true)
	return nil
}

// Watchers returns the watchers of table ([DB.Watch]), in the order they
// were added.
func (d *DB) Watchers(table string) []Watcher {
	ws := d.watchersOf(table)
	out := make([]Watcher, len(ws))
	for i, x := range ws {
		out[i] = x.w
	}
	return out
}

// watchersOf returns the watchers of table, nil if none.
func (d *DB) watchersOf(table string) []watch {
	if !d.watching.Load() {
		return nil
	}
	d.watchMu.RLock()
	defer d.watchMu.RUnlock()
	return d.watches[table]
}

// watched runs fn: directly if m's table isn't watched, otherwise in a
// transaction, with the watchers.
func (d *DB) watched(ctx context.Context, m *meta, fn func(ctx context.Context, ws []watch) error) error {
	ws := d.watchersOf(m.table)
	if ws == nil {
		return fn(ctx, nil)
	}
	if m.pk < 0 {
		return fmt.Errorf("db: %s is watched, so its model %s needs a primary key", m.table, m.typ)
	}
	return Tx(ctx, func(ctx context.Context) error { return fn(ctx, ws) })
}

func notify(ctx context.Context, ws []watch, w *Write) error {
	for _, x := range ws {
		if err := x.w.Written(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

// bulkValuesOf is the most values any of ws asks for.
func bulkValuesOf(ws []watch) int {
	n := 0
	for _, x := range ws {
		n = max(n, x.bulkValues)
	}
	return n
}

// valuesOf returns the values of v's columns (all of them if cols is nil).
func valuesOf(v reflect.Value, m *meta, cols []int) (Values, error) {
	if cols == nil {
		cols = make([]int, len(m.cols))
		for i := range cols {
			cols[i] = i
		}
	}
	out := make(Values, len(cols))
	for _, ci := range cols {
		c := m.cols[ci]
		a, err := fieldArg(v, c)
		if err != nil {
			return nil, err
		}
		out[c.name] = plainValue(a, c.json)
	}
	return out, nil
}

// stored returns the columns a create writes: all but readonly ones.
func (m *meta) stored() []int {
	out := make([]int, 0, len(m.cols))
	for i, c := range m.cols {
		if !c.readonly {
			out = append(out, i)
		}
	}
	return out
}

// afterUpdate returns a row's values after a model Update: the values
// before, with the columns the update wrote (not readonly ones, nor
// deleted_at) from v.
func afterUpdate(v reflect.Value, m *meta, before Values) (Values, error) {
	written, err := valuesOf(v, m, m.updatable())
	if err != nil {
		return nil, err
	}
	after := make(Values, len(before))
	maps.Copy(after, before)
	maps.Copy(after, written)
	return after, nil
}

// rowKey returns v's primary key, as plainValue does.
func rowKey(v reflect.Value, m *meta) (any, error) {
	a, err := fieldArg(v, m.cols[m.pk])
	if err != nil {
		return nil, err
	}
	return plainValue(a, false), nil
}

// plainValue turns a field's value into one a watcher can compare and
// encode: pointers dereferenced, driver.Valuers asked for their value,
// times in UTC to the microsecond, JSON columns as json.RawMessage.
func plainValue(a any, isJSON bool) any {
	if isJSON {
		switch s := a.(type) {
		case string:
			return json.RawMessage(s)
		case nil:
			return nil
		}
	}
	for range 4 { // a Valuer returning a pointer to a Valuer… is not worth more
		if a == nil {
			return nil
		}
		rv := reflect.ValueOf(a)
		if rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return nil
			}
		}
		if vr, ok := a.(driver.Valuer); ok {
			v, err := vr.Value()
			if err != nil {
				return fmt.Sprintf("(error: %v)", err)
			}
			a = v
			continue
		}
		if rv.Kind() == reflect.Pointer {
			a = rv.Elem().Interface()
			continue
		}
		break
	}
	if t, ok := a.(time.Time); ok {
		return t.UTC().Truncate(time.Microsecond)
	}
	return a
}

// KeyOf returns the table and primary key of row, a pointer to a model
// (or a model), as the db package writes them; for audit entries and
// links to a row.
func KeyOf(row any) (table string, key any, err error) {
	v := reflect.ValueOf(row)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", nil, errors.New("db: KeyOf: nil row")
		}
		v = v.Elem()
	}
	m, err := metaOf(v.Type())
	if err != nil {
		return "", nil, err
	}
	if m.pk < 0 {
		return "", nil, fmt.Errorf("db: %s has no primary key", m.typ)
	}
	k, err := rowKey(v, m)
	return m.table, k, err
}

// readRow reads the row of model T with v's key FOR UPDATE, including a
// soft-deleted one, and returns its values; ErrNotFound if there is none.
func readRow[T any](ctx context.Context, v reflect.Value, m *meta) (Values, error) {
	k, err := rowKey(v, m)
	if err != nil {
		return nil, err
	}
	old, err := Query[T](ctx).WithTrashed().ForUpdate().Find(k)
	if err != nil {
		return nil, err
	}
	return valuesOf(reflect.ValueOf(&old).Elem(), m, nil)
}

// chunkKeys is how many keys a chunked bulk write puts in one statement.
const chunkKeys = 1000

// watchedWrite is a bulk write on a watched table: select the matching
// rows' keys (and values) FOR UPDATE, write in chunks of keys with the
// condition, and tell the watchers. cols are the columns whose values
// before are wanted (nil: none; all: every column).
func (q *Q[T]) watchedWrite(ctx context.Context, ws []watch, op Op, cols []int, set map[string]any, head func(b *sqlBuilder)) (int64, error) {
	d, _, err := handle(ctx)
	if err != nil {
		return 0, err
	}
	qq := q.WithContext(ctx)
	limit := bulkValuesOf(ws)

	// The condition, for the record.
	wb := &sqlBuilder{d: d.dialect}
	qq.where(wb)
	if wb.err != nil {
		return 0, wb.err
	}
	where := strings.TrimPrefix(wb.String(), " WHERE ")
	args := make([]any, len(wb.args))
	for i, a := range wb.args {
		args[i] = plainValue(a, false)
	}

	// The matching rows, locked.
	names := []string{quoteName(d.dialect, q.m.table+"."+q.m.cols[q.m.pk].name)}
	for _, ci := range cols {
		if ci != q.m.pk {
			names = append(names, quoteName(d.dialect, q.m.table+"."+q.m.cols[ci].name))
		}
	}
	// In key order, so concurrent bulk writes lock rows in the same order.
	rd, rows, err := qq.ForUpdate().OrderBy(C(q.m.table + "." + q.m.cols[q.m.pk].name).Asc()).rows(names)
	if err != nil {
		return 0, err
	}
	var keys []any
	var before []RowValues
	err = func() error {
		defer rows.Close()
		s, err := newRowScanner(rd, q.m.typ, rows)
		if err != nil {
			return err
		}
		for rows.Next() {
			row := reflect.New(q.m.typ).Elem()
			if err := s.scan(rows, row); err != nil {
				return err
			}
			k, err := rowKey(row, q.m)
			if err != nil {
				return err
			}
			keys = append(keys, k)
			if len(cols) > 0 && len(before) < limit {
				vals, err := valuesOf(row, q.m, cols)
				if err != nil {
					return err
				}
				before = append(before, RowValues{k, vals})
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}

	// The write, chunk by chunk.
	var n int64
	for chunk := range slices.Chunk(keys, chunkKeys) {
		got, err := qq.WhereKeys(chunk...).write(head)
		if err != nil {
			return n, err
		}
		if got != int64(len(chunk)) {
			return n, fmt.Errorf("db: %s: a watched bulk write changed %d of the %d rows it selected; nothing was written", q.m.table, got, len(chunk))
		}
		n += got
	}
	bulk := &Bulk{Where: where, Args: args, Set: set, Keys: keys, Before: before}
	bulk.Complete = len(cols) == 0 || len(before) == len(keys)
	return n, notify(ctx, ws, &Write{Op: op, Table: q.m.table, Bulk: bulk})
}

// setOf returns a bulk update's assignments for the record, without
// updated_at.
func (q *Q[T]) setOf(d Dialect, assignments []Assignment, cols []string) map[string]any {
	set := make(map[string]any, len(assignments))
	for i, a := range assignments {
		if q.m.updatedAt >= 0 && cols[i] == q.m.cols[q.m.updatedAt].name {
			continue
		}
		if v, ok := a.expr.(valueExpr); ok {
			set[cols[i]] = plainValue(v.v, false)
			continue
		}
		b := &sqlBuilder{d: d}
		a.expr.build(b)
		args := make([]any, len(b.args))
		for j, x := range b.args {
			args[j] = plainValue(x, false)
		}
		set[cols[i]] = SetExpr{SQL: b.String(), Args: args}
	}
	return set
}

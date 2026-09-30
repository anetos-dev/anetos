// SPDX-License-Identifier: Apache-2.0

package db

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// sqlBuilder accumulates SQL text and arguments with the dialect's
// placeholders.
type sqlBuilder struct {
	d    Dialect
	sb   strings.Builder
	args []any
	err  error
}

func (b *sqlBuilder) write(s string) { b.sb.WriteString(s) }

func (b *sqlBuilder) name(n string) { b.sb.WriteString(quoteName(b.d, n)) }

func (b *sqlBuilder) arg(v any) {
	if j, ok := v.(jsonValue); ok {
		text, err := json.Marshal(j.v)
		if err != nil {
			b.fail(fmt.Errorf("db: encode JSON argument: %w", err))
		}
		v = string(text)
	}
	b.args = append(b.args, v)
	b.sb.WriteString(b.d.Placeholder(len(b.args)))
}

// raw appends SQL written with ? placeholders.
func (b *sqlBuilder) raw(s string, args []any) {
	q, a, n, err := rebindCount(offsetDialect{b.d, len(b.args)}, s, args)
	if err != nil {
		b.fail(err)
		return
	}
	if n == 0 && len(args) > 0 {
		// Native placeholders ($1) can't work in a fragment: its numbering
		// depends on the rest of the query.
		b.fail(fmt.Errorf("db: SQL fragment %q has arguments but no ? placeholders", s))
		return
	}
	b.sb.WriteString(q)
	b.args = append(b.args, a...)
}

func (b *sqlBuilder) fail(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *sqlBuilder) String() string { return b.sb.String() }

// offsetDialect numbers placeholders after the arguments already added.
type offsetDialect struct {
	Dialect
	offset int
}

func (o offsetDialect) Placeholder(n int) string { return o.Dialect.Placeholder(n + o.offset) }

// Expr is a condition or value expression, built with [Column] methods,
// [And], [Or], [Not] and [SQL].
type Expr interface {
	build(b *sqlBuilder)
}

// Column is a typed reference to a column, used to build conditions,
// orderings and assignments that the compiler checks:
//
//	posts, err := db.Query[Post](ctx).
//		Where(models.PostCols.Title.Like("%go%")).
//		OrderBy(models.PostCols.Title.Asc()).
//		Get()
//
// `anetos gen` declares the columns of every model (see the model code
// generation guide). Declare others with [Col] or [JSONCol], or use [C]
// for an untyped column. T is the type of the model field, so a nullable
// column is a Column[*time.Time] and compares with new(t).
type Column[T any] struct {
	name string
	json bool
}

// Col returns a column reference. name may be qualified: "posts.title".
func Col[T any](name string) Column[T] { return Column[T]{name: name} }

// JSONCol returns a reference to a column stored as JSON (a `db:",json"`
// field): values given to its methods are encoded as JSON, and [Pluck]
// decodes them.
func JSONCol[T any](name string) Column[T] { return Column[T]{name: name, json: true} }

// C returns an untyped column reference, for quick queries:
//
//	db.Query[Post](ctx).Where(db.C("author_id").Eq(id))
func C(name string) Column[any] { return Column[any]{name: name} }

// Name returns the column name.
func (c Column[T]) Name() string { return c.name }

// Of returns the column qualified with table (replacing any qualifier;
// "" removes it), for queries with joins:
//
//	db.Query[Post](ctx).Join("JOIN authors ON authors.id = posts.author_id").
//		Where(models.PostCols.ID.Of("posts").Gt(100))
func (c Column[T]) Of(table string) Column[T] {
	name := c.name
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	c.name = name
	if table != "" {
		c.name = table + "." + name
	}
	return c
}

// val returns the query argument for v: JSON columns encode it, and
// keep nil as NULL.
func (c Column[T]) val(v T) any {
	if !c.json {
		return v
	}
	if isNil(v) {
		return nil
	}
	return jsonValue{v}
}

// Eq is column = v.
func (c Column[T]) Eq(v T) Expr { return cmpExpr{c.name, "=", c.val(v)} }

// Ne is column <> v.
func (c Column[T]) Ne(v T) Expr { return cmpExpr{c.name, "<>", c.val(v)} }

// Gt is column > v.
func (c Column[T]) Gt(v T) Expr { return cmpExpr{c.name, ">", c.val(v)} }

// Gte is column >= v.
func (c Column[T]) Gte(v T) Expr { return cmpExpr{c.name, ">=", c.val(v)} }

// Lt is column < v.
func (c Column[T]) Lt(v T) Expr { return cmpExpr{c.name, "<", c.val(v)} }

// Lte is column <= v.
func (c Column[T]) Lte(v T) Expr { return cmpExpr{c.name, "<=", c.val(v)} }

// Like is column LIKE pattern. Case sensitivity follows the database
// (PostgreSQL is case-sensitive; MySQL and SQLite usually aren't for ASCII).
func (c Column[T]) Like(pattern string) Expr { return cmpExpr{c.name, "LIKE", pattern} }

// NotLike is column NOT LIKE pattern.
func (c Column[T]) NotLike(pattern string) Expr { return cmpExpr{c.name, "NOT LIKE", pattern} }

// In is column IN (vs…). An empty list matches nothing.
func (c Column[T]) In(vs ...T) Expr { return inExpr{c.name, c.vals(vs), false} }

// NotIn is column NOT IN (vs…). An empty list matches everything.
func (c Column[T]) NotIn(vs ...T) Expr { return inExpr{c.name, c.vals(vs), true} }

// Between is column BETWEEN lo AND hi (inclusive).
func (c Column[T]) Between(lo, hi T) Expr { return betweenExpr{c.name, c.val(lo), c.val(hi)} }

// IsNull is column IS NULL.
func (c Column[T]) IsNull() Expr { return nullExpr{c.name, false} }

// NotNull is column IS NOT NULL.
func (c Column[T]) NotNull() Expr { return nullExpr{c.name, true} }

// Asc orders by the column, ascending.
func (c Column[T]) Asc() Order { return Order{col: c.name} }

// Desc orders by the column, descending.
func (c Column[T]) Desc() Order { return Order{col: c.name, desc: true} }

// Set assigns v to the column in [Q.Update]. A nil pointer, map or slice
// sets NULL.
func (c Column[T]) Set(v T) Assignment { return Assignment{col: c.name, expr: valueExpr{c.val(v)}} }

// SetRaw assigns a SQL expression (with ? placeholders) to the column:
//
//	q.Update(db.C("views").SetRaw("views + ?", 1))
func (c Column[T]) SetRaw(sql string, args ...any) Assignment {
	return Assignment{col: c.name, expr: SQL(sql, args...)}
}

func (c Column[T]) vals(vs []T) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = c.val(v)
	}
	return out
}

// jsonValue is an argument encoded as JSON text when the query is built.
type jsonValue struct{ v any }

type cmpExpr struct {
	col string
	op  string
	v   any
}

func (e cmpExpr) build(b *sqlBuilder) {
	if isNil(e.v) && (e.op == "=" || e.op == "<>") {
		// = NULL is never true; Eq(nil) means IS NULL.
		nullExpr{e.col, e.op == "<>"}.build(b)
		return
	}
	b.name(e.col)
	b.write(" " + e.op + " ")
	b.arg(e.v)
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

type inExpr struct {
	col string
	vs  []any
	not bool
}

func (e inExpr) build(b *sqlBuilder) {
	if len(e.vs) == 0 {
		if e.not {
			b.write("1 = 1")
		} else {
			b.write("1 = 0")
		}
		return
	}
	b.name(e.col)
	if e.not {
		b.write(" NOT")
	}
	b.write(" IN (")
	for i, v := range e.vs {
		if i > 0 {
			b.write(", ")
		}
		b.arg(v)
	}
	b.write(")")
}

type betweenExpr struct {
	col    string
	lo, hi any
}

func (e betweenExpr) build(b *sqlBuilder) {
	b.name(e.col)
	b.write(" BETWEEN ")
	b.arg(e.lo)
	b.write(" AND ")
	b.arg(e.hi)
}

type nullExpr struct {
	col string
	not bool
}

func (e nullExpr) build(b *sqlBuilder) {
	b.name(e.col)
	if e.not {
		b.write(" IS NOT NULL")
	} else {
		b.write(" IS NULL")
	}
}

type valueExpr struct{ v any }

func (e valueExpr) build(b *sqlBuilder) { b.arg(e.v) }

type rawExpr struct {
	sql  string
	args []any
}

func (e rawExpr) build(b *sqlBuilder) { b.raw(e.sql, e.args) }

// SQL is a condition or value written in SQL, with ? placeholders (?? for
// a literal ?), or :name placeholders with a single [Named] argument:
//
//	q.Where(db.SQL("lower(email) = ?", email))
func SQL(sql string, args ...any) Expr { return rawExpr{sql, args} }

type logicExpr struct {
	op    string
	parts []Expr
}

func (e logicExpr) build(b *sqlBuilder) {
	parts := make([]Expr, 0, len(e.parts))
	for _, p := range e.parts {
		if p != nil {
			parts = append(parts, p)
		}
	}
	switch len(parts) {
	case 0:
		if e.op == "AND" {
			b.write("1 = 1")
		} else {
			b.write("1 = 0")
		}
		return
	case 1:
		parts[0].build(b)
		return
	}
	b.write("(")
	for i, p := range parts {
		if i > 0 {
			b.write(" " + e.op + " ")
		}
		b.write("(")
		p.build(b)
		b.write(")")
	}
	b.write(")")
}

// And is true when every condition is. And() with no conditions is true.
func And(conds ...Expr) Expr { return logicExpr{"AND", conds} }

// Or is true when any condition is. Or() with no conditions is false.
func Or(conds ...Expr) Expr { return logicExpr{"OR", conds} }

type notExpr struct{ e Expr }

func (n notExpr) build(b *sqlBuilder) {
	b.write("NOT (")
	n.e.build(b)
	b.write(")")
}

// Not negates a condition.
func Not(cond Expr) Expr { return notExpr{cond} }

// Order is an ORDER BY term, from [Column.Asc], [Column.Desc] or
// [OrderRaw].
type Order struct {
	col  string
	desc bool
	raw  Expr
}

// OrderRaw is an ORDER BY term written in SQL, e.g. "lower(title) ASC".
func OrderRaw(sql string, args ...any) Order { return Order{raw: SQL(sql, args...)} }

func (o Order) build(b *sqlBuilder) {
	if o.raw != nil {
		o.raw.build(b)
		return
	}
	b.name(o.col)
	if o.desc {
		b.write(" DESC")
	} else {
		b.write(" ASC")
	}
}

// Assignment sets a column in [Q.Update], from [Column.Set] or
// [Column.SetRaw].
type Assignment struct {
	col  string
	expr Expr
}

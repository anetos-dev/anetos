// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"anetos.dev/anetos/internal/dbutil"
)

// Relations are declared with a rel tag on a field of the model: a
// pointer for belongs_to and has_one, a slice for has_many and
// many_to_many.
//
//	type Post struct {
//		db.Model
//		AuthorID int64
//		Author   *User     `rel:"belongs_to"`   // posts.author_id → users.id
//		Comments []Comment `rel:"has_many"`     // comments.post_id → posts.id
//		Tags     []Tag     `rel:"many_to_many"` // post_tag (post_id, tag_id)
//	}
//
// Options follow the kind, comma-separated (rel:"has_many,fk=article_id"):
//
//   - belongs_to: fk, the column of this model holding the key (default:
//     the field's name in snake_case plus _id: author_id); references, the
//     related model's column it refers to (default: its primary key).
//   - has_one, has_many: fk, the related model's column pointing back
//     (default: this model's type name in snake_case plus _id: post_id);
//     local, this model's column it refers to (default: the primary key).
//   - many_to_many: pivot, the pivot table (default: both type names in
//     snake_case, sorted and joined with _: post_tag); fk, the pivot
//     column pointing to this model (default post_id); related_fk, the one
//     pointing to the related model (default tag_id). Both refer to the
//     primary keys.
//
// Relations are never loaded behind your back: load them with [Q.With],
// [Load] or [LoadMany], and filter by them with [Q.WhereHas].

type relKind int

const (
	belongsTo relKind = iota + 1
	hasOne
	hasMany
	manyToMany
)

var relKinds = map[string]relKind{"belongs_to": belongsTo, "has_one": hasOne, "has_many": hasMany, "many_to_many": manyToMany}

func (k relKind) String() string {
	for name, v := range relKinds {
		if v == k {
			return name
		}
	}
	return "relation"
}

// relMeta is one relation field. Its keys are resolved on first use, since
// the related model's metadata may need this model's (Post ↔ Comment).
type relMeta struct {
	field   string
	index   []int
	kind    relKind
	related reflect.Type // the related model (element or pointed-to type)
	opts    map[string]string

	once sync.Once
	err  error
	// Resolved:
	owner      *meta
	rm         *meta
	parentKey  int // column of owner: belongs_to fk, has_* local key, m2m pk
	relatedKey int // column of rm: belongs_to references, has_* fk, m2m pk
	pivot      string
	pivotFK    string // pivot column → owner
	pivotRelFK string // pivot column → related
}

var relOpts = map[relKind][]string{
	belongsTo:  {"fk", "references"},
	hasOne:     {"fk", "local"},
	hasMany:    {"fk", "local"},
	manyToMany: {"pivot", "fk", "related_fk"},
}

// parseRel reads a rel tag.
func parseRel(owner reflect.Type, sf reflect.StructField, index []int, tag string) (*relMeta, error) {
	kindName, rest, _ := strings.Cut(tag, ",")
	kind, ok := relKinds[strings.TrimSpace(kindName)]
	if !ok {
		return nil, fmt.Errorf("db: %s.%s: unknown relation %q (known: belongs_to, has_one, has_many, many_to_many)", owner, sf.Name, kindName)
	}
	r := &relMeta{field: sf.Name, index: index, kind: kind, opts: map[string]string{}}
	for o := range strings.SplitSeq(rest, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		k, v, _ := strings.Cut(o, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !slices.Contains(relOpts[kind], k) || v == "" {
			return nil, fmt.Errorf("db: %s.%s: bad %s option %q (known: %s, as name=value)", owner, sf.Name, kind, o, strings.Join(relOpts[kind], ", "))
		}
		r.opts[k] = v
	}
	t := sf.Type
	switch kind {
	case belongsTo, hasOne:
		if t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
			return nil, fmt.Errorf("db: %s.%s: a %s relation is a pointer to a model (*%s)", owner, sf.Name, kind, t)
		}
	default:
		if t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Struct {
			return nil, fmt.Errorf("db: %s.%s: a %s relation is a slice of models ([]%s)", owner, sf.Name, kind, t)
		}
	}
	r.related = t.Elem()
	return r, nil
}

// resolve finds the key columns, once.
func (r *relMeta) resolve(owner *meta) error {
	r.once.Do(func() { r.err = r.doResolve(owner) })
	return r.err
}

func (r *relMeta) doResolve(owner *meta) error {
	rm, err := metaOf(r.related)
	if err != nil {
		return err
	}
	r.owner, r.rm = owner, rm
	where := fmt.Sprintf("db: %s.%s (%s)", owner.typ, r.field, r.kind)
	col := func(m *meta, name, what string) (int, error) {
		if name == "" {
			if m.pk < 0 {
				return 0, fmt.Errorf("%s: %s has no primary key; name the %s column", where, m.typ, what)
			}
			return m.pk, nil
		}
		i, ok := m.byName[name]
		if !ok {
			return 0, fmt.Errorf("%s: %s has no column %q (%s); set it with the rel tag", where, m.typ, name, what)
		}
		return i, nil
	}
	switch r.kind {
	case belongsTo:
		fk := r.opts["fk"]
		if fk == "" {
			fk = snake(r.field) + "_id"
		}
		if r.parentKey, err = col(owner, fk, "fk"); err != nil {
			return err
		}
		r.relatedKey, err = col(rm, r.opts["references"], "references")
		return err
	case hasOne, hasMany:
		fk := r.opts["fk"]
		if fk == "" {
			fk = snake(owner.typ.Name()) + "_id"
		}
		if r.relatedKey, err = col(rm, fk, "fk"); err != nil {
			return err
		}
		r.parentKey, err = col(owner, r.opts["local"], "local")
		return err
	default: // many_to_many
		if r.parentKey, err = col(owner, "", "pk"); err != nil {
			return err
		}
		if r.relatedKey, err = col(rm, "", "pk"); err != nil {
			return err
		}
		a, b := snake(owner.typ.Name()), snake(rm.typ.Name())
		r.pivot = r.opts["pivot"]
		if r.pivot == "" {
			names := []string{a, b}
			sort.Strings(names)
			r.pivot = names[0] + "_" + names[1]
		}
		if r.pivotFK = r.opts["fk"]; r.pivotFK == "" {
			r.pivotFK = a + "_id"
		}
		if r.pivotRelFK = r.opts["related_fk"]; r.pivotRelFK == "" {
			r.pivotRelFK = b + "_id"
		}
		if r.pivotFK == r.pivotRelFK {
			return fmt.Errorf("%s: both pivot columns are %q; name them with fk= and related_fk=", where, r.pivotFK)
		}
		return nil
	}
}

// ---- handles ----

// Relation is a relation of model T, as [Q.With], [Load], [LoadMany] and
// [Q.WhereHas] take it. Its only implementation is [Rel]; `anetos generate`
// writes one per relation field (PostRels.Comments).
type Relation[T any] interface {
	spec() relSpec
}

// relSpec is what to load: a relation, the conditions and order of its
// query, and the relations to load on its rows.
type relSpec struct {
	owner   reflect.Type
	field   string
	err     error
	wheres  []Expr
	orders  []Order
	trashed trashed
	nested  []relSpec
}

// Rel is the relation field of model T that holds R values, as a value to
// pass to [Q.With], [Load], [Q.WhereHas], [Attach] and friends. Its
// methods return a new Rel.
type Rel[T, R any] struct {
	s relSpec
}

// RelOf returns the relation of model T declared on field, a pointer to R
// or a slice of R with a rel tag. `anetos generate` writes these; declared by
// hand, a wrong field or type is reported by [Rel.Err] and by the queries
// that use it. Nothing is checked before first use, so RelOf is safe in
// package-level variables.
//
//	var PostComments = db.RelOf[Post, Comment]("Comments")
func RelOf[T, R any](field string) Rel[T, R] {
	return Rel[T, R]{relSpec{owner: reflect.TypeFor[T](), field: field}}
}

func (r Rel[T, R]) spec() relSpec {
	s := r.s
	if s.err == nil {
		s.err = checkRel(s.owner, s.field, reflect.TypeFor[R]())
	}
	return s
}

// checkRel reports whether owner has a relation field holding related.
func checkRel(owner reflect.Type, field string, related reflect.Type) error {
	m, err := metaOf(owner)
	switch {
	case err != nil:
		return err
	case m.rels[field] == nil:
		return fmt.Errorf("db: %s has no relation field %s (add a rel tag)", owner, field)
	case m.rels[field].related != related:
		return fmt.Errorf("db: %s.%s holds %s, not %s", owner, field, m.rels[field].related, related)
	}
	return nil
}

// With also loads relations of the related rows:
//
//	db.Query[Post](ctx).With(PostRels.Comments.With(CommentRels.Author))
func (r Rel[T, R]) With(nested ...Relation[R]) Rel[T, R] {
	for _, n := range nested {
		r.s.nested = append(slices.Clip(r.s.nested), n.spec())
	}
	return r
}

// Where limits the related rows loaded (or required by [Q.WhereHas]):
//
//	PostRels.Comments.Where(CommentCols.Approved.Eq(true))
func (r Rel[T, R]) Where(conds ...Expr) Rel[T, R] {
	r.s.wheres = append(slices.Clip(r.s.wheres), conds...)
	return r
}

// OrderBy orders the related rows of a has_many or many_to_many relation,
// and decides which row a has_one relation gets when several match (the
// first). Without it, primary key order.
func (r Rel[T, R]) OrderBy(orders ...Order) Rel[T, R] {
	r.s.orders = append(slices.Clip(r.s.orders), orders...)
	return r
}

// WithTrashed includes soft-deleted related rows, which are left out by
// default.
func (r Rel[T, R]) WithTrashed() Rel[T, R] {
	r.s.trashed = withTrashed
	return r
}

// Name returns the relation's field name.
func (r Rel[T, R]) Name() string { return r.s.field }

// Err reports why the relation can't be used, as a query using it would:
// no such relation field, one that doesn't hold R, a bad rel tag, or key
// columns that don't exist. Tests can check hand-written handles with it.
func (r Rel[T, R]) Err() error { return checkSpecs(r.spec()) }

// checkSpecs resolves the keys of specs and of their nested relations, so
// a query fails before it runs rather than after.
func checkSpecs(specs ...relSpec) error {
	for _, s := range specs {
		if s.err != nil {
			return s.err
		}
		m, err := metaOf(s.owner)
		if err != nil {
			return err
		}
		r := m.rels[s.field]
		if r == nil {
			return fmt.Errorf("db: %s has no relation field %s", m.typ, s.field)
		}
		if err := r.resolve(m); err != nil {
			return err
		}
		if err := checkSpecs(s.nested...); err != nil {
			return err
		}
	}
	return nil
}

// ---- loading ----

// With loads relations of the rows the query returns, with one query per
// relation (and level of nesting) whatever the number of rows, instead of
// one per row:
//
//	posts, err := db.Query[Post](ctx).
//		With(PostRels.Author, PostRels.Comments.With(CommentRels.Author)).
//		Get()
//
// Get, First, Find, Paginate and CursorPaginate load them; All returns an
// error, since streaming rows one at a time can't batch their relations.
func (q *Q[T]) With(rels ...Relation[T]) *Q[T] {
	c := q.clone()
	for _, r := range rels {
		c.with = append(c.with, r.spec())
	}
	return c
}

// Load loads relations of one row:
//
//	err := db.Load(ctx, &post, PostRels.Comments)
func Load[T any](ctx context.Context, row *T, rels ...Relation[T]) error {
	rows := []T{*row}
	if err := LoadMany(ctx, rows, rels...); err != nil {
		return err
	}
	*row = rows[0]
	return nil
}

// LoadMany loads relations of rows, in place, with one query per relation:
//
//	err := db.LoadMany(ctx, posts, PostRels.Author)
func LoadMany[T any](ctx context.Context, rows []T, rels ...Relation[T]) error {
	if len(rows) == 0 || len(rels) == 0 {
		return nil
	}
	m, err := metaOf(reflect.TypeFor[T]())
	if err != nil {
		return err
	}
	specs := make([]relSpec, len(rels))
	for i, r := range rels {
		specs[i] = r.spec()
	}
	if err := checkSpecs(specs...); err != nil {
		return err
	}
	return loadRelations(ctx, m, reflect.ValueOf(rows), specs)
}

// loadChunk is how many keys go into one IN (…) list: well below every
// database's limit on parameters.
const loadChunk = 1000

// loadRelations loads specs into rows, a slice of structs of model m.
func loadRelations(ctx context.Context, m *meta, rows reflect.Value, specs []relSpec) error {
	seen := map[string]bool{}
	for _, s := range specs {
		if seen[s.field] {
			return fmt.Errorf("db: relation %s.%s is loaded twice; pass it once, with all its nested relations", m.typ, s.field)
		}
		seen[s.field] = true
	}
	for _, s := range specs {
		if err := loadRelation(ctx, m, rows, s); err != nil {
			return err
		}
	}
	return nil
}

func loadRelation(ctx context.Context, m *meta, rows reflect.Value, s relSpec) error {
	if s.err != nil {
		return s.err
	}
	if s.owner != m.typ {
		return fmt.Errorf("db: relation %s.%s used on %s", s.owner, s.field, m.typ)
	}
	r := m.rels[s.field]
	if r == nil {
		return fmt.Errorf("db: %s has no relation field %s", m.typ, s.field)
	}
	if err := r.resolve(m); err != nil {
		return err
	}

	// The parents' keys.
	var keys []any
	seen := map[any]bool{}
	for i := range rows.Len() {
		if k, ok := keyOf(fieldOf(rows.Index(i), m.cols[r.parentKey].index)); ok && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}

	// The related rows, and for many_to_many the parent of each.
	var related reflect.Value // []R
	var parents []any         // many_to_many: parents[i] is related[i]'s parent key
	var err error
	if r.kind == manyToMany {
		related, parents, err = loadPivoted(ctx, r, s, keys)
	} else {
		related, err = loadWhereIn(ctx, r.rm, r.relatedKey, s, keys)
	}
	if err != nil {
		return err
	}
	if len(s.nested) > 0 && related.Len() > 0 {
		if err := loadRelations(ctx, r.rm, related, s.nested); err != nil {
			return err
		}
	}

	// Hand them out.
	switch r.kind {
	case belongsTo, hasOne:
		byKey := map[any]reflect.Value{}
		for i := range related.Len() {
			row := related.Index(i)
			if k, ok := keyOf(fieldOf(row, r.rm.cols[r.relatedKey].index)); ok {
				if _, dup := byKey[k]; !dup { // has_one: the first in order
					byKey[k] = row.Addr()
				}
			}
		}
		for i := range rows.Len() {
			parent := rows.Index(i)
			k, ok := keyOf(fieldOf(parent, m.cols[r.parentKey].index))
			p, found := byKey[k]
			if !ok || !found {
				p = reflect.Zero(reflect.PointerTo(r.related))
			}
			setField(parent, r.index, p)
		}
	default:
		sliceType := reflect.SliceOf(r.related)
		byKey := map[any]reflect.Value{}
		for i := range related.Len() {
			row := related.Index(i)
			var k any
			if r.kind == hasMany {
				var ok bool
				if k, ok = keyOf(fieldOf(row, r.rm.cols[r.relatedKey].index)); !ok {
					continue
				}
			} else {
				k = parents[i]
			}
			sl, ok := byKey[k]
			if !ok {
				sl = reflect.MakeSlice(sliceType, 0, 1)
			}
			byKey[k] = reflect.Append(sl, row)
		}
		for i := range rows.Len() {
			parent := rows.Index(i)
			k, _ := keyOf(fieldOf(parent, m.cols[r.parentKey].index))
			sl, ok := byKey[k]
			if !ok {
				sl = reflect.MakeSlice(sliceType, 0, 0) // loaded: empty, not nil
			}
			// Clipped, so appending to one parent's slice can't write into
			// another's when two parents have the same key.
			setField(parent, r.index, sl.Slice3(0, sl.Len(), sl.Len()))
		}
	}
	return nil
}

// setField sets the relation field at index, allocating nil embedded
// pointers on the way only for a non-zero value.
func setField(v reflect.Value, index []int, x reflect.Value) {
	if x.IsZero() {
		if f := fieldOf(v, index); f.IsValid() {
			f.Set(x)
		}
		return
	}
	fieldAlloc(v, index).Set(x)
}

// loadWhereIn returns the rows of model rm whose column col is one of
// keys, with the spec's conditions and order. The keys are parent keys,
// so every parent's rows come from one query, in order.
func loadWhereIn(ctx context.Context, rm *meta, col int, s relSpec, keys []any) (reflect.Value, error) {
	ctx = inBatch(ctx) // its chunks count as one query for repeated-query detection
	out := reflect.MakeSlice(reflect.SliceOf(rm.typ), 0, len(keys))
	for chunk := range slices.Chunk(keys, loadChunk) {
		q := relatedQuery(ctx, rm, s)
		q.wheres = append(q.wheres, inExpr{q.qualified(rm.cols[col].name), chunk, false})
		d, rows, err := q.rows(nil)
		if err != nil {
			return out, err
		}
		if out, err = scanInto(d, rm, rows, out, nil); err != nil {
			return out, err
		}
	}
	return out, nil
}

// relatedQuery is the query for a relation's rows: the spec's conditions
// and order (primary key order by default), and the default soft-delete
// scope unless WithTrashed.
func relatedQuery(ctx context.Context, rm *meta, s relSpec) *Q[struct{}] {
	q := &Q[struct{}]{ctx: ctx, m: rm, wheres: slices.Clone(s.wheres), orders: s.orders, trashed: s.trashed}
	if len(q.orders) == 0 && rm.pk >= 0 {
		q.orders = []Order{C(q.qualified(rm.cols[rm.pk].name)).Asc()}
	}
	return q
}

// pivotKeyColumn is the pivot's parent key in the result of loadPivoted.
const pivotKeyColumn = "anetos_pivot_key"

// loadPivoted loads the related rows of a many_to_many relation joined
// with its pivot rows, one query per chunk of parent keys (so each
// parent's rows come in order), and the parent key of each row.
func loadPivoted(ctx context.Context, r *relMeta, s relSpec, keys []any) (reflect.Value, []any, error) {
	ctx = inBatch(ctx) // its chunks count as one query for repeated-query detection
	out := reflect.MakeSlice(reflect.SliceOf(r.rm.typ), 0, len(keys))
	var parents []any
	d, c, err := handle(ctx)
	if err != nil {
		return out, nil, err
	}
	for chunk := range slices.Chunk(keys, loadChunk) {
		q := relatedQuery(ctx, r.rm, s)
		pk := r.rm.table + "." + r.rm.cols[r.relatedKey].name
		q.joins = []Expr{funcExpr(func(b *sqlBuilder) {
			b.write("JOIN ")
			b.name(r.pivot)
			b.write(" ON ")
			b.name(r.pivot + "." + r.pivotRelFK)
			b.write(" = ")
			b.name(pk)
		})}
		q.wheres = append(q.wheres, inExpr{r.pivot + "." + r.pivotFK, chunk, false})
		cols := []string{r.rm.selectColumns(d.dialect), quoteName(d.dialect, r.pivot+"."+r.pivotFK) + " AS " + d.dialect.QuoteIdent(pivotKeyColumn)}
		b := q.selectSQL(d.dialect, cols)
		if b.err != nil {
			return out, nil, b.err
		}
		rows, err := d.query(ctx, c, b.String(), b.args)
		if err != nil {
			return out, nil, err
		}
		var parent any
		before := out.Len()
		if out, err = scanInto(d, r.rm, rows, out, &parent, func() {
			k, _ := keyOf(reflect.ValueOf(parent))
			parents = append(parents, k)
		}); err != nil {
			return out, nil, err
		}
		if len(parents) != out.Len() {
			return out, nil, fmt.Errorf("db: loading %s: %d rows, %d pivot keys (from %d)", r.field, out.Len(), len(parents), before)
		}
	}
	return out, parents, nil
}

// keyOf returns a key value in a form that compares equal across types
// (int32 and int64 ids, []byte and string), dereferencing pointers and
// valuers; false for NULL.
func keyOf(v reflect.Value) (any, bool) {
	if !v.IsValid() {
		return nil, false
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, false
		}
		if vr, ok := reflect.TypeAssert[driver.Valuer](v); ok && v.Kind() == reflect.Pointer {
			return valuerKey(vr)
		}
		v = v.Elem()
	}
	if vr, ok := reflect.TypeAssert[driver.Valuer](v); ok {
		return valuerKey(vr)
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if u := v.Uint(); u <= 1<<63-1 {
			return int64(u), true
		}
		return v.Uint(), true
	case reflect.String:
		return v.String(), true
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return string(v.Bytes()), true
		}
	}
	if v.Type().Comparable() {
		return v.Interface(), true
	}
	return nil, false
}

func valuerKey(vr driver.Valuer) (any, bool) {
	dv, err := vr.Value()
	if err != nil || dv == nil {
		return nil, false
	}
	return keyOf(reflect.ValueOf(dv))
}

// scanInto appends the rows of model rm to out, and closes rows. With
// extra, the pivotKeyColumn column goes there, and each is called after
// every row.
func scanInto(d *DB, rm *meta, rows *sql.Rows, out reflect.Value, extra *any, each ...func()) (reflect.Value, error) {
	defer rows.Close()
	sc, err := newRowScanner(d, rm.typ, rows)
	if err != nil {
		return out, err
	}
	if extra != nil {
		cols, err := rows.Columns()
		if err != nil {
			return out, err
		}
		i := slices.Index(cols, pivotKeyColumn)
		if i < 0 {
			return out, fmt.Errorf("db: no %s column", pivotKeyColumn)
		}
		sc.extra = map[int]*any{i: extra}
	}
	for rows.Next() {
		v := reflect.New(rm.typ).Elem()
		if err := sc.scan(rows, v); err != nil {
			return out, err
		}
		out = reflect.Append(out, v)
		for _, f := range each {
			f()
		}
	}
	return out, rows.Err()
}

// ---- filtering ----

// WhereHas keeps the rows that have at least one related row matching
// conds (and the relation's own Where conditions):
//
//	// posts with an approved comment
//	db.Query[Post](ctx).WhereHas(PostRels.Comments, CommentCols.Approved.Eq(true))
//
// It is an EXISTS subquery; conds use the related model's columns.
func (q *Q[T]) WhereHas(rel Relation[T], conds ...Expr) *Q[T] {
	return q.Where(hasExpr{rel.spec(), conds, false})
}

// WhereDoesntHave keeps the rows with no related row matching conds.
func (q *Q[T]) WhereDoesntHave(rel Relation[T], conds ...Expr) *Q[T] {
	return q.Where(hasExpr{rel.spec(), conds, true})
}

type hasExpr struct {
	s     relSpec
	conds []Expr
	not   bool
}

func (e hasExpr) build(b *sqlBuilder) {
	if e.s.err != nil {
		b.fail(e.s.err)
		return
	}
	if len(e.s.nested) > 0 {
		b.fail(fmt.Errorf("db: WhereHas(%s) can't take nested relations (With)", e.s.field))
		return
	}
	owner, err := metaOf(e.s.owner)
	if err != nil {
		b.fail(err)
		return
	}
	r := owner.rels[e.s.field]
	if r == nil {
		b.fail(fmt.Errorf("db: %s has no relation field %s", owner.typ, e.s.field))
		return
	}
	if err := r.resolve(owner); err != nil {
		b.fail(err)
		return
	}
	rm := r.rm
	alias := rm.table
	if rm.table == owner.table {
		alias = "anetos_related" // a relation of a table to itself
	}
	rcol := func(i int) string { return alias + "." + rm.cols[i].name }
	ocol := func(i int) string { return owner.table + "." + owner.cols[i].name }

	if e.not {
		b.write("NOT ")
	}
	b.write("EXISTS (SELECT 1 FROM ")
	b.name(rm.table)
	if alias != rm.table {
		b.write(" AS ")
		b.name(alias)
	}
	b.write(" WHERE ")
	var link Expr
	if r.kind == manyToMany {
		link = funcExpr(func(b *sqlBuilder) {
			b.name(rcol(r.relatedKey))
			b.write(" IN (SELECT ")
			b.name(r.pivot + "." + r.pivotRelFK)
			b.write(" FROM ")
			b.name(r.pivot)
			b.write(" WHERE ")
			b.name(r.pivot + "." + r.pivotFK)
			b.write(" = ")
			b.name(ocol(r.parentKey))
			b.write(")")
		})
	} else {
		link = funcExpr(func(b *sqlBuilder) {
			b.name(rcol(r.relatedKey))
			b.write(" = ")
			b.name(ocol(r.parentKey))
		})
	}
	all := append([]Expr{link}, e.s.wheres...)
	all = append(all, e.conds...)
	if rm.deletedAt >= 0 && e.s.trashed == withoutTrashed {
		all = append(all, C(rcol(rm.deletedAt)).IsNull())
	}
	And(all...).build(b)
	b.write(")")
}

// funcExpr is an expression built by a function.
type funcExpr func(b *sqlBuilder)

func (f funcExpr) build(b *sqlBuilder) { f(b) }

// ---- many_to_many writes ----

// Attach links row to the related rows with the given primary keys through
// the pivot table of a many_to_many relation. Keys already linked are
// skipped, also when another transaction links them at the same time, so
// Attach can be repeated; this needs a primary key or unique index on the
// pivot's two columns.
//
//	err := db.Attach(ctx, &post, PostRels.Tags, goTag.ID, webTag.ID)
func Attach[T, R any](ctx context.Context, row *T, rel Rel[T, R], ids ...any) error {
	return pivotWrite(ctx, row, rel.spec(), "attach", ids)
}

// Detach unlinks row from the related rows with the given primary keys
// (none: nothing to do). The related rows stay.
func Detach[T, R any](ctx context.Context, row *T, rel Rel[T, R], ids ...any) error {
	if len(ids) == 0 {
		return nil
	}
	return pivotWrite(ctx, row, rel.spec(), "detach", ids)
}

// DetachAll unlinks row from all its related rows.
func DetachAll[T, R any](ctx context.Context, row *T, rel Rel[T, R]) error {
	return pivotWrite(ctx, row, rel.spec(), "detach", nil)
}

// Sync makes the given primary keys row's exact set of related rows:
// missing links are added and the others removed, in one transaction.
func Sync[T, R any](ctx context.Context, row *T, rel Rel[T, R], ids ...any) error {
	return pivotWrite(ctx, row, rel.spec(), "sync", ids)
}

func pivotWrite[T any](ctx context.Context, row *T, s relSpec, op string, ids []any) error {
	ctx = inBatch(ctx) // its chunks count as one query for repeated-query detection
	if s.err != nil {
		return s.err
	}
	owner, err := metaOf(reflect.TypeFor[T]())
	if err != nil {
		return err
	}
	r := owner.rels[s.field]
	if r == nil {
		return fmt.Errorf("db: %s has no relation field %s", owner.typ, s.field)
	}
	if err := r.resolve(owner); err != nil {
		return err
	}
	if r.kind != manyToMany {
		return fmt.Errorf("db: %s works on many_to_many relations; %s.%s is %s", op, owner.typ, r.field, r.kind)
	}
	parent, ok := keyOf(fieldOf(reflect.ValueOf(row).Elem(), owner.cols[r.parentKey].index))
	if !ok || isZeroField(reflect.ValueOf(row).Elem(), owner.cols[r.parentKey].index) {
		return fmt.Errorf("db: %s: %s has no primary key yet; Create it first", op, owner.typ)
	}
	want := make([]any, 0, len(ids))
	seen := map[any]bool{}
	for _, id := range ids {
		k, ok := keyOf(reflect.ValueOf(id))
		if !ok {
			return fmt.Errorf("db: %s: nil key", op)
		}
		if !seen[k] {
			seen[k] = true
			want = append(want, k)
		}
	}
	fk, relFK := r.pivot+"."+r.pivotFK, r.pivot+"."+r.pivotRelFK
	if !InTx(ctx) {
		// A concurrent write of the same links can fail the transaction
		// for serialization (MariaDB 11.6+): run it again.
		return dbutil.Retry(ctx, func() error { return pivotTx(ctx, r, op, parent, fk, relFK, want, seen) })
	}
	return pivotTx(ctx, r, op, parent, fk, relFK, want, seen)
}

// pivotTx writes a pivot table's links, in a transaction.
func pivotTx(ctx context.Context, r *relMeta, op string, parent any, fk, relFK string, want []any, seen map[any]bool) error {
	return Tx(ctx, func(ctx context.Context) error {
		d, c, err := handle(ctx)
		if err != nil {
			return err
		}
		exec := func(build func(b *sqlBuilder)) error {
			b := &sqlBuilder{d: d.dialect}
			build(b)
			if b.err != nil {
				return b.err
			}
			_, err := d.exec(ctx, c, b.String(), b.args)
			return err
		}
		if op == "detach" {
			return exec(func(b *sqlBuilder) {
				b.write("DELETE FROM ")
				b.name(r.pivot)
				b.write(" WHERE ")
				conds := []Expr{cmpExpr{fk, "=", parent}}
				if len(want) > 0 {
					conds = append(conds, inExpr{relFK, want, false})
				}
				And(conds...).build(b)
			})
		}
		// The links there are now.
		b := &sqlBuilder{d: d.dialect}
		b.write("SELECT ")
		b.name(relFK)
		b.write(" FROM ")
		b.name(r.pivot)
		b.write(" WHERE ")
		cmpExpr{fk, "=", parent}.build(b)
		rows, err := d.query(ctx, c, b.String(), b.args)
		if err != nil {
			return err
		}
		have := map[any]bool{}
		for rows.Next() {
			var v any
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			if k, ok := keyOf(reflect.ValueOf(v)); ok {
				have[k] = true
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if op == "sync" {
			var drop []any
			for k := range have {
				if !seen[k] {
					drop = append(drop, k)
				}
			}
			if len(drop) > 0 {
				if err := exec(func(b *sqlBuilder) {
					b.write("DELETE FROM ")
					b.name(r.pivot)
					b.write(" WHERE ")
					And(cmpExpr{fk, "=", parent}, inExpr{relFK, drop, false}).build(b)
				}); err != nil {
					return err
				}
			}
		}
		var add []any
		for _, k := range want {
			if !have[k] {
				add = append(add, k)
			}
		}
		// One INSERT per chunk. A link added meanwhile by another
		// transaction is skipped by the conflict clause, which needs a
		// primary key or unique index on the two pivot columns.
		conflict := []string{d.dialect.QuoteIdent(r.pivotFK), d.dialect.QuoteIdent(r.pivotRelFK)}
		for chunk := range slices.Chunk(add, loadChunk/2) {
			if err := exec(func(b *sqlBuilder) {
				b.write("INSERT INTO ")
				b.name(r.pivot)
				b.write(" (")
				b.name(r.pivotFK)
				b.write(", ")
				b.name(r.pivotRelFK)
				b.write(") VALUES ")
				for i, k := range chunk {
					if i > 0 {
						b.write(", ")
					}
					b.write("(")
					b.arg(parent)
					b.write(", ")
					b.arg(k)
					b.write(")")
				}
				b.write(" " + d.dialect.Upsert(conflict, nil))
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

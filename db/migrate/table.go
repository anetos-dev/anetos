// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"fmt"
	"strings"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/internal/dbutil"
)

// colType is a portable column type, mapped to SQL per dialect.
type colType int

const (
	typeID colType = iota // auto-increment BIGINT primary key
	typeString
	typeText
	typeLongText
	typeInteger
	typeBigInteger
	typeSmallInteger
	typeBoolean
	typeFloat
	typeDecimal
	typeDate
	typeTimestamp
	typeJSON
	typeBinary
	typeUUID
	typeVector
)

// Column is a column being added or changed. Its methods set modifiers and
// return the column, so they chain:
//
//	t.String("email", 255).Unique()
//	t.Integer("views").Default(0)
//	t.Timestamp("published_at").Nullable()
type Column struct {
	t          *Table
	name       string
	typ        colType
	length     int // string
	precision  int // decimal
	scale      int
	nullable   bool
	hasDefault bool
	def        any
	defRaw     string
	useCurrent bool
	change     bool
	using      string
}

// Nullable allows NULL. Columns are NOT NULL by default.
func (c *Column) Nullable() *Column { c.nullable = true; return c }

// Default sets a default value: a string, number, bool or nil.
func (c *Column) Default(v any) *Column {
	c.hasDefault, c.def, c.defRaw = true, v, ""
	return c
}

// DefaultRaw sets a default written in SQL, such as "gen_random_uuid()".
func (c *Column) DefaultRaw(sql string) *Column {
	c.hasDefault, c.def, c.defRaw = true, nil, sql
	return c
}

// UseCurrent defaults a timestamp column to the current time.
func (c *Column) UseCurrent() *Column {
	c.useCurrent, c.hasDefault = true, true
	return c
}

// Unique adds a unique index on the column, named table_column_unique.
func (c *Column) Unique() *Column {
	c.t.Unique(c.name)
	return c
}

// Index adds an index on the column, named table_column_index.
func (c *Column) Index() *Column {
	c.t.Index(c.name)
	return c
}

// Primary makes the column the primary key.
func (c *Column) Primary() *Column {
	c.t.Primary(c.name)
	return c
}

// Change modifies an existing column in [Schema.Alter] instead of adding
// it: its type, nullability and default become what this definition says.
// Not supported on SQLite, whose ALTER TABLE can't change columns; create
// a new table and copy the rows instead.
func (c *Column) Change() *Column {
	c.change = true
	return c
}

// Using gives PostgreSQL the conversion for a Change() whose new type it
// can't cast to automatically: t.Integer("code").Change().Using("code::integer").
// Other databases ignore it.
func (c *Column) Using(sql string) *Column {
	c.using = sql
	return c
}

// References adds a foreign key from this column to table's column (id if
// omitted), named table_column_foreign. It returns the foreign key, so
// put column modifiers such as Nullable before it.
func (c *Column) References(table string, column ...string) *Foreign {
	return c.t.Foreign(c.name).References(table, column...)
}

// Constrained adds a foreign key to the table named after the column:
// author_id references authors(id), category_id references categories(id).
func (c *Column) Constrained() *Foreign {
	name := strings.TrimSuffix(c.name, "_id")
	return c.References(db.Plural(name))
}

// Action is what a foreign key does when the referenced row is deleted or
// its key changes.
type Action string

// Foreign key actions.
const (
	Cascade    Action = "CASCADE"
	SetNull    Action = "SET NULL"
	Restrict   Action = "RESTRICT"
	NoAction   Action = "NO ACTION"
	SetDefault Action = "SET DEFAULT"
)

// Foreign is a foreign key constraint.
type Foreign struct {
	name     string
	columns  []string
	table    string
	refs     []string
	onDelete Action
	onUpdate Action
}

// References sets the referenced table and columns (id if omitted).
func (f *Foreign) References(table string, columns ...string) *Foreign {
	if len(columns) == 0 {
		columns = []string{"id"}
	}
	f.table, f.refs = table, columns
	return f
}

// Named replaces the generated constraint name.
func (f *Foreign) Named(name string) *Foreign { f.name = name; return f }

// OnDelete sets what happens when the referenced row is deleted.
func (f *Foreign) OnDelete(a Action) *Foreign { f.onDelete = a; return f }

// OnUpdate sets what happens when the referenced key changes.
func (f *Foreign) OnUpdate(a Action) *Foreign { f.onUpdate = a; return f }

// CascadeOnDelete deletes rows when the row they reference is deleted.
func (f *Foreign) CascadeOnDelete() *Foreign { return f.OnDelete(Cascade) }

// NullOnDelete sets the column to NULL when the referenced row is deleted
// (the column must be Nullable).
func (f *Foreign) NullOnDelete() *Foreign { return f.OnDelete(SetNull) }

type index struct {
	name    string
	columns []string
	unique  bool
}

type alteration struct {
	kind string // dropColumn, renameColumn, dropIndex, dropForeign
	a, b string
}

// Table describes a table being created ([Schema.Create]) or changed
// ([Schema.Alter]).
type Table struct {
	name     string
	creating bool
	cols     []*Column
	primary  []string
	indexes  []index
	foreigns []*Foreign
	alters   []alteration
	errs     []error
	search   []string // SearchIndex
	unsearch bool     // DropSearchIndex
}

func (t *Table) add(name string, typ colType) *Column {
	if name == "" {
		t.errs = append(t.errs, fmt.Errorf("a column in %s has no name", t.name))
	}
	c := &Column{t: t, name: name, typ: typ}
	t.cols = append(t.cols, c)
	return c
}

// ID adds an auto-increment BIGINT primary key named id, matching
// db.Model.
func (t *Table) ID() *Column { return t.add("id", typeID) }

// UUID adds a UUID column (UUID on PostgreSQL, CHAR(36) on MySQL, TEXT on
// SQLite). Scan it into a string.
func (t *Table) UUID(name string) *Column { return t.add(name, typeUUID) }

// Vector adds a column of embeddings with dims dimensions: pgvector's
// vector(dims) on PostgreSQL, VECTOR(dims) on MariaDB 11.7+, a BLOB of
// float32s on SQLite. Read and write it as a db.Vector. Most apps use
// [Schema.CreateEmbeddings] instead, which makes the table package ai
// fills.
func (t *Table) Vector(name string, dims int) *Column {
	c := t.add(name, typeVector)
	c.length = dims
	return c
}

// String adds a VARCHAR column. A length of 0 means 255.
func (t *Table) String(name string, length int) *Column {
	if length < 0 {
		t.errs = append(t.errs, fmt.Errorf("%s.%s: negative length", t.name, name))
	}
	if length == 0 {
		length = 255
	}
	c := t.add(name, typeString)
	c.length = length
	return c
}

// Text adds a TEXT column (up to 64KB on MySQL).
func (t *Table) Text(name string) *Column { return t.add(name, typeText) }

// LongText adds a large text column (LONGTEXT on MySQL, TEXT elsewhere).
func (t *Table) LongText(name string) *Column { return t.add(name, typeLongText) }

// Integer adds a 32-bit integer column.
func (t *Table) Integer(name string) *Column { return t.add(name, typeInteger) }

// BigInteger adds a 64-bit integer column.
func (t *Table) BigInteger(name string) *Column { return t.add(name, typeBigInteger) }

// SmallInteger adds a 16-bit integer column.
func (t *Table) SmallInteger(name string) *Column { return t.add(name, typeSmallInteger) }

// Boolean adds a boolean column.
func (t *Table) Boolean(name string) *Column { return t.add(name, typeBoolean) }

// Float adds a double-precision floating-point column.
func (t *Table) Float(name string) *Column { return t.add(name, typeFloat) }

// Decimal adds an exact numeric column with precision digits, scale of
// them after the point: Decimal("price", 10, 2). Scan it into a string or
// a decimal type, not float64, to keep it exact. On SQLite, which has no
// exact numeric type, it is TEXT: values keep every digit, but compare
// and sort as text.
func (t *Table) Decimal(name string, precision, scale int) *Column {
	if precision < 1 || scale < 0 || scale > precision {
		t.errs = append(t.errs, fmt.Errorf("%s.%s: invalid decimal(%d, %d)", t.name, name, precision, scale))
	}
	c := t.add(name, typeDecimal)
	c.precision, c.scale = precision, scale
	return c
}

// Date adds a date column.
func (t *Table) Date(name string) *Column { return t.add(name, typeDate) }

// Timestamp adds a date and time column with microsecond precision
// (TIMESTAMPTZ on PostgreSQL, DATETIME(6) on MySQL).
func (t *Table) Timestamp(name string) *Column { return t.add(name, typeTimestamp) }

// JSON adds a JSON column (JSONB on PostgreSQL, TEXT on SQLite). Map it
// with a db:"name,json" tag.
func (t *Table) JSON(name string) *Column { return t.add(name, typeJSON) }

// Binary adds a binary column (BYTEA, LONGBLOB, BLOB).
func (t *Table) Binary(name string) *Column { return t.add(name, typeBinary) }

// ForeignID adds a BIGINT column for a foreign key; follow it with
// References or Constrained:
//
//	t.ForeignID("author_id").Constrained().CascadeOnDelete()
func (t *Table) ForeignID(name string) *Column { return t.add(name, typeBigInteger) }

// Timestamps adds created_at and updated_at, matching db.Timestamps. They
// default to the current time, so rows inserted by SQL get them too.
func (t *Table) Timestamps() {
	t.Timestamp("created_at").UseCurrent()
	t.Timestamp("updated_at").UseCurrent()
}

// SoftDeletes adds a nullable deleted_at, matching db.SoftDeletes.
func (t *Table) SoftDeletes() { t.Timestamp("deleted_at").Nullable() }

// Primary sets a (composite) primary key.
func (t *Table) Primary(columns ...string) { t.primary = columns }

// Index adds an index, named table_col1_col2_index.
func (t *Table) Index(columns ...string) {
	t.indexes = append(t.indexes, index{indexName(t.name, columns, "index"), columns, false})
}

// Unique adds a unique index, named table_col1_col2_unique.
func (t *Table) Unique(columns ...string) {
	t.indexes = append(t.indexes, index{indexName(t.name, columns, "unique"), columns, true})
}

// IndexNamed adds an index with a name of your choosing.
func (t *Table) IndexNamed(name string, columns ...string) {
	t.indexes = append(t.indexes, index{name, columns, false})
}

// UniqueNamed adds a unique index with a name of your choosing.
func (t *Table) UniqueNamed(name string, columns ...string) {
	t.indexes = append(t.indexes, index{name, columns, true})
}

// DropIndexNamed removes an index (or unique index) by name ([Schema.Alter]
// only).
func (t *Table) DropIndexNamed(name string) {
	t.alters = append(t.alters, alteration{kind: "dropIndex", a: name})
}

// DropForeignNamed removes a foreign key by name (not supported on SQLite).
func (t *Table) DropForeignNamed(name string) {
	t.alters = append(t.alters, alteration{kind: "dropForeign", a: name})
}

// Foreign starts a foreign key on existing or new columns:
//
//	t.Foreign("author_id").References("users").CascadeOnDelete()
func (t *Table) Foreign(columns ...string) *Foreign {
	f := &Foreign{name: indexName(t.name, columns, "foreign"), columns: columns}
	t.foreigns = append(t.foreigns, f)
	return f
}

// DropColumn removes columns ([Schema.Alter] only).
func (t *Table) DropColumn(names ...string) {
	for _, n := range names {
		t.alters = append(t.alters, alteration{kind: "dropColumn", a: n})
	}
}

// RenameColumn renames a column ([Schema.Alter] only).
func (t *Table) RenameColumn(from, to string) {
	t.alters = append(t.alters, alteration{kind: "renameColumn", a: from, b: to})
}

// DropIndex removes the index created by Index(columns...).
func (t *Table) DropIndex(columns ...string) {
	t.alters = append(t.alters, alteration{kind: "dropIndex", a: indexName(t.name, columns, "index")})
}

// DropUnique removes the index created by Unique(columns...).
func (t *Table) DropUnique(columns ...string) {
	t.alters = append(t.alters, alteration{kind: "dropIndex", a: indexName(t.name, columns, "unique")})
}

// SearchIndex makes the table searchable with Search
// (db.Query[Post](ctx).Search(text)): a full-text index over text
// columns, most important first, whose matches weigh more (1, 0.4, 0.2,
// then 0.1 on PostgreSQL and SQLite; MySQL weighs them alike):
//
//	t.SearchIndex("title", "body")
//
// It is built for SEARCH_LANGUAGE and SEARCH_RANKING, which the
// database must support, and recorded in the search_indexes table
// (db.SearchIndexes). PostgreSQL gets a generated search_vector column
// with a GIN index (and, for SEARCH_RANKING=bm25, a generated
// search_text column with a bm25 index); MySQL and MariaDB a generated
// search_text column with a FULLTEXT index; SQLite a <table>_search FTS5
// table, kept in sync by triggers. In Alter, rows already in the table
// are indexed.
func (t *Table) SearchIndex(columns ...string) {
	switch {
	case len(columns) == 0:
		t.errs = append(t.errs, fmt.Errorf("migrate: %s: SearchIndex needs columns", t.name))
	case t.search != nil:
		t.errs = append(t.errs, fmt.Errorf("migrate: %s: one SearchIndex per table", t.name))
	}
	t.search = columns
}

// DropSearchIndex removes the table's search index ([Schema.Alter]
// only).
func (t *Table) DropSearchIndex() { t.unsearch = true }

// DropForeign removes the foreign key created on columns (not supported
// on SQLite).
func (t *Table) DropForeign(columns ...string) {
	t.alters = append(t.alters, alteration{kind: "dropForeign", a: indexName(t.name, columns, "foreign")})
}

// maxIdent is the longest identifier every database keeps whole.
const maxIdent = dbutil.MaxIdent

// indexName follows Laravel: posts_author_id_created_at_index. Names too
// long for PostgreSQL are shortened with a hash of the full name, so
// different long names stay different, and Drop* computes the same name.
func indexName(table string, columns []string, suffix string) string {
	return dbutil.IndexName(table, columns, suffix)
}

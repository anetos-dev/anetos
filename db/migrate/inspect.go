// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// TableInfo describes a table (or a view) of the database, as
// [Runner.Inspect] finds it.
type TableInfo struct {
	// Name is the table's name.
	Name string `json:"name"`
	// Kind is "table", "view" or, on SQLite, "virtual" (a search index).
	Kind string `json:"kind"`
	// Columns are its columns, in order.
	Columns []ColumnInfo `json:"columns"`
	// Indexes are its indexes, the primary key first.
	Indexes []IndexInfo `json:"indexes"`
	// ForeignKeys are its foreign keys.
	ForeignKeys []ForeignKeyInfo `json:"foreign_keys"`
}

// ColumnInfo is a column of a [TableInfo].
type ColumnInfo struct {
	// Name is the column's name.
	Name string `json:"name"`
	// Type is the database's: "bigint", "character varying(100)",
	// "varchar(100)", "INTEGER".
	Type string `json:"type"`
	// Nullable says the column takes NULL.
	Nullable bool `json:"nullable"`
	// Default is the default's SQL ("1", "'none'", "CURRENT_TIMESTAMP"),
	// "" without one.
	Default string `json:"default,omitempty"`
	// PrimaryKey says the column is (part of) the primary key.
	PrimaryKey bool `json:"primary_key,omitempty"`
	// Generated is a generated column's expression ("(generated)" on
	// SQLite, which doesn't say it).
	Generated string `json:"generated,omitempty"`
	// Identity says the database numbers the column (an identity or
	// AUTO_INCREMENT column).
	Identity bool `json:"identity,omitempty"`
}

// IndexInfo is an index of a [TableInfo], the primary key's included.
type IndexInfo struct {
	// Name is the index's name ("PRIMARY" for SQLite's and MySQL's
	// primary key).
	Name string `json:"name"`
	// Columns are its key's columns, or the expressions it indexes
	// ("lower(email)"), in order; not the columns a PostgreSQL index
	// INCLUDEs.
	Columns []string `json:"columns"`
	// Unique says no two rows have the same key.
	Unique bool `json:"unique"`
	// Primary says it is the primary key.
	Primary bool `json:"primary,omitempty"`
	// Where is a partial index's condition, "" for the whole table.
	Where string `json:"where,omitempty"`
}

// ForeignKeyInfo is a foreign key of a [TableInfo].
type ForeignKeyInfo struct {
	// Columns are the key's columns in the table.
	Columns []string `json:"columns"`
	// Table is the table it references.
	Table string `json:"table"`
	// References are that table's columns, Columns' counterparts.
	References []string `json:"references"`
	// OnDelete is "cascade", "set null", "restrict", "set default" or
	// "no action".
	OnDelete string `json:"on_delete"`
}

// Inspect describes the database's tables and views, or those named,
// by name: their columns, indexes and foreign keys, as the database has
// them (the migrations' result). Tables of the database's own, and the
// internal tables of SQLite's search indexes, are left out.
func (r *Runner) Inspect(ctx context.Context, tables ...string) ([]TableInfo, error) {
	q := inspector{ctx: ctx, db: r.d.SQL(), dialect: r.dialect}
	all, err := q.tables()
	if err != nil {
		return nil, fmt.Errorf("migrate: list the tables: %w", err)
	}
	if len(tables) > 0 {
		var picked []TableInfo
		for _, name := range tables {
			i := slices.IndexFunc(all, func(t TableInfo) bool { return t.Name == name })
			if i < 0 {
				return nil, fmt.Errorf("migrate: no table %q", name)
			}
			picked = append(picked, all[i])
		}
		all = picked
	}
	for i := range all {
		if err := q.describe(&all[i]); err != nil {
			return nil, fmt.Errorf("migrate: describe %s: %w", all[i].Name, err)
		}
	}
	return all, nil
}

type inspector struct {
	ctx     context.Context
	db      *sql.DB
	dialect string
}

// query runs a query with ? placeholders (numbered on PostgreSQL),
// calling scan for each row.
func (q inspector) query(query string, scan func(*sql.Rows) error, args ...any) error {
	if q.dialect == "postgres" {
		for n := 1; strings.Contains(query, "?"); n++ {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", n), 1)
		}
	}
	rows, err := q.db.QueryContext(q.ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (q inspector) tables() ([]TableInfo, error) {
	var list string
	switch q.dialect {
	case "postgres":
		list = `SELECT c.relname, CASE WHEN c.relkind IN ('v', 'm') THEN 'view' ELSE 'table' END
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p', 'v', 'm')
			AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
			ORDER BY c.relname`
	case "mysql":
		list = `SELECT table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
			FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_name`
	default:
		list = `SELECT name, type FROM pragma_table_list
			WHERE schema = 'main' AND type IN ('table', 'view', 'virtual') AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
			ORDER BY name`
	}
	var out []TableInfo
	err := q.query(list, func(rows *sql.Rows) error {
		var t TableInfo
		if err := rows.Scan(&t.Name, &t.Kind); err != nil {
			return err
		}
		out = append(out, t)
		return nil
	})
	return out, err
}

func (q inspector) describe(t *TableInfo) error {
	t.Columns, t.Indexes, t.ForeignKeys = []ColumnInfo{}, []IndexInfo{}, []ForeignKeyInfo{}
	var err error
	switch q.dialect {
	case "postgres":
		err = q.postgres(t)
	case "mysql":
		err = q.mysql(t)
	default:
		err = q.sqlite(t)
	}
	if err != nil {
		return err
	}
	for _, ix := range t.Indexes {
		if ix.Primary {
			for i := range t.Columns {
				if slices.Contains(ix.Columns, t.Columns[i].Name) {
					// SQLite says an INTEGER PRIMARY KEY may be null: it
					// can't.
					t.Columns[i].PrimaryKey, t.Columns[i].Nullable = true, false
				}
			}
		}
	}
	return nil
}

// index adds column to the index name of t, adding the index first.
func (t *TableInfo) index(name, column string, unique, primary bool) *IndexInfo {
	if n := len(t.Indexes); n > 0 && t.Indexes[n-1].Name == name {
		t.Indexes[n-1].Columns = append(t.Indexes[n-1].Columns, column)
		return &t.Indexes[n-1]
	}
	t.Indexes = append(t.Indexes, IndexInfo{Name: name, Columns: []string{column}, Unique: unique, Primary: primary})
	return &t.Indexes[len(t.Indexes)-1]
}

// foreign adds a column pair to the foreign key id of t, adding the key
// first.
func (t *TableInfo) foreign(id *string, key, column, table, ref, onDelete string) {
	if n := len(t.ForeignKeys); n > 0 && *id == key {
		t.ForeignKeys[n-1].Columns = append(t.ForeignKeys[n-1].Columns, column)
		t.ForeignKeys[n-1].References = append(t.ForeignKeys[n-1].References, ref)
		return
	}
	*id = key
	t.ForeignKeys = append(t.ForeignKeys, ForeignKeyInfo{Columns: []string{column}, Table: table, References: []string{ref}, OnDelete: onDelete})
}

func (q inspector) sqlite(t *TableInfo) error {
	// table_xinfo has generated columns too (hidden 2 and 3); hidden 1
	// are a virtual table's own.
	err := q.query(`SELECT name, type, "notnull", dflt_value, hidden FROM pragma_table_xinfo(?) WHERE hidden <> 1 ORDER BY cid`, func(rows *sql.Rows) error {
		var c ColumnInfo
		var notNull bool
		var def sql.NullString
		var hidden int
		if err := rows.Scan(&c.Name, &c.Type, &notNull, &def, &hidden); err != nil {
			return err
		}
		c.Nullable, c.Default = !notNull, def.String
		if hidden > 1 {
			c.Generated = "(generated)"
		}
		t.Columns = append(t.Columns, c)
		return nil
	}, t.Name)
	if err != nil {
		return err
	}
	// The primary key: an INTEGER PRIMARY KEY has no index of its own.
	pk, err := q.sqlitePK(t.Name)
	if err != nil {
		return err
	}
	if len(pk) > 0 {
		t.Indexes = append(t.Indexes, IndexInfo{Name: "PRIMARY", Columns: pk, Unique: true, Primary: true})
	}
	// The indexes' key columns (key = 1), an expression's name NULL: its
	// text, and a partial index's condition, are in the index's SQL.
	type part struct {
		index, column, sql string
		unique, expr       bool
	}
	var parts []part
	err = q.query(`SELECT il.name, il."unique", ix.name, COALESCE(m.sql, '') FROM pragma_index_list(?) il
		JOIN pragma_index_xinfo(il.name) ix LEFT JOIN sqlite_master m ON m.type = 'index' AND m.name = il.name
		WHERE il.origin <> 'pk' AND ix.key = 1 ORDER BY il.name, ix.seqno`, func(rows *sql.Rows) error {
		var p part
		var column sql.NullString
		if err := rows.Scan(&p.index, &p.unique, &column, &p.sql); err != nil {
			return err
		}
		p.column, p.expr = column.String, !column.Valid
		parts = append(parts, p)
		return nil
	}, t.Name)
	if err != nil {
		return err
	}
	for i, p := range parts {
		n := 0 // the part's place in its index
		for j := i - 1; j >= 0 && parts[j].index == p.index; j-- {
			n++
		}
		exprs, where := indexSQL(p.sql)
		column := p.column
		if p.expr {
			column = "(expression)"
			if n < len(exprs) {
				column = exprs[n]
			}
		}
		t.index(p.index, column, p.unique, false).Where = where
	}
	// Read the keys first: a query in a row's callback would wait for a
	// pool of one connection (an in-memory database).
	type key struct{ id, table, from, onDelete, to string }
	var keys []key
	err = q.query(`SELECT id, "table", "from", COALESCE("to", ''), on_delete FROM pragma_foreign_key_list(?) ORDER BY id DESC, seq`, func(rows *sql.Rows) error {
		var k key
		if err := rows.Scan(&k.id, &k.table, &k.from, &k.to, &k.onDelete); err != nil {
			return err
		}
		keys = append(keys, k)
		return nil
	}, t.Name)
	if err != nil {
		return err
	}
	var last string
	for _, k := range keys {
		ref := k.to
		if ref == "" { // the parent's primary key
			parent, err := q.sqlitePK(k.table)
			if err != nil {
				return err
			}
			if n := q.foreignSeq(t, last, k.id); n < len(parent) {
				ref = parent[n]
			}
		}
		t.foreign(&last, k.id, k.from, k.table, ref, strings.ToLower(k.onDelete))
	}
	return nil
}

// foreignSeq is the place the next column of the foreign key id takes.
func (inspector) foreignSeq(t *TableInfo, last, id string) int {
	if n := len(t.ForeignKeys); n > 0 && last == id {
		return len(t.ForeignKeys[n-1].Columns)
	}
	return 0
}

// sqlitePK is the primary key's columns of a SQLite table, in order.
func (q inspector) sqlitePK(table string) ([]string, error) {
	var pk []string
	err := q.query(`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, func(rows *sql.Rows) error {
		var name string
		err := rows.Scan(&name)
		pk = append(pk, name)
		return err
	}, table)
	return pk, err
}

// indexSQL reads a CREATE INDEX statement: the expressions of its key,
// as written, and the condition of a partial index.
func indexSQL(stmt string) (exprs []string, where string) {
	open := strings.Index(stmt, "(")
	if open < 0 {
		return nil, ""
	}
	depth, start, quote := 0, open+1, byte(0)
	for i := open; i < len(stmt); i++ {
		c := stmt[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				exprs = append(exprs, strings.TrimSpace(stmt[start:i]))
				rest := strings.TrimSpace(stmt[i+1:])
				if len(rest) > 5 && strings.EqualFold(rest[:5], "WHERE") {
					where = strings.TrimSpace(rest[5:])
				}
				return exprs, where
			}
		case c == ',' && depth == 1:
			exprs = append(exprs, strings.TrimSpace(stmt[start:i]))
			start = i + 1
		}
	}
	return exprs, where
}

var postgresOnDelete = map[string]string{"a": "no action", "r": "restrict", "c": "cascade", "n": "set null", "d": "set default"}

func (q inspector) postgres(t *TableInfo) error {
	err := q.query(`SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
		COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), a.attgenerated <> '', a.attidentity <> ''
		FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE n.nspname = current_schema() AND c.relname = ? AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, func(rows *sql.Rows) error {
		var c ColumnInfo
		var generated bool
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &generated, &c.Identity); err != nil {
			return err
		}
		if generated { // its "default" is its expression
			c.Generated, c.Default = c.Default, ""
		}
		t.Columns = append(t.Columns, c)
		return nil
	}, t.Name)
	if err != nil {
		return err
	}
	// Each key column or expression, as pg_get_indexdef writes it; not
	// the INCLUDEd columns (past indnkeyatts).
	err = q.query(`SELECT i.relname, ix.indisunique, ix.indisprimary, pg_get_indexdef(ix.indexrelid, k.ord::int, true),
		COALESCE(pg_get_expr(ix.indpred, ix.indrelid, true), '')
		FROM pg_index ix JOIN pg_class tc ON tc.oid = ix.indrelid JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_namespace n ON n.oid = tc.relnamespace
		JOIN LATERAL generate_series(1, ix.indnkeyatts) AS k(ord) ON true
		WHERE n.nspname = current_schema() AND tc.relname = ?
		ORDER BY ix.indisprimary DESC, i.relname, k.ord`, func(rows *sql.Rows) error {
		var name, column, where string
		var unique, primary bool
		if err := rows.Scan(&name, &unique, &primary, &column, &where); err != nil {
			return err
		}
		t.index(name, column, unique, primary).Where = where
		return nil
	}, t.Name)
	if err != nil {
		return err
	}
	var last string
	return q.query(`SELECT con.conname, a.attname, ft.relname, fa.attname, con.confdeltype
		FROM pg_constraint con JOIN pg_class tc ON tc.oid = con.conrelid JOIN pg_namespace n ON n.oid = tc.relnamespace
		JOIN pg_class ft ON ft.oid = con.confrelid
		JOIN LATERAL unnest(con.conkey, con.confkey) WITH ORDINALITY AS k(col, fcol, ord) ON true
		JOIN pg_attribute a ON a.attrelid = tc.oid AND a.attnum = k.col
		JOIN pg_attribute fa ON fa.attrelid = ft.oid AND fa.attnum = k.fcol
		WHERE con.contype = 'f' AND n.nspname = current_schema() AND tc.relname = ?
		ORDER BY con.conname, k.ord`, func(rows *sql.Rows) error {
		var name, column, table, ref, onDelete string
		if err := rows.Scan(&name, &column, &table, &ref, &onDelete); err != nil {
			return err
		}
		t.foreign(&last, name, column, table, ref, postgresOnDelete[onDelete])
		return nil
	}, t.Name)
}

// mariaDB reports whether the MySQL server is MariaDB, whose catalog
// differs: defaults written as SQL ('none', NULL), no functional
// indexes.
func (q inspector) mariaDB() (bool, error) {
	var v string
	err := q.db.QueryRowContext(q.ctx, "SELECT VERSION()").Scan(&v)
	return strings.Contains(strings.ToLower(v), "mariadb"), err
}

func (q inspector) mysql(t *TableInfo) error {
	maria, err := q.mariaDB()
	if err != nil {
		return err
	}
	err = q.query(`SELECT column_name, column_type, data_type, is_nullable, column_default, extra, COALESCE(generation_expression, '')
		FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ?
		ORDER BY ordinal_position`, func(rows *sql.Rows) error {
		var c ColumnInfo
		var dataType, nullable, extra string
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &dataType, &nullable, &def, &extra, &c.Generated); err != nil {
			return err
		}
		c.Nullable = nullable == "YES"
		extra = strings.ToUpper(extra)
		c.Identity = strings.Contains(extra, "AUTO_INCREMENT")
		switch {
		case !def.Valid, maria && def.String == "NULL": // MariaDB writes a NULL default as NULL
		case maria, strings.Contains(extra, "DEFAULT_GENERATED"), !mysqlText[strings.ToLower(dataType)]:
			c.Default = def.String // SQL already, or a number
		default: // MySQL writes a text default unquoted
			c.Default = "'" + strings.ReplaceAll(def.String, "'", "''") + "'"
		}
		if c.Generated != "" {
			c.Default = ""
		}
		t.Columns = append(t.Columns, c)
		return nil
	}, t.Name)
	if err != nil {
		return err
	}
	// A functional index's part has no column_name, and MySQL 8 has its
	// expression (MariaDB has no such indexes, nor the column).
	exprCol := "NULL"
	if !maria {
		exprCol = "expression"
	}
	err = q.query(`SELECT index_name, non_unique, column_name, `+exprCol+` FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ?
		ORDER BY index_name <> 'PRIMARY', index_name, seq_in_index`, func(rows *sql.Rows) error {
		var name string
		var column, expr sql.NullString
		var nonUnique bool
		if err := rows.Scan(&name, &nonUnique, &column, &expr); err != nil {
			return err
		}
		col := column.String
		if !column.Valid {
			col = cmp.Or(expr.String, "(expression)")
		}
		t.index(name, col, !nonUnique, name == "PRIMARY")
		return nil
	}, t.Name)
	if err != nil {
		return err
	}
	var last string
	return q.query(`SELECT k.constraint_name, k.column_name, k.referenced_table_name, k.referenced_column_name, r.delete_rule
		FROM information_schema.key_column_usage k
		JOIN information_schema.referential_constraints r ON r.constraint_schema = k.constraint_schema AND r.constraint_name = k.constraint_name
		WHERE k.table_schema = DATABASE() AND k.table_name = ? AND k.referenced_table_name IS NOT NULL
		ORDER BY k.constraint_name, k.ordinal_position`, func(rows *sql.Rows) error {
		var name, column, table, ref, onDelete string
		if err := rows.Scan(&name, &column, &table, &ref, &onDelete); err != nil {
			return err
		}
		t.foreign(&last, name, column, table, ref, strings.ToLower(onDelete))
		return nil
	}, t.Name)
}

// mysqlText are MySQL's types whose defaults are text.
var mysqlText = map[string]bool{"char": true, "varchar": true, "tinytext": true, "text": true, "mediumtext": true,
	"longtext": true, "enum": true, "set": true, "binary": true, "varbinary": true}

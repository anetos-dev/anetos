// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos/db"
)

// Schema changes the database structure inside a migration. Its methods
// run immediately, in the migration's transaction where the database
// supports transactional DDL (PostgreSQL, SQLite).
type Schema struct {
	ctx     context.Context
	d       *db.DB
	dialect string
}

// NewSchema returns a Schema for the database in ctx (see db.WithDB), for
// use outside migrations, such as in tests.
func NewSchema(ctx context.Context) (*Schema, error) {
	d, err := db.From(ctx)
	if err != nil {
		return nil, err
	}
	s := &Schema{ctx: ctx, d: d, dialect: d.Dialect().Name()}
	switch s.dialect {
	case "postgres", "mysql", "sqlite":
		return s, nil
	}
	return nil, fmt.Errorf("migrate: dialect %q isn't supported", s.dialect)
}

// Context returns the migration's context. It carries the database and the
// migration's transaction, so data migrations can use the db package:
//
//	db.Query[User](s.Context()).Where(…).Update(…)
func (s *Schema) Context() context.Context { return s.ctx }

// Dialect returns "postgres", "mysql" or "sqlite", for migrations that
// need database-specific SQL.
func (s *Schema) Dialect() string { return s.dialect }

// Exec runs SQL. Without arguments the SQL is sent as written (a ? is just
// a ?, such as PostgreSQL's jsonb operator) and several statements
// separated by semicolons run one by one; trigger and procedure bodies
// (BEGIN … END, $$ … $$) stay whole. With arguments it is one statement
// with ? placeholders, as in db.Exec.
func (s *Schema) Exec(sql string, args ...any) error {
	stmts := []string{sql}
	if len(args) == 0 {
		stmts = splitStatements(sql, s.dialect)
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(s.ctx, stmt, args...); err != nil {
			return err
		}
	}
	return nil
}

// execFile runs the SQL of a migration file.
func (s *Schema) execFile(sql string) error {
	if directive(sql, "no-split") {
		_, err := db.Exec(s.ctx, sql)
		return err
	}
	return s.Exec(sql)
}

func (s *Schema) run(stmts []string) error {
	for _, stmt := range stmts {
		if _, err := db.Exec(s.ctx, stmt); err != nil {
			return fmt.Errorf("%w\n  in: %s", err, stmt)
		}
	}
	return nil
}

func (s *Schema) q(name string) string { return quoteName(s.d.Dialect(), name) }

// checkName rejects table names the schema builder can't handle.
func checkName(table string) error {
	switch {
	case table == "":
		return errors.New("migrate: empty table name")
	case strings.Contains(table, "."):
		return fmt.Errorf("migrate: %q: schema-qualified names aren't supported; connect to that schema (search_path, database) instead", table)
	case len(table) > maxIdent:
		return fmt.Errorf("migrate: table name %q is longer than %d bytes", table, maxIdent)
	}
	return nil
}

func quoteName(d db.Dialect, name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = d.QuoteIdent(p)
	}
	return strings.Join(parts, ".")
}

// Create creates a table:
//
//	err := s.Create("posts", func(t *migrate.Table) {
//		t.ID()
//		t.ForeignID("author_id").Constrained().CascadeOnDelete()
//		t.String("title", 200)
//		t.Text("body")
//		t.Timestamps()
//		t.SoftDeletes()
//		t.Index("author_id", "created_at")
//	})
func (s *Schema) Create(table string, build func(t *Table)) error {
	if err := checkName(table); err != nil {
		return err
	}
	t := &Table{name: table, creating: true}
	build(t)
	stmts, err := s.createSQL(t)
	if err != nil {
		return err
	}
	if t.unsearch {
		return fmt.Errorf("migrate: %s: DropSearchIndex belongs in Alter", table)
	}
	if t.search != nil {
		if err := s.checkSearch(); err != nil {
			return err
		}
		more, err := s.searchSQL(table, t.search, s.d.SearchConfig(), false)
		if err != nil {
			return err
		}
		stmts = append(stmts, more...)
	}
	return s.run(stmts)
}

// Alter changes a table: new columns, Change()d columns, indexes,
// foreign keys, and DropColumn, RenameColumn, DropIndex, DropUnique,
// DropForeign.
func (s *Schema) Alter(table string, build func(t *Table)) error {
	if err := checkName(table); err != nil {
		return err
	}
	t := &Table{name: table}
	build(t)
	stmts, err := s.alterSQL(t)
	if err != nil {
		return err
	}
	touches := slices.ContainsFunc(t.alters, func(a alteration) bool { return a.kind == "dropColumn" || a.kind == "renameColumn" })
	if t.unsearch || t.search != nil || touches {
		ix, has, err := s.searchIndex(table)
		if err != nil {
			return err
		}
		if has && !t.unsearch {
			for _, a := range t.alters {
				if (a.kind == "dropColumn" || a.kind == "renameColumn") && slices.ContainsFunc(ix.Columns, func(c string) bool { return strings.EqualFold(c, a.a) }) {
					return fmt.Errorf("migrate: %s.%s is in the search index: DropSearchIndex in the same Alter (and SearchIndex again without it, or with its new name)", table, a.a)
				}
			}
		}
		switch {
		case t.unsearch && !has:
			return fmt.Errorf("migrate: %s has no search index to drop", table)
		case t.unsearch:
			stmts = append(s.dropSearchSQL(ix), stmts...) // before its columns go
		case has:
			return fmt.Errorf("migrate: %s already has a search index: DropSearchIndex first", table)
		}
		if t.search != nil {
			if err := s.checkSearch(); err != nil {
				return err
			}
			more, err := s.searchSQL(table, t.search, s.d.SearchConfig(), true)
			if err != nil {
				return err
			}
			stmts = append(stmts, more...)
		}
	}
	return s.run(stmts)
}

// Drop drops a table, and its search index.
func (s *Schema) Drop(table string) error {
	if err := checkName(table); err != nil {
		return err
	}
	stmts, err := s.dropSearchWithTable(table)
	if err != nil {
		return err
	}
	return s.run(append(stmts, "DROP TABLE "+s.q(table)))
}

// DropIfExists drops a table if it exists, and its search index.
func (s *Schema) DropIfExists(table string) error {
	if err := checkName(table); err != nil {
		return err
	}
	stmts, err := s.dropSearchWithTable(table)
	if err != nil {
		return err
	}
	return s.run(append(stmts, "DROP TABLE IF EXISTS "+s.q(table)))
}

// dropSearchWithTable returns the statements that remove table's search
// index along with the table: its record, and on SQLite its FTS5 table
// (columns and indexes go with the table elsewhere).
func (s *Schema) dropSearchWithTable(table string) ([]string, error) {
	ix, has, err := s.searchIndex(table)
	if err != nil || !has {
		return nil, err
	}
	if s.dialect == "sqlite" {
		return s.dropSearchSQL(ix), nil
	}
	return []string{"DELETE FROM " + s.q(db.SearchIndexesTable) + " WHERE " + s.q("table_name") + " = " + sqlString(table)}, nil
}

// Rename renames a table.
// Indexes and foreign keys keep their names, which contain the old table
// name; drop them with DropIndexNamed and DropForeignNamed.
func (s *Schema) Rename(from, to string) error {
	if err := errors.Join(checkName(from), checkName(to)); err != nil {
		return err
	}
	_, has, err := s.searchIndex(from)
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("migrate: %s %w", from, errRenameSearch)
	}
	return s.run([]string{"ALTER TABLE " + s.q(from) + " RENAME TO " + s.q(to)})
}

// HasTable reports whether a table exists.
func (s *Schema) HasTable(table string) (bool, error) {
	if err := checkName(table); err != nil {
		return false, err
	}
	var q string
	switch s.dialect {
	case "postgres":
		q = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?"
	case "mysql":
		q = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
	default:
		q = "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	}
	n, err := db.RawFirst[int64](s.ctx, q, table)
	return n > 0, err
}

// HasColumn reports whether a table has a column.
func (s *Schema) HasColumn(table, column string) (bool, error) {
	if err := checkName(table); err != nil {
		return false, err
	}
	var q string
	switch s.dialect {
	case "postgres":
		q = "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?"
	case "mysql":
		q = "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?"
	default:
		q = "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?"
	}
	n, err := db.RawFirst[int64](s.ctx, q, table, column)
	return n > 0, err
}

// ---- DDL ----

func (s *Schema) sqlType(c *Column) string {
	switch c.typ {
	case typeID:
		switch s.dialect {
		case "postgres":
			return "BIGSERIAL PRIMARY KEY"
		case "mysql":
			return "BIGINT AUTO_INCREMENT PRIMARY KEY"
		}
		return "INTEGER PRIMARY KEY AUTOINCREMENT"
	case typeString:
		return "VARCHAR(" + strconv.Itoa(c.length) + ")"
	case typeText:
		return "TEXT"
	case typeLongText:
		if s.dialect == "mysql" {
			return "LONGTEXT"
		}
		return "TEXT"
	case typeInteger:
		if s.dialect == "mysql" {
			return "INT"
		}
		return "INTEGER"
	case typeBigInteger:
		return "BIGINT"
	case typeSmallInteger:
		return "SMALLINT"
	case typeBoolean:
		return "BOOLEAN"
	case typeFloat:
		switch s.dialect {
		case "postgres":
			return "DOUBLE PRECISION"
		case "mysql":
			return "DOUBLE"
		}
		return "REAL"
	case typeDecimal:
		switch s.dialect {
		case "mysql":
			return fmt.Sprintf("DECIMAL(%d, %d)", c.precision, c.scale)
		case "postgres":
			return fmt.Sprintf("NUMERIC(%d, %d)", c.precision, c.scale)
		}
		return "TEXT" // SQLite's NUMERIC would round through float64

	case typeDate:
		return "DATE"
	case typeTimestamp:
		switch s.dialect {
		case "postgres":
			return "TIMESTAMPTZ"
		case "mysql":
			return "DATETIME(6)"
		}
		return "DATETIME"
	case typeJSON:
		switch s.dialect {
		case "postgres":
			return "JSONB"
		case "mysql":
			return "JSON"
		}
		return "TEXT"
	case typeBinary:
		switch s.dialect {
		case "postgres":
			return "BYTEA"
		case "mysql":
			return "LONGBLOB"
		}
		return "BLOB"
	case typeUUID:
		switch s.dialect {
		case "postgres":
			return "UUID"
		case "mysql":
			return "CHAR(36)"
		}
		return "TEXT"
	}
	panic("migrate: unknown column type")
}

// columnSQL is a column definition: name, type, NULL-ness and default.
func (s *Schema) columnSQL(c *Column) (string, error) {
	var b strings.Builder
	b.WriteString(s.q(c.name) + " " + s.sqlType(c))
	if c.typ == typeID {
		return b.String(), nil
	}
	if c.nullable {
		b.WriteString(" NULL")
	} else {
		b.WriteString(" NOT NULL")
	}
	if c.hasDefault {
		if !c.nullable && !c.useCurrent && c.defRaw == "" && c.def == nil {
			return "", errors.New("a NOT NULL column can't default to NULL; make it Nullable")
		}
		def, err := s.defaultSQL(c)
		if err != nil {
			return "", err
		}
		b.WriteString(" DEFAULT " + def)
	}
	return b.String(), nil
}

func (s *Schema) defaultSQL(c *Column) (string, error) {
	switch {
	case c.useCurrent:
		if s.dialect == "mysql" && c.typ == typeTimestamp {
			return "CURRENT_TIMESTAMP(6)", nil
		}
		return "CURRENT_TIMESTAMP", nil
	case c.defRaw != "":
		return c.defRaw, nil
	}
	return literal(s.dialect, c.def)
}

// literal renders a default value as SQL.
func literal(dialect string, v any) (string, error) {
	if v == nil {
		return "NULL", nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		str := strings.ReplaceAll(rv.String(), "'", "''")
		if dialect == "mysql" {
			str = strings.ReplaceAll(str, `\`, `\\`)
		}
		return "'" + str + "'", nil
	case reflect.Bool:
		if rv.Bool() {
			return "TRUE", nil
		}
		return "FALSE", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", errors.New("migrate: a default can't be NaN or infinite")
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	}
	return "", fmt.Errorf("migrate: can't use a %T as a default; use DefaultRaw", v)
}

func (s *Schema) indexSQL(table string, ix index) string {
	kind := "INDEX"
	if ix.unique {
		kind = "UNIQUE INDEX"
	}
	return fmt.Sprintf("CREATE %s %s ON %s (%s)", kind, s.q(ix.name), s.q(table), s.list(ix.columns))
}

func (s *Schema) foreignSQL(f *Foreign) (string, error) {
	if f.table == "" {
		return "", fmt.Errorf("migrate: foreign key %s has no References", f.name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)", s.q(f.name), s.list(f.columns), s.q(f.table), s.list(f.refs))
	if f.onDelete != "" {
		b.WriteString(" ON DELETE " + string(f.onDelete))
	}
	if f.onUpdate != "" {
		b.WriteString(" ON UPDATE " + string(f.onUpdate))
	}
	return b.String(), nil
}

func (s *Schema) list(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = s.q(n)
	}
	return strings.Join(q, ", ")
}

func (s *Schema) createSQL(t *Table) ([]string, error) {
	if len(t.errs) > 0 {
		return nil, errors.Join(t.errs...)
	}
	if len(t.cols) == 0 {
		return nil, fmt.Errorf("migrate: table %s has no columns", t.name)
	}
	if len(t.alters) > 0 {
		return nil, fmt.Errorf("migrate: %s: Drop and Rename operations belong in Alter, not Create", t.name)
	}
	var defs []string
	hasID := false
	for _, c := range t.cols {
		if c.change {
			return nil, fmt.Errorf("migrate: %s.%s: Change belongs in Alter", t.name, c.name)
		}
		def, err := s.columnSQL(c)
		if err != nil {
			return nil, fmt.Errorf("migrate: %s.%s: %w", t.name, c.name, err)
		}
		defs = append(defs, def)
		hasID = hasID || c.typ == typeID
	}
	if len(t.primary) > 0 {
		if hasID {
			return nil, fmt.Errorf("migrate: %s has both ID() and Primary()", t.name)
		}
		defs = append(defs, "PRIMARY KEY ("+s.list(t.primary)+")")
	}
	for _, f := range t.foreigns {
		fk, err := s.foreignSQL(f)
		if err != nil {
			return nil, err
		}
		defs = append(defs, fk)
	}
	stmts := []string{"CREATE TABLE " + s.q(t.name) + " (\n\t" + strings.Join(defs, ",\n\t") + "\n)"}
	for _, ix := range t.indexes {
		stmts = append(stmts, s.indexSQL(t.name, ix))
	}
	return stmts, nil
}

func (s *Schema) alterSQL(t *Table) ([]string, error) {
	if len(t.errs) > 0 {
		return nil, errors.Join(t.errs...)
	}
	if len(t.primary) > 0 {
		return nil, fmt.Errorf("migrate: %s: changing the primary key isn't supported; use Exec", t.name)
	}
	table := s.q(t.name)
	var stmts []string
	for _, a := range t.alters {
		switch a.kind {
		case "dropColumn":
			stmts = append(stmts, "ALTER TABLE "+table+" DROP COLUMN "+s.q(a.a))
		case "renameColumn":
			stmts = append(stmts, "ALTER TABLE "+table+" RENAME COLUMN "+s.q(a.a)+" TO "+s.q(a.b))
		case "dropIndex":
			if s.dialect == "mysql" {
				stmts = append(stmts, "DROP INDEX "+s.q(a.a)+" ON "+table)
			} else {
				stmts = append(stmts, "DROP INDEX "+s.q(a.a))
			}
		case "dropForeign":
			switch s.dialect {
			case "postgres":
				stmts = append(stmts, "ALTER TABLE "+table+" DROP CONSTRAINT "+s.q(a.a))
			case "mysql":
				stmts = append(stmts, "ALTER TABLE "+table+" DROP FOREIGN KEY "+s.q(a.a))
			default:
				return nil, errors.New("migrate: SQLite can't drop foreign keys from a table; recreate the table")
			}
		}
	}
	for _, c := range t.cols {
		if c.typ == typeID && (s.dialect == "sqlite" || c.change) {
			return nil, fmt.Errorf("migrate: %s: can't add or change an ID column here; recreate the table", t.name)
		}
		def, err := s.columnSQL(c)
		if err != nil {
			return nil, fmt.Errorf("migrate: %s.%s: %w", t.name, c.name, err)
		}
		if !c.change {
			if !c.nullable && !c.hasDefault && c.typ != typeID {
				// PostgreSQL and SQLite refuse this on a table with rows;
				// MySQL silently fills in zero values. Fail everywhere.
				return nil, fmt.Errorf("migrate: %s.%s: a column added to an existing table needs Nullable() or a Default", t.name, c.name)
			}
			if s.dialect == "sqlite" && c.useCurrent {
				return nil, fmt.Errorf("migrate: %s.%s: SQLite can't add a column defaulting to the current time; add it Nullable, or recreate the table", t.name, c.name)
			}
			stmts = append(stmts, "ALTER TABLE "+table+" ADD COLUMN "+def)
			continue
		}
		switch s.dialect {
		case "mysql":
			stmts = append(stmts, "ALTER TABLE "+table+" MODIFY COLUMN "+def)
		case "postgres":
			col := s.q(c.name)
			// The old default may not convert to the new type: drop it
			// first, and set the new one last.
			stmts = append(stmts, "ALTER TABLE "+table+" ALTER COLUMN "+col+" DROP DEFAULT")
			typ := "ALTER TABLE " + table + " ALTER COLUMN " + col + " TYPE " + s.sqlType(c)
			if c.using != "" {
				typ += " USING " + c.using
			}
			stmts = append(stmts, typ)
			if c.nullable {
				stmts = append(stmts, "ALTER TABLE "+table+" ALTER COLUMN "+col+" DROP NOT NULL")
			} else {
				stmts = append(stmts, "ALTER TABLE "+table+" ALTER COLUMN "+col+" SET NOT NULL")
			}
			if c.hasDefault {
				d, err := s.defaultSQL(c)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, "ALTER TABLE "+table+" ALTER COLUMN "+col+" SET DEFAULT "+d)
			}
		default:
			return nil, fmt.Errorf("migrate: %s.%s: SQLite can't change columns; create a new table and copy the rows", t.name, c.name)
		}
	}
	for _, f := range t.foreigns {
		if s.dialect == "sqlite" {
			return nil, errors.New("migrate: SQLite can't add foreign keys to an existing table; declare them in Create")
		}
		fk, err := s.foreignSQL(f)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, "ALTER TABLE "+table+" ADD "+fk)
	}
	for _, ix := range t.indexes {
		stmts = append(stmts, s.indexSQL(t.name, ix))
	}
	if len(stmts) == 0 && t.search == nil && !t.unsearch {
		return nil, fmt.Errorf("migrate: Alter(%s) changes nothing", t.name)
	}
	return stmts, nil
}

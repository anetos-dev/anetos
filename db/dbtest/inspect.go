// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

func init() {
	extra = append(extra, test{"Inspect", testInspect})
}

// testInspect describes the schema builder's tables as the database has
// them, and db:schema and migrate:status print them.
func testInspect(t *testing.T, ctx context.Context) {
	dropMigrationTables(t, ctx)
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	check(t, createOwnersAndItems(s))
	r := runner(t, ctx, anetos.Development)
	tables, err := r.Inspect(ctx, "st_m_items", "st_m_owners")
	check(t, err)
	if len(tables) != 2 || tables[0].Name != "st_m_items" || tables[0].Kind != "table" {
		t.Fatalf("Inspect = %+v", tables)
	}
	items, owners := tables[0], tables[1]
	col := func(tb migrate.TableInfo, name string) migrate.ColumnInfo {
		t.Helper()
		i := slices.IndexFunc(tb.Columns, func(c migrate.ColumnInfo) bool { return c.Name == name })
		if i < 0 {
			t.Fatalf("%s has no column %s: %+v", tb.Name, name, tb.Columns)
		}
		return tb.Columns[i]
	}
	if c := col(items, "id"); !c.PrimaryKey || c.Nullable {
		t.Errorf("id: %+v", c)
	}
	if c := col(items, "owner_id"); !c.Nullable || c.PrimaryKey {
		t.Errorf("owner_id: %+v", c)
	}
	if c := col(items, "name"); c.Nullable || !strings.Contains(strings.ToLower(c.Type), "char") {
		t.Errorf("name: %+v", c)
	}
	if c := col(items, "ref"); !strings.Contains(c.Default, "'none'") {
		t.Errorf("ref's default: %+v", c)
	}
	if c := col(items, "qty"); !strings.Contains(c.Default, "1") {
		t.Errorf("qty's default: %+v", c)
	}
	if c := col(items, "body"); c.Default != "" {
		t.Errorf("body has a default: %+v", c)
	}
	if len(items.Columns) != 16 || items.Columns[0].Name != "id" || items.Columns[15].Name != "created_at" {
		t.Errorf("columns, in order: %+v", items.Columns)
	}
	hasIndex := func(tb migrate.TableInfo, unique bool, cols ...string) bool {
		return slices.ContainsFunc(tb.Indexes, func(ix migrate.IndexInfo) bool {
			return slices.Equal(ix.Columns, cols) && ix.Unique == unique && !ix.Primary
		})
	}
	if !hasIndex(items, true, "code") || !hasIndex(items, false, "owner_id", "created_at") || !hasIndex(owners, true, "name") {
		t.Errorf("indexes: %+v / %+v", items.Indexes, owners.Indexes)
	}
	if !slices.ContainsFunc(items.Indexes, func(ix migrate.IndexInfo) bool { return ix.Primary && slices.Equal(ix.Columns, []string{"id"}) }) {
		t.Errorf("no primary key index: %+v", items.Indexes)
	}
	if fk := items.ForeignKeys; len(fk) != 1 || !slices.Equal(fk[0].Columns, []string{"owner_id"}) || fk[0].Table != "st_m_owners" ||
		!slices.Equal(fk[0].References, []string{"id"}) || fk[0].OnDelete != "set null" {
		t.Errorf("foreign keys: %+v", fk)
	}
	if len(owners.ForeignKeys) != 0 {
		t.Errorf("owners' foreign keys: %+v", owners.ForeignKeys)
	}
	if _, err := r.Inspect(ctx, "st_nope"); err == nil || !strings.Contains(err.Error(), `no table "st_nope"`) {
		t.Errorf("an unknown table: %v", err)
	}
	all, err := r.Inspect(ctx)
	check(t, err)
	if !slices.ContainsFunc(all, func(tb migrate.TableInfo) bool { return tb.Name == "st_m_owners" }) ||
		slices.ContainsFunc(all, func(tb migrate.TableInfo) bool { return strings.HasPrefix(tb.Name, "sqlite_") }) {
		t.Errorf("every table: %v", all)
	}

	testInspectExtras(t, ctx, r)

	var out bytes.Buffer
	if _, err := runCommand(ctx, r, []string{"db:schema", "st_m_items"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "st_m_items\n") || !strings.Contains(got, "primary key") ||
		!strings.Contains(got, "foreign key (owner_id) references st_m_owners (id), on delete set null") || !strings.Contains(got, "(code) unique") {
		t.Errorf("db:schema:\n%s", got)
	}
	out.Reset()
	if _, err := runCommand(ctx, r, []string{"db:schema", "st_m_owners", "--json"}, &out); err != nil { // flags after a table too
		t.Fatal(err)
	}
	var js []migrate.TableInfo
	if err := json.Unmarshal(out.Bytes(), &js); err != nil || len(js) != 1 || js[0].Name != "st_m_owners" {
		t.Errorf("db:schema --json: %v\n%s", err, out.String())
	}
	out.Reset()
	if _, err := runCommand(ctx, r, []string{"migrate:status", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("migrate:status --json without migrations: %s", got)
	}
}

// testInspectExtras: expression and partial indexes, generated and
// identity columns, a foreign key to the parent's implicit key.
func testInspectExtras(t *testing.T, ctx context.Context, r *migrate.Runner) {
	dialect := d(ctx).Dialect().Name()
	var maria bool
	if dialect == "mysql" {
		var v string
		check(t, d(ctx).SQL().QueryRowContext(ctx, "SELECT VERSION()").Scan(&v))
		maria = strings.Contains(strings.ToLower(v), "mariadb")
	}
	var stmts []string
	switch dialect {
	case "sqlite":
		stmts = []string{
			`CREATE TABLE st_m_inspect (id INTEGER PRIMARY KEY, email TEXT, deleted_at TEXT, owner_id INTEGER REFERENCES st_m_owners,
				upper_email TEXT GENERATED ALWAYS AS (upper(email)) STORED)`,
			`CREATE UNIQUE INDEX st_m_inspect_email ON st_m_inspect (lower(email))`,
			`CREATE INDEX st_m_inspect_live ON st_m_inspect (owner_id, email) WHERE deleted_at IS NULL`,
		}
	case "postgres":
		stmts = []string{
			`CREATE TABLE st_m_inspect (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, email text, deleted_at timestamp,
				owner_id bigint REFERENCES st_m_owners, upper_email text GENERATED ALWAYS AS (upper(email)) STORED)`,
			`CREATE UNIQUE INDEX st_m_inspect_email ON st_m_inspect (lower(email))`,
			`CREATE INDEX st_m_inspect_live ON st_m_inspect (owner_id, email) WHERE deleted_at IS NULL`,
			`CREATE INDEX st_m_inspect_incl ON st_m_inspect (owner_id) INCLUDE (deleted_at)`,
		}
	default:
		stmts = []string{
			`CREATE TABLE st_m_inspect (id bigint AUTO_INCREMENT PRIMARY KEY, email varchar(100), deleted_at datetime NULL,
				owner_id bigint NULL, note varchar(20) NOT NULL DEFAULT '', upper_email varchar(100) GENERATED ALWAYS AS (upper(email)) STORED,
				FOREIGN KEY (owner_id) REFERENCES st_m_owners (id))`,
		}
		if !maria {
			stmts = append(stmts, `CREATE UNIQUE INDEX st_m_inspect_email ON st_m_inspect ((lower(email)))`)
		}
	}
	for _, q := range stmts {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	tables, err := r.Inspect(ctx, "st_m_inspect")
	check(t, err)
	tb := tables[0]
	byName := map[string]migrate.ColumnInfo{}
	for _, c := range tb.Columns {
		byName[c.Name] = c
	}
	if c := byName["upper_email"]; c.Generated == "" || c.Default != "" {
		t.Errorf("a generated column: %+v", c)
	}
	if c := byName["id"]; dialect != "sqlite" && !c.Identity {
		t.Errorf("an identity column: %+v", c)
	}
	if c := byName["note"]; dialect == "mysql" && c.Default != "''" {
		t.Errorf("an empty text default: %+v", c)
	}
	if fk := tb.ForeignKeys; len(fk) != 1 || !slices.Equal(fk[0].References, []string{"id"}) {
		t.Errorf("a foreign key to the parent's key: %+v", fk)
	}
	index := func(name string) migrate.IndexInfo {
		t.Helper()
		i := slices.IndexFunc(tb.Indexes, func(ix migrate.IndexInfo) bool { return ix.Name == name })
		if i < 0 {
			t.Fatalf("no index %s: %+v", name, tb.Indexes)
		}
		return tb.Indexes[i]
	}
	if dialect != "mysql" || !maria {
		if ix := index("st_m_inspect_email"); !ix.Unique || len(ix.Columns) != 1 || !strings.Contains(strings.ToLower(ix.Columns[0]), "lower") {
			t.Errorf("an expression index: %+v", ix)
		}
	}
	if dialect != "mysql" {
		if ix := index("st_m_inspect_live"); !slices.Equal(ix.Columns, []string{"owner_id", "email"}) || !strings.Contains(strings.ToLower(ix.Where), "deleted_at is null") {
			t.Errorf("a partial index: %+v", ix)
		}
	}
	if dialect == "postgres" {
		if ix := index("st_m_inspect_incl"); !slices.Equal(ix.Columns, []string{"owner_id"}) {
			t.Errorf("INCLUDE's columns are listed: %+v", ix)
		}
	}
	// A whole database with such indexes is described too.
	if _, err := r.Inspect(ctx); err != nil {
		t.Errorf("Inspect(): %v", err)
	}
}

// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos/db"
)

func schemaFor(dialect db.Dialect) *Schema {
	return &Schema{d: db.New(nil, dialect), dialect: dialect.Name()}
}

func postsTable(t *Table) {
	t.ID()
	t.ForeignID("author_id").Constrained().CascadeOnDelete()
	t.String("title", 200)
	t.String("slug", 0).Unique()
	t.Text("body").Nullable()
	t.Integer("views").Default(0)
	t.Boolean("published").Default(false)
	t.Decimal("price", 10, 2).Default(9.5)
	t.JSON("meta").Nullable()
	t.String("status", 20).Default("it's draft")
	t.Timestamps()
	t.SoftDeletes()
	t.Index("author_id", "created_at")
}

func TestCreateSQL(t *testing.T) {
	want := map[string][]string{
		"postgres": {
			`CREATE TABLE "posts" (
	"id" BIGSERIAL PRIMARY KEY,
	"author_id" BIGINT NOT NULL,
	"title" VARCHAR(200) NOT NULL,
	"slug" VARCHAR(255) NOT NULL,
	"body" TEXT NULL,
	"views" INTEGER NOT NULL DEFAULT 0,
	"published" BOOLEAN NOT NULL DEFAULT FALSE,
	"price" NUMERIC(10, 2) NOT NULL DEFAULT 9.5,
	"meta" JSONB NULL,
	"status" VARCHAR(20) NOT NULL DEFAULT 'it''s draft',
	"created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	"updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	"deleted_at" TIMESTAMPTZ NULL,
	CONSTRAINT "posts_author_id_foreign" FOREIGN KEY ("author_id") REFERENCES "authors" ("id") ON DELETE CASCADE
)`,
			`CREATE UNIQUE INDEX "posts_slug_unique" ON "posts" ("slug")`,
			`CREATE INDEX "posts_author_id_created_at_index" ON "posts" ("author_id", "created_at")`,
		},
	}
	got, err := schemaFor(db.Postgres()).createSQL(build("posts", true, postsTable))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want["postgres"]) {
		t.Errorf("postgres:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want["postgres"], "\n"))
	}

	my, err := schemaFor(db.MySQL()).createSQL(build("posts", true, postsTable))
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{"`id` BIGINT AUTO_INCREMENT PRIMARY KEY", "`meta` JSON NULL", "DECIMAL(10, 2)",
		"`created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)", "`views` INT NOT NULL"} {
		if !strings.Contains(my[0], frag) {
			t.Errorf("mysql lacks %q:\n%s", frag, my[0])
		}
	}
	if strings.Contains(my[0], "CHARSET") {
		t.Errorf("a utf8mb4 database's table names no character set:\n%s", my[0])
	}
	// A database whose default isn't utf8mb4 (D254).
	latin := schemaFor(db.MySQL())
	latin.tableCharset, latin.charsetRead = mysqlTableCharset, true
	if my, err := latin.createSQL(build("posts", true, postsTable)); err != nil || !strings.HasSuffix(my[0], "\n) DEFAULT CHARSET=utf8mb4") {
		t.Errorf("latin1 database: %v\n%s", err, strings.Join(my, "\n"))
	}
	lite, err := schemaFor(db.SQLite()).createSQL(build("posts", true, postsTable))
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{`"id" INTEGER PRIMARY KEY AUTOINCREMENT`, `"meta" TEXT NULL`, `"created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`} {
		if !strings.Contains(lite[0], frag) {
			t.Errorf("sqlite lacks %q:\n%s", frag, lite[0])
		}
	}
}

func build(name string, creating bool, fn func(*Table)) *Table {
	t := &Table{name: name, creating: creating}
	fn(t)
	return t
}

func TestMySQLStringDefaultsEscapeBackslashes(t *testing.T) {
	got, _ := literal("mysql", `a\'b`)
	if got != `'a\\''b'` {
		t.Errorf("literal = %s", got)
	}
	if _, err := literal("postgres", struct{}{}); err == nil {
		t.Error("struct default accepted")
	}
}

func TestAlterSQL(t *testing.T) {
	alter := func(t *Table) {
		t.String("subtitle", 100).Nullable()
		t.String("title", 300).Change()
		t.DropColumn("legacy")
		t.RenameColumn("body", "content")
		t.DropIndex("author_id", "created_at")
		t.DropUnique("slug")
		t.DropForeign("author_id")
		t.Index("subtitle")
	}
	pg, err := schemaFor(db.Postgres()).alterSQL(build("posts", false, alter))
	if err != nil {
		t.Fatal(err)
	}
	wantPG := []string{
		`ALTER TABLE "posts" DROP COLUMN "legacy"`,
		`ALTER TABLE "posts" RENAME COLUMN "body" TO "content"`,
		`DROP INDEX "posts_author_id_created_at_index"`,
		`DROP INDEX "posts_slug_unique"`,
		`ALTER TABLE "posts" DROP CONSTRAINT "posts_author_id_foreign"`,
		`ALTER TABLE "posts" ADD COLUMN "subtitle" VARCHAR(100) NULL`,
		`ALTER TABLE "posts" ALTER COLUMN "title" DROP DEFAULT`,
		`ALTER TABLE "posts" ALTER COLUMN "title" TYPE VARCHAR(300)`,
		`ALTER TABLE "posts" ALTER COLUMN "title" SET NOT NULL`,
		`CREATE INDEX "posts_subtitle_index" ON "posts" ("subtitle")`,
	}
	if !slices.Equal(pg, wantPG) {
		t.Errorf("postgres:\n%s", strings.Join(pg, "\n"))
	}
	my, err := schemaFor(db.MySQL()).alterSQL(build("posts", false, alter))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"DROP INDEX `posts_slug_unique` ON `posts`", "ALTER TABLE `posts` DROP FOREIGN KEY `posts_author_id_foreign`",
		"ALTER TABLE `posts` MODIFY COLUMN `title` VARCHAR(300) NOT NULL"} {
		if !slices.Contains(my, s) {
			t.Errorf("mysql lacks %s:\n%s", s, strings.Join(my, "\n"))
		}
	}
	for name, fn := range map[string]func(*Table){
		"change":      func(t *Table) { t.String("x", 1).Change() },
		"dropForeign": func(t *Table) { t.DropForeign("a") },
		"addForeign":  func(t *Table) { t.ForeignID("a").References("b") },
		"useCurrent":  func(t *Table) { t.Timestamps() },
	} {
		if _, err := schemaFor(db.SQLite()).alterSQL(build("t", false, fn)); err == nil {
			t.Errorf("sqlite %s accepted", name)
		}
	}
}

func TestTableErrors(t *testing.T) {
	s := schemaFor(db.Postgres())
	for name, fn := range map[string]func(*Table){
		"no columns":   func(*Table) {},
		"bad decimal":  func(t *Table) { t.Decimal("x", 2, 3) },
		"negative len": func(t *Table) { t.String("x", -1) },
		"id+primary":   func(t *Table) { t.ID(); t.Primary("id") },
		"no refs":      func(t *Table) { t.BigInteger("a"); t.Foreign("a") },
		"drop in create": func(t *Table) {
			t.ID()
			t.DropColumn("x")
		},
		"bad default": func(t *Table) { t.String("a", 1).Default([]int{1}) },
	} {
		if _, err := s.createSQL(build("t", true, fn)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := s.alterSQL(build("t", false, func(*Table) {})); err == nil {
		t.Error("empty alter accepted")
	}
	got, err := s.createSQL(build("pivot", true, func(t *Table) {
		t.BigInteger("post_id")
		t.BigInteger("tag_id")
		t.Primary("post_id", "tag_id")
		t.Foreign("post_id").References("posts").CascadeOnDelete().OnUpdate(Restrict)
		t.ForeignID("category_id").Nullable().Constrained().NullOnDelete()
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{`PRIMARY KEY ("post_id", "tag_id")`, `ON DELETE CASCADE ON UPDATE RESTRICT`,
		`REFERENCES "categories" ("id") ON DELETE SET NULL`} {
		if !strings.Contains(got[0], frag) {
			t.Errorf("lacks %q:\n%s", frag, got[0])
		}
	}
}

func TestSplitStatements(t *testing.T) {
	sql := `
-- a comment; with a semicolon
CREATE TABLE a (x TEXT DEFAULT 'a;b');
/* block; */ INSERT INTO a VALUES ('it''s;');
CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql;
-- trailing comment only
`
	got := splitStatements(sql, "postgres")
	if len(got) != 3 || !strings.HasPrefix(got[2], "CREATE FUNCTION") || !strings.HasSuffix(got[2], "plpgsql") {
		t.Errorf("statements: %q", got)
	}
	my := splitStatements("SELECT 1; # comment;\nSELECT 'a\\';b';", "mysql")
	if len(my) != 2 || !strings.HasSuffix(my[1], `SELECT 'a\';b'`) {
		t.Errorf("mysql statements: %q", my)
	}
}

func TestSets(t *testing.T) {
	s := NewSet("app")
	s.AddFunc("2026_01_01_000000_a", func(*Schema) error { return nil }, nil)
	for name, fn := range map[string]func(){
		"duplicate": func() { s.AddFunc("2026_01_01_000000_a", nil, nil) },
		"spaces":    func() { s.AddFunc("has space", nil, nil) },
		"empty":     func() { s.AddFunc("", nil, nil) },
		"nil":       func() { s.Add("x", nil) },
		"bad set":   func() { NewSet("a b") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			fn()
		}()
	}
	if err := s.entries[0].m.Down(nil); !errors.Is(err, ErrIrreversible) {
		t.Errorf("nil down = %v", err)
	}

	fsys := fstest.MapFS{
		"sql/2026_02_01_000000_b.up.sql":   {Data: []byte("CREATE TABLE b (x INT);")},
		"sql/2026_02_01_000000_b.down.sql": {Data: []byte("DROP TABLE b;")},
		"sql/2026_02_02_000000_c.up.sql":   {Data: []byte("CREATE TABLE c (x INT);")},
	}
	if err := s.AddFS(fsys, "sql"); err != nil {
		t.Fatal(err)
	}
	if len(s.entries) != 3 {
		t.Fatalf("entries = %d", len(s.entries))
	}
	if err := s.entries[2].m.Down(nil); !errors.Is(err, ErrIrreversible) {
		t.Errorf("missing down = %v", err)
	}
	orphan := fstest.MapFS{"sql/x.down.sql": {Data: []byte("x")}}
	if err := NewSet("p").AddFS(orphan, "sql"); err == nil {
		t.Error("orphan down file accepted")
	}

	if _, err := NewRunner(db.New(nil, db.Postgres()), []*Set{s, NewSet("app")}); err == nil {
		t.Error("duplicate set names accepted")
	}
	if _, err := NewRunner(db.New(nil, db.Postgres()), nil, WithSeeders(Seeder{Name: "x"})); err == nil {
		t.Error("seeder without Run accepted")
	}
}

func TestSplitCompoundStatements(t *testing.T) {
	cases := []struct {
		dialect, sql string
		n            int
	}{
		{"sqlite", "CREATE TRIGGER t AFTER UPDATE ON a BEGIN UPDATE a SET x = 1; UPDATE a SET y = CASE WHEN 1 THEN 2 END; END; SELECT 1", 2},
		{"mysql", "CREATE PROCEDURE p() BEGIN IF 1 THEN SELECT 1; END IF; WHILE 0 DO SELECT 2; END WHILE; END; SELECT 3;", 2},
		{"postgres", "CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; SELECT 2; END; SELECT 3", 2},
		{"postgres", "BEGIN; SELECT 1; COMMIT;", 3},
		{"postgres", "SELECT E'it\\'s; ok'; SELECT 2", 2},
		{"postgres", "SELECT /* a /* nested; */ still; */ 1; SELECT 2", 2},
		{"mysql", `SELECT "it\"s; ok"; SELECT 2`, 2},
		{"mysql", "INSERT INTO t VALUES (1--1); SELECT 2", 2},
		{"postgres", "/* only a comment; */", 0},
	}
	for _, c := range cases {
		if got := splitStatements(c.sql, c.dialect); len(got) != c.n {
			t.Errorf("%s %q: %d statements %q, want %d", c.dialect, c.sql, len(got), got, c.n)
		}
	}
}

func TestLongIndexNames(t *testing.T) {
	a := indexName("subscription_notification_preferences", []string{"organization_member_id", "notification_channel_a"}, "index")
	b := indexName("subscription_notification_preferences", []string{"organization_member_id", "notification_channel_b"}, "index")
	if len(a) > maxIdent || len(b) > maxIdent || a == b {
		t.Errorf("names: %s (%d), %s (%d)", a, len(a), b, len(b))
	}
	if short := indexName("posts", []string{"slug"}, "unique"); short != "posts_slug_unique" {
		t.Errorf("short name changed: %s", short)
	}
}

func TestSearchSQL(t *testing.T) {
	cfg := db.SearchConfig{Language: "english", Ranking: "bm25"}
	for dialect, want := range map[db.Dialect][]string{
		db.Postgres(): {
			`ALTER TABLE "posts" ADD COLUMN "search_vector" tsvector GENERATED ALWAYS AS (setweight(to_tsvector('english', coalesce("title"::text, '')), 'A') || setweight(to_tsvector('english', coalesce("body"::text, '')), 'B')) STORED`,
			`CREATE INDEX "posts_search_index" ON "posts" USING gin ("search_vector")`,
			`ALTER TABLE "posts" ADD COLUMN "search_text" text GENERATED ALWAYS AS (coalesce("title"::text, '') || ' ' || coalesce("body"::text, '')) STORED`,
			`CREATE INDEX "posts_search_bm25" ON "posts" USING bm25 ("search_text") WITH (text_config = 'english')`,
		},
		db.MySQL(): {
			"ALTER TABLE `posts` ADD COLUMN `search_text` LONGTEXT GENERATED ALWAYS AS (CONCAT_WS(' ', `title`, `body`)) STORED",
			"ALTER TABLE `posts` ADD FULLTEXT INDEX `posts_search_index` (`search_text`)",
		},
		db.SQLite(): {
			`CREATE VIRTUAL TABLE "posts_search" USING fts5("title", "body", content='posts', tokenize='porter unicode61 remove_diacritics 2')`,
			`INSERT INTO "posts_search" ("posts_search", rank) VALUES ('rank', 'bm25(1.0, 0.4)')`,
			`CREATE TRIGGER "posts_search_insert" AFTER INSERT ON "posts" BEGIN INSERT INTO "posts_search" (rowid, "title", "body") VALUES (new.rowid, new."title", new."body"); END`,
			`CREATE TRIGGER "posts_search_update" AFTER UPDATE OF "title", "body" ON "posts" BEGIN INSERT INTO "posts_search" ("posts_search", rowid, "title", "body") VALUES ('delete', old.rowid, old."title", old."body"); INSERT INTO "posts_search" (rowid, "title", "body") VALUES (new.rowid, new."title", new."body"); END`,
			`INSERT INTO "posts_search" ("posts_search") VALUES ('rebuild')`,
		},
	} {
		got, err := schemaFor(dialect).searchSQL("posts", []string{"title", "body"}, cfg, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("%s: missing\n%s\nin\n%s", dialect.Name(), w, strings.Join(got, "\n"))
			}
		}
		if last := got[len(got)-1]; !strings.Contains(last, "INSERT INTO") || !strings.Contains(last, "'posts', 'title,body', 'english', 'bm25'") {
			t.Errorf("%s: record: %s", dialect.Name(), last)
		}
	}
	s := schemaFor(db.SQLite())
	for _, cols := range [][]string{{"a", "a"}, {"a.b"}, {""}, {"a,b"}} {
		if _, err := s.searchSQL("posts", cols, cfg, false); err == nil {
			t.Errorf("columns %q: no error", cols)
		}
	}
	tbl := &Table{name: "posts"}
	tbl.SearchIndex()
	tbl.SearchIndex("a")
	tbl.SearchIndex("b")
	if len(tbl.errs) != 2 {
		t.Errorf("errors: %v", tbl.errs)
	}
}

func TestUniqueLiveSQL(t *testing.T) {
	table := func(t *Table) {
		t.ID()
		t.String("email", 100).UniqueLive()
		t.UniqueLive("team_id", "email")
		t.SoftDeletes()
	}
	for _, d := range []db.Dialect{db.Postgres(), db.SQLite()} {
		got, err := schemaFor(d).createSQL(build("users", true, table))
		if err != nil {
			t.Fatal(err)
		}
		q := func(s string) string { return d.QuoteIdent(s) }
		want := "CREATE UNIQUE INDEX " + q("users_email_unique") + " ON " + q("users") + " (" + q("email") + ") WHERE " + q("deleted_at") + " IS NULL"
		if got[1] != want || !strings.HasSuffix(got[2], " WHERE "+q("deleted_at")+" IS NULL") {
			t.Errorf("%s: %q", d.Name(), got[1:])
		}
	}
	if _, err := schemaFor(db.MySQL()).createSQL(build("users", true, table)); err == nil || !strings.Contains(err.Error(), "partial index") {
		t.Errorf("mysql: %v", err)
	}
}

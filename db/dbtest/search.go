// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

func init() {
	extra = append(extra, test{"Search", testSearch}, test{"SearchSettings", testSearchSettings})
	migrationTables = append(migrationTables, "st_s_articles", "st_s_renamed", stLongTable, db.SearchIndexesTable)
}

// stArticle is a searchable model.
type stArticle struct {
	db.Model
	db.SoftDeletes
	Title string `db:"title"`
	Body  string `db:"body"`
}

// TableName implements db.Tabler.
func (stArticle) TableName() string { return "st_s_articles" }

var articleBody = db.Col[string]("body")

// stLongTable is a table name whose search objects' names are too long
// for PostgreSQL and MySQL, and get shortened.
const stLongTable = "st_s_a_table_whose_name_is_long_enough_to_need_shortening_xx"

// stLong is a searchable model on stLongTable.
type stLong struct {
	db.Model
	Title string `db:"title"`
}

// TableName implements db.Tabler.
func (stLong) TableName() string { return stLongTable }

// testLongName checks a search index on stLongTable, with ctx's settings.
func testLongName(t *testing.T, ctx context.Context) {
	t.Helper()
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	_ = s.DropIfExists(stLongTable)
	check(t, s.Create(stLongTable, func(t *migrate.Table) {
		t.ID()
		t.String("title", 200)
		t.Timestamps()
		t.SearchIndex("title")
	}))
	check(t, db.Create(ctx, &stLong{Title: "a long table"}))
	check(t, db.Create(ctx, &stLong{Title: "tables"}))
	if got, err := db.Pluck(db.Query[stLong](ctx).Search("table"), title); err != nil || len(got) != 2 {
		t.Errorf("Search on a long table name = %q, %v", got, err)
	}
	check(t, s.Drop(stLongTable))
}

// createArticles creates st_s_articles with a search index, or without
// one when the index is to be added by an Alter.
func createArticles(t *testing.T, ctx context.Context, indexed bool) *migrate.Schema {
	t.Helper()
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	_ = s.DropIfExists("st_s_articles")
	check(t, s.Create("st_s_articles", func(t *migrate.Table) {
		t.ID()
		t.String("title", 200)
		t.Text("body").Nullable()
		t.Timestamps()
		t.SoftDeletes()
		if indexed {
			t.SearchIndex("title", "body")
		}
	}))
	return s
}

func seedArticles(t *testing.T, ctx context.Context) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, a := range []stArticle{
		{Title: "Go generics", Body: "Generics arrived in Go 1.18, running fast."},
		{Title: "Cooking rice", Body: "Rice and lentils, with no generics at all."},
		{Title: "Generic advice", Body: "Prefer small interfaces."},
		{Title: "বাংলা লেখা", Body: "আমি বাংলায় গান গাই"},
	} {
		check(t, db.Create(ctx, &a))
		ids[a.Title] = a.ID
	}
	return ids
}

func articleTitles(t *testing.T, q *db.Q[stArticle]) []string {
	t.Helper()
	rows, err := q.Get()
	check(t, err)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func testSearch(t *testing.T, ctx context.Context) {
	dialect := d(ctx).Dialect().Name()
	s := createArticles(t, ctx, true)
	ids := seedArticles(t, ctx)
	q := func() *db.Q[stArticle] { return db.Query[stArticle](ctx) }

	// Every word must match, as a prefix, in any indexed column; title
	// matches (the first column) rank first, except on MySQL, which
	// weighs columns alike.
	got := articleTitles(t, q().Search("generic"))
	if !sameSet(got, []string{"Go generics", "Cooking rice", "Generic advice"}) {
		t.Fatalf("Search(generic) = %q", got)
	}
	if dialect != "mysql" && got[2] != "Cooking rice" {
		t.Errorf("Search(generic) order = %q: the body-only match should be last", got)
	}
	got = articleTitles(t, q().Search("  Go, GENERICS!  "))
	switch dialect {
	case "mysql":
		// "go" is too short to be indexed: it only matches longer words,
		// and is optional next to "generics".
		if !sameSet(got, []string{"Go generics", "Cooking rice"}) {
			t.Errorf("MySQL Search(go generics) = %q", got)
		}
		if got := articleTitles(t, q().Search("the generics")); !sameSet(got, []string{"Go generics", "Cooking rice"}) {
			t.Errorf("MySQL Search(the generics) = %q: the stop word is optional", got)
		}
	default:
		if !slices.Equal(got, []string{"Go generics"}) {
			t.Errorf("Search(go generics) = %q", got)
		}
	}
	// A word shorter than three letters matches whole words only ("go",
	// not "lentils" for "le"): as a prefix it could expand to most of the
	// index. MySQL doesn't index such words, so there, alone, it matches
	// as a prefix of longer ones.
	if got := articleTitles(t, q().Search("go")); dialect != "mysql" && !slices.Contains(got, "Go generics") {
		t.Errorf("Search(go) = %q", got)
	}
	want := []string{}
	if dialect == "mysql" {
		want = []string{"Cooking rice"}
	}
	if got := articleTitles(t, q().Search("le")); !slices.Equal(got, want) {
		t.Errorf("Search(le) = %q, want %q", got, want)
	}
	// Scripts with combining marks, in the simple language.
	if got := articleTitles(t, q().Search("বাংলা")); !slices.Equal(got, []string{"বাংলা লেখা"}) {
		t.Errorf("Search(বাংলা) = %q", got)
	}
	if got := articleTitles(t, q().Search("nothing-like-this")); len(got) != 0 {
		t.Errorf("Search(no match) = %q", got)
	}
	// Text without words leaves the query alone; a second Search replaces
	// the first.
	if got := articleTitles(t, q().Search(" ?! ").OrderBy(id.Asc())); len(got) != 4 || got[0] != "Go generics" {
		t.Errorf("Search(no words) = %q", got)
	}
	if got := articleTitles(t, q().Search("rice").Search("advice")); !slices.Equal(got, []string{"Generic advice"}) {
		t.Errorf("Search twice = %q", got)
	}

	// With conditions, counts, pages, aggregates and tie-breaking order.
	if got := articleTitles(t, q().Where(title.Ne("Go generics")).Search("generics")); !slices.Equal(got, []string{"Cooking rice"}) {
		t.Errorf("Where + Search = %q", got)
	}
	n, err := q().Search("generic").Count()
	check(t, err)
	page, err := q().Search("generic").OrderBy(id.Desc()).Paginate(1, 2)
	check(t, err)
	if n != 3 || page.Total != 3 || len(page.Data) != 2 || page.LastPage != 2 {
		t.Errorf("Count = %d, Paginate = %+v", n, page)
	}
	if ok, err := q().Search("lentils").Exists(); err != nil || !ok {
		t.Errorf("Exists = %v, %v", ok, err)
	}
	if got, err := db.Pluck(q().Search("lentils"), title); err != nil || !slices.Equal(got, []string{"Cooking rice"}) {
		t.Errorf("Pluck = %q, %v", got, err)
	}
	if got, err := db.Max(q().Search("generic"), id); err != nil || got != ids["Generic advice"] {
		t.Errorf("Max = %d, %v", got, err)
	}
	// Distinct and grouped queries have no relevance order, but search.
	if got := articleTitles(t, q().Distinct().Search("generic")); !sameSet(got, []string{"Go generics", "Cooking rice", "Generic advice"}) {
		t.Errorf("Distinct + Search = %q", got)
	}
	if n, err := q().Distinct().Search("generic").Count(); err != nil || n != 3 {
		t.Errorf("Distinct + Search Count = %d, %v", n, err)
	}
	if ok, err := q().Distinct().Search("lentils").Exists(); err != nil || !ok {
		t.Errorf("Distinct + Search Exists = %v, %v", ok, err)
	}
	if page, err := q().Distinct().Search("generic").Paginate(1, 2); err != nil || page.Total != 3 || len(page.Data) != 2 {
		t.Errorf("Distinct + Search Paginate = %+v, %v", page, err)
	}
	type perTitle struct {
		Title string `db:"title"`
		N     int64  `db:"n"`
	}
	if got, err := db.Select[perTitle](q().Search("generic").GroupBy("title"), "title", "COUNT(*) AS n"); err != nil || len(got) != 3 {
		t.Errorf("GroupBy + Search = %+v, %v", got, err)
	}
	if n, err := q().Search("generic").GroupBy("title").Count(); err != nil || n != 3 {
		t.Errorf("GroupBy + Search Count = %d, %v", n, err)
	}
	if got := articleTitles(t, q().Search("generic").Limit(2)); len(got) != 2 || (dialect != "mysql" && got[0] == "Cooking rice") {
		t.Errorf("Search + Limit = %q", got)
	}

	// The index follows writes: updates, deletes, soft deletes.
	_, err = q().Where(id.Eq(ids["Cooking rice"])).Update(articleBody.Set("Rice and lentils."))
	check(t, err)
	_, err = q().Where(id.Eq(ids["Generic advice"])).Delete() // soft
	check(t, err)
	if got := articleTitles(t, q().Search("generic")); !slices.Equal(got, []string{"Go generics"}) {
		t.Errorf("after update and soft delete: %q", got)
	}
	if got := articleTitles(t, q().WithTrashed().Search("generic")); !sameSet(got, []string{"Go generics", "Generic advice"}) {
		t.Errorf("WithTrashed: %q", got)
	}
	_, err = q().WithTrashed().Where(id.Eq(ids["Generic advice"])).ForceDelete()
	check(t, err)
	if got := articleTitles(t, q().WithTrashed().Search("advice")); len(got) != 0 {
		t.Errorf("after delete: %q", got)
	}

	// What Search refuses.
	if _, err := q().Search("rice").Update(title.Set("x")); err == nil || !strings.Contains(err.Error(), "Search") {
		t.Errorf("Update with Search: %v", err)
	}
	if _, err := q().Search("rice").CursorPaginate("", 10); err == nil || !strings.Contains(err.Error(), "Paginate") {
		t.Errorf("CursorPaginate with Search: %v", err)
	}
	// A table without a search index.
	if _, err := db.Query[stAuthor](ctx).Search("ada").Get(); err == nil || !strings.Contains(err.Error(), "search index") {
		t.Errorf("Search without an index: %v", err)
	}

	// The index is recorded.
	idx, err := db.SearchIndexes(ctx)
	check(t, err)
	if len(idx) != 1 || idx[0].Table != "st_s_articles" || !slices.Equal(idx[0].Columns, []string{"title", "body"}) ||
		idx[0].Language != "simple" || idx[0].Ranking != "default" {
		t.Errorf("SearchIndexes = %+v", idx)
	}

	// Alter: dropped, then added over existing rows.
	check(t, s.Alter("st_s_articles", func(t *migrate.Table) { t.DropSearchIndex() }))
	if _, err := q().Search("rice").Get(); err == nil {
		t.Error("Search after DropSearchIndex: no error")
	}
	if err := s.Alter("st_s_articles", func(t *migrate.Table) { t.DropSearchIndex() }); err == nil {
		t.Error("DropSearchIndex twice: no error")
	}
	check(t, s.Alter("st_s_articles", func(t *migrate.Table) { t.SearchIndex("body") }))
	if got := articleTitles(t, q().Search("lentils")); !slices.Equal(got, []string{"Cooking rice"}) {
		t.Errorf("after Alter SearchIndex: %q", got)
	}
	if got := articleTitles(t, q().Search("cooking")); len(got) != 0 {
		t.Errorf("title no longer indexed: %q", got)
	}
	if err := s.Alter("st_s_articles", func(t *migrate.Table) { t.SearchIndex("title") }); err == nil {
		t.Error("a second SearchIndex: no error")
	}

	// Indexed columns can't be dropped or renamed under the index; in the
	// same Alter as DropSearchIndex, they can.
	if err := s.Alter("st_s_articles", func(t *migrate.Table) { t.DropColumn("body") }); err == nil || !strings.Contains(err.Error(), "search index") {
		t.Errorf("DropColumn of an indexed column: %v", err)
	}
	if err := s.Alter("st_s_articles", func(t *migrate.Table) { t.RenameColumn("body", "content") }); err == nil || !strings.Contains(err.Error(), "search index") {
		t.Errorf("RenameColumn of an indexed column: %v", err)
	}
	check(t, s.Alter("st_s_articles", func(t *migrate.Table) {
		t.DropSearchIndex()
		t.RenameColumn("body", "content")
		t.SearchIndex("content")
	}))
	if got, err := db.Pluck(q().Search("lentils"), title); err != nil || !slices.Equal(got, []string{"Cooking rice"}) {
		t.Errorf("after renaming the indexed column: %q, %v", got, err)
	}
	check(t, s.Alter("st_s_articles", func(t *migrate.Table) {
		t.DropSearchIndex()
		t.RenameColumn("content", "body")
		t.SearchIndex("body")
	}))

	// Reindex checks the columns before dropping anything.
	runner, err := migrate.NewRunner(d(ctx), nil, migrate.WithTable("st_migrations"))
	check(t, err)
	cols := d(ctx).Dialect().QuoteIdent("columns")
	_, err = db.Exec(ctx, "UPDATE "+db.SearchIndexesTable+" SET "+cols+" = 'body,nonesuch'")
	check(t, err)
	if _, err := runner.Reindex(ctx); err == nil || !strings.Contains(err.Error(), "nonesuch is gone") {
		t.Errorf("Reindex with a missing column: %v", err)
	}
	_, err = db.Exec(ctx, "UPDATE "+db.SearchIndexesTable+" SET "+cols+" = 'body'")
	check(t, err)
	if got := articleTitles(t, q().Search("lentils")); !slices.Equal(got, []string{"Cooking rice"}) {
		t.Errorf("after the refused Reindex: %q", got)
	}

	testLongName(t, ctx)

	// Rename is refused; Drop removes the index and its record.
	if err := s.Rename("st_s_articles", "st_s_renamed"); err == nil || !strings.Contains(err.Error(), "search index") {
		t.Errorf("Rename: %v", err)
	}
	check(t, s.Drop("st_s_articles"))
	if idx, err := db.SearchIndexes(ctx); err != nil || len(idx) != 0 {
		t.Errorf("after Drop: %+v, %v", idx, err)
	}
	if dialect == "sqlite" {
		if n, err := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'st_s_articles%'"); err != nil || n != 0 {
			t.Errorf("SQLite objects left after Drop: %d, %v", n, err)
		}
	}
}

// withSearch returns ctx with d's connection and other search settings.
func withSearch(ctx context.Context, cfg db.SearchConfig) (context.Context, *db.DB) {
	x := db.New(d(ctx).SQL(), d(ctx).Dialect(), db.WithSearch(cfg))
	return db.WithDB(ctx, x), x
}

func testSearchSettings(t *testing.T, ctx context.Context) {
	dialect := d(ctx).Dialect().Name()
	createArticles(t, ctx, true)
	seedArticles(t, ctx)
	runner := func(x *db.DB) *migrate.Runner {
		r, err := migrate.NewRunner(x, nil, migrate.WithTable("st_migrations"))
		check(t, err)
		return r
	}

	// The settings are checked against the database.
	english := db.SearchConfig{Language: "english", Ranking: "default"}
	enCtx, en := withSearch(ctx, english)
	err := en.CheckSearch(ctx)
	if dialect == "mysql" {
		if err == nil || !strings.Contains(err.Error(), "SEARCH_LANGUAGE=english isn't available on MySQL") {
			t.Errorf("english on MySQL: %v", err)
		}
	} else {
		check(t, err)
		// The index was built for simple: refused until reindexed.
		if err := en.Check(ctx); err == nil || !strings.Contains(err.Error(), "search:reindex") {
			t.Errorf("Check with a stale index: %v", err)
		}
		done, err := runner(en).Reindex(ctx)
		check(t, err)
		if !slices.Equal(done, []string{"st_s_articles"}) {
			t.Errorf("Reindex = %q", done)
		}
		check(t, en.Check(ctx))
		// english stems: "runs" finds "running".
		if got := articleTitles(t, db.Query[stArticle](enCtx).Search("runs")); !slices.Equal(got, []string{"Go generics"}) {
			t.Errorf("english Search(runs) = %q", got)
		}
		if err := d(ctx).Check(ctx); err == nil {
			t.Error("Check with the default settings and an english index: no error")
		}
		_, err = runner(d(ctx)).Reindex(ctx, "st_s_articles")
		check(t, err)
		check(t, d(ctx).Check(ctx))
	}
	if _, err := runner(d(ctx)).Reindex(ctx, "st_authors"); err == nil {
		t.Error("Reindex of a table without an index: no error")
	}
	if dialect == "postgres" {
		_, de := withSearch(ctx, db.SearchConfig{Language: "nonesuch", Ranking: "default"})
		if err := de.CheckSearch(ctx); err == nil || !strings.Contains(err.Error(), "pg_ts_config") {
			t.Errorf("unknown language: %v", err)
		}
	}

	// BM25: refused where the database doesn't have it.
	bm25 := db.SearchConfig{Language: "simple", Ranking: "bm25"}
	bmCtx, bm := withSearch(ctx, bm25)
	has, err := bm.Supports(ctx, db.BM25)
	check(t, err)
	if want := dialect == "sqlite"; dialect != "postgres" && has != want {
		t.Errorf("Supports(BM25) = %v on %s", has, dialect)
	}
	if !has {
		if err := bm.CheckSearch(ctx); err == nil || !strings.Contains(err.Error(), "SEARCH_RANKING=bm25 needs BM25 ranking") {
			t.Errorf("bm25 without BM25: %v", err)
		}
		if err := bm.Require("a feature", db.BM25); err != nil {
			t.Errorf("Require before Check: %v", err)
		}
	} else {
		if dialect == "postgres" {
			_, err := runner(bm).Reindex(ctx)
			check(t, err)
			if idx, err := db.SearchIndexes(ctx); err != nil || len(idx) != 1 || idx[0].Ranking != "bm25" {
				t.Errorf("after the bm25 reindex: %+v, %v", idx, err)
			}
			if err := d(ctx).Check(ctx); err == nil {
				t.Error("Check with the default ranking and a bm25 index: no error")
			}
		}
		check(t, bm.Check(ctx))
		got := articleTitles(t, db.Query[stArticle](bmCtx).Search("generics"))
		if !sameSet(got, []string{"Go generics", "Cooking rice"}) {
			t.Errorf("bm25 Search(generics) = %q", got)
		}
		if dialect == "postgres" {
			// Rows that match only as prefixes ("generic" in "generics")
			// are found even when the planner would rather use the bm25
			// index, whose scan returns whole-word matches only.
			check(t, db.Tx(bmCtx, func(ctx context.Context) error {
				for _, set := range []string{"SET LOCAL enable_seqscan = off", "SET LOCAL enable_bitmapscan = off"} {
					if _, err := db.Exec(ctx, set); err != nil {
						return err
					}
				}
				q := db.Query[stArticle](ctx).Search("generic")
				if ok, err := q.Exists(); err != nil || !ok {
					t.Errorf("bm25 Exists = %v, %v", ok, err)
				}
				page, err := q.Paginate(1, 2)
				if err != nil || page.Total != 3 || len(page.Data) != 2 || page.Data[0].Title != "Generic advice" {
					t.Errorf("bm25 Paginate = %+v, %v", page, err)
				}
				if got := articleTitles(t, q); !sameSet(got, []string{"Go generics", "Cooking rice", "Generic advice"}) {
					t.Errorf("bm25 Search(generic) = %q", got)
				}
				return nil
			}))
			testLongName(t, bmCtx)
		}
		if dialect == "postgres" {
			_, err := runner(d(ctx)).Reindex(ctx)
			check(t, err)
		}
	}

	// Requirements are checked by Check, and at once after it.
	x := db.New(d(ctx).SQL(), d(ctx).Dialect())
	check(t, x.Require("search", db.FullText))
	check(t, x.Check(ctx))
	err = x.Require("ranking", db.BM25)
	if (err == nil) != has {
		t.Errorf("Require(BM25) after Check: %v (supported: %v)", err, has)
	}
	if _, err := x.Supports(ctx, db.Capability("teleportation")); err == nil {
		t.Error("an unknown capability: no error")
	}
}

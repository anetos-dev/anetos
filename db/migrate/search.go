// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"anetos.dev/anetos/db"
)

// searchWeights weigh a search index's columns by position, as
// PostgreSQL's default ranking weighs its labels A, B, C and D.
var searchWeights = []string{"1.0", "0.4", "0.2", "0.1"}

func searchWeight(i int) string { return searchWeights[min(i, len(searchWeights)-1)] }

func searchLabel(i int) string { return string(rune('A' + min(i, 3))) }

// searchName is the name of a search index's object with suffix
// (index, bm25, insert…), shortened like index names.
func searchName(table, suffix string) string { return indexName(table, []string{"search"}, suffix) }

// checkSearchColumns rejects column names the search DDL can't use.
func checkSearchColumns(table string, cols []string) error {
	for i, c := range cols {
		if c == "" || strings.ContainsAny(c, ",.") {
			return fmt.Errorf("migrate: %s: SearchIndex column %q must be a plain column name", table, c)
		}
		if slices.Contains(cols[:i], c) {
			return fmt.Errorf("migrate: %s: SearchIndex lists %s twice", table, c)
		}
	}
	return nil
}

// searchSQL returns the statements that build table's search index over
// cols for cfg, and record it.
func (s *Schema) searchSQL(table string, cols []string, cfg db.SearchConfig, existing bool) ([]string, error) {
	if err := checkSearchColumns(table, cols); err != nil {
		return nil, err
	}
	t := s.q(table)
	lang := cfg.Language // checked: [a-z][a-z0-9_]*
	var stmts []string
	switch s.dialect {
	case "postgres":
		parts := make([]string, len(cols))
		texts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = fmt.Sprintf("setweight(to_tsvector('%s', coalesce(%s::text, '')), '%s')", lang, s.q(c), searchLabel(i))
			texts[i] = fmt.Sprintf("coalesce(%s::text, '')", s.q(c))
		}
		stmts = append(stmts,
			"ALTER TABLE "+t+" ADD COLUMN "+s.q("search_vector")+" tsvector GENERATED ALWAYS AS ("+strings.Join(parts, " || ")+") STORED",
			"CREATE INDEX "+s.q(searchName(table, "index"))+" ON "+t+" USING gin ("+s.q("search_vector")+")")
		if cfg.Ranking == "bm25" {
			stmts = append(stmts,
				"ALTER TABLE "+t+" ADD COLUMN "+s.q("search_text")+" text GENERATED ALWAYS AS ("+strings.Join(texts, " || ' ' || ")+") STORED",
				"CREATE INDEX "+s.q(searchName(table, "bm25"))+" ON "+t+" USING bm25 ("+s.q("search_text")+") WITH (text_config = '"+lang+"')")
		}
	case "mysql":
		stmts = append(stmts,
			"ALTER TABLE "+t+" ADD COLUMN "+s.q("search_text")+" LONGTEXT GENERATED ALWAYS AS (CONCAT_WS(' ', "+s.list(cols)+")) STORED",
			"ALTER TABLE "+t+" ADD FULLTEXT INDEX "+s.q(searchName(table, "index"))+" ("+s.q("search_text")+")")
	default:
		fts := s.q(table + "_search")
		tokenize := "unicode61 remove_diacritics 2"
		if lang == "english" {
			tokenize = "porter " + tokenize
		}
		weights := make([]string, len(cols))
		newVals := make([]string, len(cols))
		oldVals := make([]string, len(cols))
		for i, c := range cols {
			weights[i] = searchWeight(i)
			newVals[i] = "new." + s.q(c)
			oldVals[i] = "old." + s.q(c)
		}
		list := s.list(cols)
		insert := "INSERT INTO " + fts + " (rowid, " + list + ") VALUES (new.rowid, " + strings.Join(newVals, ", ") + ");"
		remove := "INSERT INTO " + fts + " (" + fts + ", rowid, " + list + ") VALUES ('delete', old.rowid, " + strings.Join(oldVals, ", ") + ");"
		stmts = append(stmts,
			"CREATE VIRTUAL TABLE "+fts+" USING fts5("+list+", content="+sqlString(table)+", tokenize="+sqlString(tokenize)+")",
			"INSERT INTO "+fts+" ("+fts+", rank) VALUES ('rank', 'bm25("+strings.Join(weights, ", ")+")')",
			"CREATE TRIGGER "+s.q(searchName(table, "insert"))+" AFTER INSERT ON "+t+" BEGIN "+insert+" END",
			"CREATE TRIGGER "+s.q(searchName(table, "delete"))+" AFTER DELETE ON "+t+" BEGIN "+remove+" END",
			"CREATE TRIGGER "+s.q(searchName(table, "update"))+" AFTER UPDATE OF "+list+" ON "+t+" BEGIN "+remove+" "+insert+" END")
		if existing {
			stmts = append(stmts, "INSERT INTO "+fts+" ("+fts+") VALUES ('rebuild')")
		}
	}
	return append(stmts, s.recordSearchSQL(table, cols, cfg)...), nil
}

func sqlString(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

// recordSearchSQL records a search index in the search_indexes table,
// creating it if needed (with s.tableCharset: readCharset first).
func (s *Schema) recordSearchSQL(table string, cols []string, cfg db.SearchConfig) []string {
	idx := s.q(db.SearchIndexesTable)
	return []string{
		"CREATE TABLE IF NOT EXISTS " + idx + " (" + s.q("table_name") + " VARCHAR(64) NOT NULL PRIMARY KEY, " + s.q("columns") + " TEXT NOT NULL, " +
			s.q("language") + " VARCHAR(64) NOT NULL, " + s.q("ranking") + " VARCHAR(16) NOT NULL)" + s.tableCharset,
		"DELETE FROM " + idx + " WHERE " + s.q("table_name") + " = " + sqlString(table),
		"INSERT INTO " + idx + " (" + s.list([]string{"table_name", "columns", "language", "ranking"}) + ") VALUES (" +
			strings.Join([]string{sqlString(table), sqlString(strings.Join(cols, ",")), sqlString(cfg.Language), sqlString(cfg.Ranking)}, ", ") + ")",
	}
}

// dropSearchSQL returns the statements that remove table's search
// index ix (whose table stays) and its record.
func (s *Schema) dropSearchSQL(ix db.SearchIndex) []string {
	t := s.q(ix.Table)
	var stmts []string
	switch s.dialect {
	case "postgres":
		stmts = []string{
			"DROP INDEX IF EXISTS " + s.q(searchName(ix.Table, "bm25")),
			"ALTER TABLE " + t + " DROP COLUMN IF EXISTS " + s.q("search_text"),
			"DROP INDEX IF EXISTS " + s.q(searchName(ix.Table, "index")),
			"ALTER TABLE " + t + " DROP COLUMN IF EXISTS " + s.q("search_vector"),
		}
	case "mysql":
		stmts = []string{
			"ALTER TABLE " + t + " DROP INDEX " + s.q(searchName(ix.Table, "index")),
			"ALTER TABLE " + t + " DROP COLUMN " + s.q("search_text"),
		}
	default:
		stmts = s.dropSQLiteSearch(ix.Table)
	}
	return append(stmts, "DELETE FROM "+s.q(db.SearchIndexesTable)+" WHERE "+s.q("table_name")+" = "+sqlString(ix.Table))
}

func (s *Schema) dropSQLiteSearch(table string) []string {
	return []string{
		"DROP TRIGGER IF EXISTS " + s.q(searchName(table, "insert")),
		"DROP TRIGGER IF EXISTS " + s.q(searchName(table, "delete")),
		"DROP TRIGGER IF EXISTS " + s.q(searchName(table, "update")),
		"DROP TABLE IF EXISTS " + s.q(table+"_search"),
	}
}

// searchIndex returns table's search index, if it has one.
func (s *Schema) searchIndex(table string) (db.SearchIndex, bool, error) {
	idx, err := db.SearchIndexes(s.ctx)
	if err != nil {
		return db.SearchIndex{}, false, err
	}
	for _, ix := range idx {
		if ix.Table == table {
			return ix, true, nil
		}
	}
	return db.SearchIndex{}, false, nil
}

// checkSearch checks that the database can build search indexes for its
// settings.
func (s *Schema) checkSearch() error {
	if err := s.d.CheckSearch(s.ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Reindex rebuilds search indexes for the current SEARCH_LANGUAGE and
// SEARCH_RANKING (after changing them, or to repair one): those of the
// given tables, or all. It returns the tables it rebuilt.
func (r *Runner) Reindex(ctx context.Context, tables ...string) ([]string, error) {
	ctx, unlock, err := r.prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	all, err := db.SearchIndexes(ctx)
	if err != nil {
		return nil, err
	}
	var todo []db.SearchIndex
	for _, ix := range all {
		if len(tables) == 0 || slices.Contains(tables, ix.Table) {
			todo = append(todo, ix)
		}
	}
	for _, t := range tables {
		if !slices.ContainsFunc(todo, func(ix db.SearchIndex) bool { return ix.Table == t }) {
			return nil, fmt.Errorf("migrate: %s has no search index", t)
		}
	}
	// Check every table first: on MySQL, whose DDL isn't transactional,
	// a rebuild that fails after the drop would leave no index.
	check := &Schema{ctx: ctx, d: r.d, dialect: r.dialect}
	for _, ix := range todo {
		for _, c := range ix.Columns {
			has, err := check.HasColumn(ix.Table, c)
			if err != nil {
				return nil, err
			}
			if !has {
				return nil, fmt.Errorf("migrate: reindex %s: its indexed column %s is gone; drop the index (t.DropSearchIndex) and add it again in a migration", ix.Table, c)
			}
		}
	}
	var done []string
	for _, ix := range todo {
		work := func(ctx context.Context) error {
			s := &Schema{ctx: ctx, d: r.d, dialect: r.dialect}
			if err := s.checkSearch(); err != nil {
				return err
			}
			if err := s.readCharset(); err != nil { // the search_indexes table
				return err
			}
			stmts, err := s.searchSQL(ix.Table, ix.Columns, r.d.SearchConfig(), true)
			if err != nil {
				return err
			}
			return s.run(append(s.dropSearchSQL(ix), stmts...))
		}
		switch r.dialect {
		case "mysql":
			err = work(ctx)
		case "sqlite":
			err = r.sqliteTx(ctx, work)
		default:
			err = db.Tx(ctx, work)
		}
		if err != nil {
			return done, fmt.Errorf("migrate: reindex %s: %w", ix.Table, err)
		}
		done = append(done, ix.Table)
	}
	return done, nil
}

var errRenameSearch = errors.New("has a search index: drop it first (t.DropSearchIndex in Alter), and add it again after the rename")

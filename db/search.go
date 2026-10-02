// SPDX-License-Identifier: Apache-2.0

package db

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/internal/dbutil"
)

// SearchConfig is the full-text search settings, read by [Connect].
type SearchConfig struct {
	// Language is how words are matched: "simple" (the default) matches
	// them as written, in any language; a language stems them ("generic"
	// also finds "generics" and "running" finds "run"): "english" on
	// PostgreSQL and SQLite, and on PostgreSQL any text search
	// configuration the server has (german, french, …). MySQL only has
	// "simple". Search indexes are built for it, and the app refuses to
	// start when an index was built for another one (run search:reindex).
	// SEARCH_LANGUAGE.
	Language string `env:"SEARCH_LANGUAGE" default:"simple"`
	// Ranking orders results: "default", the database's own ranking, or
	// "bm25", which the database must provide (SQLite; PostgreSQL 17+
	// with the pg_textsearch extension), or the app refuses to start.
	// SEARCH_RANKING.
	Ranking string `env:"SEARCH_RANKING" default:"default"`
}

var languageRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Validate checks the settings' form; [DB.CheckSearch] checks that the
// database supports them.
func (c SearchConfig) Validate() error {
	var errs []error
	if !languageRe.MatchString(c.Language) {
		errs = append(errs, fmt.Errorf("SEARCH_LANGUAGE %q must be a language name in lower case, such as simple or english", c.Language))
	}
	if c.Ranking != "default" && c.Ranking != "bm25" {
		errs = append(errs, fmt.Errorf("SEARCH_RANKING %q must be default or bm25", c.Ranking))
	}
	return errors.Join(errs...)
}

// WithSearch sets the search settings of a DB from [Open] or [New]
// ([Connect] reads them from SEARCH_*). Default: simple, default.
func WithSearch(cfg SearchConfig) Option { return func(d *DB) { d.search = cfg } }

// SearchConfig returns the DB's search settings.
func (d *DB) SearchConfig() SearchConfig { return d.search }

func defaultSearch() SearchConfig { return SearchConfig{Language: "simple", Ranking: "default"} }

// Capability is something a database server may or may not be able to
// do, depending on the database, its version and its extensions.
type Capability string

// The capabilities features can require ([DB.Require]).
const (
	// FullText is full-text search: [Q.Search] and search indexes. Every
	// supported database has it.
	FullText Capability = "full-text search"
	// BM25 is BM25 ranking of search results: SQLite's FTS5 has it, and
	// PostgreSQL 17+ with the pg_textsearch extension.
	BM25 Capability = "BM25 ranking"
)

// Supports reports whether the database can provide c, asking the server
// when it depends on it (its version, its extensions).
func (d *DB) Supports(ctx context.Context, c Capability) (bool, error) {
	ok, _, err := d.supports(ctx, c)
	return ok, err
}

// supports reports whether the database can provide c, and if not, how
// to get it.
func (d *DB) supports(ctx context.Context, c Capability) (bool, string, error) {
	name := d.dialect.Name()
	switch c {
	case FullText:
		switch name {
		case "postgres", "mysql", "sqlite":
			return true, "", nil
		}
		return false, "use PostgreSQL, MySQL/MariaDB or SQLite", nil
	case BM25:
		switch name {
		case "sqlite":
			return true, "", nil
		case "postgres":
			var version, ext int64
			err := d.sql.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int, (SELECT COUNT(*) FROM pg_extension WHERE extname = 'pg_textsearch')").Scan(&version, &ext)
			switch {
			case err != nil:
				return false, "", fmt.Errorf("db: check for %s: %w", c, err)
			case version < 170000:
				return false, "it needs PostgreSQL 17 or later with the pg_textsearch extension", nil
			case ext == 0:
				return false, "install the pg_textsearch extension (in shared_preload_libraries, then CREATE EXTENSION pg_textsearch)", nil
			}
			return true, "", nil
		case "mysql":
			return false, "MySQL and MariaDB don't have it; use SQLite, or PostgreSQL with pg_textsearch", nil
		}
		return false, "use SQLite, or PostgreSQL with pg_textsearch", nil
	}
	return false, "", fmt.Errorf("db: unknown capability %q", c)
}

// requirement is a feature's need of a capability.
type requirement struct {
	feature string
	caps    []Capability
}

// Require records that feature needs the database to provide caps. The
// app checks every requirement when it boots ([Connect], or [DB.Check]),
// and refuses to start if one isn't met, naming the feature, the
// database and how to get the capability. Once the DB is checked,
// Require checks at once and returns the error.
//
//	err := database.Require("relevance ranking", db.BM25)
func (d *DB) Require(feature string, caps ...Capability) error {
	d.reqMu.Lock()
	d.reqs = append(d.reqs, requirement{feature, caps})
	checked := d.checked
	d.reqMu.Unlock()
	if !checked {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return d.checkRequirement(ctx, requirement{feature, caps})
}

func (d *DB) checkRequirement(ctx context.Context, r requirement) error {
	for _, c := range r.caps {
		ok, how, err := d.supports(ctx, c)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("db: %s needs %s, which this %s database doesn't have: %s", r.feature, c, d.dialectTitle(), how)
		}
	}
	return nil
}

func (d *DB) dialectTitle() string {
	switch d.dialect.Name() {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL/MariaDB"
	case "sqlite":
		return "SQLite"
	}
	return d.dialect.Name()
}

// Check runs the checks [Connect] runs when the app boots: the search
// settings ([DB.CheckSearch]), the features' requirements
// ([DB.Require]), and that the search indexes were built for the
// current settings (unless the command being run changes the schema,
// such as migrate and search:reindex; see cmd.Command.ChangesSchema).
// Call it after [Open] to check a DB the same way.
func (d *DB) Check(ctx context.Context) error {
	if err := d.CheckSearch(ctx); err != nil {
		return err
	}
	d.reqMu.Lock()
	reqs := slices.Clone(d.reqs)
	d.reqMu.Unlock()
	for _, r := range reqs {
		if err := d.checkRequirement(ctx, r); err != nil {
			return err
		}
	}
	if c, ok := cmd.Running(ctx); !ok || !c.ChangesSchema {
		if err := d.checkSearchIndexes(ctx); err != nil {
			return err
		}
	}
	// Requirements added meanwhile are checked before Require starts
	// checking them itself.
	for {
		d.reqMu.Lock()
		more := slices.Clone(d.reqs[len(reqs):])
		if len(more) == 0 {
			d.checked = true
			d.reqMu.Unlock()
			return nil
		}
		reqs = d.reqs[: len(reqs)+len(more) : len(reqs)+len(more)]
		d.reqMu.Unlock()
		for _, r := range more {
			if err := d.checkRequirement(ctx, r); err != nil {
				return err
			}
		}
	}
}

// CheckSearch checks that the database supports the search settings:
// SEARCH_LANGUAGE (on PostgreSQL, a text search configuration the server
// has) and SEARCH_RANKING. Migrations creating search indexes check it
// too.
func (d *DB) CheckSearch(ctx context.Context) error {
	if err := d.search.Validate(); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	lang := d.search.Language
	switch d.dialect.Name() {
	case "sqlite":
		if lang != "simple" && lang != "english" {
			return fmt.Errorf("db: SEARCH_LANGUAGE=%s isn't available on SQLite, which has simple and english; use one of them, or PostgreSQL", lang)
		}
	case "mysql":
		if lang != "simple" {
			return fmt.Errorf("db: SEARCH_LANGUAGE=%s isn't available on MySQL/MariaDB, which don't stem words; set SEARCH_LANGUAGE=simple, or use PostgreSQL or SQLite", lang)
		}
	case "postgres":
		if lang != "simple" {
			var n int64
			if err := d.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM pg_ts_config WHERE cfgname = $1", lang).Scan(&n); err != nil {
				return fmt.Errorf("db: check SEARCH_LANGUAGE: %w", err)
			}
			if n == 0 {
				return fmt.Errorf("db: SEARCH_LANGUAGE=%s: this PostgreSQL server has no text search configuration of that name (SELECT cfgname FROM pg_ts_config lists them)", lang)
			}
		}
	}
	if d.search.Ranking == "bm25" {
		return d.checkRequirement(ctx, requirement{"SEARCH_RANKING=bm25", []Capability{BM25}})
	}
	return nil
}

// SearchIndex is a search index, as recorded by the migration that made
// it (see migrate.Table.SearchIndex).
type SearchIndex struct {
	// Table is the indexed table.
	Table string
	// Columns are the indexed columns, most important first.
	Columns []string
	// Language and Ranking are the settings it was built for.
	Language, Ranking string
}

// SearchIndexesTable is the table where migrations record the search
// indexes they make.
const SearchIndexesTable = "search_indexes"

// SearchIndexes returns the search indexes of the database in ctx, by
// table name.
func SearchIndexes(ctx context.Context) ([]SearchIndex, error) {
	d, c, err := handle(ctx)
	if err != nil {
		return nil, err
	}
	var exists string
	switch d.dialect.Name() {
	case "postgres":
		exists = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?"
	case "mysql":
		exists = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
	default:
		exists = "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	}
	n, err := scalar[int64](ctx, d, c, rawBuilder(d.dialect, exists, SearchIndexesTable))
	if err != nil || n == 0 {
		return nil, err
	}
	type row struct {
		Table    string `db:"table_name"`
		Columns  string `db:"columns"`
		Language string `db:"language"`
		Ranking  string `db:"ranking"`
	}
	rows, err := Raw[row](ctx, "SELECT table_name, columns, language, ranking FROM "+quoteName(d.dialect, SearchIndexesTable)+" ORDER BY table_name")
	if err != nil {
		return nil, err
	}
	out := make([]SearchIndex, len(rows))
	for i, r := range rows {
		out[i] = SearchIndex{Table: r.Table, Columns: strings.Split(r.Columns, ","), Language: r.Language, Ranking: r.Ranking}
	}
	return out, nil
}

// rawBuilder is a builder holding one statement with ? placeholders.
func rawBuilder(d Dialect, sql string, args ...any) *sqlBuilder {
	b := &sqlBuilder{d: d}
	b.raw(sql, args)
	return b
}

// checkSearchIndexes refuses search indexes built for other settings.
func (d *DB) checkSearchIndexes(ctx context.Context) error {
	idx, err := SearchIndexes(WithDB(ctx, d))
	if err != nil {
		return fmt.Errorf("db: read the search indexes: %w", err)
	}
	var stale []string
	for _, ix := range idx {
		// Only PostgreSQL builds something else for BM25 (a bm25 index).
		if ix.Language != d.search.Language || d.dialect.Name() == "postgres" && ix.Ranking != d.search.Ranking {
			stale = append(stale, fmt.Sprintf("%s (built for SEARCH_LANGUAGE=%s, SEARCH_RANKING=%s)", ix.Table, ix.Language, ix.Ranking))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("db: the search indexes of %s don't match SEARCH_LANGUAGE=%s, SEARCH_RANKING=%s: rebuild them with the search:reindex command, or set the settings back",
			strings.Join(stale, ", "), d.search.Language, d.search.Ranking)
	}
	return nil
}

// ---- queries ----

// maxSearchTerms bounds the words of a search; more are ignored.
const maxSearchTerms = 32

// searchTerms splits text into lower-case words: runs of letters,
// digits and combining marks (which some scripts' words contain), at
// most maxSearchTerms, each once.
func searchTerms(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	})
	var out []string
	for _, w := range words {
		if utf8.RuneCountInString(w) > 100 {
			continue // no real word; PostgreSQL ignores long ones too
		}
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
		if len(out) == maxSearchTerms {
			break
		}
	}
	return out
}

// searchSpec is a query's full-text search.
type searchSpec struct {
	table string
	cfg   SearchConfig
	terms []string // as the database's query syntax needs them
}

// Search keeps the rows matching text and orders them by relevance, best
// first; OrderBy terms, before or after, break ties:
//
//	posts, err := db.Query[Post](ctx).Where(published).Search(q).Paginate(page, 20)
//
// The model's table needs a search index (migrate.Table.SearchIndex).
// Every word of text must match, as a prefix ("generic" finds
// "generics"), in any indexed column; with a SEARCH_LANGUAGE other than
// simple, words are also stemmed. Punctuation is ignored, and text
// without words leaves the query as it is. A second Search replaces the
// first. Databases differ in which words they index: MySQL and MariaDB
// skip words shorter than three letters and common English words
// ("the"), so there those only match the longer words they start, and
// are optional next to other words. Update, Delete and CursorPaginate
// refuse a query with Search.
func (q *Q[T]) Search(text string) *Q[T] {
	c := q.clone()
	c.orders = slices.DeleteFunc(slices.Clone(c.orders), func(o Order) bool { _, ok := o.raw.(searchRank); return ok })
	c.search = nil
	terms := searchTerms(text)
	if c.err != nil || len(terms) == 0 {
		return c
	}
	d, err := From(c.ctx)
	if err != nil {
		c.err = err
		return c
	}
	spec := &searchSpec{table: c.m.table, cfg: d.search}
	switch d.dialect.Name() {
	case "postgres":
		quoted := make([]string, len(terms))
		for i, t := range terms {
			quoted[i] = "'" + t + "':*" // terms have no quotes
		}
		spec.terms = []string{strings.Join(quoted, " & ")}
	case "sqlite":
		quoted := make([]string, len(terms))
		for i, t := range terms {
			quoted[i] = `"` + t + `"*`
		}
		spec.terms = []string{strings.Join(quoted, " ")}
	case "mysql":
		kept, err := d.mysqlTerms(c.ctx, terms)
		if err != nil {
			c.err = err
			return c
		}
		spec.terms = []string{strings.Join(kept, " ")}
	default:
		c.err = fmt.Errorf("db: Search isn't supported on %s", d.dialect.Name())
		return c
	}
	c.search = spec
	c.orders = append([]Order{{raw: searchRank{spec}}}, c.orders...)
	return c
}

// searchCond is the WHERE condition of a search (PostgreSQL, MySQL;
// SQLite's is a join).
type searchCond struct{ s *searchSpec }

func (e searchCond) build(b *sqlBuilder) {
	switch b.d.Name() {
	case "postgres":
		b.write(quoteName(b.d, e.s.table) + "." + b.d.QuoteIdent("search_vector") + " @@ to_tsquery(CAST(")
		b.arg(e.s.cfg.Language)
		b.write(" AS regconfig), ")
		b.arg(e.s.terms[0])
		b.write(")")
	case "mysql":
		b.write("MATCH(" + quoteName(b.d, e.s.table) + "." + b.d.QuoteIdent("search_text") + ") AGAINST (")
		b.arg(e.s.terms[0])
		b.write(" IN BOOLEAN MODE)")
	default:
		b.write("1 = 1")
	}
}

// searchJoin is SQLite's search: the FTS5 table's matches, with their
// rank, joined on rowid.
type searchJoin struct{ s *searchSpec }

func (e searchJoin) build(b *sqlBuilder) {
	fts := b.d.QuoteIdent(e.s.table + "_search")
	b.write("JOIN (SELECT rowid AS anetos_rowid, rank AS anetos_rank FROM " + fts + " WHERE " + fts + " MATCH ")
	b.arg(e.s.terms[0])
	b.write(") AS anetos_search ON anetos_search.anetos_rowid = " + quoteName(b.d, e.s.table) + ".rowid")
}

// searchRank orders a search's rows, best first.
type searchRank struct{ s *searchSpec }

func (e searchRank) build(b *sqlBuilder) {
	table := quoteName(b.d, e.s.table)
	switch b.d.Name() {
	case "postgres":
		if e.s.cfg.Ranking == "bm25" {
			// pg_textsearch scores are negative: lowest first. Words that
			// match only as prefixes score 0; ts_rank_cd orders those.
			// COALESCE keeps the planner from ordering by the bm25 index,
			// whose scan returns only rows with a whole-word match.
			b.write("COALESCE(" + table + "." + b.d.QuoteIdent("search_text") + " <@> to_bm25query(")
			b.arg(strings.Join(searchTermsOfTSQuery(e.s.terms[0]), " "))
			b.write(", ")
			b.arg(dbutil.IndexName(e.s.table, []string{"search"}, "bm25")) // as migrate names it
			b.write("), 0), ")
		}
		b.write("ts_rank_cd(" + table + "." + b.d.QuoteIdent("search_vector") + ", to_tsquery(CAST(")
		b.arg(e.s.cfg.Language)
		b.write(" AS regconfig), ")
		b.arg(e.s.terms[0])
		b.write(")) DESC")
	case "mysql":
		b.write("MATCH(" + table + "." + b.d.QuoteIdent("search_text") + ") AGAINST (")
		b.arg(e.s.terms[0])
		b.write(" IN BOOLEAN MODE) DESC")
	default:
		b.write("anetos_search.anetos_rank") // FTS5: lower is better
	}
}

// searchTermsOfTSQuery returns the words of a tsquery searchSpec built.
func searchTermsOfTSQuery(q string) []string {
	var out []string
	for part := range strings.SplitSeq(q, " & ") {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(part, "'"), "':*"))
	}
	return out
}

// searchHint adds to err, from a query with Search, what a missing
// search index looks like.
func (q *Q[T]) searchHint(err error) error {
	if err == nil || q.search == nil {
		return err
	}
	msg := err.Error()
	missing := strings.Contains(msg, "does not exist") || strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "no such") || strings.Contains(msg, "Unknown column")
	if missing && (strings.Contains(msg, "search_vector") || strings.Contains(msg, "search_text") || strings.Contains(msg, q.search.table+"_search")) {
		return fmt.Errorf("%w (does %s have a search index? Add one with t.SearchIndex in a migration)", err, q.search.table)
	}
	return err
}

// mysqlWords is what MySQL's full-text indexes skip: words shorter than
// innodb_ft_min_token_size or longer than innodb_ft_max_token_size, and
// stop words.
type mysqlWords struct {
	minLen, maxLen int
	stop           map[string]bool
}

// innodbStopWords is InnoDB's default stop-word list
// (INFORMATION_SCHEMA.INNODB_FT_DEFAULT_STOPWORD, the same on MySQL and
// MariaDB), written here because reading that table may need the PROCESS
// privilege (MySQL).
var innodbStopWords = []string{"a", "about", "an", "are", "as", "at", "be", "by", "com", "de", "en", "for", "from", "how", "i", "in", "is", "it", "la", "of", "on", "or", "that", "the", "this", "to", "was", "what", "when", "where", "who", "will", "with", "und", "www"}

// mysqlTerms returns the boolean-mode terms of a search. Words the index
// has are required (+word*). Words it skips can only match longer indexed
// words they start ("ca*" finds "cat"), so they're optional (word*) next
// to indexed ones, and required when every word is skipped. A word longer
// than the longest indexed one is cut to it.
func (d *DB) mysqlTerms(ctx context.Context, terms []string) ([]string, error) {
	w, err := d.mysqlSkipped(ctx)
	if err != nil {
		return nil, err
	}
	skipped := func(t string) bool {
		n := utf8.RuneCountInString(t)
		return n < w.minLen || n > w.maxLen || w.stop[t]
	}
	all := !slices.ContainsFunc(terms, func(t string) bool { return !skipped(t) })
	out := make([]string, len(terms))
	for i, t := range terms {
		if r := []rune(t); len(r) > w.maxLen {
			t = string(r[:w.maxLen])
		}
		if all || !skipped(terms[i]) {
			out[i] = "+" + t + "*"
		} else {
			out[i] = t + "*"
		}
	}
	return out, nil
}

// mysqlSkipped reads, once, which words the server's full-text indexes
// skip. It runs on the query's connection (in its transaction, if any).
func (d *DB) mysqlSkipped(ctx context.Context) (*mysqlWords, error) {
	d.ftMu.Lock()
	w := d.ftWords
	d.ftMu.Unlock()
	if w != nil {
		return w, nil
	}
	type vars struct {
		MinLen      int64  `db:"min_len"`
		MaxLen      int64  `db:"max_len"`
		Enabled     int64  `db:"enabled"`
		UserTable   string `db:"user_table"`
		ServerTable string `db:"server_table"`
	}
	v, err := RawFirst[vars](ctx, "SELECT @@innodb_ft_min_token_size AS min_len, @@innodb_ft_max_token_size AS max_len, @@innodb_ft_enable_stopword AS enabled, "+
		"COALESCE(@@innodb_ft_user_stopword_table, '') AS user_table, COALESCE(@@innodb_ft_server_stopword_table, '') AS server_table")
	if err != nil {
		return nil, fmt.Errorf("db: read MySQL's full-text settings: %w", err)
	}
	w = &mysqlWords{minLen: int(v.MinLen), maxLen: int(v.MaxLen), stop: map[string]bool{}}
	if v.Enabled != 0 {
		words := innodbStopWords
		if table := cmp.Or(v.UserTable, v.ServerTable); table != "" {
			schema, name, _ := strings.Cut(table, "/")
			words, err = Raw[string](ctx, "SELECT value FROM "+d.dialect.QuoteIdent(schema)+"."+d.dialect.QuoteIdent(name))
			if err != nil {
				return nil, fmt.Errorf("db: read MySQL's full-text stop words (%s): %w", table, err)
			}
		}
		for _, s := range words {
			w.stop[strings.ToLower(s)] = true
		}
	}
	d.ftMu.Lock()
	d.ftWords = w
	d.ftMu.Unlock()
	return w, nil
}

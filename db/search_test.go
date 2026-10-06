// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSearchTerms(t *testing.T) {
	for _, c := range []struct {
		text string
		want []string
	}{
		{"  Go, GENERICS! go", []string{"go", "generics"}},
		{"e-mail's 1.18", []string{"e", "mail", "s", "1", "18"}},
		{"আমি বাংলায় গান", []string{"আমি", "বাংলায়", "গান"}},
		{"' OR 1=1; --", []string{"or", "1"}}, // each word once
		{`"quoted" (x) a:* &b`, []string{"quoted", "x", "a", "b"}},
		{"", nil},
		{strings.Repeat("x", 101) + " ok", []string{"ok"}},
	} {
		if got := searchTerms(c.text); !slices.Equal(got, c.want) {
			t.Errorf("searchTerms(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	if got := searchTerms(strings.Repeat("w ", 10) + strings.Repeat("a b c d e f g h i j k l m n o p q r s t u v w x y z aa bb cc dd ee ff gg ", 2)); len(got) != maxSearchTerms {
		t.Errorf("%d terms", len(got))
	}
}

func TestSearchSQL(t *testing.T) {
	for _, c := range []struct {
		dialect Dialect
		cfg     SearchConfig
		want    []string
	}{
		{Postgres(), defaultSearch(), []string{
			`"posts"."search_vector" @@ to_tsquery(CAST($1 AS regconfig), $2)`,
			`ORDER BY ts_rank_cd("posts"."search_vector", to_tsquery(CAST($`,
			`DESC, "posts"."id" ASC`,
		}},
		{Postgres(), SearchConfig{Language: "english", Ranking: "bm25"}, []string{
			`ORDER BY COALESCE("posts"."search_text" <@> to_bm25query($3, $4), 0), ts_rank_cd(`,
		}},
		{SQLite(), defaultSearch(), []string{
			`FROM "posts" JOIN (SELECT rowid AS anetos_rowid, rank AS anetos_rank FROM "posts_search" WHERE "posts_search" MATCH ?) AS anetos_search ON anetos_search.anetos_rowid = "posts".rowid`,
			`ORDER BY anetos_search.anetos_rank, "posts"."id" ASC`,
		}},
	} {
		d := New(nil, c.dialect, WithSearch(c.cfg))
		ctx := WithDB(context.Background(), d)
		q := Query[Post](ctx).OrderBy(C("posts.id").Asc()).Search(`Go "generics"`)
		sql, args := sqlOf(q, c.dialect)
		for _, w := range c.want {
			if !strings.Contains(sql, w) {
				t.Errorf("%s %+v:\n%s\nlacks %s", c.dialect.Name(), c.cfg, sql, w)
			}
		}
		switch c.dialect.Name() {
		case "postgres":
			if !slices.Contains(args, any(`'go' & 'generics':*`)) || !slices.Contains(args, any(c.cfg.Language)) {
				t.Errorf("postgres args: %v", args)
			}
			if c.cfg.Ranking == "bm25" && (!slices.Contains(args, any("go generics")) || !slices.Contains(args, any("posts_search_bm25"))) {
				t.Errorf("bm25 args: %v", args)
			}
		case "sqlite":
			if !slices.Equal(args, []any{`"go" "generics"*`}) {
				t.Errorf("sqlite args: %v", args)
			}
		}
		// Count keeps the filter and drops the ranking.
		cb := q.countSQL(c.dialect)
		if strings.Contains(cb.String(), "ORDER BY") || !strings.Contains(cb.String(), "search") {
			t.Errorf("count: %s", cb.String())
		}
	}
	// Without a DB in the context, Search fails like the query would.
	if _, err := Query[Post](context.Background()).Search("go").Get(); err == nil {
		t.Error("Search without a DB: no error")
	}
	// Without words, nothing changes.
	q := Query[Post](context.Background()).Search("  ")
	if q.err != nil || q.search != nil || len(q.orders) != 0 {
		t.Errorf("Search of no words: %+v", q)
	}
}

func TestSearchConfigValidate(t *testing.T) {
	if err := defaultSearch().Validate(); err != nil {
		t.Error(err)
	}
	for _, c := range []SearchConfig{{Language: "English", Ranking: "default"}, {Language: "x'y", Ranking: "default"}, {Language: "simple", Ranking: "tfidf"}} {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

func TestSearchOrders(t *testing.T) {
	d := New(nil, Postgres())
	ctx := WithDB(context.Background(), d)
	q := Query[Post](ctx).OrderBy(C("posts.id").Asc()).Search("go")
	// Distinct and grouped queries, Exists and counts have no ranking.
	for name, sql := range map[string]string{
		"distinct": q.Distinct().selectSQL(Postgres(), nil).String(),
		"group":    q.GroupBy("title").selectSQL(Postgres(), []string{"title"}).String(),
		"count":    q.Distinct().countSQL(Postgres()).String(),
	} {
		if _, order, _ := strings.Cut(sql, "ORDER BY"); strings.Contains(order, "ts_rank_cd(") {
			t.Errorf("%s ranks: %s", name, sql)
		}
	}
	if sql := q.Distinct().selectSQL(Postgres(), nil).String(); !strings.Contains(sql, `ORDER BY "posts"."id" ASC`) {
		t.Errorf("distinct lost its OrderBy: %s", sql)
	}
	if sql := q.Distinct().countSQL(Postgres()).String(); strings.Contains(sql, "ORDER BY") {
		t.Errorf("count orders: %s", sql)
	}
}

func TestSearchHint(t *testing.T) {
	q := Query[Post](WithDB(context.Background(), New(nil, Postgres()))).Search("go")
	if err := q.searchHint(errors.New(`column posts.search_vector does not exist`)); !strings.Contains(err.Error(), "search index") {
		t.Errorf("missing column: %v", err)
	}
	if err := q.searchHint(errors.New(`for SELECT DISTINCT, ORDER BY expressions must appear in select list: ts_rank_cd(posts.search_vector …`)); strings.Contains(err.Error(), "search index") {
		t.Errorf("another error: %v", err)
	}
}

func TestMySQLTerms(t *testing.T) {
	d := New(nil, MySQL())
	d.ftWords = &mysqlWords{minLen: 3, maxLen: 6, stop: map[string]bool{"the": true}}
	for _, c := range []struct {
		terms, want []string
	}{
		// Indexed words are required; skipped ones optional, and cut to
		// the longest indexed length; skipped short ones left out (they
		// could only match as prefixes).
		{[]string{"go", "rice", "generics", "the"}, []string{"+rice*", "generi*", "the*"}},
		// Skipped words alone are required.
		{[]string{"go", "the"}, []string{"+go*", "+the*"}},
		{[]string{"generics"}, []string{"+generi*"}},
	} {
		got, err := d.mysqlTerms(context.Background(), c.terms)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("mysqlTerms(%q) = %q, %v; want %q", c.terms, got, err, c.want)
		}
	}
}

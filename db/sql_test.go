// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type Post struct {
	Model
	SoftDeletes
	Title    string            `db:"title"`
	AuthorID int64             // author_id
	Meta     map[string]string `db:"meta,json"`
	Rank     *int              `db:"rank"`
	Author   *User             // relation: ignored
	Comments []Comment         // relation: ignored
	Views    int               `db:"views,readonly"`
	secret   string            // unexported: not a column
}

var _ = Post{secret: ""}

type User struct {
	Model
	Name string
}

type Comment struct {
	ID   string `db:"id,pk"`
	Body string
}

type Person struct {
	UserIDs    string
	HTTPStatus int
	CreatedAt  time.Time // not a timestamp: Timestamps isn't embedded
}

type custom struct {
	Code string `db:"code,pk"`
}

func (custom) TableName() string { return "legacy_codes" }

func TestMeta(t *testing.T) {
	m, err := metaOf(reflect.TypeFor[Post]())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range m.cols {
		names = append(names, c.name)
	}
	if got := strings.Join(names, ","); got != "id,created_at,updated_at,deleted_at,title,author_id,meta,rank,views" {
		t.Errorf("columns = %s", got)
	}
	if m.table != "posts" || m.cols[m.pk].name != "id" || !m.autoPK {
		t.Errorf("table %s pk %d auto %v", m.table, m.pk, m.autoPK)
	}
	if m.createdAt != 1 || m.updatedAt != 2 || m.deletedAt != 3 {
		t.Errorf("timestamps %d %d %d", m.createdAt, m.updatedAt, m.deletedAt)
	}
	if !m.cols[m.byName["meta"]].json || !m.cols[m.byName["views"]].readonly {
		t.Error("options not parsed")
	}

	p, _ := metaOf(reflect.TypeFor[Person]())
	if p.table != "people" || p.createdAt != -1 || p.pk != -1 {
		t.Errorf("person: %+v", p)
	}
	if _, ok := p.byName["user_ids"]; !ok {
		t.Errorf("columns: %v", p.byName)
	}
	c, _ := metaOf(reflect.TypeFor[custom]())
	if c.table != "legacy_codes" || c.autoPK {
		t.Errorf("custom: %+v", c)
	}
	cm, _ := metaOf(reflect.TypeFor[Comment]())
	if cm.autoPK || cm.cols[cm.pk].name != "id" {
		t.Errorf("comment pk: %+v", cm)
	}
}

func TestMetaErrors(t *testing.T) {
	type twoPK struct {
		A int `db:"a,pk"`
		B int `db:"b,pk"`
	}
	type dup struct {
		A int `db:"x"`
		B int `db:"x"`
	}
	type badOpt struct {
		A int `db:"a,nope"`
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[twoPK](), reflect.TypeFor[dup](), reflect.TypeFor[badOpt](), reflect.TypeFor[int](), reflect.TypeFor[time.Time]()} {
		if _, err := metaOf(typ); err == nil {
			t.Errorf("%s: no error", typ)
		}
	}
}

func TestNaming(t *testing.T) {
	for in, want := range map[string]string{
		"AuthorID": "author_id", "HTTPStatus": "http_status", "UserIDs": "user_ids", "ID": "id",
		"IDs": "ids", "Title": "title", "Page2Title": "page2_title", "APIKey": "api_key", "createdAt": "created_at",
	} {
		if got := snake(in); got != want {
			t.Errorf("snake(%s) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"post": "posts", "category": "categories", "day": "days", "box": "boxes", "match": "matches",
		"person": "people", "blog_post": "blog_posts", "user_status": "user_statuses", "news": "news",
		"bus": "buses", "posts": "posts", "class": "classes", "alias": "aliases", "axis": "axes",
		"analysis": "analyses", "hero": "heroes", "matrix": "matrices", "settings": "settings",
	} {
		if got := plural(in); got != want {
			t.Errorf("plural(%s) = %s, want %s", in, got, want)
		}
	}
}

func sqlOf[T any](q *Q[T], d Dialect) (string, []any) {
	b := q.selectSQL(d, nil)
	if b.err != nil {
		panic(b.err)
	}
	return b.String(), b.args
}

func TestSelectSQL(t *testing.T) {
	ctx := context.Background()
	title := Col[string]("title")
	q := Query[Post](ctx).
		Where(title.Like("go%"), Or(C("author_id").Eq(int64(1)), C("rank").IsNull())).
		Where(C("id").In(1, 2, 3)).
		OrderBy(title.Asc()).Latest().Limit(10).Offset(20)

	sqlText, args := sqlOf(q, Postgres())
	want := `SELECT "posts"."id", "posts"."created_at", "posts"."updated_at", "posts"."deleted_at", "posts"."title", "posts"."author_id", "posts"."meta", "posts"."rank", "posts"."views" FROM "posts" WHERE (("title" LIKE $1) AND ((("author_id" = $2) OR ("rank" IS NULL))) AND ("id" IN ($3, $4, $5)) AND ("posts"."deleted_at" IS NULL)) ORDER BY "title" ASC, "posts"."created_at" DESC LIMIT 10 OFFSET 20`
	if sqlText != want {
		t.Errorf("postgres:\n got %s\nwant %s", sqlText, want)
	}
	if len(args) != 5 {
		t.Errorf("args = %v", args)
	}
	my, _ := sqlOf(q, MySQL())
	if !strings.Contains(my, "`title` LIKE ?") || !strings.HasSuffix(my, "LIMIT 10 OFFSET 20") {
		t.Errorf("mysql: %s", my)
	}

	off, _ := sqlOf(Query[Post](ctx).WithTrashed().Offset(5), MySQL())
	if !strings.HasSuffix(off, "FROM `posts` LIMIT 18446744073709551615 OFFSET 5") {
		t.Errorf("mysql offset: %s", off)
	}
	off, _ = sqlOf(Query[Post](ctx).OnlyTrashed().Offset(5), SQLite())
	if !strings.HasSuffix(off, `WHERE "posts"."deleted_at" IS NOT NULL LIMIT -1 OFFSET 5`) {
		t.Errorf("sqlite offset: %s", off)
	}
	lock, _ := sqlOf(Query[User](ctx).ForUpdate(), Postgres())
	if !strings.HasSuffix(lock, "FOR UPDATE") {
		t.Errorf("lock: %s", lock)
	}
	lock, _ = sqlOf(Query[User](ctx).ForUpdate(), SQLite())
	if strings.Contains(lock, "FOR") {
		t.Errorf("sqlite lock: %s", lock)
	}
}

func TestQueriesAreImmutable(t *testing.T) {
	base := Query[User](context.Background()).Where(C("a").Eq(1))
	a := base.Where(C("b").Eq(2))
	b := base.Where(C("c").Eq(3))
	sa, _ := sqlOf(a, Postgres())
	sb, _ := sqlOf(b, Postgres())
	if strings.Contains(sa, `"c"`) || strings.Contains(sb, `"b"`) {
		t.Errorf("queries share state:\n%s\n%s", sa, sb)
	}
}

func TestExpressions(t *testing.T) {
	build := func(e Expr) (string, []any) {
		b := &sqlBuilder{d: Postgres()}
		e.build(b)
		if b.err != nil {
			t.Fatal(b.err)
		}
		return b.String(), b.args
	}
	var nilPtr *int
	cases := []struct {
		e    Expr
		want string
	}{
		{C("a").In(), "1 = 0"},
		{C("a").NotIn(), "1 = 1"},
		{And(), "1 = 1"},
		{Or(), "1 = 0"},
		{And(C("a").Eq(1)), `"a" = $1`},
		{Not(C("a").Gt(1)), `NOT ("a" > $1)`},
		{Col[*int]("a").Eq(nilPtr), `"a" IS NULL`},
		{Col[*int]("a").Ne(nil), `"a" IS NOT NULL`},
		{C("t.a").Between(1, 2), `"t"."a" BETWEEN $1 AND $2`},
		{SQL("x = ? AND y ??| ? AND z = ?", 1, 2, 3), `x = $1 AND y ?| $2 AND z = $3`},
		{C(`we"ird`).Eq(1), `"we""ird" = $1`},
	}
	for _, c := range cases {
		if got, _ := build(c.e); got != c.want {
			t.Errorf("got %s, want %s", got, c.want)
		}
	}
	b := &sqlBuilder{d: Postgres()}
	SQL("a = ?", 1, 2).build(b)
	if b.err == nil {
		t.Error("placeholder count mismatch not reported")
	}
	b = &sqlBuilder{d: Postgres()}
	And(C("x").Eq(1), SQL("a = $1", 2)).build(b)
	if b.err == nil || !strings.Contains(b.err.Error(), "no ? placeholders") {
		t.Errorf("native placeholder in a fragment: %v", b.err)
	}
}

func TestRebind(t *testing.T) {
	cases := []struct {
		d          Dialect
		in, want   string
		args, outN int
	}{
		{Postgres(), "SELECT * FROM t WHERE a = ? AND b = ?", "SELECT * FROM t WHERE a = $1 AND b = $2", 2, 2},
		{Postgres(), "SELECT '?' , \"?\" -- ?\n, a = ? /* ? */", "SELECT '?' , \"?\" -- ?\n, a = $1 /* ? */", 1, 1},
		{Postgres(), "SELECT $$ ? $$, $tag$ ? $tag$, a::text = ?", "SELECT $$ ? $$, $tag$ ? $tag$, a::text = $1", 1, 1},
		{Postgres(), "SELECT 'it''s ?' = ?", "SELECT 'it''s ?' = $1", 1, 1},
		{Postgres(), `SELECT 'C:\' = ?`, `SELECT 'C:\' = $1`, 1, 1},
		{MySQL(), `SELECT 'a\' ?' = ?`, `SELECT 'a\' ?' = ?`, 1, 1},
		{Postgres(), "SELECT * FROM t WHERE a = $1", "SELECT * FROM t WHERE a = $1", 1, 1},
		{MySQL(), "SELECT ? # what?\n, ?", "SELECT ? # what?\n, ?", 2, 2},
		{Postgres(), `SELECT E'it\'s ?', e'?' , ?`, `SELECT E'it\'s ?', e'?' , $1`, 1, 1},
		{Postgres(), "SELECT /* a /* nested ? */ still ? */ ?", "SELECT /* a /* nested ? */ still ? */ $1", 1, 1},
		{MySQL(), "SELECT /* a /* b */ ?", "SELECT /* a /* b */ ?", 1, 1},
		{Postgres(), "SELECT name = ?", "SELECT name = $1", 1, 1},
	}
	for _, c := range cases {
		args := make([]any, c.args)
		got, out, err := rebind(c.d, c.in, args)
		if err != nil || got != c.want || len(out) != c.outN {
			t.Errorf("rebind(%q) = %q %d %v, want %q", c.in, got, len(out), err, c.want)
		}
	}
	got, out, err := rebind(Postgres(), "SELECT :a, :b::int, :a, ':c'", []any{Named{"a": 1, "b": 2}})
	if err != nil || got != "SELECT $1, $2::int, $3, ':c'" || len(out) != 3 || out[2] != 1 {
		t.Errorf("named: %q %v %v", got, out, err)
	}
	for _, bad := range []struct {
		q    string
		args []any
	}{
		{"a = ? and b = ?", []any{1}},
		{"a = :missing", []any{Named{"x": 1}}},
		{"a = 1", []any{Named{"x": 1}}},
		{"a = :x and b = ?", []any{Named{"x": 1}}},
	} {
		if _, _, err := rebind(Postgres(), bad.q, bad.args); err == nil {
			t.Errorf("rebind(%q) succeeded", bad.q)
		}
	}
}

func TestWriteSQL(t *testing.T) {
	// Mass writes refuse clauses that don't port across databases.
	_, err := Query[User](context.Background()).Limit(1).Update(C("name").Set("x"))
	if err == nil || !strings.Contains(err.Error(), "only support Where") {
		t.Errorf("err = %v", err)
	}
	if _, err := Query[User](context.Background()).Update(); err == nil {
		t.Error("empty update accepted")
	}
}

func TestNoDB(t *testing.T) {
	if _, err := Query[User](context.Background()).Get(); !errors.Is(err, ErrNoDB) {
		t.Errorf("err = %v", err)
	}
	if _, err := Query[int](context.Background()).Get(); err == nil || !strings.Contains(err.Error(), "not a model") {
		t.Errorf("err = %v", err)
	}
}

func TestTimeArgs(t *testing.T) {
	zoned := time.Date(2026, 9, 30, 18, 0, 0, 500_000_000, time.FixedZone("BST", 6*3600))
	if got := SQLite().Arg(zoned); got != "2026-09-30 12:00:00.5" {
		t.Errorf("sqlite time = %v", got)
	}
	if got := SQLite().Arg(&zoned); got != "2026-09-30 12:00:00.5" {
		t.Errorf("sqlite *time = %v", got)
	}
	var nilTime *time.Time
	if got := SQLite().Arg(nilTime); got != nil {
		t.Errorf("sqlite nil = %v", got)
	}
	if got := Postgres().Arg(zoned).(time.Time); got.Location() != time.UTC || !got.Equal(zoned) {
		t.Errorf("postgres time = %v", got)
	}
	null := sql.Null[time.Time]{V: zoned, Valid: true}
	if got := SQLite().Arg(null); got != "2026-09-30 12:00:00.5" {
		t.Errorf("sqlite Null[time] = %v", got)
	}
	if got := SQLite().Arg(sql.Null[time.Time]{}); got != (sql.Null[time.Time]{}) {
		t.Errorf("invalid Null left alone: %v", got)
	}
	if got := MySQL().Arg(zoned); got != zoned {
		t.Errorf("mysql converts in the driver: %v", got)
	}
}

func TestUpsertClauses(t *testing.T) {
	if got := Postgres().Upsert([]string{`"sku"`}, []string{`"price"`}); got != `ON CONFLICT ("sku") DO UPDATE SET "price" = EXCLUDED."price"` {
		t.Error(got)
	}
	if got := SQLite().Upsert([]string{`"sku"`}, nil); got != `ON CONFLICT ("sku") DO NOTHING` {
		t.Error(got)
	}
	if got := MySQL().Upsert([]string{"`sku`"}, []string{"`price`"}); got != "ON DUPLICATE KEY UPDATE `price` = VALUES(`price`)" {
		t.Error(got)
	}
	if got := MySQL().Upsert([]string{"`sku`"}, nil); got != "ON DUPLICATE KEY UPDATE `sku` = `sku`" {
		t.Error(got)
	}
}

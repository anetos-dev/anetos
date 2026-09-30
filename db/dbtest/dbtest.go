// SPDX-License-Identifier: Apache-2.0

// Package dbtest is a conformance suite for db drivers. Each driver module
// runs it against a real database:
//
//	func TestConformance(t *testing.T) {
//		d, err := db.Open(sqlite.Driver(), cfg)
//		…
//		dbtest.Run(t, d)
//	}
//
// The suite creates and drops its own tables, all named st_*.
package dbtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/validate"
)

// Author is a model with timestamps and a unique email.
type Author struct {
	db.Model
	Name  string `db:"name" json:"name"`
	Email string `db:"email" json:"email"`
}

// TableName implements db.Tabler.
func (Author) TableName() string { return "st_authors" }

// Post is a model with soft deletes, a JSON column and nullable fields.
type Post struct {
	db.Model
	db.SoftDeletes
	AuthorID    int64             `db:"author_id"`
	Title       string            `db:"title"`
	Body        *string           `db:"body"`
	Views       int               `db:"views"`
	Score       float64           `db:"score"`
	Published   bool              `db:"published"`
	Meta        map[string]string `db:"meta,json"`
	PublishedAt *time.Time        `db:"published_at"`

	hookLog []string
}

// TableName implements db.Tabler.
func (Post) TableName() string { return "st_posts" }

var errRejected = errors.New("rejected by hook")

// BeforeSave trims the title and rejects the title "reject".
func (p *Post) BeforeSave(context.Context) error {
	p.hookLog = append(p.hookLog, "beforeSave")
	if p.Title == "reject" {
		return errRejected
	}
	p.Title = strings.TrimSpace(p.Title)
	return nil
}

// AfterCreate records that it ran.
func (p *Post) AfterCreate(context.Context) error {
	p.hookLog = append(p.hookLog, "afterCreate")
	return nil
}

// BeforeDelete records that it ran.
func (p *Post) BeforeDelete(context.Context) error {
	p.hookLog = append(p.hookLog, "beforeDelete")
	return nil
}

// Tag has a string primary key.
type Tag struct {
	Code  string `db:"code,pk"`
	Label string `db:"label"`
}

// TableName implements db.Tabler.
func (Tag) TableName() string { return "st_tags" }

var (
	title     = db.Col[string]("title")
	views     = db.Col[int]("views")
	score     = db.Col[float64]("score")
	authorID  = db.Col[int64]("author_id")
	published = db.Col[bool]("published")
	createdAt = db.Col[time.Time]("created_at")
	id        = db.Col[int64]("id")
)

var schemas = map[string][]string{
	"postgres": {
		`CREATE TABLE st_authors (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE st_posts (id BIGSERIAL PRIMARY KEY, author_id BIGINT NOT NULL, title TEXT NOT NULL, body TEXT,
			views INTEGER NOT NULL DEFAULT 0, score DOUBLE PRECISION NOT NULL DEFAULT 0, published BOOLEAN NOT NULL DEFAULT FALSE,
			meta JSONB, published_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, deleted_at TIMESTAMPTZ)`,
		`CREATE TABLE st_tags (code VARCHAR(32) PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE st_notes (id BIGSERIAL PRIMARY KEY, text TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE st_events (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL, at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, logged TIMESTAMP, day DATE)`,
	},
	"mysql": {
		`CREATE TABLE st_authors (id BIGINT AUTO_INCREMENT PRIMARY KEY, name TEXT NOT NULL, email VARCHAR(191) NOT NULL UNIQUE,
			created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL)`,
		`CREATE TABLE st_posts (id BIGINT AUTO_INCREMENT PRIMARY KEY, author_id BIGINT NOT NULL, title TEXT NOT NULL, body TEXT,
			views INT NOT NULL DEFAULT 0, score DOUBLE NOT NULL DEFAULT 0, published BOOLEAN NOT NULL DEFAULT FALSE,
			meta JSON, published_at DATETIME(6), created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL, deleted_at DATETIME(6))`,
		`CREATE TABLE st_tags (code VARCHAR(32) PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE st_notes (id BIGINT AUTO_INCREMENT PRIMARY KEY, text TEXT NOT NULL, created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL)`,
		`CREATE TABLE st_events (id BIGINT AUTO_INCREMENT PRIMARY KEY, name TEXT NOT NULL, at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, logged DATETIME(6), day DATE)`,
	},
	"sqlite": {
		`CREATE TABLE st_authors (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE st_posts (id INTEGER PRIMARY KEY AUTOINCREMENT, author_id INTEGER NOT NULL, title TEXT NOT NULL, body TEXT,
			views INTEGER NOT NULL DEFAULT 0, score REAL NOT NULL DEFAULT 0, published BOOLEAN NOT NULL DEFAULT FALSE,
			meta TEXT, published_at DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE st_tags (code VARCHAR(32) PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE st_notes (id INTEGER PRIMARY KEY AUTOINCREMENT, text TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE st_events (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, logged DATETIME, day DATE)`,
	},
}

var tables = []string{"st_authors", "st_posts", "st_tags", "st_notes", "st_events"}

// Run runs the suite against d, whose dialect must be postgres, mysql or
// sqlite.
func Run(t *testing.T, d *db.DB) {
	t.Helper()
	ddl, ok := schemas[d.Dialect().Name()]
	if !ok {
		t.Fatalf("dbtest: no schema for dialect %q", d.Dialect().Name())
	}
	ctx := db.WithDB(t.Context(), d)
	reset := func(t *testing.T) {
		t.Helper()
		for _, table := range tables {
			mustExec(t, ctx, "DROP TABLE IF EXISTS "+table)
		}
		for _, stmt := range ddl {
			mustExec(t, ctx, stmt)
		}
	}
	t.Cleanup(func() {
		for _, table := range tables {
			_, _ = db.Exec(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS "+table)
		}
	})

	tests := []struct {
		name string
		fn   func(*testing.T, context.Context)
	}{
		{"CRUD", testCRUD},
		{"Hooks", testHooks},
		{"SoftDeletes", testSoftDeletes},
		{"Queries", testQueries},
		{"Aggregates", testAggregates},
		{"Paginate", testPaginate},
		{"CursorPaginate", testCursorPaginate},
		{"Raw", testRaw},
		{"Transactions", testTransactions},
		{"CreateManyAndUpsert", testBulk},
		{"Types", testTypes},
		{"ValidationRules", testValidationRules},
		{"Concurrency", testConcurrency},
		{"StaleUpdates", testStaleUpdates},
		{"TxRelease", testTxRelease},
		{"WriteRestrictions", testWriteRestrictions},
		{"LimitedAggregates", testLimitedAggregates},
		{"EmbeddedPointer", testEmbeddedPointer},
		{"DatabaseWrittenTimes", testDatabaseWrittenTimes},
		{"SoftDeleteScopes", testSoftDeleteScopes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			tt.fn(t, ctx)
		})
	}
}

func mustExec(t *testing.T, ctx context.Context, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func seedAuthors(t *testing.T, ctx context.Context, names ...string) []Author {
	t.Helper()
	out := make([]Author, len(names))
	for i, n := range names {
		out[i] = Author{Name: n, Email: strings.ToLower(n) + "@example.com"}
		check(t, db.Create(ctx, &out[i]))
	}
	return out
}

func seedPosts(t *testing.T, ctx context.Context, n int) []Post {
	t.Helper()
	posts := make([]Post, n)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range posts {
		posts[i] = Post{
			AuthorID:  int64(i%3 + 1),
			Title:     fmt.Sprintf("post %02d", i),
			Views:     i * 10,
			Score:     float64(i) / 2,
			Published: i%2 == 0,
		}
		posts[i].CreatedAt = base.Add(time.Duration(i) * time.Hour)
		check(t, db.Create(ctx, &posts[i]))
	}
	return posts
}

func testCRUD(t *testing.T, ctx context.Context) {
	before := time.Now().Add(-time.Second)
	a := Author{Name: "Ada", Email: "ada@example.com"}
	check(t, db.Create(ctx, &a))
	if a.ID == 0 || a.CreatedAt.Before(before) || !a.CreatedAt.Equal(a.UpdatedAt) || a.CreatedAt.Location() != time.UTC {
		t.Fatalf("after Create: %+v", a)
	}
	got, err := db.Find[Author](ctx, a.ID)
	check(t, err)
	if got.Name != "Ada" || !got.CreatedAt.Equal(a.CreatedAt) || got.CreatedAt.Location() != time.UTC {
		t.Errorf("Find = %+v, want %+v", got, a)
	}

	time.Sleep(2 * time.Millisecond)
	got.Name = "Ada Lovelace"
	check(t, db.Update(ctx, &got))
	if !got.UpdatedAt.After(a.UpdatedAt) {
		t.Errorf("updated_at not advanced: %v → %v", a.UpdatedAt, got.UpdatedAt)
	}
	again, err := db.Find[Author](ctx, a.ID)
	check(t, err)
	if again.Name != "Ada Lovelace" || !again.CreatedAt.Equal(a.CreatedAt) || !again.UpdatedAt.Equal(got.UpdatedAt) {
		t.Errorf("after Update: %+v", again)
	}

	b := Author{Name: "Bob", Email: "bob@example.com"}
	check(t, db.Save(ctx, &b)) // creates
	b.Name = "Robert"
	check(t, db.Save(ctx, &b)) // updates
	if n, _ := db.Query[Author](ctx).Count(); n != 2 {
		t.Errorf("count = %d", n)
	}

	check(t, db.Delete(ctx, &b))
	if _, err := db.Find[Author](ctx, b.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("after Delete: %v", err)
	}
	var sc interface{ HTTPStatus() int }
	if !errors.As(db.ErrNotFound, &sc) || sc.HTTPStatus() != http.StatusNotFound {
		t.Error("ErrNotFound is not a 404")
	}
	if err := db.Delete(ctx, &b); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("second Delete: %v", err)
	}
	if err := db.Update(ctx, &Author{}); err == nil {
		t.Error("Update with zero key succeeded")
	}

	tag := Tag{Code: "go", Label: "Go"}
	check(t, db.Create(ctx, &tag))
	tg, err := db.Find[Tag](ctx, "go")
	check(t, err)
	if tg != tag {
		t.Errorf("tag = %+v", tg)
	}
	if err := db.Save(ctx, &tag); err == nil {
		t.Error("Save on a string key succeeded")
	}
}

func testHooks(t *testing.T, ctx context.Context) {
	p := Post{AuthorID: 1, Title: "  padded  "}
	check(t, db.Create(ctx, &p))
	if p.Title != "padded" || !slices.Equal(p.hookLog, []string{"beforeSave", "afterCreate"}) {
		t.Errorf("hooks: %q %v", p.Title, p.hookLog)
	}
	bad := Post{AuthorID: 1, Title: "reject"}
	if err := db.Create(ctx, &bad); !errors.Is(err, errRejected) {
		t.Errorf("rejecting hook: %v", err)
	}
	if n, _ := db.Query[Post](ctx).Count(); n != 1 {
		t.Errorf("rejected row was written: %d rows", n)
	}
	p.hookLog = nil
	p.Title = "reject"
	if err := db.Update(ctx, &p); !errors.Is(err, errRejected) {
		t.Errorf("update hook: %v", err)
	}
}

func testSoftDeletes(t *testing.T, ctx context.Context) {
	posts := seedPosts(t, ctx, 4)
	p := posts[0]
	check(t, db.Delete(ctx, &p))
	if !p.Trashed() || !slices.Contains(p.hookLog, "beforeDelete") {
		t.Errorf("after soft delete: %+v", p)
	}
	if _, err := db.Find[Post](ctx, p.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("Find soft-deleted: %v", err)
	}
	trashed, err := db.Query[Post](ctx).WithTrashed().Find(p.ID)
	check(t, err)
	if trashed.DeletedAt == nil || !trashed.DeletedAt.Equal(*p.DeletedAt) {
		t.Errorf("trashed row: %+v", trashed)
	}
	if n, _ := db.Query[Post](ctx).Count(); n != 3 {
		t.Errorf("count without trashed = %d", n)
	}
	if n, _ := db.Query[Post](ctx).OnlyTrashed().Count(); n != 1 {
		t.Errorf("only trashed = %d", n)
	}
	check(t, db.Restore(ctx, &p))
	if p.Trashed() {
		t.Error("still trashed after Restore")
	}
	if _, err := db.Find[Post](ctx, p.ID); err != nil {
		t.Errorf("after Restore: %v", err)
	}

	n, err := db.Query[Post](ctx).Where(authorID.Eq(2)).Delete()
	check(t, err)
	if n != 1 {
		t.Errorf("mass soft delete = %d", n)
	}
	n, err = db.Query[Post](ctx).Where(authorID.Eq(2)).Restore()
	check(t, err)
	if n != 1 {
		t.Errorf("mass restore = %d", n)
	}

	check(t, db.ForceDelete(ctx, &p))
	if _, err := db.Query[Post](ctx).WithTrashed().Find(p.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("after ForceDelete: %v", err)
	}
}

func testQueries(t *testing.T, ctx context.Context) {
	seedAuthors(t, ctx, "Ada", "Bob", "Cy")
	seedPosts(t, ctx, 10)
	q := db.Query[Post](ctx)

	count := func(q *db.Q[Post]) int64 {
		t.Helper()
		n, err := q.Count()
		check(t, err)
		return n
	}
	cases := []struct {
		name string
		q    *db.Q[Post]
		want int64
	}{
		{"eq", q.Where(authorID.Eq(1)), 4},
		{"in", q.Where(authorID.In(2, 3)), 6},
		{"empty in", q.Where(authorID.In()), 0},
		{"not in", q.Where(authorID.NotIn(1)), 6},
		{"like", q.Where(title.Like("post 0%")), 10},
		{"between", q.Where(views.Between(20, 50)), 4},
		{"gt/lte", q.Where(views.Gt(30), views.Lte(60)), 3},
		{"or", q.Where(db.Or(views.Eq(0), views.Eq(90))), 2},
		{"not", q.Where(db.Not(published.Eq(true))), 5},
		{"null", q.Where(db.C("body").IsNull()), 10},
		{"raw", q.WhereRaw("views >= ? AND author_id = ?", 30, 1), 3},
		{"scope", q.Scope(func(q *db.Q[Post]) *db.Q[Post] { return q.Where(published.Eq(true)) }), 5},
		{"distinct", q.Distinct(), 10},
		{"limited", q.Limit(3), 3},
		{"offset", q.Offset(8), 2},
		{"group", q.GroupBy("author_id"), 3},
		{"join", q.Join("JOIN st_authors a ON a.id = st_posts.author_id").Where(db.C("a.name").Eq("Bob")), 3},
	}
	for _, c := range cases {
		if got := count(c.q); got != c.want {
			t.Errorf("%s: count = %d, want %d", c.name, got, c.want)
		}
	}

	rows, err := q.Where(authorID.Eq(1)).OrderBy(views.Desc()).Limit(2).Offset(1).Get()
	check(t, err)
	if len(rows) != 2 || rows[0].Views != 60 || rows[1].Views != 30 {
		t.Errorf("ordered page: %+v", rows)
	}
	latest, err := q.Latest().First()
	check(t, err)
	if latest.Title != "post 09" {
		t.Errorf("latest = %s", latest.Title)
	}
	oldest, err := q.Oldest().First()
	check(t, err)
	if oldest.Title != "post 00" {
		t.Errorf("oldest = %s", oldest.Title)
	}
	if _, err := q.Where(views.Gt(1000)).First(); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("First on nothing: %v", err)
	}
	empty, err := q.Where(views.Gt(1000)).Get()
	check(t, err)
	if empty == nil || len(empty) != 0 {
		t.Errorf("empty Get = %#v", empty)
	}
	ok, err := q.Where(views.Eq(90)).Exists()
	check(t, err)
	none, err := q.Where(views.Eq(91)).Exists()
	check(t, err)
	if !ok || none {
		t.Errorf("Exists = %v %v", ok, none)
	}

	var seen int
	for p, err := range q.OrderBy(id.Asc()).All() {
		check(t, err)
		if p.Title != fmt.Sprintf("post %02d", seen) {
			t.Errorf("All row %d = %s", seen, p.Title)
		}
		seen++
		if seen == 5 {
			break
		}
	}
	if seen != 5 {
		t.Errorf("All yielded %d", seen)
	}

	n, err := q.Where(authorID.Eq(1)).Update(views.SetRaw("views + ?", 1), title.Set("bumped"))
	check(t, err)
	if n != 4 || count(q.Where(title.Eq("bumped"), views.Eq(1))) != 1 {
		t.Errorf("mass update: %d", n)
	}

	check(t, db.Tx(ctx, func(ctx context.Context) error {
		p, err := db.Query[Post](ctx).Where(id.Eq(1)).ForUpdate().First()
		if err != nil {
			return err
		}
		p.Views++
		return db.Update(ctx, &p)
	}))

	n, err = q.Where(authorID.Eq(3)).ForceDelete()
	check(t, err)
	if n != 3 || count(q.WithTrashed()) != 7 {
		t.Errorf("mass force delete: %d", n)
	}
}

func testAggregates(t *testing.T, ctx context.Context) {
	seedPosts(t, ctx, 5) // views 0..40, score 0..2
	q := db.Query[Post](ctx)
	sum, err := db.Sum(q, views)
	check(t, err)
	avg, err := db.Avg(q, views)
	check(t, err)
	lo, err := db.Min(q, score)
	check(t, err)
	hi, err := db.Max(q, createdAt)
	check(t, err)
	if sum != 100 || avg != 20 || lo != 0 || hi.Year() != 2026 || hi.Hour() != 16 {
		t.Errorf("sum %d avg %v min %v max %v", sum, avg, lo, hi)
	}
	zero, err := db.Sum(q.Where(views.Gt(1000)), views)
	check(t, err)
	if zero != 0 {
		t.Errorf("sum of nothing = %d", zero)
	}
	titles, err := db.Pluck(q.OrderBy(views.Desc()).Limit(2), title)
	check(t, err)
	if !slices.Equal(titles, []string{"post 04", "post 03"}) {
		t.Errorf("pluck = %v", titles)
	}

	type stat struct {
		AuthorID int64 `db:"author_id"`
		Posts    int64 `db:"posts"`
		Views    int64 `db:"total_views"`
	}
	stats, err := db.Select[stat](q.GroupBy("author_id").Having(db.SQL("COUNT(*) > ?", 1)).OrderBy(authorID.Asc()),
		"author_id", "COUNT(*) AS posts", "SUM(views) AS total_views")
	check(t, err)
	want := []stat{{1, 2, 30}, {2, 2, 50}}
	if !slices.Equal(stats, want) {
		t.Errorf("stats = %+v, want %+v", stats, want)
	}
}

func testPaginate(t *testing.T, ctx context.Context) {
	seedPosts(t, ctx, 7)
	q := db.Query[Post](ctx).OrderBy(id.Asc())
	p, err := q.Paginate(2, 3)
	check(t, err)
	if p.Total != 7 || p.LastPage != 3 || p.CurrentPage != 2 || len(p.Data) != 3 || p.Data[0].Title != "post 03" || !p.HasMore() {
		t.Errorf("page 2: %+v", p)
	}
	last, err := q.Paginate(3, 3)
	check(t, err)
	if len(last.Data) != 1 || last.HasMore() {
		t.Errorf("last page: %+v", last)
	}
	past, err := q.Paginate(9, 3)
	check(t, err)
	if past.Data == nil || len(past.Data) != 0 || past.Total != 7 {
		t.Errorf("past the end: %+v", past)
	}
	def, err := q.Paginate(0, 0)
	check(t, err)
	if def.CurrentPage != 1 || def.PerPage != db.DefaultPerPage {
		t.Errorf("defaults: %+v", def)
	}
	emptyQ, err := q.Where(views.Gt(1000)).Paginate(1, 3)
	check(t, err)
	if emptyQ.LastPage != 1 || emptyQ.Total != 0 {
		t.Errorf("empty: %+v", emptyQ)
	}
}

func testCursorPaginate(t *testing.T, ctx context.Context) {
	posts := seedPosts(t, ctx, 10)
	// Two posts share a view count, to exercise the primary-key tie-breaker.
	mustExec(t, ctx, "UPDATE st_posts SET views = 40 WHERE id = ?", posts[5].ID)
	q := db.Query[Post](ctx).OrderBy(views.Desc())

	var titles []string
	var pages []db.CursorPage[Post]
	cursor := ""
	for range 10 {
		page, err := q.CursorPaginate(cursor, 3)
		check(t, err)
		pages = append(pages, page)
		for _, p := range page.Data {
			titles = append(titles, p.Title)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	want := []string{"post 09", "post 08", "post 07", "post 06", "post 04", "post 05", "post 03", "post 02", "post 01", "post 00"}
	if !slices.Equal(titles, want) {
		t.Fatalf("forward: %v\nwant %v", titles, want)
	}
	if len(pages) != 4 || pages[0].PrevCursor != "" || pages[1].PrevCursor == "" {
		t.Fatalf("pages: %d, first prev %q", len(pages), pages[0].PrevCursor)
	}

	// Walk back from the last page.
	var back []string
	cursor = pages[len(pages)-1].PrevCursor
	for range 10 {
		page, err := q.CursorPaginate(cursor, 3)
		check(t, err)
		back = append(append([]string{}, titlesOf(page.Data)...), back...)
		if page.PrevCursor == "" {
			if page.NextCursor == "" {
				t.Error("backward page has no next cursor")
			}
			break
		}
		cursor = page.PrevCursor
	}
	if !slices.Equal(back, want[:9]) {
		t.Errorf("backward: %v\nwant %v", back, want[:9])
	}

	if _, err := q.CursorPaginate("not-a-cursor", 3); !errors.Is(err, db.ErrInvalidCursor) {
		t.Errorf("bad cursor: %v", err)
	}
	if _, err := q.OrderBy(db.OrderRaw("lower(title)")).CursorPaginate("", 3); err == nil {
		t.Error("raw order accepted")
	}
	byTime, err := db.Query[Post](ctx).OrderBy(createdAt.Asc()).CursorPaginate("", 4)
	check(t, err)
	next, err := db.Query[Post](ctx).OrderBy(createdAt.Asc()).CursorPaginate(byTime.NextCursor, 4)
	check(t, err)
	if len(next.Data) != 4 || next.Data[0].Title != "post 04" {
		t.Errorf("time cursor: %v", titlesOf(next.Data))
	}
}

func titlesOf(ps []Post) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}

func testRaw(t *testing.T, ctx context.Context) {
	seedAuthors(t, ctx, "Ada", "Bob")
	seedPosts(t, ctx, 6)
	type row struct {
		Name  string `db:"name"`
		Total int64  `db:"total"`
	}
	rows, err := db.Raw[row](ctx, `
		SELECT a.name, COUNT(p.id) AS total
		FROM st_authors a JOIN st_posts p ON p.author_id = a.id
		WHERE p.views >= ? -- a comment with a ? in it
		GROUP BY a.name ORDER BY a.name`, 10)
	check(t, err)
	if len(rows) != 2 || rows[0] != (row{"Ada", 1}) || rows[1] != (row{"Bob", 2}) {
		t.Errorf("raw rows = %+v", rows)
	}
	n, err := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM st_posts WHERE title <> '?' AND views > :min AND author_id IN (:a, :b)",
		db.Named{"min": 0, "a": 1, "b": 2})
	check(t, err)
	if n != 3 {
		t.Errorf("named count = %d", n)
	}
	names, err := db.Raw[string](ctx, "SELECT name FROM st_authors ORDER BY name DESC")
	check(t, err)
	if !slices.Equal(names, []string{"Bob", "Ada"}) {
		t.Errorf("names = %v", names)
	}
	if _, err := db.RawFirst[string](ctx, "SELECT name FROM st_authors WHERE name = ?", "nobody"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("RawFirst none: %v", err)
	}
	// Struct fields that aren't selected keep their zero values; extra
	// columns are ignored.
	authors, err := db.Raw[Author](ctx, "SELECT id, name, 42 AS extra FROM st_authors ORDER BY id")
	check(t, err)
	if len(authors) != 2 || authors[0].Name != "Ada" || authors[0].Email != "" {
		t.Errorf("partial scan: %+v", authors)
	}
	if _, err := db.Raw[int64](ctx, "SELECT id, name FROM st_authors"); err == nil {
		t.Error("two columns into a scalar accepted")
	}
	type strict struct {
		Body string `db:"body"`
	}
	if _, err := db.Raw[strict](ctx, "SELECT body FROM st_posts"); err == nil || !strings.Contains(err.Error(), "pointer") {
		t.Errorf("NULL into string: %v", err)
	}
	res, err := db.Exec(ctx, "UPDATE st_posts SET views = views + ? WHERE author_id = ?", 1, 1)
	check(t, err)
	if n, _ := res.RowsAffected(); n != 2 {
		t.Errorf("Exec affected %d", n)
	}
}

func testTransactions(t *testing.T, ctx context.Context) {
	count := func(ctx context.Context) int64 {
		t.Helper()
		n, err := db.Query[Author](ctx).Count()
		check(t, err)
		return n
	}
	var committed []string
	check(t, db.Tx(ctx, func(ctx context.Context) error {
		if !db.InTx(ctx) {
			t.Error("InTx = false inside Tx")
		}
		seedAuthors(t, ctx, "Ada")
		db.AfterCommit(ctx, func(context.Context) { committed = append(committed, "outer") })
		// A failing nested transaction rolls back to its savepoint only.
		err := db.Tx(ctx, func(ctx context.Context) error {
			seedAuthors(t, ctx, "Bob")
			db.AfterCommit(ctx, func(context.Context) { committed = append(committed, "discarded") })
			return errors.New("inner failure")
		})
		if err == nil || err.Error() != "inner failure" {
			t.Errorf("nested error: %v", err)
		}
		check(t, db.Tx(ctx, func(ctx context.Context) error {
			seedAuthors(t, ctx, "Cy")
			db.AfterCommit(ctx, func(context.Context) { committed = append(committed, "inner") })
			return nil
		}))
		if len(committed) != 0 {
			t.Error("AfterCommit ran before commit")
		}
		return nil
	}))
	if count(ctx) != 2 || !slices.Equal(committed, []string{"outer", "inner"}) {
		t.Errorf("after commit: %d authors, callbacks %v", count(ctx), committed)
	}

	boom := errors.New("boom")
	err := db.Tx(ctx, func(ctx context.Context) error {
		seedAuthors(t, ctx, "Dee")
		db.AfterCommit(ctx, func(context.Context) { t.Error("AfterCommit ran after rollback") })
		return boom
	})
	if !errors.Is(err, boom) || count(ctx) != 2 {
		t.Errorf("rollback: %v, %d authors", err, count(ctx))
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		_ = db.Tx(ctx, func(ctx context.Context) error {
			seedAuthors(t, ctx, "Eve")
			panic("oops")
		})
	}()
	if count(ctx) != 2 {
		t.Errorf("panic didn't roll back: %d", count(ctx))
	}

	ran := false
	db.AfterCommit(ctx, func(context.Context) { ran = true })
	if !ran || db.InTx(ctx) {
		t.Error("AfterCommit outside a transaction didn't run at once")
	}

	check(t, db.TxWith(ctx, &sql.TxOptions{ReadOnly: d(ctx).Dialect().Name() != "sqlite"}, func(ctx context.Context) error {
		_, err := db.Query[Author](ctx).Get()
		return err
	}))
}

func d(ctx context.Context) *db.DB {
	x, _ := db.From(ctx)
	return x
}

func testBulk(t *testing.T, ctx context.Context) {
	authors := make([]Author, 50)
	for i := range authors {
		authors[i] = Author{Name: fmt.Sprint("a", i), Email: fmt.Sprintf("a%d@example.com", i)}
	}
	check(t, db.CreateMany(ctx, authors))
	if n, _ := db.Query[Author](ctx).Count(); n != 50 {
		t.Errorf("count = %d", n)
	}
	if d(ctx).Dialect().Returning() {
		for i, a := range authors {
			got, err := db.Find[Author](ctx, a.ID)
			if err != nil || got.Name != a.Name {
				t.Fatalf("row %d: id %d → %+v %v", i, a.ID, got, err)
			}
		}
	}
	if authors[0].CreatedAt.IsZero() {
		t.Error("timestamps not set")
	}

	tags := []Tag{{"go", "Go"}, {"sql", "SQL"}}
	check(t, db.CreateMany(ctx, tags))
	check(t, db.Upsert(ctx, []Tag{{"go", "Golang"}, {"web", "Web"}}, []string{"code"}, "label"))
	check(t, db.Upsert(ctx, []Tag{{"sql", "ignored"}}, []string{"code"}))
	got, err := db.Query[Tag](ctx).OrderBy(db.C("code").Asc()).Get()
	check(t, err)
	want := []Tag{{"go", "Golang"}, {"sql", "SQL"}, {"web", "Web"}}
	if !slices.Equal(got, want) {
		t.Errorf("tags = %+v", got)
	}
	if err := db.Upsert(ctx, tags, []string{"nope"}); err == nil {
		t.Error("unknown conflict column accepted")
	}
}

func testTypes(t *testing.T, ctx context.Context) {
	body := "hello"
	dhaka := time.FixedZone("BST", 6*3600)
	pub := time.Date(2026, 9, 30, 18, 30, 15, 123456000, dhaka)
	p := Post{AuthorID: 1, Title: "types", Body: &body, Score: 3.25, Published: true,
		Meta: map[string]string{"lang": "go"}, PublishedAt: &pub}
	check(t, db.Create(ctx, &p))
	got, err := db.Find[Post](ctx, p.ID)
	check(t, err)
	if got.Body == nil || *got.Body != "hello" || got.Score != 3.25 || !got.Published || got.Meta["lang"] != "go" {
		t.Errorf("round trip: %+v", got)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(pub) || got.PublishedAt.Location() != time.UTC {
		t.Errorf("published_at = %v, want %v", got.PublishedAt, pub)
	}
	// Times compare correctly in queries whatever their zone.
	n, err := db.Query[Post](ctx).Where(db.Col[time.Time]("published_at").Gt(pub.Add(-time.Second))).Count()
	check(t, err)
	if n != 1 {
		t.Errorf("time comparison matched %d", n)
	}
	var empty Post
	empty.AuthorID, empty.Title = 1, "nulls"
	check(t, db.Create(ctx, &empty))
	e, err := db.Find[Post](ctx, empty.ID)
	check(t, err)
	if e.Body != nil || e.Meta != nil || e.PublishedAt != nil {
		t.Errorf("nulls: %+v", e)
	}
}

type signup struct {
	ID    int64  `json:"id"`
	Email string `json:"email" validate:"required|email|unique:st_authors,email,ID"`
	Buddy int64  `json:"buddy" validate:"exists:st_authors,id"`
}

func testValidationRules(t *testing.T, ctx context.Context) {
	a := seedAuthors(t, ctx, "Ada")[0]
	fields := func(in signup) map[string]string {
		t.Helper()
		err := validate.Struct(ctx, &in)
		if err == nil {
			return nil
		}
		ve, ok := errors.AsType[*validate.Errors](err)
		if !ok {
			t.Fatal(err)
		}
		return ve.FieldErrors()
	}
	if errs := fields(signup{Email: "new@example.com", Buddy: a.ID}); errs != nil {
		t.Errorf("valid: %v", errs)
	}
	errs := fields(signup{Email: "ada@example.com", Buddy: 999})
	if errs["email"] != "The email has already been taken." || errs["buddy"] != "The selected buddy is invalid." {
		t.Errorf("errors = %v", errs)
	}
	if errs := fields(signup{ID: a.ID, Email: "ada@example.com", Buddy: a.ID}); errs != nil {
		t.Errorf("editing own row: %v", errs)
	}
	if err := validate.Struct(t.Context(), &signup{Email: "x@example.com"}); !errors.Is(err, db.ErrNoDB) {
		t.Errorf("without a database: %v", err)
	}
}

func testConcurrency(t *testing.T, ctx context.Context) {
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			a := Author{Name: fmt.Sprint("c", i), Email: fmt.Sprintf("c%d@example.com", i)}
			if err := db.Create(ctx, &a); err != nil {
				errs <- err
				return
			}
			if _, err := db.Find[Author](ctx, a.ID); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n, _ := db.Query[Author](ctx).Count(); n != 20 {
		t.Errorf("count = %d", n)
	}
}

func testStaleUpdates(t *testing.T, ctx context.Context) {
	p := seedPosts(t, ctx, 1)[0]
	stale := p
	check(t, db.Delete(ctx, &p))
	stale.Title = "edited"
	check(t, db.Update(ctx, &stale)) // updating a trashed row is allowed…
	if _, err := db.Find[Post](ctx, p.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("a stale Update undeleted the row: %v", err) // …but doesn't undelete it
	}
	missing := Author{Name: "x", Email: "x@example.com"}
	missing.ID = 999
	if err := db.Update(ctx, &missing); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("Update of a missing row: %v", err)
	}
	if err := db.Save(ctx, &missing); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("Save of a missing row: %v", err)
	}
	// An update that changes nothing still finds its row (MySQL reports
	// matched rows).
	tag := Tag{"go", "Go"}
	check(t, db.Create(ctx, &tag))
	check(t, db.Update(ctx, &tag))
	check(t, db.Update(ctx, &tag))
}

func testTxRelease(t *testing.T, ctx context.Context) {
	d := d(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = db.Tx(ctx, func(ctx context.Context) error {
			if err := db.Create(ctx, &Author{Name: "a", Email: "a@example.com"}); err != nil {
				return err
			}
			_ = db.Tx(ctx, func(context.Context) error {
				runtime.Goexit() // what t.Fatal does
				return nil
			})
			return nil
		})
	}()
	<-done
	if n := d.SQL().Stats().InUse; n != 0 {
		t.Errorf("%d connections still in use after Goexit", n)
	}
	if n, _ := db.Query[Author](ctx).Count(); n != 0 {
		t.Errorf("Goexit didn't roll back: %d rows", n)
	}

	cctx, cancel := context.WithCancel(ctx)
	err := db.Tx(cctx, func(ctx context.Context) error {
		if err := db.Create(ctx, &Author{Name: "b", Email: "b@example.com"}); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("commit after cancel: %v", err)
	}

	// A query built outside the transaction joins it with WithContext.
	base := db.Query[Author](ctx)
	check(t, db.Tx(ctx, func(txCtx context.Context) error {
		if err := db.Create(txCtx, &Author{Name: "c", Email: "c@example.com"}); err != nil {
			return err
		}
		if n, err := base.WithContext(txCtx).Count(); err != nil || n != 1 {
			t.Errorf("WithContext count = %d, %v", n, err)
		}
		return nil
	}))

	// The caller's argument slice isn't modified.
	when := time.Date(2026, 1, 1, 12, 0, 0, 0, time.FixedZone("X", 3600))
	args := []any{when}
	_, err = db.Raw[int64](ctx, "SELECT COUNT(*) FROM st_authors WHERE created_at > ?", args...)
	check(t, err)
	if got := args[0].(time.Time); got.Location() != when.Location() {
		t.Error("Raw changed the caller's arguments")
	}
}

func testWriteRestrictions(t *testing.T, ctx context.Context) {
	seedPosts(t, ctx, 6)
	q := db.Query[Post](ctx)
	for name, bad := range map[string]*db.Q[Post]{
		"having":   q.GroupBy("author_id").Having(db.SQL("1 = 0")),
		"distinct": q.Distinct(),
		"lock":     q.ForUpdate(),
	} {
		if _, err := bad.ForceDelete(); err == nil {
			t.Errorf("%s: ForceDelete accepted", name)
		}
	}
	if n, _ := q.Count(); n != 6 {
		t.Errorf("rows deleted: %d left", n)
	}
	having := q.GroupBy("author_id").Having(db.SQL("COUNT(*) > ?", 2))
	n, err := having.Count()
	check(t, err)
	if n != 0 {
		t.Errorf("Count with Having = %d, want 0 groups", n)
	}
	// Mass updates accept columns qualified with the model's table.
	_, err = q.Where(views.Eq(0)).Update(db.Col[string]("st_posts.title").Set("qualified"))
	check(t, err)
	if n, _ := q.Where(title.Eq("qualified")).Count(); n != 1 {
		t.Errorf("qualified update matched %d", n)
	}
	if _, err := q.Update(db.Col[string]("other.title").Set("x")); err == nil {
		t.Error("update of another table's column accepted")
	}
}

func testLimitedAggregates(t *testing.T, ctx context.Context) {
	seedPosts(t, ctx, 5) // views 0, 10, 20, 30, 40
	q := db.Query[Post](ctx).OrderBy(views.Desc())
	all, err := db.Sum(q.Distinct(), views)
	check(t, err)
	if all != 100 {
		t.Errorf("Sum ignoring Distinct = %d", all)
	}
	top2, err := db.Sum(q.Limit(2), views)
	check(t, err)
	next2, err := db.Sum(q.Limit(2).Offset(2), views)
	check(t, err)
	hi, err := db.Max(q.Offset(4), views)
	check(t, err)
	if top2 != 70 || next2 != 30 || hi != 0 {
		t.Errorf("sum top 2 = %d, next 2 = %d, max after 4 = %d", top2, next2, hi)
	}
	if _, err := db.Sum(q.GroupBy("author_id"), views); err == nil {
		t.Error("Sum with GroupBy accepted")
	}
}

// Note embeds *db.Model through a pointer.
type Note struct {
	*db.Model
	Text string `db:"text"`
}

// TableName implements db.Tabler.
func (Note) TableName() string { return "st_notes" }

func testEmbeddedPointer(t *testing.T, ctx context.Context) {
	n := Note{Text: "hi"}
	check(t, db.Create(ctx, &n))
	if n.Model == nil || n.ID == 0 || n.CreatedAt.IsZero() {
		t.Fatalf("after Create: %+v", n.Model)
	}
	m := Note{Text: "saved"}
	check(t, db.Save(ctx, &m))
	check(t, db.CreateMany(ctx, []Note{{Text: "a"}, {Text: "b"}}))
	got, err := db.Find[Note](ctx, n.ID)
	check(t, err)
	if got.Text != "hi" || got.ID != n.ID {
		t.Errorf("Find = %+v", got)
	}
}

// Event has a column the database fills (DEFAULT CURRENT_TIMESTAMP) and a
// timestamp column without time zone.
type Event struct {
	ID     int64      `db:"id,pk"`
	Name   string     `db:"name"`
	At     time.Time  `db:"at,readonly"`
	Logged *time.Time `db:"logged"`
	Day    *time.Time `db:"day"`
}

// TableName implements db.Tabler.
func (Event) TableName() string { return "st_events" }

func testDatabaseWrittenTimes(t *testing.T, ctx context.Context) {
	for i := range 4 {
		mustExec(t, ctx, "INSERT INTO st_events (name) VALUES (?)", fmt.Sprint("e", i))
	}
	first, err := db.Query[Event](ctx).OrderBy(id.Asc()).First()
	check(t, err)
	if first.At.IsZero() || first.At.Location() != time.UTC || time.Since(first.At).Abs() > time.Hour {
		t.Errorf("database-written time = %v (should be UTC and about now)", first.At)
	}
	at := db.Col[time.Time]("at")
	if n, _ := db.Query[Event](ctx).Where(at.Eq(first.At)).Count(); n == 0 {
		t.Error("a time read back doesn't equal itself in a query")
	}
	var names []string
	cursor := ""
	for range 5 {
		page, err := db.Query[Event](ctx).OrderBy(at.Asc()).CursorPaginate(cursor, 2)
		check(t, err)
		for _, e := range page.Data {
			names = append(names, e.Name)
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if !slices.Equal(names, []string{"e0", "e1", "e2", "e3"}) {
		t.Errorf("cursor over database-written times: %v", names)
	}

	// A time in another zone is stored as the same instant, also in a
	// column without time zone.
	dhaka := time.Date(2026, 9, 30, 18, 0, 0, 0, time.FixedZone("BST", 6*3600))
	e := Event{Name: "zoned", Logged: &dhaka}
	check(t, db.Create(ctx, &e))
	got, err := db.Find[Event](ctx, e.ID)
	check(t, err)
	if got.Logged == nil || !got.Logged.Equal(dhaka) {
		t.Errorf("logged = %v, want %v", got.Logged, dhaka)
	}

	// Dates are times too: build them in UTC to keep the calendar day.
	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	d := Event{Name: "dated", Day: &day}
	check(t, db.Create(ctx, &d))
	gotDay, err := db.Find[Event](ctx, d.ID)
	check(t, err)
	if gotDay.Day == nil || gotDay.Day.Format(time.DateOnly) != "2026-03-15" {
		t.Errorf("day = %v", gotDay.Day)
	}
}

func testSoftDeleteScopes(t *testing.T, ctx context.Context) {
	posts := seedPosts(t, ctx, 3)
	p := posts[0]
	check(t, db.Delete(ctx, &p))
	original := *p.DeletedAt
	time.Sleep(2 * time.Millisecond)
	if n, err := db.Query[Post](ctx).OnlyTrashed().Delete(); err != nil || n != 0 {
		t.Errorf("OnlyTrashed().Delete() = %d, %v", n, err)
	}
	if n, _ := db.Query[Post](ctx).Count(); n != 2 {
		t.Errorf("live rows after OnlyTrashed().Delete() = %d", n)
	}
	_, err := db.Query[Post](ctx).WithTrashed().Delete()
	check(t, err)
	again, err := db.Query[Post](ctx).WithTrashed().Find(p.ID)
	check(t, err)
	if !again.DeletedAt.Equal(original) {
		t.Errorf("mass Delete moved deleted_at from %v to %v", original, again.DeletedAt)
	}
	check(t, db.Restore(ctx, &posts[1]))
	live := posts[2]
	check(t, db.Restore(ctx, &live))
	n, err := db.Query[Post](ctx).WithTrashed().Where(id.Eq(posts[0].ID)).Restore()
	check(t, err)
	if n != 1 {
		t.Errorf("Restore matched %d", n)
	}
}

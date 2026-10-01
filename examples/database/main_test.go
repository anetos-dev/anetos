// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/anetostest"
)

// region: test-api
func TestCreatePost(t *testing.T) {
	app := anetostest.New(t, setup) // a migrated database; the test leaves no rows behind
	author := anetostest.Create(app, Authors)

	app.PostJSON("/posts", map[string]any{"author_id": author.ID, "title": "Hello Go", "body": "a", "publish": true}).
		AssertCreated().
		AssertJSONPath("title", "Hello Go")
	anetostest.AssertDatabaseHas[Post](app, PostCols.Title.Eq("Hello Go"), PostCols.AuthorID.Eq(author.ID))

	app.PostJSON("/posts", map[string]any{"author_id": author.ID + 1, "title": "x", "body": "y"}).
		AssertUnprocessable().
		AssertValidationErrors("author_id")
	anetostest.AssertDatabaseCount[Post](app, 1)
}

// endregion

func TestAuthors(t *testing.T) {
	app := anetostest.New(t, setup)
	app.PostJSON("/authors", NewAuthor{Name: "Ada", Email: "ada@example.com"}).AssertCreated()
	app.PostJSON("/authors", NewAuthor{Name: "Ada 2", Email: "ada@example.com"}).
		AssertValidationErrors("email").
		AssertSee("already been taken")
	anetostest.AssertDatabaseCount[Author](app, 1)
}

func TestListAndShowPosts(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, Authors)
	now := time.Now().UTC()
	published := Posts.With(func(p *Post) { p.AuthorID, p.PublishedAt = ada.ID, &now })
	anetostest.Create(app, published.With(func(p *Post) { p.Title, p.Tags = "Hello Go", []string{"go"} }))
	anetostest.Create(app, published.With(func(p *Post) { p.Title = "Go tips" }))
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID, p.Title = ada.ID, "Go draft" }))
	anetostest.CreateMany(app, published, 3)

	app.GetJSON("/posts?q=Go&per_page=1").
		AssertOK().
		AssertJSONPath("total", 2).
		AssertJSONPath("data.0.title", "Go tips")
	app.GetJSON("/posts?per_page=10").AssertJSONPath("total", 5)

	first := app.GetJSON("/posts?q=Hello").AssertJSONPath("total", 1)
	var page struct{ Data []Post }
	first.JSON(&page)
	id := page.Data[0].ID
	path := "/posts/" + strconv.FormatInt(id, 10)
	app.GetJSON(path)
	app.GetJSON(path).AssertOK().AssertJSONPath("views", 2).AssertJSONPath("tags", []string{"go"}).
		AssertJSONPath("author.name", ada.Name) // loaded with PostRels.Author

	app.DeleteJSON(path).AssertNoContent()
	app.GetJSON(path).AssertNotFound()
	anetostest.AssertDatabaseMissing[Post](app, PostCols.ID.Eq(id))
	anetostest.AssertSoftDeleted[Post](app, PostCols.ID.Eq(id))

	app.GetJSON("/stats").AssertOK().AssertJSONPath("0.posts", 5)
}

// region: test-cache
func TestStatsCache(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, Authors.With(func(a *Author) { a.Name = "Ada" }))
	app.GetJSON("/stats").AssertJSONPath("0.posts", 0) // computed and cached

	// A post written behind the handlers' back isn't counted yet...
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID = ada.ID }))
	app.GetJSON("/stats").AssertJSONPath("0.posts", 0)

	// ...until a handler forgets the cached totals.
	app.PostJSON("/posts", map[string]any{"author_id": ada.ID, "title": "Hello", "body": "First post"}).AssertCreated()
	app.GetJSON("/stats").AssertJSONPath("0.posts", 2)
}

// endregion

// TestDatabaseCache runs the stats cache on the database store.
func TestDatabaseCache(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"CACHE_STORE": "database"}))
	anetostest.Create(app, Authors)
	app.GetJSON("/stats").AssertJSONPath("0.posts", 0)
	c, err := anetos.Resolve[*cache.Cache](app.App)
	if err != nil {
		t.Fatal(err)
	}
	n, err := db.RawFirst[int64](app.Context(), "SELECT COUNT(*) FROM cache WHERE key = ?", c.Prefix()+"stats")
	if err != nil || n != 1 {
		t.Errorf("cached rows: %d, %v", n, err)
	}
}

// TestRateLimit checks that the API answers 429 past its limit.
func TestRateLimit(t *testing.T) {
	app := anetostest.New(t, setup)
	for i := range 250 { // two windows' worth, in case a minute starts meanwhile
		res := app.GetJSON("/stats")
		if res.StatusCode == http.StatusTooManyRequests {
			if i < 120 {
				t.Errorf("limited after %d requests", i)
			}
			res.AssertHeader("X-RateLimit-Remaining", "0")
			if res.Header.Get("Retry-After") == "" {
				t.Error("no Retry-After")
			}
			return
		}
		res.AssertOK()
	}
	t.Error("never limited")
}

// region: test-relations
func TestAuthorsWithPublishedPosts(t *testing.T) {
	app := anetostest.New(t, setup)
	now := time.Now().UTC()
	ada := anetostest.Create(app, Authors.With(func(a *Author) { a.Name = "Ada" }))
	grace := anetostest.Create(app, Authors.With(func(a *Author) { a.Name = "Grace" }))
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID, p.Title, p.PublishedAt = ada.ID, "Published", &now }))
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID, p.Title = ada.ID, "Draft" }))
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID = grace.ID })) // Grace has drafts only

	app.GetJSON("/authors").
		AssertOK().
		AssertJSONPath("0.name", "Ada").
		AssertJSONPath("0.posts.0.title", "Published").
		AssertDontSee("Grace", "Draft")
}

// endregion

func TestCommands(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "blog.db")
	var out strings.Builder
	// Each invocation is a new process in real life: a new app.
	execute := func(args ...string) {
		t.Helper()
		app, err := anetos.New(anetos.WithSource(config.Map{"DB_DATABASE": dbFile, "APP_ENV": "development"}), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := setup(app); err != nil {
			t.Fatal(err)
		}
		var errOut strings.Builder
		if code := app.ExecuteArgs(t.Context(), args, &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut.String())
		}
	}
	for _, args := range [][]string{{"migrate:fresh", "--seed"}, {"blog:stats"}, {"migrate:rollback"}, {"migrate"}, {"migrate:status"}, {"routes:list"}, {"cache:clear"}, {"help"}} {
		execute(args...)
	}
	for _, want := range []string{"Seeded:      posts", "2 authors, 2 posts", "GET     /posts/{id}", "migrate:rollback", "Run pending migrations", "Cleared the memory cache"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output:\n%s", want, out.String())
		}
	}
	if strings.Count(out.String(), "Ran ") != 4 {
		t.Errorf("status:\n%s", out.String())
	}
}

// region: test-clock
// With the clock frozen, timestamps are known: created_at, published_at
// (set by the handler from anetos.Now), and deleted_at an hour later.
func TestTimestamps(t *testing.T) {
	app := anetostest.New(t, setup)
	now := app.Freeze(time.Time{})
	author := anetostest.Create(app, Authors)

	var p Post
	app.PostJSON("/posts", map[string]any{"author_id": author.ID, "title": "Hello", "body": "x", "publish": true}).
		AssertCreated().
		JSON(&p)
	anetostest.AssertDatabaseHas[Post](app, PostCols.ID.Eq(p.ID), PostCols.CreatedAt.Eq(now), PostCols.PublishedAt.Eq(&now))

	app.Travel(time.Hour)
	app.Delete("/posts/" + strconv.FormatInt(p.ID, 10)).AssertNoContent()
	later := now.Add(time.Hour)
	anetostest.AssertSoftDeleted[Post](app, PostCols.ID.Eq(p.ID), PostCols.DeletedAt.Eq(&later))
}

// endregion

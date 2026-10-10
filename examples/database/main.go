// SPDX-License-Identifier: Apache-2.0

// Command database is a small blog API on Anetos's data layer: models,
// CRUD, queries, pagination, transactions, raw SQL, database validation
// rules, a cache and rate limiting. It uses SQLite, so it runs without a
// database server.
//
//	export APP_ENV=development DB_DATABASE=blog.db HTTP_ADDR=:8080
//	go run . migrate      # create the tables
//	go run . db:seed      # optional sample data
//	go run .              # serve
//
//	curl -s localhost:8080/authors -H 'Content-Type: application/json' -d '{"name":"Ada","email":"ada@example.com"}'
//	curl -s localhost:8080/posts -H 'Content-Type: application/json' -d '{"author_id":1,"title":"Hello","body":"First post"}'
//	curl -s 'localhost:8080/posts?page=1&per_page=10'
//	curl -s localhost:8080/stats
//
// The tables are created by the migrations in migrations.go.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"
)

// region: models
// Author writes posts.
type Author struct {
	db.Model        // id, created_at, updated_at
	Name     string `db:"name" json:"name"`
	Email    string `db:"email" json:"email"`

	Posts []Post `rel:"has_many" json:"posts,omitzero"` // posts.author_id; loaded with With or Load
}

// Post is a blog post. Deleting one only marks it deleted.
type Post struct {
	db.Model
	db.SoftDeletes            // deleted_at
	AuthorID       int64      `db:"author_id" json:"author_id"`
	Title          string     `db:"title" json:"title"`
	Body           string     `db:"body" json:"body"`
	Tags           []string   `db:"tags,json" json:"tags"` // stored as JSON text
	Views          int        `db:"views" json:"views"`
	PublishedAt    *time.Time `db:"published_at" json:"published_at"` // nullable

	Author *Author `rel:"belongs_to" json:"author,omitzero"` // by AuthorID
}

// AuthorCols and PostCols, the typed columns of the models, and
// AuthorRels and PostRels, their relations, are in models_gen.go, written
// by `go tool anetos gen` (or go generate).
//
//go:generate go tool anetos gen

// endregion

// region: validation
// NewAuthor is the input for POST /authors. unique and exists are
// registered by the db package and query the request's database.
type NewAuthor struct {
	Name  string `json:"name" validate:"required|max:100"`
	Email string `json:"email" validate:"required|email|unique:authors,email"`
}

// NewPost is the input for POST /posts.
type NewPost struct {
	AuthorID int64    `json:"author_id" validate:"required|exists:authors,id"`
	Title    string   `json:"title" validate:"required|max:200"`
	Body     string   `json:"body" validate:"required"`
	Tags     []string `json:"tags" validate:"max:5|distinct|alpha_dash"`
	Publish  bool     `json:"publish"`
}

// endregion

// ListPosts is the input for GET /posts.
type ListPosts struct {
	Page    int    `query:"page"`
	PerPage int    `query:"per_page" validate:"max:100"`
	Search  string `query:"q"`
	Author  *int64 `query:"author"`
}

// PostID reads the {id} path parameter.
type PostID struct {
	ID int64 `path:"id"`
}

// Blog holds the handlers. It needs no database field: queries find the
// database in the request context.
type Blog struct{}

func (Blog) CreateAuthor(c *web.Ctx, in NewAuthor) (web.Responder, error) {
	a := Author{Name: in.Name, Email: in.Email}
	if err := db.Create(c, &a); err != nil {
		return nil, err
	}
	return web.Created(a), nil
}

func (Blog) CreatePost(c *web.Ctx, in NewPost) (web.Responder, error) {
	p := Post{AuthorID: in.AuthorID, Title: in.Title, Body: in.Body, Tags: in.Tags}
	if in.Publish {
		now := anetos.Now(c).UTC() // the app's clock: tests can freeze it
		p.PublishedAt = &now
	}
	if err := db.Create(c, &p); err != nil {
		return nil, err
	}
	// region: forget
	// GET /stats counts the new post. The post is saved either way: a
	// cache failure only delays that, so log it rather than fail.
	if err := cache.Forget(c, "stats"); err != nil {
		c.Logger().Warn("forget the cached stats", "error", err)
	}
	// endregion
	return web.Created(p), nil
}

// region: list-posts
func (Blog) ListPosts(c *web.Ctx, in ListPosts) (db.Page[Post], error) {
	q := db.Query[Post](c).Where(PostCols.PublishedAt.NotNull())
	if in.Search != "" {
		q = q.Where(PostCols.Title.Contains(in.Search)) // % and _ match themselves
	}
	if in.Author != nil {
		q = q.Where(PostCols.AuthorID.Eq(*in.Author))
	}
	return q.With(PostRels.Author).Latest().Paginate(in.Page, in.PerPage) // one query for all the authors
}

// endregion

func (Blog) ShowPost(c *web.Ctx, in PostID) (Post, error) {
	// Count the view and read the post in one transaction.
	var p Post
	err := db.Tx(c, func(ctx context.Context) error {
		if _, err := db.Query[Post](ctx).Where(PostCols.ID.Eq(in.ID)).Update(PostCols.Views.SetRaw("views + 1")); err != nil {
			return err
		}
		var err error
		p, err = db.Query[Post](ctx).With(PostRels.Author).Find(in.ID)
		return err // ErrNotFound becomes a 404
	})
	return p, err
}

func (Blog) DeletePost(c *web.Ctx, in PostID) (web.Responder, error) {
	p, err := db.Find[Post](c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := db.Delete(c, &p); err != nil { // soft delete
		return nil, err
	}
	if err := cache.Forget(c, "stats"); err != nil {
		c.Logger().Warn("forget the cached stats", "error", err)
	}
	return web.NoContent(), nil
}

// region: where-has
// ListAuthors returns the authors with a published post, with those posts.
func (Blog) ListAuthors(c *web.Ctx, _ struct{}) ([]Author, error) {
	published := PostCols.PublishedAt.NotNull()
	return db.Query[Author](c).
		WhereHas(AuthorRels.Posts, published).
		With(AuthorRels.Posts.Where(published).OrderBy(PostCols.PublishedAt.Desc())).
		OrderBy(AuthorCols.Name.Asc()).
		Get()
}

// endregion

// region: raw
// AuthorStats is one row of GET /stats.
type AuthorStats struct {
	Name  string `db:"name" json:"name"`
	Posts int64  `db:"posts" json:"posts"`
	Views int64  `db:"views" json:"views"`
}

func authorStats(ctx context.Context) ([]AuthorStats, error) {
	return db.Raw[AuthorStats](ctx, `
		SELECT a.name, COUNT(p.id) AS posts, COALESCE(SUM(p.views), 0) AS views
		FROM authors a LEFT JOIN posts p ON p.author_id = a.id AND p.deleted_at IS NULL
		GROUP BY a.id, a.name
		ORDER BY views DESC`)
}

// endregion

// region: remember
// Stats serves the totals from the cache, computing them at most once a
// minute (and after a post is created or deleted).
func (Blog) Stats(c *web.Ctx, _ struct{}) ([]AuthorStats, error) {
	return cache.Remember(c, "stats", time.Minute, authorStats)
}

// endregion

func routes(r *web.Router) {
	var b Blog
	// region: ratelimit
	// 120 requests a minute per client IP, counted in the app's cache.
	r = r.Group("", ratelimit.Middleware("api", ratelimit.PerMinute(120)))
	// endregion
	r.Post("/authors", web.H(b.CreateAuthor))
	r.Get("/authors", web.H(b.ListAuthors))
	r.Post("/posts", web.H(b.CreatePost))
	r.Get("/posts", web.H(b.ListPosts))
	r.Get("/posts/{id}", web.H(b.ShowPost))
	r.Delete("/posts/{id}", web.H(b.DeletePost))
	r.Get("/stats", web.H(b.Stats))
}

// addCommands adds the app's own commands to the binary.
func addCommands(app *anetos.App) {
	// region: custom-command
	app.Command("blog:stats", "Print how many authors and posts there are", func(ctx context.Context, args *cmd.Args) error {
		authors, err := db.Query[Author](ctx).Count()
		if err != nil {
			return err
		}
		posts, err := db.Query[Post](ctx).Count()
		if err != nil {
			return err
		}
		fmt.Fprintf(args.Stdout, "%d authors, %d posts\n", authors, posts)
		return nil
	})
	// endregion
}

// setup connects to the database, builds the migration runner (the
// migrate commands), the cache and the server. Tests call it too.
func setup(app *anetos.App) (*web.Server, error) {
	// region: connect
	// DB_CONNECTION (default sqlite) picks one of the drivers passed here.
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	// endregion
	// region: runner
	if _, err := migrate.New(app, []*migrate.Set{Migrations, cache.Migrations("")}, migrate.WithSeeders(Seeders...)); err != nil {
		return nil, err
	}
	// endregion
	// region: cache
	// CACHE_STORE (default memory) picks the store; database uses the
	// cache table from cache.Migrations.
	if _, err := cache.New(app); err != nil {
		return nil, err
	}
	// endregion
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	routes(srv.Router())
	addCommands(app)
	return srv, nil
}

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	// region: commands
	// go run .                 run the app (the default command)
	// go run . migrate         and migrate:rollback, migrate:status, migrate:fresh --seed, db:seed
	// go run . routes:list     every route
	// go run . blog:stats      a custom command (addCommands)
	// go run . cache:clear     empty the cache
	// go run . help            every command
	app.Execute()
	// endregion
}

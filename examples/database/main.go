// SPDX-License-Identifier: Apache-2.0

// Command database is a small blog API on Anetos's data layer: models,
// CRUD, queries, pagination, transactions, raw SQL and database validation
// rules. It uses SQLite, so it runs without a database server.
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
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/web"
)

// region: models
// Author writes posts.
type Author struct {
	db.Model        // id, created_at, updated_at
	Name     string `db:"name" json:"name"`
	Email    string `db:"email" json:"email"`
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
}

// Typed columns for conditions and ordering. Model code generation
// (roadmap F9) will write these for you.
var (
	authorID    = db.Col[int64]("author_id")
	title       = db.Col[string]("title")
	views       = db.Col[int]("views")
	publishedAt = db.Col[*time.Time]("published_at")
)

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

// region: handlers
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
		now := time.Now().UTC()
		p.PublishedAt = &now
	}
	if err := db.Create(c, &p); err != nil {
		return nil, err
	}
	return web.Created(p), nil
}

func (Blog) ListPosts(c *web.Ctx, in ListPosts) (db.Page[Post], error) {
	q := db.Query[Post](c).Where(publishedAt.NotNull())
	if in.Search != "" {
		q = q.Where(title.Like("%" + in.Search + "%"))
	}
	if in.Author != nil {
		q = q.Where(authorID.Eq(*in.Author))
	}
	return q.Latest().Paginate(in.Page, in.PerPage)
}

func (Blog) ShowPost(c *web.Ctx, in PostID) (Post, error) {
	// Count the view and read the post in one transaction.
	var p Post
	err := db.Tx(c, func(ctx context.Context) error {
		if _, err := db.Query[Post](ctx).Where(db.C("id").Eq(in.ID)).Update(views.SetRaw("views + 1")); err != nil {
			return err
		}
		var err error
		p, err = db.Find[Post](ctx, in.ID)
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
	return web.NoContent(), nil
}

// endregion

// region: raw
// AuthorStats is one row of GET /stats.
type AuthorStats struct {
	Name  string `db:"name" json:"name"`
	Posts int64  `db:"posts" json:"posts"`
	Views int64  `db:"views" json:"views"`
}

func (Blog) Stats(c *web.Ctx, _ struct{}) ([]AuthorStats, error) {
	return db.Raw[AuthorStats](c, `
		SELECT a.name, COUNT(p.id) AS posts, COALESCE(SUM(p.views), 0) AS views
		FROM authors a LEFT JOIN posts p ON p.author_id = a.id AND p.deleted_at IS NULL
		GROUP BY a.id, a.name
		ORDER BY views DESC`)
}

// endregion

func routes(r *web.Router) {
	var b Blog
	r.Post("/authors", web.H(b.CreateAuthor))
	r.Post("/posts", web.H(b.CreatePost))
	r.Get("/posts", web.H(b.ListPosts))
	r.Get("/posts/{id}", web.H(b.ShowPost))
	r.Delete("/posts/{id}", web.H(b.DeletePost))
	r.Get("/stats", web.H(b.Stats))
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// setup connects to the database, builds the migration runner and the
// server. Tests call it too.
func setup(ctx context.Context, app *anetos.App) (*web.Server, *migrate.Runner, error) {
	// region: connect
	// DB_CONNECTION (default sqlite) picks one of the drivers passed here.
	if _, err := db.Connect(ctx, app, sqlite.Driver()); err != nil {
		return nil, nil, err
	}
	// endregion
	// region: runner
	runner, err := migrate.ForApp(app, []*migrate.Set{Migrations}, migrate.WithSeeders(Seeders...))
	if err != nil {
		return nil, nil, err
	}
	// endregion
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, nil, err
	}
	routes(srv.Router())
	return srv, runner, nil
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, err := anetos.New()
	if err != nil {
		return err
	}
	_, runner, err := setup(ctx, app)
	if err != nil {
		return err
	}
	// region: commands
	// go run . migrate | migrate:rollback | migrate:status | migrate:fresh --seed | db:seed
	if handled, err := runner.Command(ctx, os.Args[1:], os.Stdout); handled {
		return errors.Join(err, app.Close())
	}
	// endregion
	return app.Run(ctx)
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"time"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// region: set
// Migrations is the app's migration set. In a larger app it lives in its
// own package (database/migrations), with one file per migration.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.Add("2026_10_01_120000_create_authors", createAuthors{})
	Migrations.Add("2026_10_01_120100_create_posts", createPosts{})
	Migrations.AddFunc("2026_10_02_090000_add_posts_views_index",
		func(s *migrate.Schema) error {
			return s.Alter("posts", func(t *migrate.Table) { t.Index("views") })
		},
		func(s *migrate.Schema) error {
			return s.Alter("posts", func(t *migrate.Table) { t.DropIndex("views") })
		})
}

// endregion

// region: create-posts
type createPosts struct{}

func (createPosts) Up(s *migrate.Schema) error {
	return s.Create("posts", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("author_id").Constrained().CascadeOnDelete() // references authors(id)
		t.String("title", 200)
		t.Text("body")
		t.JSON("tags").Nullable()
		t.Integer("views").Default(0)
		t.Timestamp("published_at").Nullable()
		t.Timestamps()
		t.SoftDeletes()
		t.Index("author_id", "published_at")
	})
}

func (createPosts) Down(s *migrate.Schema) error { return s.Drop("posts") }

// endregion

type createAuthors struct{}

func (createAuthors) Up(s *migrate.Schema) error {
	return s.Create("authors", func(t *migrate.Table) {
		t.ID()
		t.String("name", 100)
		t.String("email", 255).Unique()
		t.Timestamps()
	})
}

func (createAuthors) Down(s *migrate.Schema) error { return s.Drop("authors") }

// region: seeders
// Seeders fill a development database with sample data: go run . db:seed
var Seeders = []migrate.Seeder{
	{Name: "authors", Run: func(ctx context.Context) error {
		return db.CreateMany(ctx, []Author{
			{Name: "Ada", Email: "ada@example.com"},
			{Name: "Grace", Email: "grace@example.com"},
		})
	}},
	{Name: "posts", Run: func(ctx context.Context) error {
		ada, err := db.Query[Author](ctx).Where(AuthorCols.Email.Eq("ada@example.com")).First()
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		return db.CreateMany(ctx, []Post{
			{AuthorID: ada.ID, Title: "Hello, Anetos", Body: "The first post.", PublishedAt: &now},
			{AuthorID: ada.ID, Title: "A draft", Body: "Not published yet."},
		})
	}},
}

// endregion

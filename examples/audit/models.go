// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"strconv"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// User is an account. They log in with API tokens only, in this example.
type User struct {
	db.Model
	Name  string `db:"name" json:"name"`
	Email string `db:"email" json:"email"`
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable: no passwords here.
func (u *User) AuthPassword() string { return "" }

// region: model
// Document is what the log tracks. Deleting one soft-deletes it; its
// slug is unique among the documents that aren't deleted.
type Document struct {
	db.Model
	db.SoftDeletes
	OwnerID    int64  `db:"owner_id" json:"owner_id"`
	Slug       string `db:"slug" json:"slug"`
	Title      string `db:"title" json:"title"`
	Body       string `db:"body" json:"body"`
	Status     string `db:"status" json:"status"`    // draft, published, archived
	ShareToken string `db:"share_token" json:"-"`    // redacted in the log: its name says token
	Views      int    `db:"view_count" json:"views"` // left out of the log: audit.Except
}

// endregion

var (
	colOwnerID = db.Col[int64]("owner_id")
	colStatus  = db.Col[string]("status")
	colViews   = db.Col[int]("view_count")
)

// users tells package auth how to find users.
var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrUserNotFound
		}
		u, err := db.Find[User](ctx, n)
		if errors.Is(err, db.ErrNotFound) {
			return nil, auth.ErrUserNotFound
		}
		return &u, err
	},
	ByLogin: func(context.Context, string) (*User, error) { return nil, auth.ErrUserNotFound },
}

// Migrations creates the app's tables.
var Migrations = func() *migrate.Set {
	s := migrate.NewSet("app")
	s.AddFunc("2026_10_06_000001_create_users_and_documents",
		func(s *migrate.Schema) error {
			if err := s.Create("users", func(t *migrate.Table) {
				t.ID()
				t.String("name", 255)
				t.String("email", 255).Unique()
				t.Timestamps()
			}); err != nil {
				return err
			}
			// region: migration
			return s.Create("documents", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("owner_id").References("users")
				t.String("slug", 100).UniqueLive() // PostgreSQL and SQLite
				t.String("title", 255)
				t.Text("body")
				t.String("status", 20).Default("draft")
				t.String("share_token", 64)
				t.Integer("view_count").Default(0)
				t.Timestamps()
				t.SoftDeletes()
			})
			// endregion
		},
		func(s *migrate.Schema) error {
			return errors.Join(s.Drop("documents"), s.Drop("users"))
		})
	return s
}()

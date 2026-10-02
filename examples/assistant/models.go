// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strconv"
	"strings"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// User is an account, signing in with a password.
type User struct {
	db.Model
	Name     string `db:"name" json:"name"`
	Email    string `db:"email" json:"email"` // lower case
	Password string `db:"password" json:"-"`  // password.Hash
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable.
func (u *User) AuthPassword() string { return u.Password }

// Article is a help-center article: what the assistant answers from.
type Article struct {
	db.Model
	Title string `db:"title" json:"title"`
	Body  string `db:"body" json:"body"`
}

var colEmail = db.Col[string]("email")

// users tells package auth how to find users.
var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		u, err := db.Find[User](ctx, n)
		return &u, err
	},
	ByLogin: func(ctx context.Context, email string) (*User, error) {
		u, err := db.Query[User](ctx).Where(colEmail.Eq(strings.ToLower(email))).First()
		return &u, err
	},
}

// Migrations creates the app's tables.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.AddFunc("2026_10_02_130000_create_users_and_articles",
		func(s *migrate.Schema) error {
			if err := s.Create("users", func(t *migrate.Table) {
				t.ID()
				t.String("name", 255)
				t.String("email", 255).Unique()
				t.String("password", 255)
				t.Timestamps()
			}); err != nil {
				return err
			}
			return s.Create("articles", func(t *migrate.Table) {
				t.ID()
				t.String("title", 255)
				t.Text("body")
				t.Timestamps()
				t.SearchIndex("title", "body") // search_articles finds their words too
			})
		},
		func(s *migrate.Schema) error {
			if err := s.Drop("articles"); err != nil {
				return err
			}
			return s.Drop("users")
		})
	// region: migration
	Migrations.AddFunc("2026_10_02_140000_create_articles_embeddings",
		func(s *migrate.Schema) error {
			return s.CreateEmbeddings("articles", embeddingDims) // articles_embeddings
		},
		func(s *migrate.Schema) error { return s.DropEmbeddings("articles") })
	// endregion
}

// helpCenter is the articles seed adds: the help center of Tidy, a
// made-up to-do app.
var helpCenter = []Article{
	{Title: "Export your lists", Body: "Open Settings, then Data, and choose Export. Tidy writes every list to a CSV file, one row per task, with its due date and tags. Exports of large accounts are emailed to you within an hour."},
	{Title: "Share a list with your team", Body: "Open the list, choose Share and enter your teammates' email addresses. They can view and edit the list; only you can delete it. Shared lists need a Team plan."},
	{Title: "Plans and billing", Body: "The Free plan has five lists. The Pro plan has unlimited lists and reminders, for 4 dollars a month. The Team plan adds shared lists, for 8 dollars a user a month. Invoices are under Settings, then Billing."},
	{Title: "Work offline", Body: "Tidy keeps your lists on your device: add and check off tasks without a connection. Changes sync when you're back online; if a task changed on two devices, the latest change wins."},
	{Title: "Reset your password", Body: "On the sign-in page, choose Forgot password and enter your email address. The reset link works for one hour and only once."},
}

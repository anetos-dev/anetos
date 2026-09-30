// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// region: user
// User is an account. The two Auth methods are what package auth needs.
type User struct {
	db.Model
	Name            string     `db:"name" json:"name"`
	Email           string     `db:"email" json:"email"` // stored in lower case
	Password        string     `db:"password" json:"-"`  // password.Hash
	RememberToken   string     `db:"remember_token" json:"-"`
	EmailVerifiedAt *time.Time `db:"email_verified_at" json:"email_verified_at"`
	Admin           bool       `db:"admin" json:"admin"`
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable.
func (u *User) AuthPassword() string { return u.Password }

// endregion

var (
	colEmail    = db.Col[string]("email")
	colPassword = db.Col[string]("password")
	colRemember = db.Col[string]("remember_token")
	colVerified = db.Col[*time.Time]("email_verified_at")
	colID       = db.Col[int64]("id")
)

// region: users
// users tells package auth how to find and update users.
var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		u, err := db.Find[User](ctx, n) // db.ErrNotFound: no such user
		return &u, err
	},
	ByLogin: func(ctx context.Context, email string) (*User, error) {
		u, err := db.Query[User](ctx).Where(colEmail.Eq(strings.ToLower(email))).First()
		return &u, err
	},
	RememberToken: func(u *User) string { return u.RememberToken },
	SetRememberToken: func(ctx context.Context, u *User, token string) error {
		_, err := db.Query[User](ctx).Where(colID.Eq(u.ID)).Update(colRemember.Set(token))
		return err
	},
	SetPassword: func(ctx context.Context, u *User, hash string) error {
		_, err := db.Query[User](ctx).Where(colID.Eq(u.ID)).Update(colPassword.Set(hash))
		return err
	},
}

// endregion

// Policies say what a user may do. The compiler checks their types.
type Policies struct{}

// region: policy
// ManageUsers is for the admin page.
func (Policies) ManageUsers(_ context.Context, u *User) bool { return u.Admin }

// ViewUser lets users see their own profile, and admins everyone's.
func (Policies) ViewUser(_ context.Context, u *User, other *User) bool {
	return u.Admin || u.ID == other.ID
}

// endregion

var policies Policies

// Migrations creates the users table; the api_tokens table comes from
// auth.Migrations.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.AddFunc("2026_10_01_120000_create_users",
		func(s *migrate.Schema) error {
			return s.Create("users", func(t *migrate.Table) {
				t.ID()
				t.String("name", 100)
				t.String("email", 255).Unique()
				t.String("password", 255)
				t.String("remember_token", 100).Default("")
				t.Timestamp("email_verified_at").Nullable()
				t.Boolean("admin").Default(false)
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.Drop("users") })
}

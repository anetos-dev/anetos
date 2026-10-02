// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/social"
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

// region: social
// findOrCreate returns the user of a provider account: the one it is
// linked to; else the user with its verified email address (who then
// signs in with either); else a new user. An address the provider hasn't
// verified can't be trusted to find anyone, and nor can one the user
// hasn't verified here: someone could have registered it first, with a
// password, to take over the account of whoever signs in with it later.
func findOrCreate(ctx context.Context, p social.Profile) (*User, error) {
	id, linked, err := social.FindLink(ctx, p)
	if err != nil {
		return nil, err
	}
	if linked {
		u, err := users.ByID(ctx, id)
		if !errors.Is(err, db.ErrNotFound) {
			return u, err
		}
		// The user was deleted: forget the link and start again.
		if err := social.Unlink(ctx, id, p.Provider); err != nil {
			return nil, err
		}
	}
	if p.Email == "" || !p.EmailVerified {
		return nil, &social.ErrNoAccount{Message: "Your " + p.Provider + " account has no verified email address."}
	}
	var u *User
	err = db.Tx(ctx, func(ctx context.Context) error { // the user and the link, or neither
		u, err = users.ByLogin(ctx, p.Email)
		if errors.Is(err, db.ErrNotFound) {
			now := anetos.Now(ctx).UTC()
			name := p.Name
			if name == "" {
				name = p.Email
			}
			u = &User{Name: name, Email: strings.ToLower(p.Email), EmailVerifiedAt: &now} // no password
			err = db.Create(ctx, u)
		}
		if err != nil {
			return err
		}
		if u.EmailVerifiedAt == nil {
			return &social.ErrNoAccount{Message: "An account with this email address exists. Log in with your password and verify the address, then sign in with " + p.Provider + "."}
		}
		// Linked already to another account at the provider: the address
		// was reused, not the same person.
		links, err := social.Links(ctx, u.AuthID())
		if err != nil {
			return err
		}
		if slices.ContainsFunc(links, func(l social.Account) bool { return l.Provider == p.Provider }) {
			return &social.ErrNoAccount{Message: "The account with this email address signs in with another " + p.Provider + " account."}
		}
		return social.Link(ctx, p, u.AuthID())
	})
	return u, err
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

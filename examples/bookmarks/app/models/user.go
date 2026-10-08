// SPDX-License-Identifier: Apache-2.0

package models

import (
	"context"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
)

// User is an account (anetos make:auth). AuthID and AuthPassword are what
// package auth needs; Users tells it how to find and update users. The
// API shows users as handlers.UserResponse, never as this struct.
type User struct {
	db.Model
	Name            string     `db:"name"`
	Email           string     `db:"email"`       // stored in lower case
	Password        string     `db:"password"`    // password.Hash
	SessionKey      string     `db:"session_key"` // replaced when the password changes or two-factor sign-in is turned on: older reset links and sign-in challenges stop working
	EmailVerifiedAt *time.Time `db:"email_verified_at"`
	DisabledAt      *time.Time `db:"disabled_at"` // set: can't sign in, and its tokens stop working
	TwoFactor       string     `db:"two_factor"`  // two-factor sign-in, encrypted by package auth
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable.
func (u *User) AuthPassword() string { return u.Password }

// Users tells package auth how to find users, which are disabled, and
// how to store their session keys, upgraded password hashes and
// two-factor sign-in.
var Users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		u, err := db.Find[User](ctx, n) // db.ErrNotFound: no such user
		return &u, err
	},
	ByLogin: func(ctx context.Context, email string) (*User, error) {
		u, err := db.Query[User](ctx).Where(UserCols.Email.Eq(strings.ToLower(email))).First()
		return &u, err
	},
	SetPassword: func(ctx context.Context, u *User, hash string) error {
		_, err := db.Query[User](ctx).Where(UserCols.ID.Eq(u.ID)).Update(UserCols.Password.Set(hash))
		return err
	},
	Disabled:   func(u *User) bool { return u.DisabledAt != nil },
	SessionKey: func(u *User) string { return u.SessionKey },
	SetSessionKey: func(ctx context.Context, u *User, key string) error {
		_, err := db.Query[User](ctx).Where(UserCols.ID.Eq(u.ID)).Update(UserCols.SessionKey.Set(key))
		return err
	},
	TwoFactor: func(u *User) string { return u.TwoFactor },
	SetTwoFactor: func(ctx context.Context, u *User, state string) error {
		u.TwoFactor = state
		_, err := db.Query[User](ctx).Where(UserCols.ID.Eq(u.ID)).Update(UserCols.TwoFactor.Set(state))
		return err
	},
}

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
// package auth needs; Users tells it how to find and update users.
type User struct {
	db.Model
	Name            string     `db:"name" json:"name"`
	Email           string     `db:"email" json:"email"` // stored in lower case
	Password        string     `db:"password" json:"-"`  // password.Hash
	RememberToken   string     `db:"remember_token" json:"-"`
	EmailVerifiedAt *time.Time `db:"email_verified_at" json:"email_verified_at"`
	// Plan is trial, free, or a paid plan the billing service set (pro,
	// team).
	Plan        string     `db:"plan" json:"plan"`
	TrialEndsAt *time.Time `db:"trial_ends_at" json:"trial_ends_at"` // while Plan is trial
}

// TrialDays is how long a new account's trial lasts.
const TrialDays = 14

// StartTrial puts a new user on the trial plan.
func (u *User) StartTrial(now time.Time) {
	end := now.UTC().AddDate(0, 0, TrialDays)
	u.Plan, u.TrialEndsAt = "trial", &end
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable.
func (u *User) AuthPassword() string { return u.Password }

// Users tells package auth how to find users and store their tokens and
// upgraded password hashes.
var Users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrUserNotFound
		}
		u, err := db.Find[User](ctx, n) // db.ErrNotFound: no such user
		return &u, err
	},
	ByLogin: func(ctx context.Context, email string) (*User, error) {
		u, err := db.Query[User](ctx).Where(UserCols.Email.Eq(strings.ToLower(email))).First()
		return &u, err
	},
	RememberToken: func(u *User) string { return u.RememberToken },
	SetRememberToken: func(ctx context.Context, u *User, token string) error {
		_, err := db.Query[User](ctx).Where(UserCols.ID.Eq(u.ID)).Update(UserCols.RememberToken.Set(token))
		return err
	},
	SetPassword: func(ctx context.Context, u *User, hash string) error {
		_, err := db.Query[User](ctx).Where(UserCols.ID.Eq(u.ID)).Update(UserCols.Password.Set(hash))
		return err
	},
}

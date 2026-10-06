// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// User is a member of staff, who signs in with an email and a password.
type User struct {
	db.Model
	Name     string `db:"name" json:"name"`
	Email    string `db:"email" json:"email"`
	Password string `db:"password" json:"-"`
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable.
func (u *User) AuthPassword() string { return u.Password }

// AdminName is the user's name in the admin's header.
func (u *User) AdminName() string { return u.Name }

// users tells package auth how to find users.
var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		return found(db.Find[User](ctx, n))
	},
	ByLogin: func(ctx context.Context, email string) (*User, error) {
		return found(db.Query[User](ctx).Where(db.C("email").Eq(strings.ToLower(email))).First())
	},
}

func found(u User, err error) (*User, error) {
	if errors.Is(err, db.ErrNotFound) {
		return nil, auth.ErrNoUser
	}
	return &u, err
}

// Category groups products.
type Category struct {
	db.Model
	Name string `db:"name" json:"name"`
}

// Product is something the shop sells. Deleted products go to the trash.
type Product struct {
	db.Model
	db.SoftDeletes
	CategoryID int64  `db:"category_id" json:"category_id"`
	Name       string `db:"name" json:"name"`
	SKU        string `db:"sku" json:"sku"`
	Price      int64  `db:"price" json:"price"` // in cents
	Stock      int    `db:"stock" json:"stock"`
	Status     string `db:"status" json:"status"` // draft, active, archived
}

// Migrations creates the app's tables.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.AddFunc("2026_10_06_120000_create_shop_tables",
		func(s *migrate.Schema) error {
			return errors.Join(
				s.Create("users", func(t *migrate.Table) {
					t.ID()
					t.String("name", 255)
					t.String("email", 255).Unique()
					t.String("password", 255)
					t.Timestamps()
				}),
				s.Create("categories", func(t *migrate.Table) {
					t.ID()
					t.String("name", 100)
					t.Timestamps()
				}),
				s.Create("products", func(t *migrate.Table) {
					t.ID()
					t.BigInteger("category_id")
					t.String("name", 255)
					t.String("sku", 50).Unique()
					t.BigInteger("price")
					t.Integer("stock")
					t.String("status", 20)
					t.Timestamps()
					t.SoftDeletes()
				}),
			)
		},
		func(s *migrate.Schema) error {
			return errors.Join(s.Drop("products"), s.Drop("categories"), s.Drop("users"))
		})
}

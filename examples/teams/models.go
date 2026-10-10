// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
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

// Team is a group of users working on projects. Its members are the users
// with a role in its scope: there is no membership table.
type Team struct {
	db.Model
	Name string `db:"name" json:"name"`
}

// Project belongs to a team.
type Project struct {
	db.Model
	TeamID int64  `db:"team_id" json:"team_id"`
	Name   string `db:"name" json:"name"`
}

var (
	colID     = db.Col[int64]("id")
	colTeamID = db.Col[int64]("team_id")
	colName   = db.Col[string]("name")
)

// users tells package auth how to find users.
var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrUserNotFound
		}
		u, err := db.Find[User](ctx, n)
		return &u, err
	},
	ByLogin: func(context.Context, string) (*User, error) { return nil, auth.ErrUserNotFound },
}

// Migrations creates the app's tables.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.AddFunc("2026_10_02_120000_create_teams",
		func(s *migrate.Schema) error {
			if err := s.Create("users", func(t *migrate.Table) {
				t.ID()
				t.String("name", 255)
				t.String("email", 255).Unique()
				t.Timestamps()
			}); err != nil {
				return err
			}
			if err := s.Create("teams", func(t *migrate.Table) {
				t.ID()
				t.String("name", 255)
				t.Timestamps()
			}); err != nil {
				return err
			}
			return s.Create("projects", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("team_id").Constrained().CascadeOnDelete()
				t.String("name", 255)
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error {
			for _, table := range []string{"projects", "teams", "users"} {
				if err := s.Drop(table); err != nil {
					return err
				}
			}
			return nil
		})
}

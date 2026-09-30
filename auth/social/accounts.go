// SPDX-License-Identifier: Apache-2.0

package social

import (
	"context"
	"errors"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// Account links a provider account to a user, in the social_accounts
// table.
type Account struct {
	db.Model
	// Provider is the provider's name.
	Provider string `db:"provider" json:"provider"`
	// Subject is the account's identifier at the provider.
	Subject string `db:"subject" json:"subject"`
	// UserID is the user's AuthID.
	UserID string `db:"user_id" json:"user_id"`
	// Email is the account's address when it was linked.
	Email string `db:"email" json:"email"`
}

// TableName implements db.Tabler.
func (Account) TableName() string { return "social_accounts" }

// Migrations returns the migration creating the social_accounts table,
// for migrate.ForApp.
func Migrations() *migrate.Set {
	s := migrate.NewSet("social")
	s.AddFunc("2026_10_01_000300_create_social_accounts_table",
		func(s *migrate.Schema) error {
			return s.Create("social_accounts", func(t *migrate.Table) {
				t.ID()
				t.String("provider", 50)
				t.String("subject", 255)
				t.String("user_id", 255)
				t.String("email", 255).Default("")
				t.Timestamps()
				t.Unique("provider", "subject")
				t.Index("user_id")
			})
		},
		func(s *migrate.Schema) error { return s.Drop("social_accounts") })
	return s
}

var (
	colProvider = db.Col[string]("provider")
	colSubject  = db.Col[string]("subject")
	colUserID   = db.Col[string]("user_id")
)

// FindLink returns the AuthID of the user p's account is linked to, and
// whether it is linked.
func FindLink(ctx context.Context, p Profile) (string, bool, error) {
	a, err := db.Query[Account](ctx).Where(colProvider.Eq(p.Provider), colSubject.Eq(p.Subject)).First()
	if errors.Is(err, db.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return a.UserID, true, nil
}

// Link links p's account to the user userID (an AuthID), so that signing
// in with it again finds that user. Linking an account that is already
// linked moves it.
func Link(ctx context.Context, p Profile, userID string) error {
	return db.Tx(ctx, func(ctx context.Context) error {
		if _, err := db.Query[Account](ctx).Where(colProvider.Eq(p.Provider), colSubject.Eq(p.Subject)).Delete(); err != nil {
			return err
		}
		return db.Create(ctx, &Account{Provider: p.Provider, Subject: p.Subject, UserID: userID, Email: p.Email})
	})
}

// Links returns the accounts linked to the user userID.
func Links(ctx context.Context, userID string) ([]Account, error) {
	return db.Query[Account](ctx).Where(colUserID.Eq(userID)).OrderBy(colProvider.Asc()).Get()
}

// Unlink removes the link of the user userID's account at provider.
func Unlink(ctx context.Context, userID, provider string) error {
	_, err := db.Query[Account](ctx).Where(colUserID.Eq(userID), colProvider.Eq(provider)).Delete()
	return err
}

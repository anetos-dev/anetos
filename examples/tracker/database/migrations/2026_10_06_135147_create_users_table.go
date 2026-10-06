// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

// The users table (anetos make:auth). API tokens are in the api_tokens
// table, from auth.Migrations.
func init() {
	All.AddFunc("2026_10_06_135147_create_users_table",
		func(s *migrate.Schema) error {
			return s.Create("users", func(t *migrate.Table) {
				t.ID()
				t.String("name", 100)
				t.String("email", 255).Unique()
				t.String("password", 255)
				t.String("remember_token", 100).Default("")
				t.String("session_key", 100).Default("")
				t.Timestamp("email_verified_at").Nullable()
				t.Timestamp("disabled_at").Nullable()
				t.String("two_factor", 1024).Default("") // encrypted by package auth
				t.String("pending_email", 255).Default("")
				t.String("locale", 35).Default("")
				t.String("time_zone", 64).Default("")
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error {
			return s.DropIfExists("users")
		},
	)
}

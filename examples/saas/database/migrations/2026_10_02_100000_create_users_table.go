// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

// The users table (anetos make:auth). API tokens are in the api_tokens
// table, from auth.Migrations.
func init() {
	All.AddFunc("2026_10_02_100000_create_users_table",
		func(s *migrate.Schema) error {
			return s.Create("users", func(t *migrate.Table) {
				t.ID()
				t.String("name", 100)
				t.String("email", 255).Unique()
				t.String("password", 255)
				t.String("remember_token", 100).Default("")
				t.Timestamp("email_verified_at").Nullable()
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error {
			return s.DropIfExists("users")
		},
	)
}

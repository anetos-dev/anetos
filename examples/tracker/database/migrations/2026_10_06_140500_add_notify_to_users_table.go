// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

// Whether a user gets the notifications by email (the settings page).
func init() {
	All.AddFunc("2026_10_06_140500_add_notify_to_users_table",
		func(s *migrate.Schema) error {
			return s.Alter("users", func(t *migrate.Table) {
				t.Boolean("notify").Default(true)
			})
		},
		func(s *migrate.Schema) error {
			return s.Alter("users", func(t *migrate.Table) { t.DropColumn("notify") })
		},
	)
}

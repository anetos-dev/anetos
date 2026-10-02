// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

// Users' plans: trial, free, pro or team.
func init() {
	All.AddFunc("2026_10_02_100100_add_plans_to_users_table",
		func(s *migrate.Schema) error {
			return s.Alter("users", func(t *migrate.Table) {
				t.String("plan", 20).Default("free")
				t.Timestamp("trial_ends_at").Nullable()
				t.Index("plan", "trial_ends_at")
			})
		},
		func(s *migrate.Schema) error {
			return s.Alter("users", func(t *migrate.Table) {
				t.DropIndex("plan", "trial_ends_at")
				t.DropColumn("trial_ends_at", "plan")
			})
		},
	)
}

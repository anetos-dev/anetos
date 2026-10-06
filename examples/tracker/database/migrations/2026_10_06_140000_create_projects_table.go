// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_06_140000_create_projects_table",
		func(s *migrate.Schema) error {
			return s.Create("projects", func(t *migrate.Table) {
				t.ID()
				t.String("key", 10).Unique()
				t.String("name", 100)
				t.Text("description")
				t.Integer("last_number").Default(0)
				t.Timestamp("archived_at").Nullable()
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.DropIfExists("projects") },
	)
}

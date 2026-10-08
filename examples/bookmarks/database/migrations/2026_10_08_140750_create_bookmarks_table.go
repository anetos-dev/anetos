// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_08_140750_create_bookmarks_table",
		func(s *migrate.Schema) error {
			return s.Create("bookmarks", func(t *migrate.Table) {
				t.ID()
				// region: user-id
				t.ForeignID("user_id").Constrained().CascadeOnDelete() // whose bookmark
				// endregion
				t.String("url", 255)
				t.String("title", 255)
				t.Text("notes")
				t.Boolean("archived")
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error {
			return s.DropIfExists("bookmarks")
		},
	)
}

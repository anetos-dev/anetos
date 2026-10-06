// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_06_140300_create_comments_table",
		func(s *migrate.Schema) error {
			return s.Create("comments", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("issue_id").Constrained().CascadeOnDelete()
				t.ForeignID("author_id").References("users")
				t.Text("body")
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.DropIfExists("comments") },
	)
}

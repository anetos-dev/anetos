package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_06_142838_create_comments_table",
		// region: migration
		func(s *migrate.Schema) error {
			return s.Create("comments", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("issue_id").Constrained().CascadeOnDelete()
				t.ForeignID("author_id").References("users")
				t.Text("body")
				t.Timestamps()
			})
		},
		// endregion
		func(s *migrate.Schema) error {
			return s.DropIfExists("comments")
		},
	)
}

package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_06_142537_create_issues_table",
		// region: migration
		func(s *migrate.Schema) error {
			return s.Create("issues", func(t *migrate.Table) {
				t.ID()
				t.String("title", 200)
				t.Text("body")
				t.String("status", 10).Default("open")
				t.ForeignID("author_id").References("users")
				t.Timestamps()
			})
		},
		// endregion
		func(s *migrate.Schema) error {
			return s.DropIfExists("issues")
		},
	)
}

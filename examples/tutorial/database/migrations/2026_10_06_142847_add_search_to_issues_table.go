package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_06_142847_add_search_to_issues_table",
		// region: migration
		func(s *migrate.Schema) error {
			return s.Alter("issues", func(t *migrate.Table) {
				t.SearchIndex("title", "body") // titles weigh more
			})
		},
		func(s *migrate.Schema) error {
			return s.Alter("issues", func(t *migrate.Table) {
				t.DropSearchIndex()
			})
		},
		// endregion
	)
}

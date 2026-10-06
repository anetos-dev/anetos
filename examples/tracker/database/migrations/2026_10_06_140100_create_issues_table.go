// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_06_140100_create_issues_table",
		func(s *migrate.Schema) error {
			return s.Create("issues", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("project_id").Constrained().CascadeOnDelete()
				t.Integer("number")
				t.String("title", 200)
				t.Text("body")
				t.String("status", 10).Default("open")
				t.String("priority", 10).Default("normal")
				t.ForeignID("author_id").References("users")
				t.ForeignID("assignee_id").Nullable().References("users").NullOnDelete()
				t.Timestamp("closed_at").Nullable()
				t.Timestamps()
				t.SoftDeletes()
				t.Unique("project_id", "number")
				t.Index("project_id", "status")
				t.SearchIndex("title", "body") // titles weigh more
			})
		},
		func(s *migrate.Schema) error { return s.DropIfExists("issues") },
	)
}

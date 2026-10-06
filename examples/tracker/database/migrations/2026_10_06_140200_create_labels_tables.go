// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

// Labels, and the issue_label pivot table of Issue.Labels.
func init() {
	All.AddFunc("2026_10_06_140200_create_labels_tables",
		func(s *migrate.Schema) error {
			err := s.Create("labels", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("project_id").Constrained().CascadeOnDelete()
				t.String("name", 50)
				t.String("color", 7).Default("#6b7280")
				t.Timestamps()
				t.Unique("project_id", "name")
			})
			if err != nil {
				return err
			}
			return s.Create("issue_label", func(t *migrate.Table) {
				t.ForeignID("issue_id").Constrained().CascadeOnDelete()
				t.ForeignID("label_id").Constrained().CascadeOnDelete()
				t.Primary("issue_id", "label_id")
			})
		},
		func(s *migrate.Schema) error {
			if err := s.DropIfExists("issue_label"); err != nil {
				return err
			}
			return s.DropIfExists("labels")
		},
	)
}

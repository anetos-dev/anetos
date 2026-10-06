// SPDX-License-Identifier: Apache-2.0

package migrations

import "anetos.dev/anetos/db/migrate"

func init() {
	All.AddFunc("2026_10_06_140400_create_attachments_table",
		func(s *migrate.Schema) error {
			return s.Create("attachments", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("issue_id").Constrained().CascadeOnDelete()
				t.ForeignID("uploader_id").References("users")
				t.String("name", 255)
				t.String("path", 255)
				t.BigInteger("size")
				t.String("content_type", 100)
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.DropIfExists("attachments") },
	)
}

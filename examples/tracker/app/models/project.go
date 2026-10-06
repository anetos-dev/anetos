// SPDX-License-Identifier: Apache-2.0

package models

import (
	"time"

	"anetos.dev/anetos/db"
)

// Project groups issues. Its key (WEB) is in its URLs and its issues'
// names (WEB-12), and never changes: the project's roles are granted in
// its scope, project:WEB.
type Project struct {
	db.Model
	Key         string     `db:"key" json:"key"`
	Name        string     `db:"name" json:"name"`
	Description string     `db:"description" json:"description"`
	LastNumber  int        `db:"last_number,readonly" json:"-"` // its newest issue's number: only Issue.BeforeCreate writes it
	ArchivedAt  *time.Time `db:"archived_at" json:"archived_at"`

	Issues []Issue `rel:"has_many" json:"-"`
	Labels []Label `rel:"has_many" json:"-"`
}

// Archived reports whether the project is archived: read-only.
func (p Project) Archived() bool { return p.ArchivedAt != nil }

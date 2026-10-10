// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"

	"anetos.dev/anetos/admin"

	"anetos.dev/anetos/examples/tracker/app/models"
)

// ProjectForm is what the admin edits of a project. The key is in its
// URLs and grants, so it stays; projects are made on the site, where
// their maker becomes the owner.
type ProjectForm struct {
	Name        string         `json:"name" validate:"required|max:100"`
	Description string         `json:"description" validate:"max:2000" admin:"textarea"`
	ArchivedAt  admin.DateTime `json:"archived_at" label:"Archived" admin:"help=Set to archive the project: its issues become read-only."`
}

// Projects is the admin's resource for models.Project, at /admin/projects.
func Projects(p *admin.Panel) error {
	return admin.Add(p, admin.Resource[models.Project, ProjectForm]{
		Name: "projects",
		Columns: []admin.Column[models.Project]{
			admin.TextColumn[models.Project]("key", "Key"),
			admin.TextColumn[models.Project]("name", "Name"),
			{Label: "Issues", Value: func(p models.Project) any { return p.LastNumber }},
			admin.TextColumn[models.Project]("archived_at", "Archived"),
			admin.TextColumn[models.Project]("created_at", "Created"),
		},
		Search:      []string{"key", "name"},
		RecordTitle: func(p models.Project) string { return p.Key + " " + p.Name },
		NoCreate:    true,
		NoDelete:    true, // archive instead: deleting would delete its issues
		Edit: func(m models.Project) ProjectForm {
			f := ProjectForm{Name: m.Name, Description: m.Description}
			if m.ArchivedAt != nil {
				f.ArchivedAt = admin.DateTime{Time: *m.ArchivedAt}
			}
			return f
		},
		Apply: func(_ context.Context, in ProjectForm, m *models.Project) error {
			m.Name, m.Description, m.ArchivedAt = in.Name, in.Description, nil
			if !in.ArchivedAt.IsZero() {
				t := in.ArchivedAt.Time
				m.ArchivedAt = &t
			}
			return nil
		},
	})
}

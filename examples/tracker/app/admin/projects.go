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
			admin.Field[models.Project]("Key", "key"),
			admin.Field[models.Project]("Name", "name"),
			{Title: "Issues", Value: func(p models.Project) any { return p.LastNumber }},
			admin.Field[models.Project]("Archived", "archived_at"),
			admin.Field[models.Project]("Created", "created_at"),
		},
		Search:   []string{"key", "name"},
		Label:    func(p models.Project) string { return p.Key + " " + p.Name },
		NoCreate: true,
		NoDelete: true, // archive instead: deleting would delete its issues
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

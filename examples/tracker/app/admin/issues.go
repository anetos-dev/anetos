// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"strconv"

	"anetos.dev/anetos"
	"anetos.dev/anetos/admin"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/tracker/app/models"
)

// IssueForm is what the admin edits of an issue: its text, state and
// priority. Issues are opened on the site, in their project.
type IssueForm struct {
	Title    string `json:"title" validate:"required|max:200"`
	Body     string `json:"body" label:"Description" validate:"max:20000" admin:"textarea"`
	Status   string `json:"status" validate:"required|in:open,closed" admin:"select=open|closed"`
	Priority string `json:"priority" validate:"required|in:low,normal,high,urgent" admin:"select=low|normal|high|urgent"`
}

// Issues is the admin's resource for models.Issue, at /admin/issues,
// with a trash (issues are soft-deleted) and their history.
func Issues(p *admin.Panel) error {
	return admin.Add(p, admin.Resource[models.Issue, IssueForm]{
		Name: "issues",
		Columns: []admin.Column[models.Issue]{
			{Label: "Issue", Value: func(i models.Issue) any { return ref(i) }},
			admin.TextColumn[models.Issue]("title", "Title"),
			admin.TextColumn[models.Issue]("status", "Status"),
			admin.TextColumn[models.Issue]("priority", "Priority"),
			admin.TextColumn[models.Issue]("created_at", "Created"),
		},
		Search: []string{"title", "body"},
		Filters: []admin.Filter[models.Issue]{
			admin.Equals[models.Issue]("status", "Status", admin.Choices(models.Open, models.Closed)...),
			admin.Equals[models.Issue]("priority", "Priority", admin.Choices(models.Priorities...)...),
		},
		Query:       func(q *db.Q[models.Issue]) *db.Q[models.Issue] { return q.With(models.IssueRels.Project) },
		RecordTitle: ref,
		NoCreate:    true,
		Edit: func(m models.Issue) IssueForm {
			return IssueForm{Title: m.Title, Body: m.Body, Status: m.Status, Priority: m.Priority}
		},
		Apply: func(ctx context.Context, in IssueForm, m *models.Issue) error {
			if in.Status != m.Status {
				m.ClosedAt = nil
				if in.Status == models.Closed {
					now := anetos.Now(ctx)
					m.ClosedAt = &now
				}
			}
			m.Title, m.Body, m.Status, m.Priority = in.Title, in.Body, in.Status, in.Priority
			return nil
		},
	})
}

// ref is the issue's name, WEB-12, when its project is loaded.
func ref(i models.Issue) string {
	if i.Project == nil {
		return "#" + strconv.Itoa(i.Number)
	}
	return i.Ref(i.Project.Key)
}

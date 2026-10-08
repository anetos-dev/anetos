// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"slices"
	"strings"
	"time"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
)

// API is the JSON API, for clients with an API token (Authorization:
// Bearer <token>). It checks the same permissions as the pages; a
// read-only token can only view.
type API struct{}

// APIProject is a project in the API.
type APIProject struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	ArchivedAt  *time.Time `json:"archived_at"`
}

// Projects lists the projects the user sees.
func (API) Projects(c *web.Ctx, _ struct{}) ([]APIProject, error) {
	keys, all, err := access.VisibleKeys(c)
	if err != nil {
		return nil, err
	}
	q := db.Query[models.Project](c).OrderBy(models.ProjectCols.Key.Asc())
	if !all {
		q = q.Where(models.ProjectCols.Key.In(keys...))
	}
	projects, err := q.Get()
	if err != nil {
		return nil, err
	}
	out := make([]APIProject, len(projects))
	for i, p := range projects {
		out[i] = APIProject{p.Key, p.Name, p.Description, p.ArchivedAt}
	}
	return out, nil
}

// APIIssue is an issue in the API.
type APIIssue struct {
	Ref       string     `json:"ref"` // WEB-12
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Status    string     `json:"status"`
	Priority  string     `json:"priority"`
	Author    string     `json:"author"`             // the author's email address
	Assignee  string     `json:"assignee,omitempty"` // the assignee's
	Labels    []string   `json:"labels"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at"`
}

func apiIssue(project models.Project, i models.Issue) APIIssue {
	out := APIIssue{
		Ref: i.Ref(project.Key), Number: i.Number, Title: i.Title, Body: i.Body, Status: i.Status,
		Priority: i.Priority, Labels: []string{}, CreatedAt: i.CreatedAt, ClosedAt: i.ClosedAt,
	}
	if i.Author != nil {
		out.Author = i.Author.Email
	}
	if i.Assignee != nil {
		out.Assignee = i.Assignee.Email
	}
	for _, l := range i.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	return out
}

// APIIssueQuery is GET /api/projects/{project}/issues: by status (open,
// closed, all; default open), search words, a page of 50.
type APIIssueQuery struct {
	Project string `path:"project"`
	Status  string `query:"status" validate:"in:open,closed,all"`
	Q       string `query:"q" validate:"max:200"`
	Page    int    `query:"page"`
}

// APIIssuePage is a page of issues.
type APIIssuePage struct {
	Issues   []APIIssue `json:"issues"`
	Page     int        `json:"page"`
	LastPage int        `json:"last_page"`
	Total    int64      `json:"total"`
}

// Issues lists a project's issues, newest first (best matches first
// with q).
func (API) Issues(c *web.Ctx, in APIIssueQuery) (APIIssuePage, error) {
	project, err := loadProject(c, in.Project, access.ViewIssues)
	if err != nil {
		return APIIssuePage{}, err
	}
	q := db.Query[models.Issue](c).Where(models.IssueCols.ProjectID.Eq(project.ID)).
		With(models.IssueRels.Author, models.IssueRels.Assignee, models.IssueRels.Labels.OrderBy(models.LabelCols.Name.Asc())).
		OrderBy(models.IssueCols.Number.Desc())
	switch in.Status {
	case "", models.Open:
		q = q.Where(models.IssueCols.Status.Eq(models.Open))
	case models.Closed:
		q = q.Where(models.IssueCols.Status.Eq(models.Closed))
	}
	page, err := q.Search(in.Q).Paginate(in.Page, 50)
	if err != nil {
		return APIIssuePage{}, err
	}
	out := APIIssuePage{Issues: make([]APIIssue, len(page.Data)), Page: page.CurrentPage, LastPage: page.LastPage, Total: page.Total}
	for i, issue := range page.Data {
		out.Issues[i] = apiIssue(project, issue)
	}
	return out, nil
}

// Issue returns one issue.
func (API) Issue(c *web.Ctx, in IssuePath) (APIIssue, error) {
	project, issue, err := loadIssue(c, in, access.ViewIssues)
	if err != nil {
		return APIIssue{}, err
	}
	return apiIssue(project, issue), nil
}

// APINewIssue opens an issue through the API.
type APINewIssue struct {
	Project  string   `path:"project"`
	Title    string   `json:"title" validate:"required|max:200"`
	Body     string   `json:"body" validate:"max:20000"`
	Priority string   `json:"priority" validate:"in:low,normal,high,urgent"` // default normal
	Labels   []string `json:"labels" validate:"max:20"`                      // the labels' names
}

// CreateIssue opens an issue (201: the route's status, with the issue).
// Labels are given by name; unknown ones are a 422.
func (API) CreateIssue(c *web.Ctx, in APINewIssue) (APIIssue, error) {
	project, err := loadProject(c, in.Project, access.CreateIssues)
	if err != nil {
		return APIIssue{}, err
	}
	fields := IssueFields{Title: in.Title, Body: in.Body, Priority: in.Priority}
	if fields.Priority == "" {
		fields.Priority = "normal"
	}
	if fields.Labels, err = labelIDs(c, project, in.Labels); err != nil {
		return APIIssue{}, err
	}
	issue, err := openIssue(c, project, fields)
	if err != nil {
		return APIIssue{}, err
	}
	if err := db.Load(c, &issue, models.IssueRels.Labels.OrderBy(models.LabelCols.Name.Asc())); err != nil {
		return APIIssue{}, err
	}
	return apiIssue(project, issue), nil
}

// labelIDs returns the IDs of the project's labels with the names; an
// unknown name fails validation.
func labelIDs(ctx context.Context, project models.Project, names []string) ([]int64, error) {
	if len(names) == 0 {
		return nil, nil
	}
	for i := range names {
		names[i] = strings.ToLower(strings.TrimSpace(names[i]))
	}
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	ids, err := db.Pluck(db.Query[models.Label](ctx).
		Where(models.LabelCols.ProjectID.Eq(project.ID), models.LabelCols.Name.In(names...)), models.LabelCols.ID)
	if err == nil && len(ids) != len(names) {
		err = validate.Fail("labels", i18n.T(ctx, "issues.errors.labels"))
	}
	return ids, err
}

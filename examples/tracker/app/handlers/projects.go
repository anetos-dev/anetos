// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views"
)

// Projects serves the projects: the list, creating one, a project's
// issues and its settings.
type Projects struct{}

// Index lists the projects the user has a role in (every project for
// administrators), with their open and closed issue counts.
func (Projects) Index(c *web.Ctx) error {
	keys, all, err := access.VisibleKeys(c)
	if err != nil {
		return err
	}
	q := db.Query[models.Project](c).OrderBy(models.ProjectCols.ArchivedAt.Asc(), models.ProjectCols.Name.Asc())
	if !all {
		q = q.Where(models.ProjectCols.Key.In(keys...))
	}
	projects, err := q.Get()
	if err != nil {
		return err
	}
	rows := make([]views.ProjectRow, len(projects))
	for i, p := range projects {
		rows[i].Project = p
	}
	if err := countIssues(c, rows); err != nil {
		return err
	}
	return c.Render(http.StatusOK, views.Projects(rows))
}

// countIssues fills in the open and closed counts of the projects, with
// one query.
func countIssues(ctx context.Context, rows []views.ProjectRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	at := map[int64]int{}
	for i, r := range rows {
		ids[i] = r.Project.ID
		at[r.Project.ID] = i
	}
	type count struct {
		ProjectID int64  `db:"project_id"`
		Status    string `db:"status"`
		N         int64  `db:"n"`
	}
	counts, err := db.Select[count](db.Query[models.Issue](ctx).
		Where(models.IssueCols.ProjectID.In(ids...)).
		GroupBy("project_id", "status"), "project_id", "status", "COUNT(*) AS n")
	if err != nil {
		return err
	}
	for _, n := range counts {
		if n.Status == models.Open {
			rows[at[n.ProjectID]].Open = n.N
		} else {
			rows[at[n.ProjectID]].Done = n.N
		}
	}
	return nil
}

// New shows the form for a new project.
func (Projects) New(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.NewProject())
}

// ProjectInput is a new project.
type ProjectInput struct {
	Key         string `json:"key" validate:"required|ascii|alpha_num|uppercase|between:2,10|unique:projects,key"`
	Name        string `json:"name" validate:"required|max:100"`
	Description string `json:"description" validate:"max:2000"`
}

// Create creates a project, owned by the user who creates it.
func (Projects) Create(c *web.Ctx, in ProjectInput) (web.Responder, error) {
	u, err := currentUser(c)
	if err != nil {
		return nil, err
	}
	if in.Key[0] >= '0' && in.Key[0] <= '9' {
		return nil, validate.Fail("key", i18n.T(c, "projects.errors.key_letter"))
	}
	p := models.Project{Key: in.Key, Name: strings.TrimSpace(in.Name), Description: in.Description}
	err = db.Tx(c, func(ctx context.Context) error { // the project and its owner, or neither
		if err := db.Create(ctx, &p); err != nil {
			return err
		}
		return rbac.Assign(ctx, u.AuthID(), access.Project(p.Key), access.Owner)
	})
	if err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "projects.status.created"))
	return web.RedirectRoute("projects.show", p.Key), nil
}

// IssueFilter is the query of a project's page: which issues, in what
// order, which page.
type IssueFilter struct {
	Project  string `path:"project"`
	Status   string `query:"status" validate:"in:open,closed,all"`
	Label    string `query:"label" validate:"max:50"`
	Assignee string `query:"assignee" validate:"max:20"`
	Q        string `query:"q" validate:"max:200"`
	Sort     string `query:"sort" validate:"in:newest,oldest,updated,priority"`
	Page     int    `query:"page"`
}

// Show is a project's page: its issues, filtered, 25 a page.
func (Projects) Show(c *web.Ctx, in IssueFilter) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.ViewIssues)
	if err != nil {
		return nil, err
	}
	f := views.Filter{Status: in.Status, Label: in.Label, Assignee: in.Assignee, Q: in.Q, Sort: in.Sort}
	if f.Status == "" {
		f.Status = models.Open
	}
	q := db.Query[models.Issue](c).Where(models.IssueCols.ProjectID.Eq(project.ID)).
		With(models.IssueRels.Assignee, models.IssueRels.Labels.OrderBy(models.LabelCols.Name.Asc()))
	if f.Status != "all" {
		q = q.Where(models.IssueCols.Status.Eq(f.Status))
	}
	if f.Label != "" {
		q = q.WhereHas(models.IssueRels.Labels, models.LabelCols.Name.Of("labels").Eq(f.Label))
	}
	switch f.Assignee {
	case "":
	case "none":
		q = q.Where(models.IssueCols.AssigneeID.IsNull())
	case "me":
		u, err := currentUser(c)
		if err != nil {
			return nil, err
		}
		q = q.Where(models.IssueCols.AssigneeID.Eq(&u.ID))
	default:
		id, err := strconv.ParseInt(f.Assignee, 10, 64)
		if err != nil {
			return nil, validate.Fail("assignee", i18n.T(c, "issues.errors.assignee"))
		}
		q = q.Where(models.IssueCols.AssigneeID.Eq(&id))
	}
	switch f.Sort {
	case "oldest":
		q = q.OrderBy(models.IssueCols.Number.Asc())
	case "updated":
		q = q.OrderBy(models.IssueCols.UpdatedAt.Desc(), models.IssueCols.Number.Desc())
	case "priority":
		q = q.OrderBy(db.OrderRaw(priorityOrder), models.IssueCols.Number.Desc())
	default:
		q = q.OrderBy(models.IssueCols.Number.Desc())
	}
	page, err := q.Search(f.Q).Paginate(in.Page, 25) // with words, the best matches first
	if err != nil {
		return nil, err
	}
	labels, err := db.Query[models.Label](c).Where(models.LabelCols.ProjectID.Eq(project.ID)).OrderBy(models.LabelCols.Name.Asc()).Get()
	if err != nil {
		return nil, err
	}
	assignees, err := assignable(c, project)
	if err != nil {
		return nil, err
	}
	return web.View(views.ProjectPage(views.IssueList{
		Project: project, Page: page, Filter: f, Labels: labels, Assignees: assignees,
		CanCreate: !project.Archived() && access.Can(c, project.Key, access.CreateIssues),
		CanManage: access.Can(c, project.Key, access.ManageProjects),
	})), nil
}

// Settings shows a project's settings: its name, labels and members.
func (Projects) Settings(c *web.Ctx, in ProjectPath) (web.Responder, error) {
	project, err := loadSettings(c, in.Project)
	if err != nil {
		return nil, err
	}
	page, err := settingsPage(c, project)
	if err != nil {
		return nil, err
	}
	return web.View(views.ProjectSettingsPage(page)), nil
}

func settingsPage(c *web.Ctx, project models.Project) (views.ProjectSettings, error) {
	page := views.ProjectSettings{Project: project, Roles: access.ProjectRoles}
	var err error
	if page.Labels, err = db.Query[models.Label](c).Where(models.LabelCols.ProjectID.Eq(project.ID)).OrderBy(models.LabelCols.Name.Asc()).Get(); err != nil {
		return page, err
	}
	if page.Members, err = members(c, project); err != nil {
		return page, err
	}
	u, err := currentUser(c)
	if err != nil {
		return page, err
	}
	page.MeID = u.ID
	return page, nil
}

// UpdateProjectInput is the project's settings form.
type UpdateProjectInput struct {
	Project     string `path:"project"`
	Name        string `json:"name" validate:"required|max:100"`
	Description string `json:"description" validate:"max:2000"`
}

// Update saves the project's name and description. (db.Update writes
// every column but LastNumber, which is read-only in the model: an issue
// opened meanwhile keeps its number.)
func (Projects) Update(c *web.Ctx, in UpdateProjectInput) (web.Responder, error) {
	project, err := loadSettings(c, in.Project)
	if err != nil {
		return nil, err
	}
	project.Name, project.Description = strings.TrimSpace(in.Name), in.Description
	if err := db.Update(c, &project); err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "projects.status.saved"))
	return web.RedirectRoute("projects.settings", project.Key), nil
}

// Archive archives the project (read-only), or brings it back.
func (Projects) Archive(c *web.Ctx, in ProjectPath) (web.Responder, error) {
	project, err := loadSettings(c, in.Project)
	if err != nil {
		return nil, err
	}
	msg := "projects.status.unarchived"
	if project.ArchivedAt == nil {
		now := anetos.Now(c)
		project.ArchivedAt = &now
		msg = "projects.status.archived"
	} else {
		project.ArchivedAt = nil
	}
	if err := db.Update(c, &project); err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, msg))
	return web.RedirectRoute("projects.settings", project.Key), nil
}

// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views"
)

// ProjectPath reads the project's key from the path: /p/{project}.
type ProjectPath struct {
	Project string `path:"project"`
}

// IssuePath reads the project's key and the issue's number:
// /p/{project}/issues/{number}.
type IssuePath struct {
	Project string `path:"project"`
	Number  int    `path:"number"`
}

// loadProject returns the project with the key if the signed-in user may
// do p in it. A project they can't see is a 404, as if it didn't exist;
// one they see but may not do p in, a 403. Archived projects are
// read-only: only viewing is allowed.
func loadProject(ctx context.Context, key string, p rbac.Permission) (models.Project, error) {
	return findProject(ctx, key, p, false)
}

// loadSettings is loadProject for the project's own settings, which its
// owners change even when it's archived (to bring it back).
func loadSettings(ctx context.Context, key string) (models.Project, error) {
	return findProject(ctx, key, access.ManageProjects, true)
}

func findProject(ctx context.Context, key string, p rbac.Permission, archivedOK bool) (models.Project, error) {
	project, err := db.Query[models.Project](ctx).Where(models.ProjectCols.Key.Eq(key)).First()
	if err != nil {
		return project, err // db.ErrNotFound: 404
	}
	// The stored key, not the URL's: a case-insensitive collation finds
	// WEB for /p/web, and grants are in project:WEB.
	if !access.Can(ctx, project.Key, access.ViewIssues) {
		return project, db.ErrNotFound
	}
	if err := access.Authorize(ctx, project.Key, p); err != nil {
		return project, err
	}
	if project.Archived() && p != access.ViewIssues && !archivedOK {
		return project, web.Error(http.StatusForbidden, i18n.T(ctx, "projects.archived"))
	}
	return project, nil
}

// loadIssue returns the project and its issue with the number, as
// loadProject checks it.
func loadIssue(ctx context.Context, in IssuePath, p rbac.Permission) (models.Project, models.Issue, error) {
	project, err := loadProject(ctx, in.Project, p)
	if err != nil {
		return project, models.Issue{}, err
	}
	issue, err := db.Query[models.Issue](ctx).
		Where(models.IssueCols.ProjectID.Eq(project.ID), models.IssueCols.Number.Eq(in.Number)).
		With(models.IssueRels.Author, models.IssueRels.Assignee, models.IssueRels.Labels.OrderBy(models.LabelCols.Name.Asc())).
		First()
	return project, issue, err
}

// currentUser returns the signed-in user.
func currentUser(ctx context.Context) (*models.User, error) {
	return auth.Current[*models.User](ctx)
}

// members returns the project's members, by name, with their roles (the
// highest, if they have several).
func members(ctx context.Context, project models.Project) ([]views.Member, error) {
	grants, err := rbac.Assignments(ctx, access.Project(project.Key))
	if err != nil {
		return nil, err
	}
	roles := map[int64]string{}
	var ids []int64
	for _, g := range grants {
		id, err := strconv.ParseInt(g.UserID, 10, 64)
		if err != nil {
			continue
		}
		if old, ok := roles[id]; !ok {
			ids = append(ids, id)
			roles[id] = g.Role
		} else if slices.Index(access.ProjectRoles, g.Role) < slices.Index(access.ProjectRoles, old) {
			roles[id] = g.Role
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	users, err := db.Query[models.User](ctx).Where(models.UserCols.ID.In(ids...)).OrderBy(models.UserCols.Name.Asc()).Get()
	if err != nil {
		return nil, err
	}
	out := make([]views.Member, len(users))
	for i, u := range users {
		out[i] = views.Member{User: u, Role: roles[u.ID]}
	}
	return out, nil
}

// assignable returns the members who can be assigned issues: owners and
// members, not viewers.
func assignable(ctx context.Context, project models.Project) ([]models.User, error) {
	ms, err := members(ctx, project)
	if err != nil {
		return nil, err
	}
	var users []models.User
	for _, m := range ms {
		if m.Role != access.Viewer {
			users = append(users, m.User)
		}
	}
	return users, nil
}

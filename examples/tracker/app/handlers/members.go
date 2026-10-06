// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views"
)

// AddMemberInput gives a user, by email address, a role in the project.
type AddMemberInput struct {
	Project string `path:"project"`
	Email   string `json:"email" validate:"required|email|max:255"`
	Role    string `json:"role" validate:"required|in:owner,member,viewer"`
}

// AddMember gives a user a role in the project, replacing the one they
// had there. The user must have an account.
func (Projects) AddMember(c *web.Ctx, in AddMemberInput) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.ManageProjects)
	if err != nil {
		return nil, err
	}
	u, err := db.Query[models.User](c).Where(models.UserCols.Email.Eq(strings.ToLower(in.Email))).First()
	if errors.Is(err, db.ErrNotFound) {
		return nil, validate.Fail("email", i18n.T(c, "members.errors.no_account"))
	}
	if err != nil {
		return nil, err
	}
	if err := setRole(c, project, u.ID, in.Role); err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "members.status.added", "name", u.Name))
	return web.RedirectRoute("projects.settings", project.Key), nil
}

// MemberInput changes a member's role.
type MemberInput struct {
	Project string `path:"project"`
	User    int64  `path:"user"`
	Role    string `json:"role" validate:"required|in:owner,member,viewer"`
}

// UpdateMember changes a member's role.
func (Projects) UpdateMember(c *web.Ctx, in MemberInput) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.ManageProjects)
	if err != nil {
		return nil, err
	}
	if err := setRole(c, project, in.User, in.Role); err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "members.status.saved"))
	return web.RedirectRoute("projects.settings", project.Key), nil
}

// MemberPath is a member of a project: /p/{project}/members/{user}.
type MemberPath struct {
	Project string `path:"project"`
	User    int64  `path:"user"`
}

// RemoveMember takes the member's roles in the project away. Their
// issues and comments stay.
func (Projects) RemoveMember(c *web.Ctx, in MemberPath) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.ManageProjects)
	if err != nil {
		return nil, err
	}
	ms, err := members(c, project)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(ms, func(m views.Member) bool { return m.User.ID == in.User }) {
		return nil, db.ErrNotFound
	}
	if err := setRole(c, project, in.User, ""); err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "members.status.removed"))
	return web.RedirectRoute("projects.settings", project.Key), nil
}

// setRole gives the user exactly the role in the project ("": none). No
// one gives more than they have, or changes the roles of someone who has
// more (rbac.AuthorizeRole, rbac.AuthorizeRolesOf), and a project keeps
// at least one owner. (Under read committed, two owners demoting each
// other at the same moment could both pass the check; a project's
// owners rarely race, so the tracker accepts it.)
func setRole(c *web.Ctx, project models.Project, userID int64, role string) error {
	scope, id := access.Project(project.Key), strconv.FormatInt(userID, 10)
	if role != "" {
		if err := rbac.AuthorizeRole(c, scope, role); err != nil {
			return err
		}
	}
	if err := rbac.AuthorizeRolesOf(c, scope, id); err != nil {
		return err
	}
	return db.Tx(c, func(ctx context.Context) error {
		var roles []string
		if role != "" {
			roles = []string{role}
		}
		if err := rbac.Sync(ctx, id, scope, roles...); err != nil {
			return err
		}
		grants, err := rbac.Assignments(ctx, scope)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(grants, func(a rbac.Assignment) bool { return a.Role == access.Owner }) {
			return validate.Fail("role", i18n.T(ctx, "members.errors.last_owner"))
		}
		return nil
	})
}

// LabelInput is a new label.
type LabelInput struct {
	Project string `path:"project"`
	Name    string `json:"label" validate:"required|max:50"`
	Color   string `json:"color" validate:"required|size:7|starts_with:#"`
}

// CreateLabel adds a label to the project.
func (Projects) CreateLabel(c *web.Ctx, in LabelInput) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.ManageProjects)
	if err != nil {
		return nil, err
	}
	if !hexColor.MatchString(in.Color) {
		return nil, validate.Fail("color", i18n.T(c, "labels.errors.color"))
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	exists, err := db.Query[models.Label](c).Where(models.LabelCols.ProjectID.Eq(project.ID), models.LabelCols.Name.Eq(name)).Exists()
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, validate.Fail("label", i18n.T(c, "labels.errors.taken"))
	}
	if err := db.Create(c, &models.Label{ProjectID: project.ID, Name: name, Color: strings.ToLower(in.Color)}); err != nil {
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "labels.status.created"))
	return web.RedirectRoute("projects.settings", project.Key), nil
}

// hexColor is a color as the color input sends it: #rrggbb.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// LabelPath is a label of a project: /p/{project}/labels/{label}.
type LabelPath struct {
	Project string `path:"project"`
	Label   int64  `path:"label"`
}

// DeleteLabel deletes a label, and takes it off its issues.
func (Projects) DeleteLabel(c *web.Ctx, in LabelPath) (web.Responder, error) {
	project, err := loadProject(c, in.Project, access.ManageProjects)
	if err != nil {
		return nil, err
	}
	n, err := db.Query[models.Label](c).Where(models.LabelCols.ID.Eq(in.Label), models.LabelCols.ProjectID.Eq(project.ID)).Delete()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, db.ErrNotFound
	}
	c.Session().Flash("status", i18n.T(c, "labels.status.deleted"))
	return web.RedirectRoute("projects.settings", project.Key), nil
}

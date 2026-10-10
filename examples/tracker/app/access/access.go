// SPDX-License-Identifier: Apache-2.0

// Package access declares who may do what (package auth/rbac): the
// permissions, and the roles a user has in a project. A project's roles
// are grants in its scope ("project:WEB"); the global admin role (from
// anetos make:admin) passes every check.
package access

import (
	"context"

	"anetos.dev/anetos/auth/rbac"
)

// The permissions, checked in a project's scope.
const (
	ViewIssues     rbac.Permission = "issues.view"     // see the project, its issues and comments
	Comment        rbac.Permission = "issues.comment"  // comment on issues
	CreateIssues   rbac.Permission = "issues.create"   // open issues, attach files
	EditIssues     rbac.Permission = "issues.edit"     // edit, close, assign and label any issue
	ManageProjects rbac.Permission = "projects.manage" // the project's settings, labels and members
)

// Permissions are all of them, for rbac.New.
var Permissions = []rbac.Permission{ViewIssues, Comment, CreateIssues, EditIssues, ManageProjects}

// The roles a project gives, from most to least.
const (
	Owner  = "owner"
	Member = "member"
	Viewer = "viewer"
)

// Roles are the roles in code: the admin (every permission, everywhere),
// and the project roles.
var Roles = []rbac.Role{
	{Name: "admin", Title: "Administrator", Super: true},
	{Name: Owner, Title: "Owner", Permissions: []rbac.Permission{ViewIssues, Comment, CreateIssues, EditIssues, ManageProjects}},
	{Name: Member, Title: "Member", Permissions: []rbac.Permission{ViewIssues, Comment, CreateIssues, EditIssues}},
	{Name: Viewer, Title: "Viewer", Permissions: []rbac.Permission{ViewIssues, Comment}},
}

// ProjectRoles are the roles a project's owners can give, in order.
var ProjectRoles = []string{Owner, Member, Viewer}

// Project is the scope of a project's grants: "project:WEB". Keys don't
// change, so the grants stay with the project.
func Project(key string) rbac.Scope { return rbac.ScopeOf("project", key) }

// Can reports whether the signed-in user has the permission in the
// project.
func Can(ctx context.Context, projectKey string, p rbac.Permission) bool {
	return rbac.CanIn(ctx, Project(projectKey), p)
}

// Authorize returns nil if the signed-in user has the permission in the
// project, else a 403 error.
func Authorize(ctx context.Context, projectKey string, p rbac.Permission) error {
	return rbac.AuthorizeIn(ctx, Project(projectKey), p)
}

// VisibleKeys returns the keys of the projects the signed-in user has a
// role in, and whether they see every project (an administrator).
func VisibleKeys(ctx context.Context) (keys []string, all bool, err error) {
	if rbac.Can(ctx, ViewIssues) { // a global grant: administrators
		return nil, true, nil
	}
	g, err := rbac.Current(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, s := range g.Scopes("project") {
		keys = append(keys, s.ID())
	}
	return keys, false, nil
}

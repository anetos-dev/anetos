// SPDX-License-Identifier: Apache-2.0

// Package rbac gives users roles and permissions, globally or in a scope
// such as a team, and checks them for the request's signed-in user.
//
// The app declares its permissions as typed constants, and the roles
// built from them, in code:
//
//	const (
//		ViewProjects   rbac.Permission = "projects.view"
//		CreateProjects rbac.Permission = "projects.create"
//	)
//
//	reg, err := rbac.ForApp(app, []rbac.Permission{ViewProjects, CreateProjects},
//		rbac.Role{Name: "owner", Permissions: []rbac.Permission{ViewProjects, CreateProjects}},
//		rbac.Role{Name: "viewer", Permissions: []rbac.Permission{ViewProjects}},
//	)
//
// Users get roles (and single permissions) in the database, from the
// tables [Migrations] creates, globally or in a [Scope]; administrators
// can add roles of their own ([CreateRole]), built from the declared
// permissions:
//
//	team := rbac.ScopeOf("team", t.ID) // "team:42"
//	err := rbac.Assign(ctx, u.AuthID(), team, "owner")
//
// Handlers and middleware check the signed-in user (package auth):
//
//	if err := rbac.AuthorizeIn(c, team, CreateProjects); err != nil {
//		return nil, err // 401 for a guest, 403 without the permission
//	}
//	r.With(rbac.RequireIn(rbac.PathScope("team", "team"), ViewProjects)).Get("/teams/{team}/projects", h.Projects)
//
// A global grant applies in every scope. A user's grants are read in one
// query (two with roles from the database) once per unit of work: a
// request, a job, a tool call. A request signed in with an API token may
// use only the permissions that are also among the token's abilities.
//
// See docs/site/guides/roles-and-permissions.md.
package rbac

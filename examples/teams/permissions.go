// SPDX-License-Identifier: Apache-2.0

package main

import "anetos.dev/anetos/auth/rbac"

// region: permissions
// The app's permissions: what a user may do. Each is also the API token
// ability that allows it.
const (
	ViewProjects   rbac.Permission = "projects.view"
	CreateProjects rbac.Permission = "projects.create"
	DeleteProjects rbac.Permission = "projects.delete"
	ManageMembers  rbac.Permission = "members.manage"
	DeleteTeams    rbac.Permission = "teams.delete"
	ManageRoles    rbac.Permission = "roles.manage"   // roles of the database
	ViewAllTeams   rbac.Permission = "teams.view-all" // support staff
)

// permissions are all of them, for rbac.ForApp.
var permissions = []rbac.Permission{ViewProjects, CreateProjects, DeleteProjects, ManageMembers, DeleteTeams, ManageRoles, ViewAllTeams}

// roles are the roles declared in code. Owners, members and guests are
// given in a team; admins and support staff globally, so they apply in
// every team.
var roles = []rbac.Role{
	{Name: "admin", Title: "Administrator", Super: true},
	{Name: "support", Title: "Support", Permissions: []rbac.Permission{ViewAllTeams, ViewProjects}},
	{Name: "owner", Title: "Owner", Permissions: []rbac.Permission{ViewProjects, CreateProjects, DeleteProjects, ManageMembers, DeleteTeams}},
	{Name: "member", Title: "Member", Permissions: []rbac.Permission{ViewProjects, CreateProjects}},
	{Name: "guest", Title: "Guest", Permissions: []rbac.Permission{ViewProjects}},
}

// teamScope is a team's scope: "team:42".
func teamScope(id int64) rbac.Scope { return rbac.ScopeOf("team", id) }

// endregion

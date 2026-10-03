// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
)

// region: test-helpers
// newUser creates a user with a role (none for "") and returns them with
// an API token having the abilities ("*" if none).
func newUser(t *testing.T, app *anetostest.App, name string, scope rbac.Scope, role string, abilities ...string) (*User, string) {
	t.Helper()
	u := &User{Name: name, Email: strings.ToLower(name) + "@example.com"}
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	if role != "" {
		if err := rbac.Assign(app.Context(), u.AuthID(), scope, role); err != nil {
			t.Fatal(err)
		}
	}
	if len(abilities) == 0 {
		abilities = []string{"*"}
	}
	a := anetos.MustResolve[*auth.Auth[*User]](app.App)
	token, _, err := a.CreateToken(app.Context(), u, "test", abilities, 0)
	if err != nil {
		t.Fatal(err)
	}
	return u, token
}

// as makes later requests with the token.
func as(app *anetostest.App, token string) *anetostest.App {
	return app.WithHeader("Authorization", "Bearer "+token)
}

// endregion

func newTeam(t *testing.T, app *anetostest.App, name string) Team {
	t.Helper()
	team := Team{Name: name}
	if err := db.Create(app.Context(), &team); err != nil {
		t.Fatal(err)
	}
	return team
}

// region: test-roles
func TestTeamRoles(t *testing.T) {
	app := anetostest.New(t, setup)
	acme, globex := newTeam(t, app, "Acme"), newTeam(t, app, "Globex")
	_, owner := newUser(t, app, "Bob", teamScope(acme.ID), "owner")
	_, guest := newUser(t, app, "Gus", teamScope(acme.ID), "guest")
	acmeProjects := fmt.Sprintf("/api/teams/%d/projects", acme.ID)

	as(app, owner).PostJSON(acmeProjects, NewProject{Name: "Rocket"}).AssertCreated()
	as(app, guest).GetJSON(acmeProjects).AssertOK().AssertJSONPath("0.name", "Rocket")
	as(app, guest).PostJSON(acmeProjects, NewProject{Name: "Anvil"}).AssertForbidden()
	// A role in Acme says nothing about Globex.
	as(app, owner).GetJSON(fmt.Sprintf("/api/teams/%d/projects", globex.ID)).AssertForbidden()
	app.WithHeader("Authorization", "").GetJSON(acmeProjects).AssertStatus(http.StatusUnauthorized)
}

// endregion

func TestGlobalRoles(t *testing.T) {
	app := anetostest.New(t, setup)
	acme := newTeam(t, app, "Acme")
	newTeam(t, app, "Globex")
	_, admin := newUser(t, app, "Ada", rbac.Global, "admin")
	_, support := newUser(t, app, "Sam", rbac.Global, "support")
	_, owner := newUser(t, app, "Bob", teamScope(acme.ID), "owner")
	acmeProjects := fmt.Sprintf("/api/teams/%d/projects", acme.ID)

	// A global role applies in every team.
	as(app, admin).PostJSON(acmeProjects, NewProject{Name: "Rocket"}).AssertCreated()
	as(app, support).GetJSON(acmeProjects).AssertOK()
	as(app, support).PostJSON(acmeProjects, NewProject{Name: "Anvil"}).AssertForbidden()
	// Administrators pass every check, even for a team that doesn't exist.
	as(app, admin).GetJSON("/api/teams/999/projects").AssertNotFound()

	// Support staff see every team; others, theirs.
	as(app, support).GetJSON("/api/teams").AssertOK().AssertJSONPath("1.name", "Globex")
	var teams []Team
	as(app, owner).GetJSON("/api/teams").AssertOK().JSON(&teams)
	if len(teams) != 1 || teams[0].ID != acme.ID {
		t.Errorf("Bob's teams: %+v", teams)
	}
	as(app, owner).GetJSON(fmt.Sprintf("/api/teams/%d/permissions", acme.ID)).
		AssertJSON([]string{"projects.view", "projects.create", "projects.delete", "members.manage", "teams.delete"})
}

func TestCreateAndDeleteTeam(t *testing.T) {
	app := anetostest.New(t, setup)
	u, token := newUser(t, app, "Bob", rbac.Global, "")
	var team Team
	as(app, token).PostJSON("/api/teams", NewTeam{Name: "Acme"}).AssertCreated().JSON(&team)
	// The creator owns it.
	g, err := rbac.Of(app.Context(), u.AuthID())
	if err != nil {
		t.Fatal(err)
	}
	if !g.HasRoleIn(teamScope(team.ID), "owner") {
		t.Fatal("Bob doesn't own the team he created")
	}
	path := fmt.Sprintf("/api/teams/%d", team.ID)
	as(app, token).PostJSON(path+"/projects", NewProject{Name: "Rocket"}).AssertCreated()
	as(app, token).Delete(path).AssertNoContent()
	anetostest.AssertDatabaseMissing[Project](app, colName.Eq("Rocket"))
	members, err := rbac.Assignments(app.Context(), teamScope(team.ID))
	if err != nil || len(members) != 0 {
		t.Errorf("the deleted team's members: %v, %v", members, err)
	}
}

// region: test-members
func TestMembers(t *testing.T) {
	app := anetostest.New(t, setup)
	acme := newTeam(t, app, "Acme")
	_, owner := newUser(t, app, "Bob", teamScope(acme.ID), "owner")
	cy, member := newUser(t, app, "Cy", teamScope(acme.ID), "member")
	dee, _ := newUser(t, app, "Dee", rbac.Global, "")
	members := fmt.Sprintf("/api/teams/%d/members", acme.ID)

	as(app, owner).PutJSON(fmt.Sprintf("%s/%d", members, dee.ID), map[string]string{"role": "guest"}).AssertNoContent()
	as(app, owner).PutJSON(fmt.Sprintf("%s/%d", members, cy.ID), map[string]string{"role": "owner"}).AssertNoContent()
	// No one gives more than they have.
	as(app, owner).PutJSON(fmt.Sprintf("%s/%d", members, dee.ID), map[string]string{"role": "admin"}).AssertForbidden()
	as(app, owner).PutJSON(fmt.Sprintf("%s/%d", members, dee.ID), map[string]string{"role": "ghost"}).AssertUnprocessable()
	// Members don't manage members.
	_, guest := newUser(t, app, "Gus", teamScope(acme.ID), "member")
	as(app, guest).GetJSON(members).AssertForbidden()

	// Cy is an owner now, Dee a guest.
	as(app, member).GetJSON(members).AssertOK().
		AssertJSONPath("1.user_id", cy.AuthID()).AssertJSONPath("1.role", "owner").
		AssertJSONPath("2.user_id", dee.AuthID()).AssertJSONPath("2.role", "guest")
	as(app, owner).Delete(fmt.Sprintf("%s/%d", members, dee.ID)).AssertNoContent()
	as(app, owner).GetJSON(members).AssertJSONPath("2.user_id", "4") // Gus; Dee left

	// A team that doesn't exist has no members, even for administrators.
	_, admin := newUser(t, app, "Ada", rbac.Global, "admin")
	as(app, admin).PutJSON(fmt.Sprintf("/api/teams/999/members/%d", dee.ID), map[string]string{"role": "owner"}).AssertNotFound()
}

func TestRecruiters(t *testing.T) {
	app := anetostest.New(t, setup)
	acme := newTeam(t, app, "Acme")
	if err := rbac.CreateRole(app.Context(), rbac.Role{Name: "recruiter", Permissions: []rbac.Permission{ManageMembers}}); err != nil {
		t.Fatal(err)
	}
	bob, _ := newUser(t, app, "Bob", teamScope(acme.ID), "owner")
	_, recruiter := newUser(t, app, "Rae", teamScope(acme.ID), "recruiter")
	dee, _ := newUser(t, app, "Dee", rbac.Global, "")
	members := fmt.Sprintf("/api/teams/%d/members", acme.ID)

	// A recruiter adds recruiters, but can't demote or remove an owner.
	as(app, recruiter).PutJSON(fmt.Sprintf("%s/%d", members, dee.ID), map[string]string{"role": "recruiter"}).AssertNoContent()
	as(app, recruiter).PutJSON(fmt.Sprintf("%s/%d", members, bob.ID), map[string]string{"role": "recruiter"}).AssertForbidden()
	as(app, recruiter).Delete(fmt.Sprintf("%s/%d", members, bob.ID)).AssertForbidden()
}

// endregion

// region: test-tokens
func TestTokenAbilities(t *testing.T) {
	app := anetostest.New(t, setup)
	acme := newTeam(t, app, "Acme")
	// Bob owns Acme, but this token may only view projects.
	_, token := newUser(t, app, "Bob", teamScope(acme.ID), "owner", "projects.view")
	projects := fmt.Sprintf("/api/teams/%d/projects", acme.ID)

	as(app, token).GetJSON(projects).AssertOK()
	as(app, token).PostJSON(projects, NewProject{Name: "Rocket"}).AssertForbidden()
}

// endregion

func TestCustomRoles(t *testing.T) {
	app := anetostest.New(t, setup)
	acme := newTeam(t, app, "Acme")
	_, admin := newUser(t, app, "Ada", rbac.Global, "admin")
	_, owner := newUser(t, app, "Bob", teamScope(acme.ID), "owner")
	dee, _ := newUser(t, app, "Dee", rbac.Global, "")

	auditor := NewRole{Name: "auditor", Title: "Auditor", Permissions: []rbac.Permission{ViewProjects, ViewAllTeams}}
	as(app, owner).PostJSON("/api/roles", auditor).AssertForbidden()
	as(app, admin).PostJSON("/api/roles", auditor).AssertCreated()
	as(app, admin).PostJSON("/api/roles", auditor).AssertStatus(http.StatusConflict)
	as(app, admin).PostJSON("/api/roles", NewRole{Name: "x", Permissions: []rbac.Permission{"ghost.walk"}}).AssertUnprocessable()
	as(app, admin).GetJSON("/api/roles").AssertJSONPath("5.name", "auditor").AssertJSONPath("5.custom", true)

	// An owner can't give the auditor role in their team: teams.view-all
	// is a global permission they don't have.
	as(app, owner).PutJSON(fmt.Sprintf("/api/teams/%d/members/%d", acme.ID, dee.ID), map[string]string{"role": "auditor"}).
		AssertForbidden()
	if err := rbac.Assign(app.Context(), dee.AuthID(), rbac.Global, "auditor"); err != nil {
		t.Fatal(err)
	}
	g, err := rbac.Of(app.Context(), dee.AuthID())
	if err != nil {
		t.Fatal(err)
	}
	if !g.CanIn(teamScope(acme.ID), ViewProjects) || g.CanIn(teamScope(acme.ID), CreateProjects) {
		t.Errorf("an auditor's permissions in Acme: %v", g.Permissions(teamScope(acme.ID)))
	}
}

func TestSeed(t *testing.T) {
	app := anetostest.New(t, setup)
	var out bytes.Buffer
	a := anetos.MustResolve[*auth.Auth[*User]](app.App)
	if err := seed(app.Context(), a, &cmd.Args{Name: "seed", Stdout: &out, Stderr: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Bob  owner    (team:1)") {
		t.Errorf("seed printed:\n%s", out.String())
	}
}

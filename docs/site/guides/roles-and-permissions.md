---
title: Roles and permissions
since: v0.3.0
---

# Roles and permissions

Give users roles, globally or in a team, and check what they may do,
with package `auth/rbac`.

## Before you start

- Set up [authentication](authentication.md): permissions are checked
  for the request's signed-in user, signed in with a session or an API
  token.
- Run the migrations of `rbac.Migrations()` (the `rbac_grants` and
  `rbac_roles` tables) with your own.

The code here comes from [`examples/teams`](../../../examples/teams), a
JSON API where users work in teams.

## Steps

### 1. Declare permissions and roles

A permission is something a user may do. Declare each as a constant of
type `rbac.Permission`, and the roles as sets of them:

```go
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
```

(Copied from [`examples/teams/permissions.go`](../../../examples/teams/permissions.go), region `permissions`.)

Permission and role names are lowercase letters, digits and `. _ : -`.
A super role (`Super: true`) has every permission. `teamScope` makes the
scope a team's roles are given in: grants apply globally (`rbac.Global`)
or in one scope, such as a team, a project or an organization.

### 2. Set them up

```go
if _, err := rbac.ForApp(app, permissions, roles...); err != nil {
	return nil, err
}
```

(Copied from [`examples/teams`](../../../examples/teams/main.go), region `setup`.)

`rbac.ForApp` checks them (a role can only hold declared permissions)
and adds the `rbac:*` commands. Pass `rbac.Migrations()` to
`migrate.ForApp` with the app's own sets:

```go
// illustrative
sets := []*migrate.Set{Migrations, auth.Migrations(), rbac.Migrations()}
```

Packages with permissions of their own, such as the
[admin](admin.md), declare them at setup with `Registry.Declare`
(idempotent, before the app serves), so roles stored in the database can
grant them. A role declared in code can only hold permissions declared
when `rbac.ForApp` runs, so list such permissions with the app's.

### 3. Give users roles

`rbac.Assign` gives a user (by `AuthID`) roles in a scope. Here, the
user who creates a team owns it:

```go
// CreateTeam creates a team, owned by the user who creates it.
func (Handlers) CreateTeam(c *web.Ctx, in NewTeam) (web.Responder, error) {
	userID, err := auth.CurrentID(c)
	if err != nil {
		return nil, err
	}
	team := Team{Name: in.Name}
	err = db.Tx(c, func(ctx context.Context) error { // the team and its owner, or neither
		if err := db.Create(ctx, &team); err != nil {
			return err
		}
		return rbac.Assign(ctx, userID, teamScope(team.ID), "owner")
	})
	if err != nil {
		return nil, err
	}
	return web.Created(team), nil
}
```

(Copied from [`examples/teams`](../../../examples/teams/main.go), region `create-team`.)

`rbac.Sync` makes a list the user's only roles in a scope (an empty list
removes them from it), `rbac.Unassign` takes one away, and
`rbac.Grant` and `rbac.Revoke` give single permissions without a role.
From the command line:

```sh
go run . rbac:assign 1 admin                  # globally
go run . rbac:assign --scope=team:42 7 member # in team 42
go run . rbac:user 7                          # what user 7 has, by scope
```

### 4. Check them on routes

`rbac.Require` lets through users with permissions globally;
`rbac.RequireIn` in the scope it finds for the request, such as
`rbac.PathScope`'s, from a path parameter. Put them after the auth
middleware:

```go
api := r.Group("/api", a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
inTeam := rbac.PathScope("team", "team")             // /teams/42/… is "team:42"

api.Get("/teams", h.Teams)
api.Post("/teams", web.H(h.CreateTeam))
api.Get("/teams/{team}/permissions", web.H(h.MyPermissions))
api.With(rbac.RequireIn(inTeam, DeleteTeams)).Delete("/teams/{team}", web.H(h.DeleteTeam))
api.With(rbac.RequireIn(inTeam, ViewProjects)).Get("/teams/{team}/projects", web.H(h.Projects))
api.With(rbac.RequireIn(inTeam, CreateProjects)).Post("/teams/{team}/projects", web.H(h.CreateProject))
api.With(rbac.RequireIn(inTeam, DeleteProjects)).Delete("/teams/{team}/projects/{id}", web.H(h.DeleteProject))

members := api.With(rbac.RequireIn(inTeam, ManageMembers))
members.Get("/teams/{team}/members", web.H(h.Members))
members.Put("/teams/{team}/members/{user}", web.H(h.SetMember))
members.Delete("/teams/{team}/members/{user}", web.H(h.RemoveMember))

api.Get("/roles", h.Roles)
api.With(rbac.Require(ManageRoles)).Post("/roles", web.H(h.CreateRole))
```

(Copied from [`examples/teams`](../../../examples/teams/main.go), region `routes`.)

A guest gets 401 and a user without the permission 403. A global grant
applies in every scope: an administrator passes every team's checks,
whether the team exists or not, so handlers still find the team (404).

### 5. Check them in handlers and pages

`rbac.Authorize` and `rbac.AuthorizeIn` return nil, `auth.ErrUnauthenticated`
(401) or `auth.ErrForbidden` (403), which a handler returns as they are;
`rbac.Can` and `rbac.CanIn` return a boolean, false for a guest, to show
or hide a button:

```go
// illustrative
if err := rbac.AuthorizeIn(c, teamScope(team.ID), DeleteProjects); err != nil {
	return nil, err
}
data["CanInvite"] = rbac.CanIn(c, teamScope(team.ID), ManageMembers)
```

`rbac.Current` returns the user's grants, read once per request, to ask
more: their roles and permissions in a scope, or their scopes of a kind,
such as their teams:

```go
// Teams lists the user's teams: those where they have a role, or every
// team for support staff.
func (Handlers) Teams(c *web.Ctx) error {
	q := db.Query[Team](c).OrderBy(colID.Asc())
	if !rbac.Can(c, ViewAllTeams) {
		g, err := rbac.Current(c)
		if err != nil {
			return err
		}
		var ids []int64
		for _, s := range g.Scopes("team") {
			if id, err := strconv.ParseInt(s.ID(), 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
		q = q.Where(colID.In(ids...))
	}
	teams, err := q.Get()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, teams)
}
```

(Copied from [`examples/teams`](../../../examples/teams/main.go), region `list-teams`.)

### 6. Let users manage members

A team's members are the users with a role in its scope:
`rbac.Assignments(ctx, scope)` lists them. Before giving a role, check
that the user giving it has every permission it allows in the scope,
with `rbac.AuthorizeRole`, so no one gives more than they have, and,
before changing or taking away a user's roles, that they could give
those too, with `rbac.AuthorizeRolesOf`, so no one demotes someone who
has more:

```go
// SetMember makes a user a member of the team with a role, replacing the
// role they had there.
func (Handlers) SetMember(c *web.Ctx, in SetMemberInput) (web.Responder, error) {
	// Grants in a team that doesn't exist would apply to the next team
	// given its ID.
	if _, err := findTeam(c, in.Team); err != nil {
		return nil, err
	}
	u, err := db.Find[User](c, in.User)
	if err != nil {
		return nil, err
	}
	team := teamScope(in.Team)
	// No one gives more than they have, or changes the roles of someone
	// who has more: an owner can make owners, not administrators.
	if err := rbac.AuthorizeRole(c, team, in.Role); err != nil {
		return nil, err
	}
	if err := rbac.AuthorizeRolesOf(c, team, u.AuthID()); err != nil {
		return nil, err
	}
	if err := rbac.Sync(c, u.AuthID(), team, in.Role); err != nil {
		return nil, err
	}
	return web.NoContent(), nil
}
```

(Copied from [`examples/teams`](../../../examples/teams/main.go), region `set-member`.)

Give roles only in scopes that exist: a grant in team 7 before team 7
exists would apply to the team that later gets that ID. For the same
reason, when a team is deleted, `rbac.RemoveScope` takes every role in it
away (also when a cascade deletes it); when a user is, `rbac.RemoveUser`.

`rbac.AuthorizeRole` compares permissions, not names: a role with no
permissions is anyone's to give. Gate actions with permissions
(`rbac.Authorize`) rather than role names (`rbac.HasRole`).

### 7. Let administrators add roles

Roles declared in code change with the code. Administrators can add
their own, stored in the database and made of the declared permissions:

```go
// CreateRole adds a role made of the app's permissions: an "auditor" who
// may view every team's projects, say.
func (Handlers) CreateRole(c *web.Ctx, in NewRole) (web.Responder, error) {
	role := rbac.Role{Name: in.Name, Title: in.Title, Permissions: in.Permissions}
	if err := rbac.CreateRole(c, role); err != nil {
		return nil, err // 422 for an unknown permission, 409 for a name taken
	}
	return web.Created(role), nil
}
```

(Copied from [`examples/teams`](../../../examples/teams/main.go), region `create-role`.)

`rbac.UpdateRole` changes one, `rbac.DeleteRole` deletes it and takes it
from everyone, and `rbac.Roles` lists them all, after the code's.

## How it works

A grant is a role or a permission of a user in a scope, a row of
`rbac_grants`. A user's grants are read in one query the first time a
request (or a job, or a tool call) checks them, and kept for the rest of
it; changes made with the package's functions are seen at once. A
request signed in with an API token may use only the permissions among
the token's abilities, so a token for reading can't delete, whatever
its user's roles. See [Roles and permissions](../concepts/roles-and-permissions.md).

> **Coming from Laravel?** This is spatie/laravel-permission's model:
> `$user->assignRole('writer')` is `rbac.Assign(ctx, id, rbac.Global,
> "writer")`, `$user->can('edit articles')` is `rbac.Can(ctx,
> EditArticles)`, `role:`/`permission:` middleware is `rbac.Require`, and
> teams are scopes. Permissions are constants in code rather than rows:
> a misspelled constant doesn't compile, and checking a permission that
> isn't declared is an error.

## Testing it

Give users roles in the test's database, then make requests as them.
`examples/teams` signs in with API tokens:

```go
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
```

(Copied from [`examples/teams/main_test.go`](../../../examples/teams/main_test.go), region `test-helpers`.)

```go
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
```

(Copied from [`examples/teams/main_test.go`](../../../examples/teams/main_test.go), region `test-roles`.)

A token's abilities narrow its user's permissions:

```go
func TestTokenAbilities(t *testing.T) {
	app := anetostest.New(t, setup)
	acme := newTeam(t, app, "Acme")
	// Bob owns Acme, but this token may only view projects.
	_, token := newUser(t, app, "Bob", teamScope(acme.ID), "owner", "projects.view")
	projects := fmt.Sprintf("/api/teams/%d/projects", acme.ID)

	as(app, token).GetJSON(projects).AssertOK()
	as(app, token).PostJSON(projects, NewProject{Name: "Rocket"}).AssertForbidden()
}
```

(Copied from [`examples/teams/main_test.go`](../../../examples/teams/main_test.go), region `test-tokens`.)

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `rbac: permission "…" isn't declared` (500) | A check of a permission not passed to `rbac.ForApp` | Add it to the permissions |
| `rbac: no permissions in the context` | `rbac.ForApp` wasn't called, or the context isn't the app's | Call it in setup; use the request's or the job's context |
| Every check returns 401 | The route lacks the auth middleware | Put `a.Middleware` or `a.TokenMiddleware` before `rbac.Require` |
| A user's role allows nothing | The role is no longer declared, or was deleted | `rbac:roles` lists such roles; declare it again, or `rbac:unassign` it |
| An API client gets 403 though its user has the role | The token lacks the permission's ability | Create the token with the permission's name among its abilities, or `"*"` |
| A team's owner can't give a role | They don't have every permission it allows there (`AuthorizeRole`) | Give that role from an account that has them |
| A new team has members already | Roles were given in its scope before it existed, or a deleted team's weren't removed | Check that the team exists before giving roles; call `rbac.RemoveScope` when deleting it |

## Next steps

- [Authorization](authorization.md): typed policies, for rules about
  the thing acted on ("authors edit their own posts")
- [Add an admin panel](admin.md): pages for staff, with a permission per
  resource and action
- [Roles and permissions reference](../reference/auth.md#roles-and-permissions)
- [API tokens](authentication.md#7-give-api-clients-tokens)

// SPDX-License-Identifier: Apache-2.0

// Command teams is a JSON API where users work in teams, with roles in
// each team (owner, member, guest) and across all of them (admin,
// support), and roles administrators add: package auth/rbac. Users sign
// in with API tokens; seed creates some.
//
//	anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080
//	go run . migrate
//	go run . seed
//	go run .
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/web"
)

// Handlers serves the API.
type Handlers struct{}

// TeamPath reads {team} from the path.
type TeamPath struct {
	Team int64 `path:"team"`
}

// findTeam returns the team, or db.ErrNotFound (404): administrators pass
// every team's checks, whether it exists or not.
func findTeam(c *web.Ctx, id int64) (Team, error) { return db.Find[Team](c, id) }

// region: list-teams
// Teams lists the user's teams: those where they have a role, or every
// team for support staff.
func (Handlers) Teams(c *web.Ctx, _ struct{}) ([]Team, error) {
	q := db.Query[Team](c).OrderBy(colID.Asc())
	if !rbac.Can(c, ViewAllTeams) {
		g, err := rbac.Current(c)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for _, s := range g.Scopes("team") {
			if id, err := strconv.ParseInt(s.ID(), 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
		q = q.Where(colID.In(ids...))
	}
	return q.Get()
}

// endregion

// NewTeam is a team to create.
type NewTeam struct {
	Name string `json:"name" validate:"required|max:255"`
}

// region: create-team
// CreateTeam creates a team, owned by the user who creates it.
func (Handlers) CreateTeam(c *web.Ctx, in NewTeam) (Team, error) {
	userID, err := auth.CurrentID(c)
	if err != nil {
		return Team{}, err
	}
	team := Team{Name: in.Name}
	err = db.Tx(c, func(ctx context.Context) error { // the team and its owner, or neither
		if err := db.Create(ctx, &team); err != nil {
			return err
		}
		return rbac.Assign(ctx, userID, teamScope(team.ID), "owner")
	})
	if err != nil {
		return Team{}, err
	}
	return team, nil
}

// endregion

// region: delete-team
// DeleteTeam deletes a team, its projects, and every role in it.
func (Handlers) DeleteTeam(c *web.Ctx, in TeamPath) (web.Empty, error) {
	team, err := findTeam(c, in.Team)
	if err != nil {
		return web.Empty{}, err
	}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := db.Delete(ctx, &team); err != nil { // projects: ON DELETE CASCADE
			return err
		}
		return rbac.RemoveScope(ctx, teamScope(team.ID))
	})
	if err != nil {
		return web.Empty{}, err
	}
	return web.Empty{}, nil
}

// endregion

// MyPermissions returns what the user may do in the team, for a front end
// to show or hide its buttons.
func (Handlers) MyPermissions(c *web.Ctx, in TeamPath) ([]rbac.Permission, error) {
	g, err := rbac.Current(c)
	if err != nil {
		return nil, err
	}
	perms := g.Permissions(teamScope(in.Team))
	if perms == nil {
		perms = []rbac.Permission{}
	}
	return perms, nil
}

// Projects lists a team's projects.
func (Handlers) Projects(c *web.Ctx, in TeamPath) ([]Project, error) {
	if _, err := findTeam(c, in.Team); err != nil {
		return nil, err
	}
	projects, err := db.Query[Project](c).Where(colTeamID.Eq(in.Team)).OrderBy(colName.Asc()).Get()
	if err != nil {
		return nil, err
	}
	return projects, nil
}

// NewProject is a project to create in a team.
type NewProject struct {
	Team int64  `path:"team"`
	Name string `json:"name" validate:"required|max:255"`
}

// CreateProject creates a project in a team.
func (Handlers) CreateProject(c *web.Ctx, in NewProject) (Project, error) {
	if _, err := findTeam(c, in.Team); err != nil {
		return Project{}, err
	}
	p := Project{TeamID: in.Team, Name: in.Name}
	if err := db.Create(c, &p); err != nil {
		return Project{}, err
	}
	return p, nil
}

// ProjectPath reads {team} and {id} from the path.
type ProjectPath struct {
	Team int64 `path:"team"`
	ID   int64 `path:"id"`
}

// DeleteProject deletes a team's project.
func (Handlers) DeleteProject(c *web.Ctx, in ProjectPath) (web.Empty, error) {
	// The team's project only: a permission in one team says nothing
	// about another's projects.
	n, err := db.Query[Project](c).Where(colTeamID.Eq(in.Team), colID.Eq(in.ID)).Delete()
	if err != nil {
		return web.Empty{}, err
	}
	if n == 0 {
		return web.Empty{}, db.ErrNotFound
	}
	return web.Empty{}, nil
}

// Members lists a team's members and their roles.
func (Handlers) Members(c *web.Ctx, in TeamPath) ([]rbac.Assignment, error) {
	if _, err := findTeam(c, in.Team); err != nil {
		return nil, err
	}
	members, err := rbac.Assignments(c, teamScope(in.Team))
	if err != nil {
		return nil, err
	}
	return members, nil
}

// SetMemberInput gives a user a role in a team.
type SetMemberInput struct {
	Team int64  `path:"team"`
	User int64  `path:"user"`
	Role string `json:"role" validate:"required"`
}

// region: set-member
// SetMember makes a user a member of the team with a role, replacing the
// role they had there.
func (Handlers) SetMember(c *web.Ctx, in SetMemberInput) (web.Empty, error) {
	// Grants in a team that doesn't exist would apply to the next team
	// given its ID.
	if _, err := findTeam(c, in.Team); err != nil {
		return web.Empty{}, err
	}
	u, err := db.Find[User](c, in.User)
	if err != nil {
		return web.Empty{}, err
	}
	team := teamScope(in.Team)
	// No one gives more than they have, or changes the roles of someone
	// who has more: an owner can make owners, not administrators.
	if err := rbac.AuthorizeRole(c, team, in.Role); err != nil {
		return web.Empty{}, err
	}
	if err := rbac.AuthorizeRolesOf(c, team, u.AuthID()); err != nil {
		return web.Empty{}, err
	}
	if err := rbac.Sync(c, u.AuthID(), team, in.Role); err != nil {
		return web.Empty{}, err
	}
	return web.Empty{}, nil
}

// endregion

// MemberPath reads {team} and {user} from the path.
type MemberPath struct {
	Team int64 `path:"team"`
	User int64 `path:"user"`
}

// RemoveMember takes the user's roles in the team away.
func (Handlers) RemoveMember(c *web.Ctx, in MemberPath) (web.Empty, error) {
	if _, err := findTeam(c, in.Team); err != nil {
		return web.Empty{}, err
	}
	userID, team := strconv.FormatInt(in.User, 10), teamScope(in.Team)
	if err := rbac.AuthorizeRolesOf(c, team, userID); err != nil {
		return web.Empty{}, err
	}
	if err := rbac.Sync(c, userID, team); err != nil {
		return web.Empty{}, err
	}
	return web.Empty{}, nil
}

// Roles lists every role, declared in code or added by administrators.
func (Handlers) Roles(c *web.Ctx, _ struct{}) ([]rbac.Role, error) {
	return rbac.Roles(c)
}

// NewRole is a role an administrator adds.
type NewRole struct {
	Name        string            `json:"name" validate:"required|max:100"`
	Title       string            `json:"title" validate:"max:255"`
	Permissions []rbac.Permission `json:"permissions"`
}

// region: create-role
// CreateRole adds a role made of the app's permissions: an "auditor" who
// may view every team's projects, say.
func (Handlers) CreateRole(c *web.Ctx, in NewRole) (rbac.Role, error) {
	role := rbac.Role{Name: in.Name, Title: in.Title, Permissions: in.Permissions}
	if err := rbac.CreateRole(c, role); err != nil {
		return rbac.Role{}, err // 422 for an unknown permission, 409 for a name taken
	}
	return role, nil
}

// endregion

// setup connects the database and adds the cache, auth, roles and
// permissions, the migrations, the server and the routes. Tests call it
// too.
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	sets := []*migrate.Set{Migrations, auth.Migrations(), rbac.Migrations()}
	if _, err := migrate.ForApp(app, sets); err != nil {
		return nil, err
	}
	if _, err := cache.ForApp(app); err != nil { // auth's login throttling
		return nil, err
	}
	a, err := auth.ForApp(app, users)
	if err != nil {
		return nil, err
	}
	// region: setup
	if _, err := rbac.ForApp(app, permissions, roles...); err != nil {
		return nil, err
	}
	// endregion
	app.Command("seed", "Create users, a team and API tokens to try the API with", func(ctx context.Context, args *cmd.Args) error {
		return seed(ctx, a, args)
	})
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	routes(srv.Router(), a)
	return srv, nil
}

func routes(r *web.Router, a *auth.Auth[*User]) {
	var h Handlers
	// region: routes
	api := r.Group("/api", a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
	inTeam := rbac.PathScope("team", "team")             // /teams/42/… is "team:42"

	api.Get("/teams", web.H(h.Teams))
	api.Post("/teams", web.H(h.CreateTeam)).Status(http.StatusCreated)
	api.Get("/teams/{team}/permissions", web.H(h.MyPermissions))
	api.With(rbac.RequireIn(inTeam, DeleteTeams)).Delete("/teams/{team}", web.H(h.DeleteTeam))
	api.With(rbac.RequireIn(inTeam, ViewProjects)).Get("/teams/{team}/projects", web.H(h.Projects))
	api.With(rbac.RequireIn(inTeam, CreateProjects)).Post("/teams/{team}/projects", web.H(h.CreateProject)).Status(http.StatusCreated)
	api.With(rbac.RequireIn(inTeam, DeleteProjects)).Delete("/teams/{team}/projects/{id}", web.H(h.DeleteProject))

	members := api.With(rbac.RequireIn(inTeam, ManageMembers))
	members.Get("/teams/{team}/members", web.H(h.Members))
	members.Put("/teams/{team}/members/{user}", web.H(h.SetMember))
	members.Delete("/teams/{team}/members/{user}", web.H(h.RemoveMember))

	api.Get("/roles", web.H(h.Roles))
	api.With(rbac.Require(ManageRoles)).Post("/roles", web.H(h.CreateRole)).Status(http.StatusCreated)
	// endregion
}

// seed creates an administrator, a support agent, a team with an owner
// and a member, and an API token for each user.
func seed(ctx context.Context, a *auth.Auth[*User], args *cmd.Args) error {
	acme := Team{Name: "Acme"}
	if err := db.Create(ctx, &acme); err != nil {
		return err
	}
	people := []struct {
		user  User
		scope rbac.Scope
		role  string
	}{
		{User{Name: "Ada", Email: "ada@example.com"}, rbac.Global, "admin"},
		{User{Name: "Sam", Email: "sam@example.com"}, rbac.Global, "support"},
		{User{Name: "Bob", Email: "bob@example.com"}, teamScope(acme.ID), "owner"},
		{User{Name: "Cy", Email: "cy@example.com"}, teamScope(acme.ID), "member"},
	}
	for _, p := range people {
		u := p.user
		if err := db.Create(ctx, &u); err != nil {
			return err
		}
		if err := rbac.Assign(ctx, u.AuthID(), p.scope, p.role); err != nil {
			return err
		}
		token, _, err := a.CreateToken(ctx, &u, "seed", []string{"*"}, 0)
		if err != nil {
			return err
		}
		fmt.Fprintf(args.Stdout, "%-4s %-8s (%s)  %s\n", u.Name, p.role, p.scope, token)
	}
	return nil
}

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute()
}

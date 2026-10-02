// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/encryption"
)

func init() {
	extra = append(extra, test{"RBAC", testRBAC})
}

const (
	stView   rbac.Permission = "projects.view"
	stCreate rbac.Permission = "projects.create"
	stDelete rbac.Permission = "projects.delete"
	stManage rbac.Permission = "members.manage"
	stUsers  rbac.Permission = "users.manage"
)

var stPerms = []rbac.Permission{stView, stCreate, stDelete, stManage, stUsers}

var stRoles = []rbac.Role{
	{Name: "admin", Super: true},
	{Name: "owner", Permissions: []rbac.Permission{stView, stCreate, stDelete, stManage}},
	{Name: "editor", Permissions: []rbac.Permission{stView, stCreate}},
	{Name: "viewer", Permissions: []rbac.Permission{stView}},
}

// testRBAC gives roles and permissions in the tables rbac.Migrations
// creates, and checks them.
func testRBAC(t *testing.T, ctx context.Context) {
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{auth.Migrations(), rbac.Migrations()}, migrate.WithTable("st_rbac_migrations"))
	check(t, err)
	_, err = r.Up(ctx)
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		for _, table := range []string{"rbac_grants", "rbac_roles", "api_tokens", "st_rbac_migrations"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
		}
	})
	reg, err := rbac.New(stPerms, stRoles...)
	check(t, err)
	ctx = rbac.WithRegistry(ctx, reg)
	acme, globex := rbac.ScopeOf("team", 1), rbac.ScopeOf("team", 2)

	// Roles, globally and in teams; assigning twice keeps one.
	check(t, rbac.Assign(ctx, "ada", acme, "owner"))
	check(t, rbac.Assign(ctx, "ada", acme, "owner"))
	check(t, rbac.Assign(ctx, "bob", acme, "viewer", "editor"))
	check(t, rbac.Assign(ctx, "bob", globex, "viewer"))
	check(t, rbac.Assign(ctx, "root", rbac.Global, "admin"))
	check(t, rbac.Grant(ctx, "cy", globex, stDelete))
	check(t, rbac.Grant(ctx, "cy", rbac.Global, stView))

	can := func(user string, scope rbac.Scope, p rbac.Permission) bool {
		t.Helper()
		g, err := rbac.Of(ctx, user)
		check(t, err)
		return g.CanIn(scope, p)
	}
	for _, c := range []struct {
		user  string
		scope rbac.Scope
		p     rbac.Permission
		want  bool
	}{
		{"ada", acme, stManage, true},
		{"ada", globex, stView, false}, // a team's role, in another team
		{"ada", rbac.Global, stView, false},
		{"bob", acme, stCreate, true},    // editor
		{"bob", globex, stCreate, false}, // only a viewer there
		{"root", acme, stDelete, true},   // a global super role, everywhere
		{"root", rbac.Global, stUsers, true},
		{"cy", globex, stDelete, true}, // a permission given directly
		{"cy", acme, stView, true},     // a global permission
		{"cy", acme, stDelete, false},
		{"nobody", rbac.Global, stView, false},
	} {
		if got := can(c.user, c.scope, c.p); got != c.want {
			t.Errorf("%s CanIn(%s, %s) = %v, want %v", c.user, c.scope, c.p, got, c.want)
		}
	}
	g, err := rbac.Of(ctx, "bob")
	check(t, err)
	if got := g.Roles(acme); !slices.Equal(got, []string{"editor", "viewer"}) {
		t.Errorf("bob's roles in acme: %v", got)
	}
	if got := g.Scopes("team"); !slices.Equal(got, []rbac.Scope{acme, globex}) {
		t.Errorf("bob's teams: %v", got)
	}
	if got := g.Permissions(acme); !slices.Equal(got, []rbac.Permission{stView, stCreate}) {
		t.Errorf("bob's permissions in acme: %v", got)
	}
	if !g.HasRoleIn(acme, "editor") || g.HasRoleIn(globex, "editor") || g.HasRole("viewer") {
		t.Error("bob's roles")
	}

	got, err := rbac.Assignments(ctx, acme)
	check(t, err)
	if want := []rbac.Assignment{{UserID: "ada", Role: "owner"}, {UserID: "bob", Role: "editor"}, {UserID: "bob", Role: "viewer"}}; !slices.Equal(got, want) {
		t.Errorf("Assignments(acme) = %v, want %v", got, want)
	}
	users, err := rbac.UsersWith(ctx, acme, stCreate)
	check(t, err)
	if want := []string{"ada", "bob", "root"}; !slices.Equal(users, want) {
		t.Errorf("UsersWith(acme, create) = %v, want %v", users, want)
	}
	users, err = rbac.UsersWith(ctx, globex, stView)
	check(t, err)
	if want := []string{"bob", "cy", "root"}; !slices.Equal(users, want) {
		t.Errorf("UsersWith(globex, view) = %v, want %v", users, want)
	}

	// Sync replaces a scope's roles; Unassign and Revoke take some away.
	check(t, rbac.Sync(ctx, "bob", acme, "viewer"))
	if can("bob", acme, stCreate) || !can("bob", acme, stView) || !can("bob", globex, stView) {
		t.Error("after Sync, bob is a viewer of acme, and still of globex")
	}
	check(t, rbac.Unassign(ctx, "bob", globex, "viewer"))
	check(t, rbac.Revoke(ctx, "cy", globex, stDelete))
	if can("bob", globex, stView) || can("cy", globex, stDelete) || !can("cy", globex, stView) {
		t.Error("Unassign and Revoke")
	}

	// Unknown roles and permissions, and invalid scopes, are refused.
	if err := rbac.Assign(ctx, "bob", acme, "ghost"); !errors.Is(err, rbac.ErrUnknownRole) {
		t.Errorf("unknown role: %v", err)
	}
	if err := rbac.Grant(ctx, "bob", acme, "ghost.walk"); !errors.Is(err, rbac.ErrUnknownPermission) {
		t.Errorf("unknown permission: %v", err)
	}
	if err := rbac.Assign(ctx, "bob", "team", "viewer"); !errors.Is(err, rbac.ErrInvalidScope) {
		t.Errorf("invalid scope: %v", err)
	}

	// Roles of the database.
	check(t, rbac.CreateRole(ctx, rbac.Role{Name: "auditor", Title: "Auditor", Permissions: []rbac.Permission{stView, stUsers}}))
	if err := rbac.CreateRole(ctx, rbac.Role{Name: "auditor"}); !errors.Is(err, rbac.ErrRoleExists) {
		t.Errorf("a second auditor: %v", err)
	}
	for _, bad := range []rbac.Role{{Name: "owner"}, {Name: "Bad Name"}, {Name: "god", Super: true}} {
		if err := rbac.CreateRole(ctx, bad); !errors.Is(err, rbac.ErrInvalidRole) {
			t.Errorf("CreateRole(%+v): %v", bad, err)
		}
	}
	if err := rbac.CreateRole(ctx, rbac.Role{Name: "x", Permissions: []rbac.Permission{"ghost.walk"}}); !errors.Is(err, rbac.ErrUnknownPermission) {
		t.Errorf("a role with an unknown permission: %v", err)
	}
	check(t, rbac.Assign(ctx, "dee", acme, "auditor"))
	if !can("dee", acme, stUsers) || can("dee", acme, stCreate) {
		t.Error("auditor's permissions")
	}
	check(t, rbac.UpdateRole(ctx, rbac.Role{Name: "auditor", Title: "Auditor", Permissions: []rbac.Permission{stView}}))
	if can("dee", acme, stUsers) || !can("dee", acme, stView) {
		t.Error("auditor's permissions after UpdateRole")
	}
	if users, err := rbac.UsersWith(ctx, acme, stView); err != nil || !slices.Contains(users, "dee") {
		t.Errorf("UsersWith a custom role: %v, %v", users, err)
	}
	all, err := rbac.Roles(ctx)
	check(t, err)
	if len(all) != 5 || all[4].Name != "auditor" || !all[4].Custom || all[0].Custom {
		t.Errorf("Roles: %+v", all)
	}
	if role, err := rbac.FindRole(ctx, "auditor"); err != nil || role.DisplayName() != "Auditor" {
		t.Errorf("FindRole: %+v, %v", role, err)
	}
	if err := rbac.UpdateRole(ctx, rbac.Role{Name: "ghost"}); !errors.Is(err, rbac.ErrUnknownRole) {
		t.Errorf("UpdateRole(ghost): %v", err)
	}
	check(t, rbac.DeleteRole(ctx, "auditor"))
	if can("dee", acme, stView) {
		t.Error("a deleted role still allows")
	}
	if n, err := db.Query[stGrant](ctx).Where(db.Col[string]("user_id").Eq("dee")).Count(); err != nil || n != 0 {
		t.Errorf("DeleteRole left %d grants (%v)", n, err)
	}
	if err := rbac.DeleteRole(ctx, "owner"); !errors.Is(err, rbac.ErrInvalidRole) {
		t.Errorf("DeleteRole(owner): %v", err)
	}

	// A role no longer declared allows nothing, and rbac:roles warns.
	check(t, db.Create(ctx, &stGrant{UserID: "eve", Kind: "role", Name: "retired"}))
	check(t, db.Create(ctx, &stGrant{UserID: "eve", Scope: "team:9", Kind: "role", Name: "retired"}))
	if g, err := rbac.Of(ctx, "eve"); err != nil || g.Can(stView) || len(g.Roles(rbac.Global)) != 0 || g.HasRole("retired") || len(g.Scopes("team")) != 0 {
		t.Errorf("a retired role: %v", err)
	}
	// A role of the database created later with its name doesn't revive it.
	check(t, rbac.CreateRole(ctx, rbac.Role{Name: "retired", Permissions: []rbac.Permission{stView}}))
	if can("eve", rbac.Global, stView) {
		t.Error("CreateRole revived a retired role's assignments")
	}
	check(t, rbac.DeleteRole(ctx, "retired"))
	check(t, db.Create(ctx, &stGrant{UserID: "eve", Kind: "role", Name: "retired"}))

	// IDs and scopes compare exactly, on every database (MySQL's text
	// collations ignore case, accents and trailing spaces).
	check(t, rbac.Assign(ctx, "Kim", rbac.ScopeOf("org", "ABC"), "owner"))
	check(t, rbac.Assign(ctx, "kim", rbac.ScopeOf("org", "abc"), "viewer"))
	check(t, rbac.Assign(ctx, "kim", rbac.ScopeOf("org", "e"), "viewer"))
	for _, c := range []struct {
		user  string
		scope rbac.Scope
		p     rbac.Permission
		want  bool
	}{
		{"Kim", rbac.ScopeOf("org", "ABC"), stManage, true},
		{"kim", rbac.ScopeOf("org", "ABC"), stView, false},
		{"Kim", rbac.ScopeOf("org", "abc"), stView, false},
		{"kim", rbac.ScopeOf("org", "abc"), stManage, false},
		{"kim", rbac.ScopeOf("org", "é"), stView, false},
		{"kim", rbac.ScopeOf("org", "e "), stView, false},
		{"kim ", rbac.ScopeOf("org", "e"), stView, false},
	} {
		if got := can(c.user, c.scope, c.p); got != c.want {
			t.Errorf("%q CanIn(%q, %s) = %v, want %v", c.user, c.scope, c.p, got, c.want)
		}
	}
	if got, err := rbac.Assignments(ctx, rbac.ScopeOf("org", "abc")); err != nil || len(got) != 1 || got[0].UserID != "kim" {
		t.Errorf("Assignments(org:abc) = %v, %v", got, err)
	}
	if got, err := rbac.UsersWith(ctx, rbac.ScopeOf("org", "é"), stView); err != nil || slices.Contains(got, "kim") {
		t.Errorf("UsersWith(org:é) = %v, %v", got, err)
	}
	check(t, rbac.RemoveScope(ctx, rbac.ScopeOf("org", "ABC")))
	if !can("kim", rbac.ScopeOf("org", "abc"), stView) {
		t.Error("RemoveScope(org:ABC) removed org:abc's grants")
	}
	check(t, rbac.RemoveUser(ctx, "kim"))
	check(t, rbac.RemoveUser(ctx, "Kim"))

	testRBACRequests(t, ctx, reg, acme)
	testRBACUnits(t, ctx, acme)

	// RemoveScope and RemoveUser.
	check(t, rbac.RemoveScope(ctx, acme))
	if can("ada", acme, stView) || !can("root", acme, stView) {
		t.Error("after RemoveScope, only global grants apply")
	}
	if err := rbac.RemoveScope(ctx, rbac.Global); !errors.Is(err, rbac.ErrInvalidScope) {
		t.Errorf("RemoveScope(Global): %v", err)
	}
	check(t, rbac.RemoveUser(ctx, "root"))
	if can("root", acme, stView) {
		t.Error("after RemoveUser")
	}

	// Rolling the migration back drops the tables.
	_, err = r.Rollback(ctx, 1)
	check(t, err)
	if _, err := db.Exec(ctx, "SELECT 1 FROM rbac_grants"); err == nil {
		t.Error("rbac_grants survives the rollback")
	}
}

// stGrant is a row of rbac_grants, written behind the package's back.
type stGrant struct {
	db.Model
	UserID string `db:"user_id"`
	Scope  string `db:"scope"`
	Kind   string `db:"kind"`
	Name   string `db:"name"`
}

func (stGrant) TableName() string { return "rbac_grants" }

// testRBACRequests checks the signed-in user's permissions, through API
// tokens, the middleware and the commands.
func testRBACRequests(t *testing.T, ctx context.Context, reg *rbac.Registry, acme rbac.Scope) {
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.New(k)
	cfg, err := auth.LoadConfig(nil)
	check(t, err)
	a, err := auth.New(cfg, auth.Users[stAuthUser]{
		ByID:    func(_ context.Context, id string) (stAuthUser, error) { return stAuthUser{id}, nil },
		ByLogin: func(context.Context, string) (stAuthUser, error) { return stAuthUser{}, auth.ErrNoUser },
	}, enc)
	check(t, err)
	full, _, err := a.CreateToken(ctx, stAuthUser{"ada"}, "full", []string{"*"}, 0)
	check(t, err)
	viewOnly, _, err := a.CreateToken(ctx, stAuthUser{"ada"}, "view", []string{string(stView)}, 0)
	check(t, err)

	var canView, canCreate, owner bool
	var authErr error
	h := a.TokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := r.Context()
		canView, canCreate, owner = rbac.CanIn(c, acme, stView), rbac.CanIn(c, acme, stCreate), rbac.HasRoleIn(c, acme, "owner")
		authErr = rbac.AuthorizeIn(c, acme, stManage)
	}))
	gated := a.TokenMiddleware(rbac.RequireIn(rbac.PathScope("team", "team"), stCreate)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	mux := http.NewServeMux()
	mux.Handle("/teams/{team}/projects", gated)
	call := func(h http.Handler, path, token string) int {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	call(h, "/", full)
	if !canView || !canCreate || !owner || authErr != nil {
		t.Errorf("a full token: view %v, create %v, owner %v, %v", canView, canCreate, owner, authErr)
	}
	call(h, "/", viewOnly)
	if !canView || canCreate || owner || !errors.Is(authErr, auth.ErrForbidden) {
		t.Errorf("a view-only token: view %v, create %v, owner %v, %v", canView, canCreate, owner, authErr)
	}
	call(h, "/", "")
	if canView || !errors.Is(authErr, auth.ErrUnauthenticated) {
		t.Errorf("a guest: view %v, %v", canView, authErr)
	}
	for _, c := range []struct {
		path, token string
		want        int
	}{
		{"/teams/1/projects", full, http.StatusOK},
		{"/teams/1/projects", viewOnly, http.StatusForbidden},
		{"/teams/2/projects", full, http.StatusForbidden},
		{"/teams/1/projects", "", http.StatusUnauthorized},
	} {
		if got := call(mux, c.path, c.token); got != c.want {
			t.Errorf("GET %s with token %q: %d, want %d", c.path, c.token, got, c.want)
		}
	}

	// AuthorizeRole: an owner may give owner and viewer, not admin.
	var giveOwner, giveAdmin error
	call(a.TokenMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		giveOwner, giveAdmin = rbac.AuthorizeRole(r.Context(), acme, "owner"), rbac.AuthorizeRole(r.Context(), acme, "admin")
	})), "/", full)
	if giveOwner != nil || !errors.Is(giveAdmin, auth.ErrForbidden) {
		t.Errorf("AuthorizeRole: owner %v, admin %v", giveOwner, giveAdmin)
	}
	// Roles of the database too; an empty role is anyone's to give; changing
	// an owner's roles needs what an owner has.
	check(t, rbac.CreateRole(ctx, rbac.Role{Name: "lead", Permissions: []rbac.Permission{stView, stUsers}}))
	check(t, rbac.CreateRole(ctx, rbac.Role{Name: "empty"}))
	check(t, rbac.Assign(ctx, "hal", acme, "owner"))
	var giveLead, giveEmpty, overHal, overNobody error
	gives := a.TokenMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		c := r.Context()
		giveLead, giveEmpty = rbac.AuthorizeRole(c, acme, "lead"), rbac.AuthorizeRole(c, acme, "empty")
		overHal, overNobody = rbac.AuthorizeRolesOf(c, acme, "hal"), rbac.AuthorizeRolesOf(c, acme, "nobody")
	}))
	call(gives, "/", full)
	if !errors.Is(giveLead, auth.ErrForbidden) || giveEmpty != nil || overHal != nil || overNobody != nil {
		t.Errorf("ada: lead %v, empty %v, over hal %v, over nobody %v", giveLead, giveEmpty, overHal, overNobody)
	}
	call(gives, "/", viewOnly) // the token limits what ada may give
	if !errors.Is(overHal, auth.ErrForbidden) || giveEmpty != nil {
		t.Errorf("a view-only token: over hal %v, empty %v", overHal, giveEmpty)
	}
	check(t, rbac.Assign(ctx, "root2", rbac.Global, "admin"))
	root2, _, err := a.CreateToken(ctx, stAuthUser{"root2"}, "view", []string{string(stView), string(stUsers)}, 0)
	check(t, err)
	var giveSuper, giveLead2 error
	var rootRoles []string
	call(a.TokenMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		giveSuper, giveLead2 = rbac.AuthorizeRole(r.Context(), acme, "admin"), rbac.AuthorizeRole(r.Context(), acme, "lead")
		g, _ := rbac.Current(r.Context())
		rootRoles = g.Roles(rbac.Global)
	})), "/", root2)
	if !errors.Is(giveSuper, auth.ErrForbidden) || giveLead2 != nil || rootRoles != nil {
		t.Errorf("an admin's limited token: super %v, lead %v, roles %v", giveSuper, giveLead2, rootRoles)
	}
	check(t, rbac.DeleteRole(ctx, "lead"))
	check(t, rbac.DeleteRole(ctx, "empty"))
	check(t, rbac.RemoveUser(ctx, "hal"))
	check(t, rbac.RemoveUser(ctx, "root2"))

	// The commands.
	app, err := anetos.New(anetos.WithSource(config.Map{}), anetos.WithLogOutput(io.Discard))
	check(t, err)
	_, err = rbac.ForApp(app, stPerms, stRoles...)
	check(t, err)
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		c := slices.IndexFunc(app.Commands(), func(c cmd.Command) bool { return c.Name == args[0] })
		if c < 0 {
			t.Fatalf("no command %s", args[0])
		}
		check(t, app.Commands()[c].Run(rbac.WithRegistry(ctx, reg), &cmd.Args{Name: args[0], Args: args[1:], Stdout: &out, Stderr: &out}))
		return out.String()
	}
	if out := run("rbac:assign", "--scope=team:1", "fay", "viewer"); !strings.Contains(out, "fay has role viewer (team:1)") {
		t.Errorf("rbac:assign: %q", out)
	}
	if out := run("rbac:user", "fay"); !strings.Contains(out, "team:1") || !strings.Contains(out, "projects.view") {
		t.Errorf("rbac:user: %q", out)
	}
	if out := run("rbac:roles"); !strings.Contains(out, "viewer") || !strings.Contains(out, `role "retired"`) {
		t.Errorf("rbac:roles: %q", out)
	}
	if out := run("rbac:unassign", "--scope=team:1", "fay", "viewer"); !strings.Contains(out, "no longer") {
		t.Errorf("rbac:unassign: %q", out)
	}
}

// testRBACUnits checks that grants are read once per unit of work.
func testRBACUnits(t *testing.T, ctx context.Context, acme rbac.Scope) {
	app, err := anetos.New(anetos.WithSource(config.Map{}), anetos.WithLogOutput(io.Discard))
	check(t, err)
	_, err = rbac.ForApp(app, stPerms, stRoles...)
	check(t, err)
	unit, end := app.StartUnit(ctx, anetos.Unit{Kind: "job", Name: "test"})
	defer end()
	can := func(ctx context.Context) bool {
		g, err := rbac.Of(ctx, "gus")
		check(t, err)
		return g.CanIn(acme, stView)
	}
	if can(unit) {
		t.Fatal("gus can view")
	}
	// A change behind the package's back isn't seen until the next unit…
	check(t, db.Create(ctx, &stGrant{UserID: "gus", Scope: "team:1", Kind: "permission", Name: "projects.view"}))
	if can(unit) {
		t.Error("the unit read the grants again")
	}
	next, endNext := app.StartUnit(ctx, anetos.Unit{Kind: "job", Name: "test"})
	defer endNext()
	if !can(next) {
		t.Error("the next unit doesn't see the grant")
	}
	// …but its own changes are.
	check(t, rbac.Revoke(unit, "gus", acme, stView))
	if can(unit) {
		t.Error("the unit doesn't see its own change")
	}

	// What a transaction read isn't kept if it rolls back.
	errRollback := errors.New("rollback")
	err = db.Tx(unit, func(ctx context.Context) error {
		check(t, rbac.Grant(ctx, "gus", acme, stView))
		if !can(ctx) {
			t.Error("the transaction doesn't see its grant")
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	if can(unit) {
		t.Error("a rolled-back grant is kept")
	}
	// What it read is kept once it commits.
	check(t, db.Tx(unit, func(ctx context.Context) error {
		check(t, rbac.Grant(ctx, "gus", acme, stView))
		return nil
	}))
	if !can(unit) {
		t.Error("a committed grant isn't seen")
	}
	check(t, rbac.Revoke(unit, "gus", acme, stView))
}

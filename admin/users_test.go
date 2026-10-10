// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
)

type UserForm struct {
	Name  string `json:"name" validate:"required|max:100"`
	Email string `json:"email" validate:"required|email"`
}

// mail records the emails the users resource sends.
type mail struct {
	mu   sync.Mutex
	sent []string
}

func (m *mail) send(kind string) func(context.Context, *User) error {
	return func(_ context.Context, u *User) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.sent = append(m.sent, kind+" "+u.Email)
		return nil
	}
}

func usersApp(t *testing.T) (*anetostest.App, *mail) {
	t.Helper()
	m := &mail{}
	app := anetostest.New(t, setupWith(func(p *Panel) error {
		a := anetos.MustResolve[*auth.Auth[*User]](p.app)
		err := Users(p, Resource[User, UserForm]{
			Name:        "users",
			Columns:     []Column[User]{TextColumn[User]("name", "Name"), TextColumn[User]("email", "Email")},
			Search:      []string{"name", "email"},
			RecordTitle: func(u User) string { return u.Name },
			Edit:        func(u User) UserForm { return UserForm{Name: u.Name, Email: u.Email} },
			Apply: func(_ context.Context, in UserForm, u *User) error {
				u.Name, u.Email = in.Name, in.Email
				return nil
			},
		}, Accounts[*User]{
			Auth: a, DisabledAt: "disabled_at", VerifiedAt: "email_verified_at",
			SendVerification: m.send("verify"), SendPasswordReset: m.send("reset"),
		})
		if err != nil {
			return err
		}
		return Roles(p)
	}))
	// A support role, made in the database from the admin's permissions.
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(rbac.CreateRole(app.Context(), rbac.Role{Name: "support", Title: "Support", Permissions: slices.Concat(
		[]rbac.Permission{Access, "admin.users.impersonate", AssignRoles}, PermissionsOf("users", "view", "update"))}))
	check(rbac.CreateRole(app.Context(), rbac.Role{Name: "roler", Permissions: slices.Concat(
		[]rbac.Permission{Access}, PermissionsOf("roles"))}))
	return app, m
}

// person creates a user with roles (none: no role).
func person(t *testing.T, app *anetostest.App, name string, roles ...string) *User {
	t.Helper()
	u := &User{Name: name, Email: strings.ToLower(name) + "@example.com", Password: testPassword}
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	if len(roles) > 0 {
		if err := rbac.Assign(app.Context(), u.AuthID(), rbac.Global, roles...); err != nil {
			t.Fatal(err)
		}
	}
	return u
}

func as(app *anetostest.App, u *User) *anetostest.App {
	app.PostForm("/test/login/"+u.AuthID(), nil).AssertNoContent()
	return app
}

func reload(t *testing.T, app *anetostest.App, u *User) *User {
	t.Helper()
	got, err := db.Find[User](app.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &got
}

// events returns the audit log's actions about a user, oldest first, with
// who did them ("ada" or "ada as bob").
func events(t *testing.T, app *anetostest.App, u *User) []string {
	t.Helper()
	entries, err := db.Query[audit.Entry](app.Context()).Where(db.C("subject_type").Eq("users"), db.C("subject_id").Eq(u.AuthID())).
		OrderBy(db.C("id").Asc()).Get()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Action == audit.Created {
			continue // the test made them
		}
		who := e.ActorID
		if e.ActingAs != "" {
			who += " as " + e.ActingAs
		}
		out = append(out, e.Action+" by "+who)
	}
	return out
}

func userURL(u *User, suffix string) string { return "/admin/users/" + u.AuthID() + suffix }

func TestUserAccounts(t *testing.T) {
	app, m := usersApp(t)
	ada, bob := person(t, app, "Ada", "admin"), person(t, app, "Bob")
	as(app, ada)

	app.Get(userURL(bob, "")).AssertOK().AssertSee("Account", "Active", "Not verified", "Roles and permissions", "API tokens",
		">Disable<", ">Mark email verified<", ">Send verification email<", ">Send password reset<", ">Log out everywhere<", ">Impersonate<")
	app.Get("/admin/users?status=active&email=unverified").AssertSee("Bob")
	app.Get("/admin/users?status=disabled").AssertDontSee(">Bob<")

	app.PostForm(userURL(bob, "/actions/disable"), nil).AssertRedirect(userURL(bob, "")).Follow().
		AssertSee("Account disabled.", "Disabled on", ">Enable<").AssertDontSee(">Impersonate<")
	if reload(t, app, bob).DisabledAt == nil {
		t.Error("not disabled")
	}
	app.Get("/admin/users?status=disabled").AssertSee("Bob")
	app.PostForm(userURL(bob, "/actions/enable"), nil).Follow().AssertSee("Account enabled.")
	app.PostForm(userURL(bob, "/actions/send-verification"), nil).Follow().AssertSee("Verification email sent.")
	app.PostForm(userURL(bob, "/actions/verify"), nil).Follow().AssertSee("Email address marked as verified.", "Verified on").
		AssertDontSee(">Send verification email<")
	app.PostForm(userURL(bob, "/actions/send-password-reset"), nil).Follow().AssertSee("Password reset email sent.")
	if fmt.Sprint(m.sent) != "[verify bob@example.com reset bob@example.com]" {
		t.Errorf("sent %v", m.sent)
	}
	app.PostForm(userURL(bob, "/actions/logout"), nil).Follow().AssertSee("Logged out of every browser and device.")
	if reload(t, app, bob).SessionKey == "" {
		t.Error("no new session key")
	}

	// API tokens.
	a := anetos.MustResolve[*auth.Auth[*User]](app.App)
	_, tok, err := a.CreateToken(app.Context(), bob, "CLI", []string{"posts.read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.CreateToken(app.Context(), bob, "CI", []string{"*"}, 0); err != nil {
		t.Fatal(err)
	}
	app.Get(userURL(bob, "")).AssertSee("CLI", "posts.read", "CI", "Revoke all")
	app.PostForm(userURL(bob, fmt.Sprintf("/tokens/%d/revoke", tok.ID)), nil).Follow().AssertSee("API token revoked.").AssertDontSee("posts.read")
	app.PostForm(userURL(bob, "/tokens/revoke-all"), nil).Follow().AssertSee("Every API token revoked.", "No API tokens.")

	// Roles and permissions.
	app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"support"}}).Follow().AssertSee("Role support given.")
	app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"support"}, "scope": {"team:7"}}).Follow().AssertSee("Role support given in team:7.")
	app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"support"}, "scope": {"team"}}).Follow().AssertSee("invalid scope")
	app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"ghost"}}).Follow().AssertSee("unknown role")
	page := app.Get(userURL(bob, "")).AssertSee("Support", "team:7", "Everywhere").Text()
	if !strings.Contains(page, `<option value="admin">`) {
		t.Error("the admin role isn't offered to an admin")
	}
	app.PostForm(userURL(bob, "/roles/remove"), url.Values{"role": {"support"}, "scope": {"team:7"}}).Follow().AssertSee("Role support taken away in team:7.")
	if err := rbac.Grant(app.Context(), bob.AuthID(), rbac.Global, "admin.users.view"); err != nil {
		t.Fatal(err)
	}
	app.Get(userURL(bob, "")).AssertSee("admin.users.view")
	app.PostForm(userURL(bob, "/permissions/revoke"), url.Values{"permission": {"admin.users.view"}}).Follow().
		AssertSee("Permission admin.users.view taken away.")

	// Everything is in the log, by Ada.
	got := events(t, app, bob)
	for _, want := range []string{"user.disabled by " + ada.AuthID(), "user.enabled by " + ada.AuthID(), "user.verification_sent by " + ada.AuthID(),
		"user.verified by " + ada.AuthID(), "user.password_reset_sent by " + ada.AuthID(), "user.logged_out_everywhere by " + ada.AuthID(),
		"user.token_revoked by " + ada.AuthID(), "user.tokens_revoked by " + ada.AuthID(), "rbac.role_assigned by " + ada.AuthID(),
		"rbac.role_removed by " + ada.AuthID(), "rbac.permission_revoked by " + ada.AuthID()} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in %v", want, got)
		}
	}

	// Deleting for good takes the roles and tokens too.
	if _, _, err := a.CreateToken(app.Context(), bob, "CLI", []string{"*"}, 0); err != nil {
		t.Fatal(err)
	}
	app.PostForm(userURL(bob, "/delete"), nil).AssertRedirect("/admin/users")
	if given, _ := rbac.GivenTo(app.Context(), bob.AuthID()); len(given) != 0 {
		t.Errorf("roles left: %v", given)
	}
	if n, _ := db.Query[auth.Token](app.Context()).Where(db.C("user_id").Eq(bob.AuthID())).Count(); n != 0 {
		t.Errorf("%d tokens left", n)
	}
}

func TestWhoManagesWhom(t *testing.T) {
	app, _ := usersApp(t)
	ada, sue, bob := person(t, app, "Ada", "admin"), person(t, app, "Sue", "support"), person(t, app, "Bob")

	// Oneself: not disabled, deleted, impersonated or given roles.
	as(app, ada)
	app.Get(userURL(ada, "")).AssertOK().AssertDontSee(">Disable<", ">Impersonate<", ">Give role<", `value="delete"`)
	app.PostForm(userURL(ada, "/actions/disable"), nil).Follow().AssertSee("You can&#39;t disable your own account.")
	app.PostForm(userURL(ada, "/delete"), nil).Follow().AssertSee("You can&#39;t delete your own account here.")
	app.PostForm(userURL(ada, "/roles"), url.Values{"role": {"support"}}).Follow().AssertSee("You can&#39;t change your own roles.")
	app.PostForm("/admin/users/bulk", url.Values{"action": {"delete"}, "ids": {ada.AuthID(), bob.AuthID()}}).Follow().
		AssertSee("Deleted: 1 of 2.", "Some were left")
	if reload(t, app, ada) == nil {
		t.Fatal("ada deleted")
	}
	bob = person(t, app, "Bob")

	// Support manages plain users, not administrators.
	as(app, sue)
	app.Get(userURL(bob, "")).AssertOK().AssertSee(">Disable<", ">Impersonate<", ">Edit<")
	app.Get(userURL(ada, "")).AssertOK().AssertDontSee(">Disable<", ">Impersonate<")
	app.PostForm(userURL(ada, "/actions/disable"), nil).Follow().AssertSee("You may not manage Ada: they have permissions you don&#39;t.")
	if reload(t, app, ada).DisabledAt != nil {
		t.Fatal("support disabled an admin")
	}
	app.PostForm(userURL(ada, "/actions/impersonate"), nil).Follow().AssertSee("You may not manage Ada")
	// Roles: only those support could give.
	page := app.Get(userURL(bob, "")).Text()
	if strings.Contains(page, `<option value="admin">`) || !strings.Contains(page, `<option value="support">`) {
		t.Error("support is offered the admin role, or not its own")
	}
	app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"admin"}}).Follow().AssertSee("You may not give the role admin")
	app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"support"}}).Follow().AssertSee("Role support given.")
	// Without roles.assign, no role changes at all.
	app.PostForm(userURL(bob, "/actions/disable"), nil).Follow().AssertSee("Account disabled.")
	as(app, ada)
	if err := rbac.Assign(app.Context(), sue.AuthID(), rbac.Global, "roler"); err != nil {
		t.Fatal(err)
	}
	if err := rbac.Unassign(app.Context(), sue.AuthID(), rbac.Global, "support"); err != nil {
		t.Fatal(err)
	}
	as(app, sue)
	app.Get("/admin/users").AssertForbidden()
	app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"roler"}}).AssertForbidden()
}

func TestImpersonation(t *testing.T) {
	app, _ := usersApp(t)
	ada, bob := person(t, app, "Ada", "admin"), person(t, app, "Bob")
	as(app, ada)
	app.Get("/test/banner").AssertOK().AssertDontSee("impersonating")

	app.PostForm(userURL(bob, "/actions/impersonate"), nil).AssertRedirect("/")
	app.Get("/test/banner").AssertSee("You're impersonating <strong>Bob</strong> (logged in as Ada).", `action="/admin/impersonation/stop"`)
	app.Get("/admin").AssertForbidden() // Bob has no access
	app.PostForm("/test/rename", url.Values{"name": {"Robert"}}).AssertNoContent()

	app.PostForm("/admin/impersonation/stop", nil).AssertRedirect(userURL(bob, "")).Follow().AssertSee("You&#39;re yourself again.", "Robert")
	app.Get("/test/banner").AssertDontSee("impersonating")
	app.PostForm("/admin/impersonation/stop", nil).AssertRedirect("/admin")

	got := events(t, app, bob)
	want := []string{"user.impersonated by " + ada.AuthID(), "updated by " + ada.AuthID() + " as user:" + bob.AuthID(), "user.impersonation_ended by " + ada.AuthID()}
	if !slices.Equal(got, want) {
		t.Errorf("events %v, want %v", got, want)
	}
	// While impersonating someone, the admin's own pages show the banner too,
	// and no one else can be impersonated until it stops.
	carl := person(t, app, "Carl")
	// Logging back in forgot the confirmed password.
	app.PostForm(userURL(bob, "/actions/impersonate"), nil).AssertRedirect("/admin/confirm?back=%2Fadmin%2Fusers%2F" + bob.AuthID())
	app.PostForm("/admin/confirm", url.Values{"password": {"secret"}, "back": {userURL(bob, "")}}).AssertRedirect(userURL(bob, ""))
	app.PostForm(userURL(bob, "/actions/impersonate"), nil).AssertRedirect("/")
	if err := rbac.Assign(app.Context(), bob.AuthID(), rbac.Global, "support"); err != nil {
		t.Fatal(err)
	}
	app.Get("/admin").AssertOK().AssertSee(`class="anetos-acting"`, "Stop impersonating Robert")
	app.Get(userURL(carl, "")).AssertOK().AssertDontSee(">Impersonate<")
	app.PostForm(userURL(carl, "/actions/impersonate"), nil).Follow().AssertSee("You&#39;re impersonating someone already")
	if got := events(t, app, carl); len(got) != 0 {
		t.Errorf("events of carl: %v", got)
	}
}

func TestNoImpersonationAtAHost(t *testing.T) {
	app := anetostest.New(t, setupWith(func(p *Panel) error {
		a := anetos.MustResolve[*auth.Auth[*User]](p.app)
		return Users(p, Resource[User, struct{}]{Columns: []Column[User]{TextColumn[User]("name", "Name")}}, Accounts[*User]{Auth: a})
	}), anetostest.Env(map[string]string{"ADMIN_HOST": "admin.example.com"}))
	p := anetos.MustResolve[*Panel](app.App)
	r := p.byName["users"].(*res[User, struct{}])
	if slices.ContainsFunc(r.Actions, func(a Action[User]) bool { return a.Name == "impersonate" }) {
		t.Error("impersonating a user is offered at a host of the admin's own")
	}
}

func TestRolesEdgeCases(t *testing.T) {
	app, _ := usersApp(t)
	ada, bob := person(t, app, "Ada", "admin"), person(t, app, "Bob", "support")
	as(app, ada)
	// Taking away what they don't have, exactly, is refused.
	app.PostForm(userURL(bob, "/roles/remove"), url.Values{"role": {"SUPPORT"}}).Follow().AssertSee("Bob doesn&#39;t have the role SUPPORT.")
	app.PostForm(userURL(bob, "/permissions/revoke"), url.Values{"permission": {"admin.access"}}).Follow().
		AssertSee("Bob doesn&#39;t have the permission admin.access.")
	if given, _ := rbac.GivenTo(app.Context(), bob.AuthID()); len(given) != 1 {
		t.Errorf("given %v", given)
	}
	// A stored role with a permission no longer declared: saved without it.
	if err := rbac.CreateRole(app.Context(), rbac.Role{Name: "old", Permissions: []rbac.Permission{Access}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(app.Context(), `UPDATE rbac_roles SET permissions = '["admin.access","gone.perm"]' WHERE name = 'old'`); err != nil {
		t.Fatal(err)
	}
	app.PostForm("/admin/roles/old", url.Values{"permissions": {"admin.access", "admin.posts.view"}}).Follow().AssertSee("Saved.")
	if role, _ := rbac.FindRole(app.Context(), "old"); !slices.Equal(role.Permissions, []rbac.Permission{Access, "admin.posts.view"}) {
		t.Errorf("old = %v", role.Permissions)
	}
	// "new" is the page that makes roles.
	app.PostForm("/admin/roles", url.Values{"name": {"new"}}).AssertOK().AssertSee("the name new is the admin")
}

func TestRolesPages(t *testing.T) {
	app, _ := usersApp(t)
	ada, rae := person(t, app, "Ada", "admin"), person(t, app, "Rae", "roler")
	person(t, app, "Sue", "support")
	as(app, ada)
	app.Get("/admin/roles").AssertOK().AssertSee("admin", "Code", "Support", "The database", "All (super)", "5 permissions")
	app.Get("/admin/roles/admin").AssertOK().AssertSee("Every permission", "Ada").AssertDontSee(">Edit<")
	app.Get("/admin/roles/support").AssertOK().AssertSee("admin.users.view", "Sue", ">Edit<")
	app.Get("/admin/roles/ghost").AssertNotFound()

	app.Get("/admin/roles/new").AssertOK().AssertSee(`value="admin.posts.view"`)
	app.PostForm("/admin/roles", url.Values{"name": {"Bad Name"}}).AssertOK().AssertSee("invalid role name")
	app.PostForm("/admin/roles", url.Values{"name": {"admin"}}).AssertOK().AssertSee("declared in code")
	app.PostForm("/admin/roles", url.Values{"name": {"editor"}, "title": {"Editor"}, "permissions": {"admin.access", "admin.posts.view", "nope.perm"}}).
		AssertRedirect("/admin/roles/editor").Follow().AssertSee("Role editor created.", "admin.posts.view")
	role, err := rbac.FindRole(app.Context(), "editor")
	if err != nil || !slices.Equal(role.Permissions, []rbac.Permission{Access, "admin.posts.view"}) {
		t.Errorf("editor = %+v, %v", role, err)
	}
	app.PostForm("/admin/roles/editor", url.Values{"title": {"Editors"}, "permissions": {"admin.posts.view", "admin.posts.update"}}).
		Follow().AssertSee("Saved.", "admin.posts.update")
	app.PostForm("/admin/roles/admin", url.Values{"title": {"x"}}).Follow().AssertSee("declared in the app&#39;s code")

	// Someone who manages roles without the users' permissions: they
	// make roles from their own, and leave others' alone.
	as(app, rae)
	app.Get("/admin/roles/new").AssertOK().AssertDontSee(`value="admin.posts.view"`).AssertSee(`value="admin.roles.view"`)
	app.PostForm("/admin/roles", url.Values{"name": {"helper"}, "permissions": {"admin.access", "admin.users.view"}}).Follow().AssertSee("Role helper created.")
	if role, _ := rbac.FindRole(app.Context(), "helper"); !slices.Equal(role.Permissions, []rbac.Permission{Access}) {
		t.Errorf("helper = %+v", role)
	}
	app.Get("/admin/roles/support").AssertOK().AssertDontSee(">Edit<", `value="delete"`)
	app.PostForm("/admin/roles/support", url.Values{"title": {"x"}}).Follow().AssertSee("This role has permissions you don&#39;t")
	app.PostForm("/admin/roles/support/delete", nil).Follow().AssertSee("This role has permissions you don&#39;t")
	app.PostForm("/admin/roles/helper/delete", nil).AssertRedirect("/admin/roles").Follow().AssertSee("Role helper deleted.")

	// Editing a role keeps the permissions one can't give.
	as(app, ada)
	if err := rbac.UpdateRole(app.Context(), rbac.Role{Name: "editor", Permissions: []rbac.Permission{Access, "admin.posts.view", "admin.users.view"}}); err != nil {
		t.Fatal(err)
	}
	if err := rbac.Assign(app.Context(), rae.AuthID(), rbac.Global, "editor"); err != nil {
		t.Fatal(err)
	}
	as(app, rae)
	// Rae has admin.posts.view and admin.users.view through editor now,
	// so may edit it; what she can't give stays.
	app.PostForm("/admin/roles/editor", url.Values{"permissions": {"admin.posts.view"}}).Follow().AssertSee("Saved.")
	if role, _ := rbac.FindRole(app.Context(), "editor"); !slices.Equal(role.Permissions, []rbac.Permission{"admin.posts.view"}) {
		t.Errorf("editor = %+v", role.Permissions)
	}

	var actions []string
	entries, _ := db.Query[audit.Entry](app.Context()).Where(db.C("subject_type").Eq("rbac_roles")).OrderBy(db.C("id").Asc()).Get()
	for _, e := range entries {
		actions = append(actions, e.Action+" "+e.SubjectID)
	}
	if want := []string{"rbac.role_created editor", "rbac.role_updated editor", "rbac.role_created helper", "rbac.role_deleted helper", "rbac.role_updated editor"}; !slices.Equal(actions, want) {
		t.Errorf("logged %v, want %v", actions, want)
	}
	_ = http.StatusOK
}

func TestUsersErrors(t *testing.T) {
	app := anetostest.New(t, setup)
	p := anetos.MustResolve[*Panel](app.App)
	a := anetos.MustResolve[*auth.Auth[*User]](app.App)
	q := &Panel{byName: map[string]resource{}, reg: p.reg}
	r := Resource[User, struct{}]{Columns: []Column[User]{TextColumn[User]("name", "Name")}}
	for name, acc := range map[string]Accounts[*User]{
		"no auth":     {},
		"no column":   {Auth: a, DisabledAt: "gone_at"},
		"not a time":  {Auth: a, VerifiedAt: "name"},
		"not refused": {Auth: anetos.MustResolve[*auth.Auth[*User]](app.App), DisabledAt: "email_verified_at"},
	} {
		if err := Users(q, r, acc); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := Roles(&Panel{}); err == nil {
		t.Error("Roles without rbac")
	}
}

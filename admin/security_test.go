// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
)

// unconfirmed signs in a new user with perms who hasn't confirmed their
// password.
func unconfirmed(t *testing.T, app *anetostest.App, name string, perms ...string) *User {
	t.Helper()
	u := &User{Name: name, Password: testPassword}
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	if len(perms) == 1 && perms[0] == "admin" {
		if err := rbac.Assign(app.Context(), u.AuthID(), rbac.Global, "admin"); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, p := range perms {
			if err := rbac.Grant(app.Context(), u.AuthID(), rbac.Global, rbac.Permission(p)); err != nil {
				t.Fatal(err)
			}
		}
	}
	app.PostForm("/test/login/"+u.AuthID()+"?confirm=no", nil).AssertNoContent()
	return u
}

func TestConfirmBeforeDanger(t *testing.T) {
	app := anetostest.New(t, setup)
	unconfirmed(t, app, "Ada", "admin")
	p := newPost(t, app, "Doomed", "draft")

	// Deleting asks for the password first, then goes back.
	app.WithHeader("Referer", "http://example.test"+postURL(p, "")).PostForm(postURL(p, "/delete"), nil).
		AssertRedirect("/admin/confirm?back="+url.QueryEscape(postURL(p, ""))).Follow().
		AssertSee("Confirm your password", "Confirm your password to go on.", `name="back" value="`+postURL(p, "")+`"`)
	app.PostForm("/admin/confirm", url.Values{"password": {"wrong"}, "back": {postURL(p, "")}}).
		AssertStatus(422).AssertSee("That isn&#39;t your password.")
	app.PostForm("/admin/confirm", url.Values{"password": {"secret"}, "back": {postURL(p, "")}}).
		AssertRedirect(postURL(p, "")).Follow().AssertSee("Password confirmed: go on.")
	app.PostForm(postURL(p, "/delete"), nil).AssertRedirect("/admin/posts")

	// Only the admin's pages are gone back to.
	for _, back := range []string{"//evil.example/admin", "https://evil.example/admin", "/elsewhere", "/admin/confirm?back=/admin/posts", "/\t/evil"} {
		app.PostForm("/admin/confirm", url.Values{"password": {"secret"}, "back": {back}}).AssertRedirect("/admin")
	}
	app.Get("/admin/confirm?back=https://evil.example").AssertSee(`name="back" value="/admin"`)
}

func TestConfirmBulkAndActions(t *testing.T) {
	app := anetostest.New(t, setup)
	p := newPost(t, app, "One", "draft")
	// Without the permission, 403 before any password.
	unconfirmed(t, app, "Vera", string(Access), "admin.posts.view")
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"delete"}, "ids": {fmt.Sprint(p.ID)}}).AssertForbidden()
	unconfirmed(t, app, "Ada", "admin")
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"delete"}, "ids": {fmt.Sprint(p.ID)}}).
		AssertRedirect("/admin/confirm?back=%2Fadmin%2Fposts")
	// Not dangerous: no password.
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"feature"}, "ids": {fmt.Sprint(p.ID)}}).AssertRedirect("/admin/posts")
	app.PostForm(postURL(p, "/actions/publish"), nil).AssertRedirect(postURL(p, ""))
}

func TestConfirmOff(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ADMIN_CONFIRM": "false"}))
	unconfirmed(t, app, "Ada", "admin")
	p := newPost(t, app, "Doomed", "draft")
	app.PostForm(postURL(p, "/delete"), nil).AssertRedirect("/admin/posts")
}

func TestConfirmWithoutPassword(t *testing.T) {
	app := anetostest.New(t, setup)
	u := &User{Name: "Ada"} // signs in another way
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := rbac.Assign(app.Context(), u.AuthID(), rbac.Global, "admin"); err != nil {
		t.Fatal(err)
	}
	app.PostForm("/test/login/"+u.AuthID(), nil).AssertNoContent()
	p := newPost(t, app, "Doomed", "draft")
	app.PostForm(postURL(p, "/delete"), nil).AssertRedirect("/admin/confirm?back=%2Fadmin")
	app.Get("/admin/confirm").AssertOK().AssertSee("Your account has no password (you sign in another way)")
}

func TestReservedNames(t *testing.T) {
	anetostest.New(t, setupWith(func(p *Panel) error {
		for _, name := range []string{"confirm", "two-factor-required", "impersonation"} {
			if err := p.add(&activityRes{p: p, in: resInfo{Name: name, Title: "X"}}); err == nil || !strings.Contains(err.Error(), "admin's own") {
				t.Errorf("%s: %v", name, err)
			}
		}
		return nil
	}))
}

// turnOnTwoFactor turns on u's two-factor sign-in.
func turnOnTwoFactor(t *testing.T, app *anetostest.App, u *User) {
	t.Helper()
	a := anetos.MustResolve[*auth.Auth[*User]](app.App)
	setup, err := a.StartTwoFactor(app.Context(), u, u.Name)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TwoFactorCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmTwoFactor(app.Context(), u, code); err != nil {
		t.Fatal(err)
	}
}

func TestTwoFactorRequired(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ADMIN_TWO_FACTOR": "required"}))
	ada := signIn(t, app, "Ada", "admin")
	app.Get("/admin/posts").AssertRedirect("/admin/two-factor-required").Follow().AssertStatus(403).
		AssertSee("Two-factor sign-in required", `href="/two-factor"`)
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"feature"}}).AssertForbidden()
	turnOnTwoFactor(t, app, ada)
	app.Get("/admin/posts").AssertRedirect("/login") // turning it on signed out the sessions without a code
	app.PostForm("/test/login/"+ada.AuthID(), nil).AssertNoContent()
	app.Get("/admin/posts").AssertOK()
	app.Get("/admin/two-factor-required").AssertRedirect("/admin")
	// Without the admin's permission: 403 as ever.
	signIn(t, app, "Mallory")
	app.Get("/admin/two-factor-required").AssertForbidden()
}

func TestAllowIPs(t *testing.T) {
	// The test client's address is 192.0.2.1.
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ADMIN_ALLOW_IPS": "10.0.0.0/8, 2001:db8::1"}))
	signIn(t, app, "Ada", "admin")
	app.Get("/admin").AssertNotFound()
	app.Get("/admin/_assets/admin.css").AssertNotFound()
	app.PostForm("/admin/impersonation/stop", nil).AssertNotFound()

	app = anetostest.New(t, setup, anetostest.Env(map[string]string{"ADMIN_ALLOW_IPS": "10.0.0.1,192.0.2.0/24"}))
	signIn(t, app, "Ada", "admin")
	app.Get("/admin").AssertOK()
}

func TestSecurityConfig(t *testing.T) {
	for _, bad := range []config.Map{{"ADMIN_TWO_FACTOR": "always"}, {"ADMIN_ALLOW_IPS": "10.0.0.0/8,nope"}} {
		if _, err := config.Get[Config](bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if got, err := parseAllowed([]string{"10.1.2.3/8", "::ffff:192.0.2.1", " "}); err != nil || len(got) != 2 ||
		got[0].String() != "10.0.0.0/8" || got[1].String() != "192.0.2.1/32" {
		t.Errorf("parseAllowed: %v, %v", got, err)
	}
}

func TestTwoFactorOffForAUser(t *testing.T) {
	app, _ := usersApp(t)
	ada, bob := person(t, app, "Ada", "admin"), person(t, app, "Bob")
	turnOnTwoFactor(t, app, bob)
	turnOnTwoFactor(t, app, ada)
	as(app, ada)
	app.Get(userURL(bob, "")).AssertSee("Two-factor sign-in", "On, 8 recovery codes left", ">Turn off two-factor sign-in<")
	app.PostForm(userURL(bob, "/actions/two-factor-off"), nil).AssertRedirect(userURL(bob, "")).Follow().
		AssertSee("Two-factor sign-in turned off.", "<dd>Off</dd>").AssertDontSee(">Turn off two-factor sign-in<")
	if got := events(t, app, bob); len(got) != 1 || !strings.HasPrefix(got[0], "user.two_factor_disabled") {
		t.Errorf("events %v", got)
	}
	app.PostForm(userURL(ada, "/actions/two-factor-off"), nil).Follow().AssertSee("Turn off your own two-factor sign-in")
}

// Every dangerous action asks first, after the permission is checked.
func TestConfirmEverywhere(t *testing.T) {
	app, _ := usersApp(t)
	bob := person(t, app, "Bob")
	ada := person(t, app, "Ada", "admin")
	app.PostForm("/test/login/"+ada.AuthID()+"?confirm=no", nil).AssertNoContent()
	ask := func(r *anetostest.Response) {
		t.Helper()
		r.AssertStatus(303)
		if loc := r.Header.Get("Location"); !strings.HasPrefix(loc, "/admin/confirm?back=") {
			t.Errorf("%s %s: redirect to %s", r.Request.Method, r.Request.URL, loc)
		}
	}
	ask(app.Get("/admin/roles/new"))
	ask(app.Get("/admin/roles/support/edit"))
	ask(app.PostForm("/admin/roles", url.Values{"name": {"x"}}))
	ask(app.PostForm("/admin/roles/support", url.Values{"title": {"x"}}))
	ask(app.PostForm("/admin/roles/support/delete", nil))
	ask(app.PostForm(userURL(bob, "/roles"), url.Values{"role": {"support"}}))
	ask(app.PostForm(userURL(bob, "/roles/remove"), url.Values{"role": {"support"}}))
	ask(app.PostForm(userURL(bob, "/permissions/revoke"), url.Values{"permission": {"admin.access"}}))
	ask(app.PostForm(userURL(bob, "/actions/disable"), nil))
	ask(app.PostForm(userURL(bob, "/actions/impersonate"), nil))
	ask(app.PostForm(userURL(bob, "/delete"), nil))
	// The GET form comes back to itself.
	app.Get("/admin/roles/new").AssertRedirect("/admin/confirm?back=%2Fadmin%2Froles%2Fnew")
	if reload(t, app, bob).DisabledAt != nil {
		t.Error("disabled without the password")
	}

	ops := opsApp(t, make(chan string, 1), nil)
	unconfirmed(t, ops, "Ada", "admin")
	ask(ops.PostForm("/admin/jobs/failed/flush", nil))
	p := newPost(t, ops, "Doomed", "draft")
	if err := db.Delete(ops.Context(), &p); err != nil {
		t.Fatal(err)
	}
	ask(ops.PostForm(postURL(p, "/force-delete"), nil))
}

func TestLocalBack(t *testing.T) {
	app := anetostest.New(t, setup)
	p := anetos.MustResolve[*Panel](app.App)
	for in, want := range map[string]string{
		"/admin/posts?x=1": "/admin/posts?x=1", "/admin": "/admin", "/admin/": "/admin/",
		"/admin/../x": "/admin", "/admin/..\\..\\evil.example": "/admin", "/adminx": "/admin",
		"/admin/./posts": "/admin", "/admin//posts": "/admin", "//evil.example": "/admin",
	} {
		if got := p.local(in, "/admin"); got != want {
			t.Errorf("local(%q) = %q, want %q", in, got, want)
		}
	}
}

// At a host of its own, the admin links to the app's two-factor page on
// APP_URL; the confirmation page is behind the requirement too.
func TestTwoFactorRequiredAtAHost(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ADMIN_TWO_FACTOR": "required",
		"ADMIN_HOST": "admin.example.com", "APP_URL": "https://example.com"}))
	signIn(t, app, "Ada", "admin")
	get := func(path string) *anetostest.Response {
		req, err := http.NewRequest(http.MethodGet, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "admin.example.com"
		return app.Do(req)
	}
	get("/confirm").AssertRedirect("/two-factor-required")
	get("/two-factor-required").AssertStatus(403).AssertSee(`href="https://example.com/two-factor"`)
}

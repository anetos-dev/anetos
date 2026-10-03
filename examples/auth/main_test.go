// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/db"
)

// emailedLink returns the path of the last link emailed with subject:
// anetostest records the app's emails.
func emailedLink(t *testing.T, app *anetostest.App, subject string) string {
	t.Helper()
	sent := anetostest.Mailables[LinkMail](app)
	for _, m := range slices.Backward(sent) {
		if m.Subject == subject {
			u, err := url.Parse(m.URL)
			if err != nil {
				t.Fatal(err)
			}
			return u.RequestURI()
		}
	}
	t.Fatalf("no email %q", subject)
	return ""
}

func createUser(t *testing.T, app *anetostest.App, name, email string, admin bool) *User {
	t.Helper()
	hash, err := password.Hash("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Name: name, Email: email, Password: hash, Admin: admin}
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

// region: test-auth
func TestRegisterAndLogin(t *testing.T) {
	app := anetostest.New(t, setup)

	app.Get("/register").AssertOK()
	app.PostForm("/register", url.Values{"name": {"Ada"}, "email": {"Ada@Example.com"},
		"password": {"password1"}, "password_confirmation": {"password1"}}).
		AssertRedirect("/dashboard").
		Follow().
		AssertSee("Hello, Ada", "Please verify your email address")

	// The emailed link verifies the address.
	app.Get(emailedLink(t, app, "Verify your email address")).AssertRedirect("/dashboard").Follow().
		AssertSee("Your email address is verified.").
		AssertDontSee("Please verify")

	app.PostForm("/logout", nil).AssertRedirect("/login")
	app.Get("/dashboard").AssertRedirect("/login") // guests go to the login page

	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"wrong"}}).
		AssertValidationErrors("email")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}}).
		AssertRedirect("/dashboard") // the page asked for before logging in
}

// endregion

func TestRegisterValidation(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/register")
	app.PostForm("/register", url.Values{"name": {"Ada"}, "email": {"ada@example.com"},
		"password": {"short"}, "password_confirmation": {"other"}}).
		AssertValidationErrors("password")
	// An address taken in another case.
	app.PostForm("/register", url.Values{"name": {"Ada"}, "email": {"ADA@example.com"},
		"password": {"password1"}, "password_confirmation": {"password1"}}).
		AssertValidationErrors("email")
}

func TestPasswordReset(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)

	app.Get("/forgot-password").AssertOK()
	app.PostForm("/forgot-password", url.Values{"email": {"nobody@example.com"}}).AssertRedirect("/login")
	anetostest.AssertMailNotSent[LinkMail](app, nil) // no account, no email, same answer
	app.PostForm("/forgot-password", url.Values{"email": {"ada@example.com"}}).
		AssertRedirect("/login").
		AssertSessionHas("status", "If that address has an account, we've emailed it a link.")
	link := emailedLink(t, app, "Reset your password")
	token, _ := url.ParseQuery(link[len("/reset-password?"):])

	app.Get(link).AssertOK()
	form := url.Values{"token": {token.Get("token")}, "password": {"new password"}, "password_confirmation": {"new password"}}
	app.PostForm("/reset-password", form).AssertRedirect("/login")
	app.Get(link)
	app.PostForm("/reset-password", form).AssertValidationErrors("password") // used: the password changed

	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}}).AssertValidationErrors("email")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"new password"}}).AssertRedirect("/dashboard")
}

func TestResetRevokesTokens(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}})
	app.PostForm("/tokens", url.Values{"name": {"cli"}})
	token := app.Session().String("token")
	app.PostForm("/logout", nil)

	app.Get("/forgot-password")
	app.PostForm("/forgot-password", url.Values{"email": {"ada@example.com"}})
	link := emailedLink(t, app, "Reset your password")
	q, _ := url.ParseQuery(link[len("/reset-password?"):])
	app.Get(link)
	app.PostForm("/reset-password", url.Values{"token": {q.Get("token")}, "password": {"new password"}, "password_confirmation": {"new password"}}).
		AssertRedirect("/login")
	app.WithHeader("Authorization", "Bearer "+token).GetJSON("/api/me").AssertStatus(http.StatusUnauthorized)
}

func TestLoginThrottle(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/login")
	// Five failures a minute; up to eleven in case a minute starts
	// meanwhile.
	for range 11 {
		res := app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"wrong"}})
		if strings.Contains(string(res.Follow().Body), "Too many login attempts") {
			break
		}
	}
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}}).
		AssertValidationErrors("email").
		Follow().
		AssertSee("Too many login attempts")
}

// region: test-api-token
func TestAPIToken(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}})
	app.PostForm("/tokens", url.Values{"name": {"cli"}}).AssertRedirect("/dashboard")
	token := app.Session().String("token") // flashed to show once

	// The API routes don't use the session: only the token counts.
	app.GetJSON("/api/me").AssertStatus(http.StatusUnauthorized)
	app.WithHeader("Authorization", "Bearer "+token).GetJSON("/api/me").
		AssertOK().
		AssertJSONPath("email", "ada@example.com")
}

// endregion

func TestPolicies(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := createUser(t, app, "Ada", "ada@example.com", false)
	bob := createUser(t, app, "Bob", "bob@example.com", true)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}})
	app.GetJSON("/users/" + ada.AuthID()).AssertOK()
	app.GetJSON("/users/" + bob.AuthID()).AssertForbidden()
	app.Get("/admin").AssertForbidden()

	app.PostForm("/logout", nil)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"bob@example.com"}, "password": {"password1"}})
	app.GetJSON("/users/" + ada.AuthID()).AssertOK()
	app.Get("/admin").AssertOK().AssertSee("Ada &lt;ada@example.com&gt;")
}

// region: test-clock
// The reset link works for AUTH_RESET_TTL (60 minutes): travel past it.
func TestResetLinkExpires(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/forgot-password")
	app.PostForm("/forgot-password", url.Values{"email": {"ada@example.com"}})
	q, _ := url.ParseQuery(emailedLink(t, app, "Reset your password")[len("/reset-password?"):])

	app.Travel(61 * time.Minute)
	app.Get("/reset-password?" + q.Encode())
	app.PostForm("/reset-password", url.Values{"token": {q.Get("token")}, "password": {"new password"}, "password_confirmation": {"new password"}}).
		AssertValidationErrors("password")
}

// endregion

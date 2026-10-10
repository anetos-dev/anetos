// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/saas/app/mailers"
	"anetos.dev/anetos/examples/saas/app/models"
)

// The account tests (anetos make:auth). Emails aren't sent in tests: the
// mailables are recorded, and the tests follow their links.

// authRegister signs ada up and returns the app, logged in.
func authRegister(t *testing.T) *anetostest.App {
	t.Helper()
	app := anetostest.New(t, setup)
	app.Get("/register").AssertOK()
	app.PostForm("/register", url.Values{
		"name": {"Ada"}, "email": {"Ada@Example.com"},
		"password": {"correct horse"}, "password_confirmation": {"correct horse"},
	}).AssertRedirect("/dashboard")
	return app
}

// authLink returns the path of an emailed link.
func authLink(t *testing.T, u string) string {
	t.Helper()
	p, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	return p.RequestURI()
}

func TestRegisterAndVerify(t *testing.T) {
	app := authRegister(t)
	app.Get("/dashboard").AssertOK().AssertSee("Hello, Ada", "Please verify your email address")
	sent := anetostest.Mailables[mailers.VerifyEmail](app)
	if len(sent) != 1 || sent[0].Email != "ada@example.com" {
		t.Fatalf("verification emails: %+v", sent)
	}
	app.Get(authLink(t, sent[0].URL)).AssertRedirect("/dashboard").AssertSessionHas("status", "Your email address is verified.")
	app.Get("/dashboard").AssertDontSee("Please verify your email address")

	app.PostForm("/register", url.Values{"email": {"ada@example.com"}}).AssertStatus(http.StatusSeeOther) // logged in: to AUTH_HOME_URL

	// The link can be sent again; an already verified address isn't.
	app.PostForm("/email/verification-notification", nil).AssertRedirect("/dashboard")
	if n := len(anetostest.Mailables[mailers.VerifyEmail](app)); n != 1 {
		t.Errorf("%d verification emails", n)
	}
}

func TestResendVerification(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/email/verification-notification", nil).AssertRedirect("/dashboard").
		AssertSessionHas("status", "We've emailed you a new verification link.")
	if n := len(anetostest.Mailables[mailers.VerifyEmail](app)); n != 2 {
		t.Errorf("%d verification emails", n)
	}
}

func TestRegisterValidation(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/logout", nil).AssertRedirect("/login")
	app.Get("/register")
	app.PostForm("/register", url.Values{
		"name": {"Ada"}, "email": {"ada@example.com"},
		"password": {"short"}, "password_confirmation": {"other"},
	}).AssertValidationErrors("password")
	app.PostForm("/register", url.Values{
		"name": {"Ada"}, "email": {"ADA@example.com"},
		"password": {"correct horse"}, "password_confirmation": {"correct horse"},
	}).AssertValidationErrors("email") // taken
	// A name is one line of text.
	app.PostForm("/register", url.Values{
		"name": {"  Grace \r\nBcc: x@example.com "}, "email": {"grace@example.com"},
		"password": {"correct horse"}, "password_confirmation": {"correct horse"},
	}).AssertRedirect("/dashboard")
	anetostest.AssertDatabaseHas[models.User](app, models.UserCols.Name.Eq("Grace Bcc: x@example.com"))
}

func TestLoginAndLogout(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/logout", nil).AssertRedirect("/login")
	app.Get("/dashboard").AssertRedirect("/login")

	app.Get("/login").AssertOK()
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"wrong"}}).AssertValidationErrors("email")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}).AssertRedirect("/dashboard")
	app.Get("/dashboard").AssertOK()
}

func TestLoginReturnsToTheRequestedPage(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/logout", nil)
	app.Get("/dashboard?tab=tokens").AssertRedirect("/login")
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}).AssertRedirect("/dashboard?tab=tokens")
}

func TestPasswordReset(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/logout", nil)
	app.Get("/forgot-password").AssertOK()
	app.PostForm("/forgot-password", url.Values{"email": {"nobody@example.com"}}).AssertRedirect("/login")
	anetostest.AssertMailNotSent[mailers.ResetPassword](app, nil) // no account, same answer
	app.PostForm("/forgot-password", url.Values{"email": {"ada@example.com"}}).AssertRedirect("/login")
	sent := anetostest.Mailables[mailers.ResetPassword](app)
	if len(sent) != 1 {
		t.Fatalf("reset emails: %+v", sent)
	}

	app.Get(authLink(t, sent[0].URL)).AssertOK()
	token := strings.TrimPrefix(authLink(t, sent[0].URL), "/reset-password?token=")
	token, _ = url.QueryUnescape(token)
	form := url.Values{"token": {token}, "password": {"new password"}, "password_confirmation": {"new password"}}
	app.PostForm("/reset-password", form).AssertRedirect("/login")
	app.Get(authLink(t, sent[0].URL))
	app.PostForm("/reset-password", form).AssertValidationErrors("password") // used once

	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"new password"}}).AssertRedirect("/dashboard")
}

// A new password logs out other browsers and revokes the API tokens.
func TestResetLogsOutAndRevokesTokens(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/tokens", url.Values{"name": {"cli"}})
	token := app.Session().String("token")
	app.PostForm("/logout", nil)
	app.Get("/forgot-password")
	app.PostForm("/forgot-password", url.Values{"email": {"ada@example.com"}})
	sent := anetostest.Mailables[mailers.ResetPassword](app)
	reset, _ := url.Parse(sent[0].URL)
	app.Get(reset.RequestURI())
	app.PostForm("/reset-password", url.Values{"token": {reset.Query().Get("token")}, "password": {"new password"}, "password_confirmation": {"new password"}}).
		AssertRedirect("/login")
	app.WithHeader("Authorization", "Bearer "+token).GetJSON("/api/me").AssertStatus(http.StatusUnauthorized)
}

func TestAPIToken(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/tokens", url.Values{"name": {"cli"}}).AssertRedirect("/dashboard")
	token := app.Session().String("token")
	if token == "" {
		t.Fatal("no token flashed")
	}
	app.Get("/dashboard").AssertSee("cli", token)

	var me models.User
	app.WithHeader("Authorization", "Bearer "+token).GetJSON("/api/me").AssertOK().JSON(&me)
	if me.Email != "ada@example.com" {
		t.Errorf("/api/me = %+v", me)
	}
	app.WithHeader("Authorization", "Bearer nope").GetJSON("/api/me").AssertStatus(http.StatusUnauthorized)

	// Revoked, it stops working.
	tok, err := db.Query[auth.Token](app.Context()).First()
	if err != nil {
		t.Fatal(err)
	}
	app.WithHeader("Authorization", "")
	app.PostForm("/tokens/"+strconv.FormatInt(tok.ID, 10)+"/delete", nil).AssertRedirect("/dashboard")
	app.WithHeader("Authorization", "Bearer "+token).GetJSON("/api/me").AssertStatus(http.StatusUnauthorized)
}

func TestSocialLogin(t *testing.T) {
	// Without SOCIAL_* settings, there's no login with Google or GitHub.
	off := anetostest.Env(map[string]string{"SOCIAL_GOOGLE_CLIENT_ID": "", "SOCIAL_GITHUB_CLIENT_ID": ""})
	anetostest.New(t, setup, off).Get("/login").AssertOK().AssertDontSee("Log in with")

	// FakeSocial: Google and GitHub log in through a stand-in provider.
	app := anetostest.New(t, setup, anetostest.FakeSocial())
	app.Get("/login").AssertSee("Log in with Google", "Log in with GitHub")
	grace := anetostest.SocialAccount{ID: "g-1", Email: "Grace@Example.com", EmailVerified: true, Name: "Grace"}
	app.SocialLogin("/auth/google/redirect", grace).AssertRedirect("/dashboard")
	app.Get("/dashboard").AssertSee("Hello, Grace").AssertDontSee("Please verify")
	app.PostForm("/logout", nil)

	// The same account again: the same user.
	app.SocialLogin("/auth/google/redirect", grace).AssertRedirect("/dashboard")
	anetostest.AssertDatabaseCount[models.User](app, 1)
	anetostest.AssertDatabaseHas[models.User](app, models.UserCols.Email.Eq("grace@example.com"))
}

// Logging in with a provider finds an account by its address only when
// both the provider and the app have verified it.
func TestSocialLoginFindsVerifiedAccounts(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeSocial())
	app.Get("/register")
	app.PostForm("/register", url.Values{
		"name": {"Ada"}, "email": {"ada@example.com"},
		"password": {"correct horse"}, "password_confirmation": {"correct horse"},
	}).AssertRedirect("/dashboard")
	app.PostForm("/logout", nil)
	ada := anetostest.SocialAccount{ID: "42", Email: "ada@example.com", EmailVerified: true, Name: "Ada L."}

	// Not verified here yet: refused.
	app.SocialLogin("/auth/github/redirect", ada).AssertRedirect("/login").
		Follow().AssertSee("An account with this email address exists")
	// Not verified there: refused.
	app.SocialLogin("/auth/github/redirect", anetostest.SocialAccount{ID: "43", Email: "ada@example.com"}).
		AssertRedirect("/login").Follow().AssertSee("no verified email address")

	// Verified here: the account is found, and linked.
	app.Get(authLink(t, anetostest.Mailables[mailers.VerifyEmail](app)[0].URL)) // logs nobody in
	app.SocialLogin("/auth/github/redirect", ada).AssertRedirect("/dashboard")
	app.Get("/dashboard").AssertSee("Hello, Ada")
	app.PostForm("/logout", nil)
	anetostest.AssertDatabaseCount[models.User](app, 1)

	// Another GitHub account with the address (reused) isn't linked to Ada.
	app.SocialLogin("/auth/github/redirect", anetostest.SocialAccount{ID: "44", Email: "ada@example.com", EmailVerified: true}).
		AssertRedirect("/login").Follow().AssertSee("logs in with another account there")
}

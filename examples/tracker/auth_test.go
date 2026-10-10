// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/tracker/app/mailers"
	"anetos.dev/anetos/examples/tracker/app/models"
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
	}).AssertRedirect("/projects") // auth.WithDefaultHomeURL, in auth.go
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
	}).AssertRedirect("/projects")
	anetostest.AssertDatabaseHas[models.User](app, models.UserCols.Name.Eq("Grace Bcc: x@example.com"))
}

func TestLoginAndLogout(t *testing.T) {
	app := authRegister(t)
	app.PostForm("/logout", nil).AssertRedirect("/login")
	app.Get("/dashboard").AssertRedirect("/login")

	app.Get("/login").AssertOK()
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"wrong"}}).AssertValidationErrors("email")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}).AssertRedirect("/dashboard") // the page asked for above
	app.Get("/dashboard").AssertOK()
}

// Two-factor authentication: turned on with the authenticator app's code, then
// asked for after the password; a recovery code works once.
func TestTwoFactor(t *testing.T) {
	app := authRegister(t)
	app.Get("/two-factor").AssertRedirect("/confirm-password") // the password again first
	app.PostForm("/confirm-password", url.Values{"password": {"wrong"}}).AssertValidationErrors("password")
	app.PostForm("/confirm-password", url.Values{"password": {"correct horse"}}).AssertRedirect("/two-factor")
	app.PostForm("/two-factor", nil).AssertRedirect("/two-factor")
	app.Get("/two-factor").AssertOK().AssertSee("<svg")

	a := anetos.MustResolve[*auth.Auth[*models.User]](app.App)
	u, err := models.Users.ByLogin(app.Context(), "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	setup, err := a.StartedTwoFactor(u, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TwoFactorCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	app.PostForm("/two-factor/confirm", url.Values{"code": {"000000"}}).AssertValidationErrors("code")
	page := app.PostForm("/two-factor/confirm", url.Values{"code": {code}}).AssertRedirect("/two-factor").Follow().AssertOK().Text()
	recovery := regexp.MustCompile(`<code>([a-z2-7]{5}-[a-z2-7]{5})</code>`).FindAllStringSubmatch(page, -1)
	if len(recovery) != 8 {
		t.Fatalf("%d recovery codes shown", len(recovery))
	}

	app.PostForm("/logout", nil)
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}).AssertRedirect("/two-factor-challenge")
	app.Get("/dashboard").AssertRedirect("/login") // not yet
	app.Get("/two-factor-challenge").AssertOK()
	app.PostForm("/two-factor-challenge", url.Values{"code": {"000000"}}).AssertValidationErrors("code")
	app.PostForm("/two-factor-challenge", url.Values{"code": {recovery[0][1]}}).AssertRedirect("/dashboard")
	app.PostForm("/logout", nil)
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}})
	app.PostForm("/two-factor-challenge", url.Values{"code": {recovery[0][1]}}).AssertValidationErrors("code") // used
}

// The settings: name, password (other sessions logged out), language and
// time zone.
func TestSettings(t *testing.T) {
	app := authRegister(t)
	app.Get("/settings").AssertOK().AssertSee("Ada", "ada@example.com", "Asia/Dhaka")
	app.PostForm("/settings/profile", url.Values{"name": {"  Ada \n Lovelace "}}).AssertRedirect("/settings")
	app.Get("/settings").AssertSee(`value="Ada Lovelace"`, "Saved.")

	app.PostForm("/settings/password", url.Values{"current_password": {"wrong"}, "password": {"new password"}, "password_confirmation": {"new password"}}).
		AssertValidationErrors("current_password")
	app.PostForm("/settings/password", url.Values{"current_password": {"correct horse"}, "password": {"short"}, "password_confirmation": {"short"}}).
		AssertValidationErrors("password")
	app.PostForm("/settings/password", url.Values{"current_password": {"correct horse"}, "password": {"new password"}, "password_confirmation": {"new password"}}).
		AssertRedirect("/settings")
	app.Get("/dashboard").AssertOK() // this session stays logged in
	app.PostForm("/logout", nil)
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}).AssertValidationErrors("email")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"new password"}}).AssertRedirect("/projects")

	app.PostForm("/settings/preferences", url.Values{"locale": {"xx"}, "time_zone": {"Asia/Dhaka"}}).AssertValidationErrors("locale")
	app.PostForm("/settings/preferences", url.Values{"locale": {"en"}, "time_zone": {"Mars/Base"}}).AssertValidationErrors("time_zone")
	app.PostForm("/settings/preferences", url.Values{"locale": {"en"}, "time_zone": {"Asia/Dhaka"}}).AssertRedirect("/settings")
	u, err := models.Users.ByLogin(app.Context(), "ada@example.com")
	if err != nil || u.Locale != "en" || u.TimeZone != "Asia/Dhaka" || u.Name != "Ada Lovelace" {
		t.Errorf("saved %+v, %v", u, err)
	}
	// Deleting accounts is off: AllowAccountDeletion in routes/auth.go.
	app.PostForm("/settings/delete", nil).AssertNotFound()
}

// A new email address: after the password again, once its link is
// followed; the old address is told, and its reset links stop working.
func TestChangeEmail(t *testing.T) {
	app := authRegister(t)
	verify := anetostest.Mailables[mailers.VerifyEmail](app)
	app.Get(authLink(t, verify[0].URL)) // the old address is told only once verified
	app.PostForm("/settings/email", url.Values{"email": {"new@example.com"}}).AssertRedirect("/confirm-password")
	app.PostForm("/confirm-password", url.Values{"password": {"correct horse"}})
	app.PostForm("/settings/email", url.Values{"email": {"ADA@example.com"}}).AssertValidationErrors("email") // the same
	app.PostForm("/settings/email", url.Values{"email": {"New@Example.com"}}).AssertRedirect("/settings")
	app.Get("/settings").AssertSee("new@example.com", "Cancel the change")
	sent := anetostest.Mailables[mailers.ChangeEmail](app)
	notices := anetostest.Mailables[mailers.EmailChanging](app)
	if len(sent) != 1 || sent[0].Email != "new@example.com" || len(notices) != 1 || notices[0].Email != "ada@example.com" {
		t.Fatalf("emails %+v %+v", sent, notices)
	}
	// Until the link is followed, the old address logs in.
	if u, err := models.Users.ByLogin(app.Context(), "ada@example.com"); err != nil || u.PendingEmail != "new@example.com" {
		t.Fatalf("before the link: %+v, %v", u, err)
	}
	app.Get(authLink(t, sent[0].URL)).AssertRedirect("/settings").Follow().AssertSee("Your email address is now new@example.com.")
	u, err := models.Users.ByLogin(app.Context(), "new@example.com")
	if err != nil || u.PendingEmail != "" || u.EmailVerifiedAt == nil {
		t.Fatalf("after the link: %+v, %v", u, err)
	}
	app.Get(authLink(t, sent[0].URL)).AssertStatus(http.StatusBadRequest) // used
}

// A change of address that wasn't the owner's: the link to the old
// address undoes it, even once it's made, and secures the account.
func TestRevertEmailChange(t *testing.T) {
	app := authRegister(t)
	app.Get(authLink(t, anetostest.Mailables[mailers.VerifyEmail](app)[0].URL))
	app.PostForm("/confirm-password", url.Values{"password": {"correct horse"}})
	app.PostForm("/settings/email", url.Values{"email": {"thief@example.com"}}).AssertRedirect("/settings")
	app.Get(authLink(t, anetostest.Mailables[mailers.ChangeEmail](app)[0].URL)).AssertRedirect("/settings") // the change is made

	revert := authLink(t, anetostest.Mailables[mailers.EmailChanging](app)[0].RevertURL)
	app.Get(revert).AssertOK().AssertSee("ada@example.com", "thief@example.com")
	q, _ := url.ParseQuery(revert[strings.Index(revert, "?")+1:])
	app.PostForm("/settings/email/revert", url.Values{"token": {q.Get("token")}}).AssertRedirect("/login")
	app.Get("/dashboard").AssertRedirect("/login") // everyone logged out

	u, err := models.Users.ByLogin(app.Context(), "ada@example.com")
	if err != nil || u.Password != "" || u.PendingEmail != "" || u.EmailVerifiedAt == nil {
		t.Fatalf("after undoing: %+v, %v", u, err)
	}
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}).AssertValidationErrors("email")
	resets := anetostest.Mailables[mailers.ResetPassword](app)
	if len(resets) != 1 || resets[0].Email != "ada@example.com" {
		t.Fatalf("reset emails: %+v", resets)
	}
	// The reset link works: the owner chooses a new password.
	link := authLink(t, resets[0].URL)
	rq, _ := url.ParseQuery(link[strings.Index(link, "?")+1:])
	app.Get(link)
	app.PostForm("/reset-password", url.Values{"token": {rq.Get("token")}, "password": {"brand new pass"}, "password_confirmation": {"brand new pass"}}).
		AssertRedirect("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"brand new pass"}}).AssertRedirect("/dashboard")
	// Once done, the link has nothing to undo.
	app.PostForm("/settings/email/revert", url.Values{"token": {q.Get("token")}}).AssertStatus(http.StatusBadRequest)
}

func TestDisabledAccount(t *testing.T) {
	app := authRegister(t)
	// Disabled (an admin's "Disable"): logged out, and can't log in.
	if _, err := db.Query[models.User](app.Context()).Where(models.UserCols.Email.Eq("ada@example.com")).
		Update(models.UserCols.DisabledAt.Set(new(time.Now().UTC()))); err != nil {
		t.Fatal(err)
	}
	app.Get("/dashboard").AssertRedirect("/login")
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}).AssertValidationErrors("email")
	app.Get("/login").AssertSee("This account is disabled.")
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

	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"new password"}}).AssertRedirect("/projects")
}

// A new password logs out other browsers and revokes the API tokens.
func TestResetLogsOutAndRevokesTokens(t *testing.T) {
	app := authRegister(t)
	app.Get("/dashboard")
	app.PostForm("/confirm-password", url.Values{"password": {"correct horse"}})
	app.PostForm("/tokens", url.Values{"name": {"cli"}})
	token := app.Session().GetString("token")
	if token == "" {
		t.Fatal("no token")
	}
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
	// A token works without the browser: the password again first.
	app.Get("/dashboard")
	app.PostForm("/tokens", url.Values{"name": {"cli"}}).AssertRedirect("/confirm-password")
	app.PostForm("/confirm-password", url.Values{"password": {"correct horse"}}).AssertRedirect("/dashboard")
	app.PostForm("/tokens", url.Values{"name": {"cli"}}).AssertRedirect("/dashboard")
	token := app.Session().GetString("token")
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
	app.SocialLogin("/auth/google/redirect", grace).AssertRedirect("/projects")
	app.Get("/dashboard").AssertSee("Hello, Grace").AssertDontSee("Please verify")
	app.PostForm("/logout", nil)

	// The same account again: the same user.
	app.SocialLogin("/auth/google/redirect", grace).AssertRedirect("/projects")
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
	}).AssertRedirect("/projects")
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
	app.SocialLogin("/auth/github/redirect", ada).AssertRedirect("/projects")
	app.Get("/dashboard").AssertSee("Hello, Ada")
	app.PostForm("/logout", nil)
	anetostest.AssertDatabaseCount[models.User](app, 1)

	// Another GitHub account with the address (reused) isn't linked to Ada.
	app.SocialLogin("/auth/github/redirect", anetostest.SocialAccount{ID: "44", Email: "ada@example.com", EmailVerified: true}).
		AssertRedirect("/login").Follow().AssertSee("logs in with another account there")
}

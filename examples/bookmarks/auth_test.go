// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"

	"bookmarks/app/handlers"
	"bookmarks/app/mailers"
	"bookmarks/app/models"
)

// The account tests (anetos make:auth). Emails aren't sent in tests: the
// mailables are recorded, and the tests take the tokens of their links.

// authClient is the client app's address in the tests (AUTH_CLIENT_URL).
const authClient = "https://app.example.com"

// authApp returns the app, with AUTH_CLIENT_URL set, and its clock
// stopped: the limits count tries in windows of the clock (a minute), and
// two-factor codes change every 30 seconds.
func authApp(t *testing.T) *anetostest.App {
	t.Helper()
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"AUTH_CLIENT_URL": authClient}))
	app.Freeze(time.Time{})
	return app
}

// authRegister signs ada up, and sends her token with the requests that
// follow.
func authRegister(t *testing.T) (*anetostest.App, handlers.LoginResponse) {
	t.Helper()
	app := authApp(t)
	var res handlers.LoginResponse
	app.PostJSON("/api/v1/register", map[string]any{
		"name": "Ada", "email": "Ada@Example.com", "device_name": "Ada's phone",
		"password": "correct horse", "password_confirmation": "correct horse",
	}).AssertCreated().AssertHeader("Cache-Control", "no-store").JSON(&res)
	if res.Token == "" || res.User == nil || res.User.Email != "ada@example.com" || res.User.EmailVerifiedAt != nil {
		t.Fatalf("registration: %+v", res)
	}
	app.WithHeader("Authorization", "Bearer "+res.Token)
	return app, res
}

// linkToken returns the token of an emailed link to the client app.
func linkToken(t *testing.T, link, path string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || !strings.HasPrefix(link, authClient+path+"?") {
		t.Fatalf("link %q: %v", link, err)
	}
	return u.Query().Get("token")
}

// authLogin logs in with the password and returns the response.
func authLogin(app *anetostest.App, password string) *anetostest.Response {
	return app.PostJSON("/api/v1/login", map[string]any{"email": "ada@example.com", "password": password})
}

func TestRegisterAndVerify(t *testing.T) {
	app, _ := authRegister(t)
	app.GetJSON("/api/v1/me").AssertOK().AssertJSONPath("name", "Ada").AssertJSONPath("email_verified_at", nil)
	sent := anetostest.Mailables[mailers.VerifyEmail](app)
	if len(sent) != 1 || sent[0].Email != "ada@example.com" {
		t.Fatalf("verification emails: %+v", sent)
	}
	token := linkToken(t, sent[0].URL, "/verify-email")
	app.PostJSON("/api/v1/verify-email", map[string]any{"token": "nonsense"}).AssertStatus(http.StatusBadRequest)
	app.PostJSON("/api/v1/verify-email", map[string]any{"token": token}).AssertNoContent()
	var me handlers.UserResponse
	app.GetJSON("/api/v1/me").AssertOK().JSON(&me)
	if me.EmailVerifiedAt == nil {
		t.Error("the address isn't verified")
	}
	// A verified address gets no more links.
	app.PostJSON("/api/v1/email/verification-notification", nil).AssertNoContent()
	if n := len(anetostest.Mailables[mailers.VerifyEmail](app)); n != 1 {
		t.Errorf("%d verification emails", n)
	}
}

func TestResendVerification(t *testing.T) {
	app, _ := authRegister(t)
	app.PostJSON("/api/v1/email/verification-notification", nil).AssertNoContent()
	if n := len(anetostest.Mailables[mailers.VerifyEmail](app)); n != 2 {
		t.Errorf("%d verification emails", n)
	}
}

func TestRegisterValidation(t *testing.T) {
	app, _ := authRegister(t)
	app.PostJSON("/api/v1/register", map[string]any{
		"name": "Ada", "email": "ada@example.com", "password": "short", "password_confirmation": "other",
	}).AssertValidationErrors("password")
	app.PostJSON("/api/v1/register", map[string]any{
		"name": "Ada", "email": "ADA@example.com", "password": "correct horse", "password_confirmation": "correct horse",
	}).AssertValidationErrors("email") // taken
	// A name is one line of text.
	app.PostJSON("/api/v1/register", map[string]any{
		"name": "  Grace \r\nBcc: x@example.com ", "email": "grace@example.com",
		"password": "correct horse", "password_confirmation": "correct horse",
	}).AssertCreated().AssertJSONPath("user.name", "Grace Bcc: x@example.com")
}

func TestLoginAndLogout(t *testing.T) {
	app, _ := authRegister(t)
	app.PostJSON("/api/v1/logout", nil).AssertNoContent()
	app.GetJSON("/api/v1/me").AssertStatus(http.StatusUnauthorized) // the token was revoked
	app.WithHeader("Authorization", "")
	app.GetJSON("/api/v1/me").AssertStatus(http.StatusUnauthorized).AssertHeader("WWW-Authenticate", "Bearer")

	authLogin(app, "wrong").AssertValidationErrors("email")
	var res handlers.LoginResponse
	authLogin(app, "correct horse").AssertOK().JSON(&res)
	if res.Token == "" || res.TwoFactor {
		t.Fatalf("login: %+v", res)
	}
	app.WithHeader("Authorization", "Bearer "+res.Token)
	app.GetJSON("/api/v1/me").AssertOK().AssertJSONPath("email", "ada@example.com")
}

func TestLoginThrottled(t *testing.T) {
	app, _ := authRegister(t)
	for range 5 {
		authLogin(app, "wrong").AssertValidationErrors("email")
	}
	res := authLogin(app, "correct horse").AssertStatus(http.StatusTooManyRequests)
	if res.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

// Two-factor authentication: turned on with the authenticator app's code, then
// asked for after the password; a recovery code works once.
func TestTwoFactor(t *testing.T) {
	app, reg := authRegister(t)
	var other handlers.LoginResponse
	authLogin(app, "correct horse").AssertOK().JSON(&other)
	app.PostJSON("/api/v1/two-factor", map[string]any{"password": "wrong"}).AssertValidationErrors("password")
	var setup handlers.TwoFactorSetupResponse
	app.PostJSON("/api/v1/two-factor", map[string]any{"password": "correct horse"}).AssertOK().JSON(&setup)
	if setup.Secret == "" || !strings.HasPrefix(setup.URI, "otpauth://totp/") || !strings.HasPrefix(setup.QRCode, "<svg") {
		t.Fatalf("setup: %+v", setup)
	}
	code, err := auth.TwoFactorCode(setup.Secret, anetos.Now(app.Context()))
	if err != nil {
		t.Fatal(err)
	}
	app.PostJSON("/api/v1/two-factor/confirm", map[string]any{"code": "000000"}).AssertValidationErrors("code")
	var codes handlers.RecoveryCodesResponse
	app.PostJSON("/api/v1/two-factor/confirm", map[string]any{"code": code}).AssertOK().JSON(&codes)
	if len(codes.RecoveryCodes) != 8 {
		t.Fatalf("recovery codes: %+v", codes)
	}
	app.GetJSON("/api/v1/two-factor").AssertOK().AssertJSONPath("enabled", true).AssertJSONPath("recovery_codes", 8)
	// The tokens made with the password alone are revoked.
	app.WithHeader("Authorization", "Bearer "+other.Token)
	app.GetJSON("/api/v1/me").AssertStatus(http.StatusUnauthorized)
	app.WithHeader("Authorization", "Bearer "+reg.Token)
	app.GetJSON("/api/v1/me").AssertOK()

	// The password isn't enough now: the login answers a challenge.
	app.WithHeader("Authorization", "")
	var res handlers.LoginResponse
	authLogin(app, "correct horse").AssertOK().JSON(&res)
	if !res.TwoFactor || res.Challenge == "" || res.Token != "" {
		t.Fatalf("login: %+v", res)
	}
	app.PostJSON("/api/v1/login/two-factor", map[string]any{"challenge": res.Challenge, "code": "000000"}).AssertValidationErrors("code")
	app.PostJSON("/api/v1/login/two-factor", map[string]any{"challenge": "nonsense", "code": codes.RecoveryCodes[0]}).AssertValidationErrors("challenge")
	var loggedIn handlers.LoginResponse
	app.PostJSON("/api/v1/login/two-factor", map[string]any{"challenge": res.Challenge, "code": codes.RecoveryCodes[0]}).AssertOK().JSON(&loggedIn)
	if loggedIn.Token == "" {
		t.Fatalf("two-factor login: %+v", loggedIn)
	}
	app.PostJSON("/api/v1/login/two-factor", map[string]any{"challenge": res.Challenge, "code": codes.RecoveryCodes[0]}).AssertValidationErrors("code") // used

	app.WithHeader("Authorization", "Bearer "+loggedIn.Token)
	app.PostJSON("/api/v1/two-factor/recovery-codes", map[string]any{"password": "correct horse"}).AssertOK().JSON(&codes)
	if len(codes.RecoveryCodes) != 8 {
		t.Errorf("new recovery codes: %+v", codes)
	}
	app.PostJSON("/api/v1/two-factor/disable", map[string]any{"password": "correct horse"}).AssertNoContent()
	app.WithHeader("Authorization", "")
	var again handlers.LoginResponse
	authLogin(app, "correct horse").AssertOK().JSON(&again)
	if again.TwoFactor || again.Token == "" {
		t.Errorf("login with two-factor authentication off: %+v", again)
	}
}

func TestDisabledAccount(t *testing.T) {
	app, _ := authRegister(t)
	now := anetos.Now(app.Context()).UTC()
	if _, err := db.Query[models.User](app.Context()).Where(models.UserCols.Email.Eq("ada@example.com")).
		Update(models.UserCols.DisabledAt.Set(&now)); err != nil {
		t.Fatal(err)
	}
	app.GetJSON("/api/v1/me").AssertStatus(http.StatusUnauthorized) // its tokens stop working
	app.WithHeader("Authorization", "")
	authLogin(app, "correct horse").AssertValidationErrors("email")
}

func TestPasswordReset(t *testing.T) {
	app, _ := authRegister(t)
	app.WithHeader("Authorization", "")
	// The same answer for an address without an account.
	app.PostJSON("/api/v1/forgot-password", map[string]any{"email": "nobody@example.com"}).AssertNoContent()
	app.PostJSON("/api/v1/forgot-password", map[string]any{"email": "ada@example.com"}).AssertNoContent()
	sent := anetostest.Mailables[mailers.ResetPassword](app)
	if len(sent) != 1 || sent[0].Email != "ada@example.com" {
		t.Fatalf("reset emails: %+v", sent)
	}
	token := linkToken(t, sent[0].URL, "/reset-password")
	app.PostJSON("/api/v1/reset-password", map[string]any{
		"token": token, "password": "new password", "password_confirmation": "new password",
	}).AssertNoContent()
	// The link works once.
	app.PostJSON("/api/v1/reset-password", map[string]any{
		"token": token, "password": "other password", "password_confirmation": "other password",
	}).AssertValidationErrors("token")
	authLogin(app, "correct horse").AssertValidationErrors("email")
	authLogin(app, "new password").AssertOK()
}

// A reset link proves the address: a never-verified one is verified, and
// the two-factor authentication whoever registered it set up is turned off.
func TestResetOfUnverifiedAddress(t *testing.T) {
	app, _ := authRegister(t)
	var setup handlers.TwoFactorSetupResponse
	app.PostJSON("/api/v1/two-factor", map[string]any{"password": "correct horse"}).AssertOK().JSON(&setup)
	code, err := auth.TwoFactorCode(setup.Secret, anetos.Now(app.Context()))
	if err != nil {
		t.Fatal(err)
	}
	app.PostJSON("/api/v1/two-factor/confirm", map[string]any{"code": code}).AssertOK()
	app.WithHeader("Authorization", "")
	app.PostJSON("/api/v1/forgot-password", map[string]any{"email": "ada@example.com"}).AssertNoContent()
	token := linkToken(t, anetostest.Mailables[mailers.ResetPassword](app)[0].URL, "/reset-password")
	app.PostJSON("/api/v1/reset-password", map[string]any{
		"token": token, "password": "new password", "password_confirmation": "new password",
	}).AssertNoContent()
	var res handlers.LoginResponse
	authLogin(app, "new password").AssertOK().JSON(&res)
	if res.TwoFactor || res.User == nil || res.User.EmailVerifiedAt == nil {
		t.Errorf("login after the reset: %+v", res)
	}
}

// A reset revokes every token: whoever had the account loses its API
// access too.
func TestResetRevokesTokens(t *testing.T) {
	app, reg := authRegister(t)
	app.WithHeader("Authorization", "")
	app.PostJSON("/api/v1/forgot-password", map[string]any{"email": "ada@example.com"}).AssertNoContent()
	token := linkToken(t, anetostest.Mailables[mailers.ResetPassword](app)[0].URL, "/reset-password")
	app.PostJSON("/api/v1/reset-password", map[string]any{
		"token": token, "password": "new password", "password_confirmation": "new password",
	}).AssertNoContent()
	app.WithHeader("Authorization", "Bearer "+reg.Token)
	app.GetJSON("/api/v1/me").AssertStatus(http.StatusUnauthorized)
}

// A new password keeps the request's token, and revokes the others.
func TestChangePassword(t *testing.T) {
	app, reg := authRegister(t)
	var other handlers.LoginResponse
	authLogin(app, "correct horse").AssertOK().JSON(&other)
	app.PutJSON("/api/v1/password", map[string]any{
		"current_password": "wrong", "password": "new password", "password_confirmation": "new password",
	}).AssertValidationErrors("current_password")
	app.PutJSON("/api/v1/password", map[string]any{
		"current_password": "correct horse", "password": "new password", "password_confirmation": "new password",
	}).AssertNoContent()
	app.GetJSON("/api/v1/me").AssertOK()
	app.WithHeader("Authorization", "Bearer "+other.Token)
	app.GetJSON("/api/v1/me").AssertStatus(http.StatusUnauthorized)
	app.WithHeader("Authorization", "Bearer "+reg.Token)
	authLogin(app, "new password").AssertOK()
}

func TestAPITokens(t *testing.T) {
	app, reg := authRegister(t)
	app.PostJSON("/api/v1/tokens", map[string]any{"name": "deploy", "abilities": []string{"deploy"}, "password": "wrong"}).
		AssertValidationErrors("password")
	var created handlers.NewTokenResponse
	app.PostJSON("/api/v1/tokens", map[string]any{"name": "deploy", "abilities": []string{"deploy"}, "password": "correct horse"}).
		AssertCreated().JSON(&created)
	if created.Token == "" || created.Name != "deploy" || len(created.Abilities) != 1 || created.ExpiresAt == nil {
		t.Fatalf("new token: %+v", created)
	}
	var tokens []handlers.TokenResponse
	app.GetJSON("/api/v1/tokens").AssertOK().JSON(&tokens)
	if len(tokens) != 2 || tokens[0].Name != "deploy" || tokens[0].Current || !tokens[1].Current || tokens[1].Name != "Ada's phone" {
		t.Fatalf("tokens: %+v", tokens)
	}
	for _, bad := range [][]string{{""}, {"two words"}, {strings.Repeat("a", 101)}} {
		app.PostJSON("/api/v1/tokens", map[string]any{"name": "x", "abilities": bad, "password": "correct horse"}).
			AssertValidationErrors("abilities")
	}

	// A token with narrower abilities uses the API, not the account.
	app.WithHeader("Authorization", "Bearer "+created.Token)
	app.GetJSON("/api/v1/me").AssertOK()
	app.GetJSON("/api/v1/tokens").AssertForbidden()
	app.PostJSON("/api/v1/tokens", map[string]any{"name": "more", "password": "correct horse"}).AssertForbidden()
	app.PutJSON("/api/v1/password", map[string]any{
		"current_password": "correct horse", "password": "new password", "password_confirmation": "new password",
	}).AssertForbidden()
	app.PostJSON("/api/v1/two-factor", map[string]any{"password": "correct horse"}).AssertForbidden()

	app.WithHeader("Authorization", "Bearer "+reg.Token)
	app.DeleteJSON("/api/v1/tokens/" + strconv.FormatInt(created.ID, 10)).AssertNoContent()
	app.WithHeader("Authorization", "Bearer "+created.Token)
	app.GetJSON("/api/v1/me").AssertStatus(http.StatusUnauthorized)
}

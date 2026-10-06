// SPDX-License-Identifier: Apache-2.0

// Command auth is a small app with accounts: registration, login with
// "remember me" and login throttling, two-factor sign-in, logout, email
// verification, password reset, API tokens and typed policies, over
// SQLite. The
// verification and reset links are emailed (MAIL_DRIVER=log writes them
// to the log). anetos make:auth writes this kind of code into an app.
//
//	go tool anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080
//	go run . migrate
//	go run .
//
// Then open http://localhost:8080/register.
package main

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/qr"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"
)

// LinkMail is an email with a link: verification, password reset.
type LinkMail struct {
	To, Subject, URL string
}

// Build implements mailer.Mailable: a text email.
func (m LinkMail) Build(context.Context) (*mailer.Message, error) {
	return &mailer.Message{To: []mailer.Address{{Address: m.To}}, Subject: m.Subject, Text: m.Subject + ":\n\n" + m.URL + "\n"}, nil
}

// sendLink emails a link to path on the app's public URL (APP_URL).
func sendLink(c *web.Ctx, to, subject, path string) error {
	link, err := mailer.URL(c, path)
	if err != nil {
		return err
	}
	return mailer.Send(c, LinkMail{To: to, Subject: subject, URL: link})
}

// Accounts holds the handlers.
type Accounts struct {
	auth   *auth.Auth[*User]
	social *social.Social[*User]
}

// RegisterInput is the registration form.
type RegisterInput struct {
	Name                 string `json:"name" validate:"required|max:100"`
	Email                string `json:"email" validate:"required|email|max:255"`
	Password             string `json:"password" validate:"required|min:8|max:1024|confirmed"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// region: register
func (h Accounts) Register(c *web.Ctx, in RegisterInput) (web.Responder, error) {
	email := strings.ToLower(in.Email) // stored and looked up in lower case
	taken, err := db.Query[User](c).Where(colEmail.Eq(email)).Exists()
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, validate.Fail("email", "The email has already been taken.")
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	u := &User{Name: in.Name, Email: email, Password: hash}
	if err := db.Create(c, u); err != nil {
		return nil, err
	}
	// The account exists now: a mail server that fails doesn't undo it
	// (an app with a queue sends with mailer.Queue, which retries).
	if err := sendLink(c, u.Email, "Verify your email address", "/verify-email?token="+url.QueryEscape(h.auth.VerificationToken(u, u.Email))); err != nil {
		c.Logger().Error("send the verification link", "error", err)
	}
	if err := h.auth.Login(c, u, false); err != nil {
		return nil, err
	}
	return web.Redirect("/dashboard"), nil
}

// endregion

// LoginInput is the login form.
type LoginInput struct {
	Email    string `json:"email" validate:"required|email"`
	Password string `json:"password" validate:"required"`
	Remember bool   `json:"remember"`
}

// region: login
func (h Accounts) Login(c *web.Ctx, in LoginInput) (web.Responder, error) {
	_, err := h.auth.Attempt(c, in.Email, in.Password, in.Remember)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, validate.Fail("email", "These credentials don't match our records.")
	case errors.As(err, &throttled):
		return nil, validate.Fail("email", "Too many login attempts. Try again in a minute.")
	case errors.Is(err, auth.ErrTwoFactorRequired):
		return web.Redirect("/two-factor-challenge"), nil // the password was right: now the code
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, h.auth.Config().HomeURL)), nil // the page they wanted, or AUTH_HOME_URL
}

func (h Accounts) Logout(c *web.Ctx) error {
	if err := h.auth.Logout(c); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/login")
}

// endregion

// CodeInput is a two-factor code: the authenticator app's, or a recovery
// code.
type CodeInput struct {
	Code string `json:"code" validate:"required|max:30"`
}

// region: challenge
// Challenge finishes a sign-in waiting for a code (after Login).
func (h Accounts) Challenge(c *web.Ctx, in CodeInput) (web.Responder, error) {
	_, err := h.auth.AttemptTwoFactor(c, in.Code)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		return nil, validate.Fail("code", "That code isn't right.")
	case errors.As(err, &throttled):
		return nil, validate.Fail("code", "Too many tries. Try again in a minute.")
	case errors.Is(err, auth.ErrNoPendingSignIn): // none, or it expired
		return web.Redirect("/login"), nil
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, h.auth.Config().HomeURL)), nil
}

// endregion

// NewPasswordInput is the change of password form.
type NewPasswordInput struct {
	CurrentPassword      string `json:"current_password"`
	Password             string `json:"password" validate:"required|min:8|max:1024|confirmed"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// region: change-password
// ChangePassword changes the signed-in user's password. Their other
// browsers and devices are signed out; this one stays signed in.
func (h Accounts) ChangePassword(c *web.Ctx, in NewPasswordInput) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	switch err := h.auth.ChangePassword(c, u, in.CurrentPassword, in.Password); {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, validate.Fail("current_password", "That isn't your password.")
	case err != nil:
		return nil, err
	}
	c.Session().Flash("status", "Password changed.")
	return web.Redirect("/dashboard"), nil
}

// endregion

// PasswordInput is the password confirmation form.
type PasswordInput struct {
	Password string `json:"password" validate:"required"`
}

// region: confirm
// ConfirmPassword checks the password again (it holds for
// AUTH_CONFIRM_TTL), then goes back to the page that asked for it.
func (h Accounts) ConfirmPassword(c *web.Ctx, in PasswordInput) (web.Responder, error) {
	switch err := h.auth.ConfirmPassword(c, in.Password); {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, validate.Fail("password", "That isn't your password.")
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, h.auth.Config().HomeURL)), nil
}

// endregion

// region: two-factor
// TwoFactor shows two-factor sign-in: off, being set up (a QR code for
// the authenticator app), or on; and the recovery codes, once.
func (h Accounts) TwoFactor(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	st, err := h.auth.TwoFactor(u)
	if err != nil {
		return err
	}
	data := map[string]any{"On": st.On, "Left": st.RecoveryCodes, "Codes": strings.Fields(view.Flash(c, "codes"))}
	if st.Started {
		setup, err := h.auth.StartedTwoFactor(u, u.Email)
		if err != nil {
			return err
		}
		svg, err := qr.SVG(setup.URI, qr.M, 200) // otpauth://totp/…
		if err != nil {
			return err
		}
		data["Secret"], data["QR"] = setup.Secret, template.HTML(svg) //nolint:gosec // package qr's markup
	}
	return render(c, "two-factor", data)
}

// StartTwoFactor makes a new secret, shown by TwoFactor until confirmed.
func (h Accounts) StartTwoFactor(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	if _, err := h.auth.StartTwoFactor(c, u, u.Email); err != nil && !errors.Is(err, auth.ErrTwoFactorOn) {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/two-factor")
}

// ConfirmTwoFactor turns it on with a code of the app, and shows the
// recovery codes, once.
func (h Accounts) ConfirmTwoFactor(c *web.Ctx, in CodeInput) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	codes, err := h.auth.ConfirmTwoFactor(c, u, in.Code)
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		return nil, validate.Fail("code", "That code isn't right.")
	case err != nil:
		return nil, err
	}
	c.Session().Flash("codes", strings.Join(codes, " "))
	return web.Redirect("/two-factor"), nil
}

// DisableTwoFactor turns it off.
func (h Accounts) DisableTwoFactor(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	if err := h.auth.DisableTwoFactor(c, u); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/two-factor")
}

// endregion

// region: verify
func (h Accounts) VerifyEmail(c *web.Ctx, in TokenQuery) (web.Responder, error) {
	u, email, err := h.auth.CheckVerificationToken(c, in.Token)
	if err != nil {
		return nil, err // 400 for a bad or expired link
	}
	if email == u.Email && u.EmailVerifiedAt == nil {
		now := anetos.Now(c).UTC()
		if _, err := db.Query[User](c).Where(colID.Eq(u.ID)).Update(colVerified.Set(&now)); err != nil {
			return nil, err
		}
	}
	c.Session().Flash("status", "Your email address is verified.")
	return web.Redirect("/dashboard"), nil
}

// endregion

// TokenQuery reads ?token= from a link.
type TokenQuery struct {
	Token string `query:"token" validate:"required"`
}

// ForgotInput is the forgotten-password form.
type ForgotInput struct {
	Email string `json:"email" validate:"required|email"`
}

// ResetInput is the new-password form.
type ResetInput struct {
	Token                string `json:"token" validate:"required"`
	Password             string `json:"password" validate:"required|min:8|max:1024|confirmed"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// region: reset
func (h Accounts) SendReset(c *web.Ctx, in ForgotInput) (web.Responder, error) {
	u, err := users.ByLogin(c, in.Email)
	switch {
	case err == nil:
		if err := sendLink(c, u.Email, "Reset your password", "/reset-password?token="+url.QueryEscape(h.auth.PasswordResetToken(u))); err != nil {
			return nil, err
		}
	case !errors.Is(err, db.ErrNotFound):
		return nil, err
	}
	// The same answer either way, so the form doesn't reveal who has an
	// account.
	c.Session().Flash("status", "If that address has an account, we've emailed it a link.")
	return web.Redirect("/login"), nil
}

func (h Accounts) Reset(c *web.Ctx, in ResetInput) (web.Responder, error) {
	u, err := h.auth.CheckPasswordResetToken(c, in.Token)
	if errors.Is(err, auth.ErrInvalidToken) {
		return nil, validate.Fail("password", "This reset link is invalid or has expired. Ask for a new one.")
	}
	if err != nil {
		return nil, err
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	// Only if the password is still the one the link was made for: two
	// uses of the link at once change it once.
	n, err := db.Query[User](c).Where(colID.Eq(u.ID), colPassword.Eq(u.Password)).Update(colPassword.Set(hash))
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, validate.Fail("password", "This reset link is invalid or has expired. Ask for a new one.")
	}
	// The new password signs out every session and remembered browser
	// (their password fingerprint no longer matches); API tokens are
	// revoked too.
	if err := h.auth.RevokeAllTokens(c, u); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Your password has been reset. You can log in now.")
	return web.Redirect("/login"), nil
}

// endregion

// NewTokenInput is the API token form.
type NewTokenInput struct {
	Name string `json:"name" validate:"required|max:100"`
}

// TokenID reads {id} from the path.
type TokenID struct {
	ID int64 `path:"id"`
}

// region: api-tokens
func (h Accounts) CreateToken(c *web.Ctx, in NewTokenInput) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	plain, _, err := h.auth.CreateToken(c, u, in.Name, []string{"profile:read"}, 90*24*time.Hour)
	if err != nil {
		return nil, err
	}
	c.Session().Flash("token", plain) // shown once
	return web.Redirect("/dashboard"), nil
}

// Me is GET /api/me, for API clients with a token.
func (Accounts) Me(c *web.Ctx) error {
	if !auth.TokenCan(c, "profile:read") {
		return auth.ErrForbidden
	}
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, u)
}

// endregion

func (h Accounts) RevokeToken(c *web.Ctx, in TokenID) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	if err := h.auth.RevokeToken(c, u, in.ID); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Token revoked.")
	return web.Redirect("/dashboard"), nil
}

// UserID reads {id} from the path.
type UserID struct {
	ID int64 `path:"id"`
}

// region: authorize
func (Accounts) ShowUser(c *web.Ctx, in UserID) (*User, error) {
	other, err := db.Find[User](c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := auth.Authorize(c, policies.ViewUser, &other); err != nil {
		return nil, err // 403 unless it's you, or you're an admin
	}
	return &other, nil
}

func (Accounts) Admin(c *web.Ctx) error {
	if err := auth.AuthorizeUser(c, policies.ManageUsers); err != nil {
		return err
	}
	all, err := db.Query[User](c).OrderBy(colID.Asc()).Get()
	if err != nil {
		return err
	}
	return render(c, "admin", map[string]any{"Users": all})
}

// endregion

// setup connects the database and adds the cache, sessions, auth, the
// migrations, the server and the routes. Tests call it too.
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	sets := []*migrate.Set{Migrations, auth.Migrations(), social.Migrations(), cache.Migrations(""), session.Migrations("")}
	if _, err := migrate.ForApp(app, sets); err != nil {
		return nil, err
	}
	if _, err := cache.ForApp(app); err != nil { // login throttling counts failures in the cache
		return nil, err
	}
	sessions, err := session.ForApp(app)
	if err != nil {
		return nil, err
	}
	if _, err := mailer.ForApp(app); err != nil { // MAIL_DRIVER: log in development
		return nil, err
	}
	// region: setup
	// AUTH_* settings. Signing in leads to /dashboard, unless
	// AUTH_HOME_URL names another page.
	a, err := auth.ForApp(app, users, auth.DefaultHomeURL("/dashboard"))
	if err != nil {
		return nil, err
	}
	// endregion
	// region: social-setup
	// Providers with SOCIAL_<NAME>_CLIENT_ID and _CLIENT_SECRET set; their
	// callbacks are APP_URL/auth/<name>/callback.
	s, err := social.ForApp(app, a, findOrCreate, social.Configured(app, social.Google(), social.GitHub()))
	if err != nil {
		return nil, err
	}
	// endregion
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	routes(srv.Router(), sessions, a, s)
	return srv, nil
}

func routes(r *web.Router, sessions *session.Manager, a *auth.Auth[*User], s *social.Social[*User]) {
	h := Accounts{auth: a, social: s}
	// region: routes
	pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
	pages.Get("/", func(c *web.Ctx) error { return c.Redirect(http.StatusSeeOther, "/dashboard") })
	pages.Get("/verify-email", web.H(h.VerifyEmail))

	guests := pages.Group("", a.Guest) // signed-in users go to AUTH_HOME_URL
	guests.Get("/register", h.page("register"))
	guests.Post("/register", web.H(h.Register))
	guests.Get("/login", h.page("login"))
	guests.Post("/login", web.H(h.Login))
	guests.Get("/forgot-password", h.page("forgot"))
	guests.With(ratelimit.Middleware("forgot-password", ratelimit.PerMinute(5))).Post("/forgot-password", web.H(h.SendReset))
	guests.Get("/reset-password", h.page("reset"))
	guests.Post("/reset-password", web.H(h.Reset))
	guests.Get("/auth/{provider}/redirect", s.Redirect) // "Sign in with …" links here
	guests.Get("/auth/{provider}/callback", s.Callback)
	guests.Get("/two-factor-challenge", h.page("challenge")) // AUTH_CHALLENGE_URL
	guests.Post("/two-factor-challenge", web.H(h.Challenge))

	members := pages.Group("", a.Require) // guests go to AUTH_LOGIN_URL
	members.Get("/dashboard", h.Dashboard)
	members.Post("/logout", h.Logout)
	members.Post("/tokens/{id}/delete", web.H(h.RevokeToken))
	members.Get("/users/{id}", web.H(h.ShowUser))
	members.Get("/admin", h.Admin)
	members.Post("/password", web.H(h.ChangePassword))
	members.Get("/confirm-password", h.page("confirm")) // AUTH_CONFIRM_URL
	members.Post("/confirm-password", web.H(h.ConfirmPassword))

	// Two-factor sign-in (AUTH_TWO_FACTOR_URL) and API tokens (they work
	// without the browser): the password again first.
	secure := members.Group("", a.RequireConfirmed)
	secure.Post("/tokens", web.H(h.CreateToken))
	secure.Get("/two-factor", h.TwoFactor)
	secure.Post("/two-factor", h.StartTwoFactor)
	secure.Post("/two-factor/confirm", web.H(h.ConfirmTwoFactor))
	secure.Post("/two-factor/disable", h.DisableTwoFactor)

	api := r.Group("/api", a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
	api.Get("/me", h.Me)
	// endregion
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

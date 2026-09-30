// SPDX-License-Identifier: Apache-2.0

// Command auth is a small app with accounts: registration, login with
// "remember me" and login throttling, logout, email verification,
// password reset, API tokens and typed policies, over SQLite. Links that
// would be emailed (verification, password reset) are logged until
// Anetos has mail (v0.2, B9).
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
	"log"
	"log/slog"
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
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"
)

// sendLink delivers a link by email. Until Anetos has mail it logs it;
// tests replace it to follow the links.
var sendLink = func(c *web.Ctx, to, subject, link string) {
	c.Logger().Info("email", slog.String("to", to), slog.String("subject", subject), slog.String("link", link))
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
	sendLink(c, u.Email, "Verify your email address", "/verify-email?token="+url.QueryEscape(h.auth.VerificationToken(u, u.Email)))
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
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, "/dashboard")), nil // the page they wanted, if any
}

func (h Accounts) Logout(c *web.Ctx) error {
	if err := h.auth.Logout(c); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/login")
}

// endregion

// region: verify
func (h Accounts) VerifyEmail(c *web.Ctx, in TokenQuery) (web.Responder, error) {
	u, email, err := h.auth.CheckVerificationToken(c, in.Token)
	if err != nil {
		return nil, err // 400 for a bad or expired link
	}
	if email == u.Email && u.EmailVerifiedAt == nil {
		now := time.Now().UTC()
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
		sendLink(c, u.Email, "Reset your password", "/reset-password?token="+url.QueryEscape(h.auth.PasswordResetToken(u)))
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
	// region: setup
	a, err := auth.ForApp(app, users) // AUTH_* settings
	if err != nil {
		return nil, err
	}
	// endregion
	// region: social-setup
	// Providers with SOCIAL_<NAME>_CLIENT_ID and _CLIENT_SECRET set; their
	// callbacks are APP_URL/auth/<name>/callback.
	s, err := social.ForApp(app, a, findOrCreate, social.Configured(app, socialProviders...), socialOptions...)
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

// socialProviders are the providers users may sign in with; tests
// replace them, and socialOptions, with a fake provider.
var (
	socialProviders = []social.Provider{social.Google(), social.GitHub()}
	socialOptions   []social.Option
)

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

	members := pages.Group("", a.Require) // guests go to AUTH_LOGIN_URL
	members.Get("/dashboard", h.Dashboard)
	members.Post("/logout", h.Logout)
	members.Post("/tokens", web.H(h.CreateToken))
	members.Post("/tokens/{id}/delete", web.H(h.RevokeToken))
	members.Get("/users/{id}", web.H(h.ShowUser))
	members.Get("/admin", h.Admin)

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

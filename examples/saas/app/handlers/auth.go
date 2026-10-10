// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"

	"anetos.dev/anetos/examples/saas/app/jobs"
	"anetos.dev/anetos/examples/saas/app/mailers"
	"anetos.dev/anetos/examples/saas/app/models"
	"anetos.dev/anetos/examples/saas/views"
)

// Accounts serves registration, login and logout, login with Google
// and GitHub, email verification, password reset and API tokens (anetos
// make:auth). Hashing, tokens, sessions and throttling are package
// auth's, and the login flow package social's; this is your code to
// change.
type Accounts struct {
	Auth   *auth.Auth[*models.User]
	Social *social.Social[*models.User]
}

// RegisterInput is the registration form.
type RegisterInput struct {
	Name                 string `json:"name" validate:"required|max:100"`
	Email                string `json:"email" validate:"required|email|max:255"`
	Password             string `json:"password" validate:"required|min:8|max:1024|confirmed"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// LoginInput is the login form.
type LoginInput struct {
	Email    string `json:"email" validate:"required|email"`
	Password string `json:"password" validate:"required"`
	Remember bool   `json:"remember"`
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

// TokenQuery reads ?token= from a link.
type TokenQuery struct {
	Token string `query:"token" validate:"required"`
}

// NewTokenInput is the API token form.
type NewTokenInput struct {
	Name string `json:"name" validate:"required|max:100"`
}

// TokenID reads {id} from the path.
type TokenID struct {
	ID int64 `path:"id"`
}

// RegisterPage shows the registration form.
func (h Accounts) RegisterPage(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.Register(h.socialButtons()))
}

// Register creates the account, emails the verification link and logs
// the user in, then goes to AUTH_HOME_URL.
func (h Accounts) Register(c *web.Ctx, in RegisterInput) (web.Responder, error) {
	name := cleanName(in.Name)
	if name == "" {
		return nil, validate.Fail("name", "The name field is required.")
	}
	email := strings.ToLower(in.Email) // stored and looked up in lower case
	taken, err := emailTaken(c, email)
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
	u := &models.User{Name: name, Email: email, Password: hash}
	u.StartTrial(anetos.Now(c))
	// The user and the emails, or neither: the email and the welcome job
	// are queued once the transaction commits.
	err = db.Tx(c, func(ctx context.Context) error {
		if err := db.Create(ctx, u); err != nil {
			return err
		}
		if err := welcome(ctx, u); err != nil {
			return err
		}
		return h.sendVerification(ctx, u)
	})
	if err != nil {
		if taken, _ := emailTaken(c, email); taken { // registered meanwhile
			return nil, validate.Fail("email", "The email has already been taken.")
		}
		return nil, err
	}
	if err := h.Auth.Login(c, u, false); err != nil {
		return nil, err
	}
	return web.Redirect(web.LocalePath(c, h.Auth.Config().HomeURL)), nil // AUTH_HOME_URL
}

// region: dispatch
// welcome queues the welcome email of a new user, once the transaction
// that creates them commits: a worker sends it (jobs.SendWelcome).
func welcome(ctx context.Context, u *models.User) error {
	return queue.Dispatch(ctx, jobs.SendWelcome{UserID: u.ID}, queue.AfterCommit())
}

// endregion

// emailTaken reports whether an account has the address.
func emailTaken(ctx context.Context, email string) (bool, error) {
	return db.Query[models.User](ctx).Where(models.UserCols.Email.Eq(email)).Exists()
}

// cleanName returns name on one line: control characters dropped, runs
// of spaces made one.
func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	return strings.Join(strings.Fields(name), " ")
}

// sendVerification emails u a link that verifies their address.
func (h Accounts) sendVerification(ctx context.Context, u *models.User) error {
	link, err := mailer.URL(ctx, "/verify-email?token="+url.QueryEscape(h.Auth.VerificationToken(u, u.Email)))
	if err != nil {
		return err
	}
	return mailer.Queue(ctx, mailers.VerifyEmail{Name: u.Name, Email: u.Email, URL: link}, queue.AfterCommit())
}

// LoginPage shows the login form.
func (h Accounts) LoginPage(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.Login(h.socialButtons()))
}

// socialButtons returns a "Log in with …" button per provider whose
// SOCIAL_<NAME>_CLIENT_ID and _CLIENT_SECRET are set.
func (h Accounts) socialButtons() []views.SocialButton {
	var buttons []views.SocialButton
	for _, name := range h.Social.Providers() {
		buttons = append(buttons, views.SocialButton{Name: name, Title: h.Social.Title(name)})
	}
	return buttons
}

// SocialUser returns the user of an account logging in with Google or
// GitHub: the one it is linked to; else the user with its verified email
// address (who then logs in either way); else a new user, without a
// password. An address the provider hasn't verified can't be trusted to
// find anyone, and nor can one the user hasn't verified here: someone
// could have registered it first, with a password, to take over the
// account of whoever logs in with it later.
func SocialUser(ctx context.Context, p social.Profile) (*models.User, error) {
	id, linked, err := social.FindLink(ctx, p)
	if err != nil {
		return nil, err
	}
	if linked {
		u, err := models.Users.ByID(ctx, id)
		if !errors.Is(err, db.ErrNotFound) {
			return u, err
		}
		// The user was deleted: forget the link and start again.
		if err := social.Unlink(ctx, id, p.Provider); err != nil {
			return nil, err
		}
	}
	if p.Email == "" || !p.EmailVerified {
		return nil, &social.NoAccountError{Message: "Your account there has no verified email address."}
	}
	var u *models.User
	err = db.Tx(ctx, func(ctx context.Context) error { // the user and the link, or neither
		u, err = models.Users.ByLogin(ctx, p.Email)
		if errors.Is(err, db.ErrNotFound) {
			now := anetos.Now(ctx).UTC()
			name := cleanName(p.Name)
			if name == "" {
				name, _, _ = strings.Cut(p.Email, "@")
			}
			u = &models.User{Name: name, Email: strings.ToLower(p.Email), EmailVerifiedAt: &now}
			u.StartTrial(now)
			if err = db.Create(ctx, u); err == nil {
				err = welcome(ctx, u)
			}
		}
		if err != nil {
			return err
		}
		if u.EmailVerifiedAt == nil {
			return &social.NoAccountError{Message: "An account with this email address exists. Log in with your password and verify the address first."}
		}
		// Linked already to another account there: the address was
		// reused, not the same person.
		links, err := social.Links(ctx, u.AuthID())
		if err != nil {
			return err
		}
		if slices.ContainsFunc(links, func(l social.Account) bool { return l.Provider == p.Provider }) {
			return &social.NoAccountError{Message: "The account with this email address logs in with another account there."}
		}
		return social.Link(ctx, p, u.AuthID())
	})
	return u, err
}

// Login logs the user in, throttling repeated failures (AUTH_THROTTLE),
// and goes to the page they wanted, or AUTH_HOME_URL (the dashboard,
// unless set).
func (h Accounts) Login(c *web.Ctx, in LoginInput) (web.Responder, error) {
	_, err := h.Auth.Attempt(c, in.Email, in.Password, in.Remember)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, validate.Fail("email", "These credentials don't match our records.")
	case errors.As(err, &throttled):
		return nil, validate.Fail("email", "Too many login attempts. Try again in a minute.")
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, h.Auth.Config().HomeURL)), nil
}

// Logout logs the user out.
func (h Accounts) Logout(c *web.Ctx) error {
	if err := h.Auth.Logout(c); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/login")
}

// VerifyEmail marks the address of the link as verified. A bad or
// expired link, or one for an address the user has since changed, is a
// 400.
func (h Accounts) VerifyEmail(c *web.Ctx, in TokenQuery) (web.Responder, error) {
	u, email, err := h.Auth.CheckVerificationToken(c, in.Token)
	if err != nil {
		return nil, err
	}
	if email != u.Email {
		return nil, web.Error(http.StatusBadRequest, "This link is for another email address.")
	}
	if u.EmailVerifiedAt == nil {
		now := anetos.Now(c).UTC()
		if _, err := db.Query[models.User](c).Where(models.UserCols.ID.Eq(u.ID)).Update(models.UserCols.EmailVerifiedAt.Set(&now)); err != nil {
			return nil, err
		}
	}
	c.Session().Flash("status", "Your email address is verified.")
	return web.RedirectRoute("dashboard"), nil
}

// ResendVerification emails the verification link again.
func (h Accounts) ResendVerification(c *web.Ctx) error {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt == nil {
		// At most 6 an hour for one account, whatever the IP address.
		res, err := ratelimit.Allow(c, "verification:"+u.AuthID(), ratelimit.PerHour(6))
		if err != nil {
			return err
		}
		if !res.Allowed {
			return web.Error(http.StatusTooManyRequests, "Too many emails. Try again later.")
		}
		if err := h.sendVerification(c, u); err != nil {
			return err
		}
		c.Session().Flash("status", "We've emailed you a new verification link.")
	}
	return c.Redirect(http.StatusSeeOther, "/dashboard")
}

// ForgotPage shows the forgotten-password form.
func (Accounts) ForgotPage(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.ForgotPassword())
}

// SendReset emails a password reset link to the address, if it has an
// account (and was sent fewer than 3 in the hour), and answers the same
// either way, so the form doesn't reveal who has one.
func (h Accounts) SendReset(c *web.Ctx, in ForgotInput) (web.Responder, error) {
	res, err := ratelimit.Allow(c, "password-reset:"+strings.ToLower(in.Email), ratelimit.PerHour(3))
	if err != nil {
		return nil, err
	}
	u, err := models.Users.ByLogin(c, in.Email)
	switch {
	case err == nil && !res.Allowed:
		// Enough links for now: answer as usual.
	case err == nil:
		link, err := mailer.URL(c, "/reset-password?token="+url.QueryEscape(h.Auth.PasswordResetToken(u)))
		if err != nil {
			return nil, err
		}
		if err := mailer.Queue(c, mailers.ResetPassword{Name: u.Name, Email: u.Email, URL: link}); err != nil {
			return nil, err
		}
	case !errors.Is(err, db.ErrNotFound):
		return nil, err
	}
	c.Session().Flash("status", "If that address has an account, we've emailed it a link.")
	return web.RedirectRoute("login"), nil
}

// ResetPage shows the new-password form of a reset link.
func (Accounts) ResetPage(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.ResetPassword(view.Old(c, "token", c.Request().URL.Query().Get("token"))))
}

// Reset sets the new password. The link works once: it is tied to the
// old password. The new one logs out every session and remembered
// browser, and revokes the API tokens.
func (h Accounts) Reset(c *web.Ctx, in ResetInput) (web.Responder, error) {
	u, err := h.Auth.CheckPasswordResetToken(c, in.Token)
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
	n, err := db.Query[models.User](c).Where(models.UserCols.ID.Eq(u.ID), models.UserCols.Password.Eq(u.Password)).
		Update(models.UserCols.Password.Set(hash))
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, validate.Fail("password", "This reset link is invalid or has expired. Ask for a new one.")
	}
	if err := h.Auth.RevokeAllTokens(c, u); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Your password has been reset. You can log in now.")
	return web.RedirectRoute("login"), nil
}

// Dashboard shows the account and its API tokens.
func (h Accounts) Dashboard(c *web.Ctx) error {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return err
	}
	tokens, err := h.Auth.Tokens(c, u)
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, views.Dashboard(u, tokens, view.Flash(c, "token")))
}

// CreateToken issues an API token, shown once on the dashboard.
func (h Accounts) CreateToken(c *web.Ctx, in NewTokenInput) (web.Responder, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return nil, err
	}
	// "*": every ability; name narrower ones and check them with
	// auth.TokenCan. The token expires in 90 days.
	plain, _, err := h.Auth.CreateToken(c, u, in.Name, []string{"*"}, 90*24*time.Hour)
	if err != nil {
		return nil, err
	}
	c.Session().Flash("token", plain)
	return web.RedirectRoute("dashboard"), nil
}

// RevokeToken deletes one of the user's API tokens.
func (h Accounts) RevokeToken(c *web.Ctx, in TokenID) (web.Responder, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return nil, err
	}
	if err := h.Auth.RevokeToken(c, u, in.ID); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Token revoked.")
	return web.RedirectRoute("dashboard"), nil
}

// Me is GET /api/me, for API clients: Authorization: Bearer <token>.
func (Accounts) Me(c *web.Ctx) error {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, u)
}

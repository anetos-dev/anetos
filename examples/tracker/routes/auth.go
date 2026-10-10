// SPDX-License-Identifier: Apache-2.0

package routes

import (
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"

	"anetos.dev/anetos/examples/tracker/app/handlers"
	"anetos.dev/anetos/examples/tracker/app/models"
)

// Auth adds the account routes (anetos make:auth). Guests may register,
// log in (with a password, Google or GitHub, then a two-factor code if
// they have it on) and reset their password; logged-in users have a
// dashboard, API tokens, two-factor authentication and logout; API clients use
// /api with a token.
func Auth(r *web.Router, sessions *session.Manager, a *auth.Auth[*models.User], s *social.Social[*models.User]) {
	// On /settings, users may change their email address, and not delete
	// their account: change these to suit the app.
	h := handlers.Accounts{Auth: a, Social: s, AllowEmailChange: true, AllowAccountDeletion: false}
	pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
	pages.Get("/verify-email", web.H(h.VerifyEmail)).Name("verification.verify")
	pages.Get("/settings/email/verify", web.H(h.VerifyEmailChange)).Name("settings.email.verify") // the link to a new address
	// The link to the old address, which undoes a change of address (it
	// works even if changes were turned off since).
	pages.Get("/settings/email/revert", h.RevertEmailPage).Name("settings.email.revert")
	pages.With(ratelimit.Middleware("email-revert", ratelimit.PerMinute(10))).Post("/settings/email/revert", web.H(h.RevertEmail))

	guests := pages.Group("", a.Guest) // logged-in users go to AUTH_HOME_URL
	guests.Get("/register", h.RegisterPage).Name("register")
	guests.With(ratelimit.Middleware("register", ratelimit.PerMinute(10))).Post("/register", web.H(h.Register))
	guests.Get("/login", h.LoginPage).Name("login")
	guests.Post("/login", web.H(h.Login))
	guests.Get("/forgot-password", h.ForgotPage).Name("password.request")
	guests.With(ratelimit.Middleware("forgot-password", ratelimit.PerMinute(5))).
		Post("/forgot-password", web.H(h.SendReset)).Name("password.email")
	guests.Get("/reset-password", h.ResetPage).Name("password.reset")
	guests.Post("/reset-password", web.H(h.Reset)).Name("password.update")
	// Log in with a provider: to its page, and back (404 for providers
	// whose settings aren't set).
	guests.Get("/auth/{provider}/redirect", s.Redirect).Name("social.redirect")
	guests.Get("/auth/{provider}/callback", s.Callback).Name("social.callback")
	// The code of two-factor authentication, after the password (AUTH_CHALLENGE_URL).
	guests.Get("/two-factor-challenge", h.ChallengePage).Name("two-factor.challenge")
	guests.Post("/two-factor-challenge", web.H(h.Challenge))

	loggedIn := pages.Group("", a.Require) // guests go to AUTH_LOGIN_URL
	loggedIn.Get("/dashboard", h.Dashboard).Name("dashboard")
	loggedIn.Post("/logout", h.Logout).Name("logout")
	loggedIn.With(ratelimit.Middleware("verification", ratelimit.PerMinute(3))).
		Post("/email/verification-notification", h.ResendVerification).Name("verification.send")
	loggedIn.Post("/tokens/{id}/delete", web.H(h.RevokeToken)).Name("tokens.delete")
	// The account settings (AUTH_SETTINGS_URL).
	loggedIn.Get("/settings", h.Settings).Name("settings")
	loggedIn.Post("/settings/profile", web.H(h.UpdateProfile)).Name("settings.profile")
	loggedIn.Post("/settings/password", web.H(h.ChangePassword)).Name("settings.password")
	loggedIn.Post("/settings/preferences", web.H(h.UpdatePreferences)).Name("settings.preferences")
	loggedIn.Post("/settings/email/cancel", h.CancelEmailChange).Name("settings.email.cancel")
	loggedIn.Get("/confirm-password", h.ConfirmPage).Name("password.confirm") // AUTH_CONFIRM_URL
	loggedIn.Post("/confirm-password", web.H(h.ConfirmPassword))

	// Two-factor authentication (AUTH_TWO_FACTOR_URL), a new email address, API
	// tokens and deleting the account, once the password is confirmed
	// again (AUTH_CONFIRM_TTL).
	secure := loggedIn.Group("", a.RequireConfirmed)
	if h.AllowEmailChange {
		secure.Post("/settings/email", web.H(h.ChangeEmail)).Name("settings.email")
	}
	if h.AllowAccountDeletion {
		secure.Post("/settings/delete", h.DeleteAccount).Name("settings.delete")
	}
	// A token works without the browser, so it needs the password again
	// too.
	secure.Post("/tokens", web.H(h.CreateToken)).Name("tokens.create")
	secure.Get("/two-factor", h.TwoFactorPage).Name("two-factor")
	secure.Post("/two-factor", h.StartTwoFactor).Name("two-factor.start")
	secure.Post("/two-factor/confirm", web.H(h.ConfirmTwoFactor)).Name("two-factor.confirm")
	secure.Post("/two-factor/recovery-codes", h.NewRecoveryCodes).Name("two-factor.recovery-codes")
	secure.Post("/two-factor/disable", h.DisableTwoFactor).Name("two-factor.disable")

	api := r.Group("/api", web.JSONErrors, a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
	api.Get("/me", web.H(h.Me)).Name("api.me")
}

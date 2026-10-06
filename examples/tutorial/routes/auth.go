package routes

import (
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"

	"tracker/app/handlers"
	"tracker/app/models"
)

// Auth adds the account routes (anetos make:auth). Guests may register,
// log in (with a password, Google or GitHub, then a two-factor code if
// they have it on) and reset their password; signed-in users have a
// dashboard, API tokens, two-factor sign-in and logout; API clients use
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

	guests := pages.Group("", a.Guest) // signed-in users go to AUTH_HOME_URL
	guests.Get("/register", h.RegisterPage).Name("register")
	guests.With(ratelimit.Middleware("register", ratelimit.PerMinute(10))).Post("/register", web.H(h.Register))
	guests.Get("/login", h.LoginPage).Name("login")
	guests.Post("/login", web.H(h.Login))
	guests.Get("/forgot-password", h.ForgotPage).Name("password.request")
	guests.With(ratelimit.Middleware("forgot-password", ratelimit.PerMinute(5))).
		Post("/forgot-password", web.H(h.SendReset)).Name("password.email")
	guests.Get("/reset-password", h.ResetPage).Name("password.reset")
	guests.Post("/reset-password", web.H(h.Reset)).Name("password.update")
	// Sign in with a provider: to its page, and back (404 for providers
	// whose settings aren't set).
	guests.Get("/auth/{provider}/redirect", s.Redirect).Name("social.redirect")
	guests.Get("/auth/{provider}/callback", s.Callback).Name("social.callback")
	// The code of two-factor sign-in, after the password (AUTH_CHALLENGE_URL).
	guests.Get("/two-factor-challenge", h.ChallengePage).Name("two-factor.challenge")
	guests.Post("/two-factor-challenge", web.H(h.Challenge))

	members := pages.Group("", a.Require) // guests go to AUTH_LOGIN_URL
	members.Get("/dashboard", h.Dashboard).Name("dashboard")
	members.Post("/logout", h.Logout).Name("logout")
	members.With(ratelimit.Middleware("verification", ratelimit.PerMinute(3))).
		Post("/email/verification-notification", h.ResendVerification).Name("verification.send")
	members.Post("/tokens/{id}/delete", web.H(h.RevokeToken)).Name("tokens.destroy")
	// The account settings (AUTH_SETTINGS_URL).
	members.Get("/settings", h.Settings).Name("settings")
	members.Post("/settings/profile", web.H(h.UpdateProfile)).Name("settings.profile")
	members.Post("/settings/password", web.H(h.ChangePassword)).Name("settings.password")
	members.Post("/settings/preferences", web.H(h.UpdatePreferences)).Name("settings.preferences")
	members.Post("/settings/email/cancel", h.CancelEmailChange).Name("settings.email.cancel")
	members.Get("/confirm-password", h.ConfirmPage).Name("password.confirm") // AUTH_CONFIRM_URL
	members.Post("/confirm-password", web.H(h.ConfirmPassword))

	// region: routes-issues
	// The tracker's pages, for signed-in users.
	var issues handlers.Issues
	members.Get("/issues", web.H(issues.Index)).Name("issues.index")
	members.Get("/issues/new", issues.New).Name("issues.new")
	members.Post("/issues", web.H(issues.Create)).Name("issues.store")
	members.Get("/issues/{id}/edit", web.H(issues.Edit)).Name("issues.edit")
	members.Put("/issues/{id}", web.H(issues.Update)).Name("issues.update")
	// endregion
	// region: routes-issue-page
	members.Get("/issues/{id}", web.H(issues.Show)).Name("issues.show")
	members.Post("/issues/{id}/comments", web.H(issues.Comment)).Name("comments.store")
	members.Post("/issues/{id}/status", web.H(issues.SetStatus)).Name("issues.status")
	// endregion
	// region: routes-search
	members.Get("/search", web.H(issues.Search)).Name("search")
	// endregion

	// Two-factor sign-in (AUTH_TWO_FACTOR_URL), a new email address, API
	// tokens and deleting the account, once the password is confirmed
	// again (AUTH_CONFIRM_TTL).
	secure := members.Group("", a.RequireConfirmed)
	if h.AllowEmailChange {
		secure.Post("/settings/email", web.H(h.ChangeEmail)).Name("settings.email")
	}
	if h.AllowAccountDeletion {
		secure.Post("/settings/delete", h.DeleteAccount).Name("settings.delete")
	}
	// A token works without the browser, so it needs the password again
	// too.
	secure.Post("/tokens", web.H(h.CreateToken)).Name("tokens.store")
	secure.Get("/two-factor", h.TwoFactorPage).Name("two-factor")
	secure.Post("/two-factor", h.StartTwoFactor).Name("two-factor.start")
	secure.Post("/two-factor/confirm", web.H(h.ConfirmTwoFactor)).Name("two-factor.confirm")
	secure.Post("/two-factor/recovery-codes", h.NewRecoveryCodes).Name("two-factor.recovery-codes")
	secure.Post("/two-factor/disable", h.DisableTwoFactor).Name("two-factor.disable")

	api := r.Group("/api", a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
	api.Get("/me", h.Me).Name("api.me")
}

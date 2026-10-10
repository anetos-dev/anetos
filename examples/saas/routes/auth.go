// SPDX-License-Identifier: Apache-2.0

package routes

import (
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"

	"anetos.dev/anetos/examples/saas/app/handlers"
	"anetos.dev/anetos/examples/saas/app/models"
)

// Auth adds the account routes (anetos make:auth). Guests may register,
// log in (with a password, Google or GitHub) and reset their password;
// logged-in users have a dashboard, API tokens and logout; API clients
// use /api with a token.
func Auth(r *web.Router, sessions *session.Manager, a *auth.Auth[*models.User], s *social.Social[*models.User]) {
	h := handlers.Accounts{Auth: a, Social: s}
	pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
	pages.Get("/verify-email", web.H(h.VerifyEmail)).Name("verification.verify")

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

	members := pages.Group("", a.Require) // guests go to AUTH_LOGIN_URL
	members.Get("/dashboard", h.Dashboard).Name("dashboard")
	members.Post("/logout", h.Logout).Name("logout")
	members.With(ratelimit.Middleware("verification", ratelimit.PerMinute(3))).
		Post("/email/verification-notification", h.ResendVerification).Name("verification.send")
	members.Post("/tokens", web.H(h.CreateToken)).Name("tokens.store")
	members.Post("/tokens/{id}/delete", web.H(h.RevokeToken)).Name("tokens.destroy")

	api := r.Group("/api", a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
	api.Get("/me", h.Me).Name("api.me")
}

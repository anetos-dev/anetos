// SPDX-License-Identifier: Apache-2.0

package routes

import (
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"

	"bookmarks/app/handlers"
	"bookmarks/app/models"
)

// Auth adds the account routes (anetos make:auth) to the API, version 1,
// as in api.go. Clients log in with a token: register or log in (with a
// two-factor code if the user has it on) to get one, then send it in
// every request as "Authorization: Bearer <token>".
func Auth(r *web.Router, a *auth.Auth[*models.User]) {
	h := handlers.Accounts{Auth: a}
	api := r.Group("/api/v1", a.TokenMiddleware).As("api.")

	api.With(ratelimit.Middleware("register", ratelimit.PerMinute(10))).
		Post("/register", web.H(h.Register)).Name("register").Status(http.StatusCreated)
	api.Post("/login", web.H(h.Login)).Name("login") // throttled by package auth (AUTH_THROTTLE)
	api.Post("/login/two-factor", web.H(h.LoginTwoFactor)).Name("login.two-factor")
	api.With(ratelimit.Middleware("forgot-password", ratelimit.PerMinute(5))).
		Post("/forgot-password", web.H(h.ForgotPassword)).Name("password.email")
	api.With(ratelimit.Middleware("reset-password", ratelimit.PerMinute(10))).
		Post("/reset-password", web.H(h.ResetPassword)).Name("password.reset")
	api.With(ratelimit.Middleware("verify-email", ratelimit.PerMinute(10))).
		Post("/verify-email", web.H(h.VerifyEmail)).Name("verification.verify")

	loggedIn := api.Group("", a.Require) // 401 without a valid token
	loggedIn.Get("/me", web.H(h.Me)).Name("me")
	loggedIn.Post("/logout", web.H(h.Logout)).Name("logout")
	loggedIn.With(ratelimit.Middleware("verification", ratelimit.PerMinute(3))).
		Post("/email/verification-notification", web.H(h.ResendVerification)).Name("verification.send")
	// region: logged-in
	Bookmarks(loggedIn) // the user's bookmarks, with their token
	// endregion

	// The account itself, for tokens with every ability ("*", a login's):
	// a token made for a program, with narrower ones, gets 403. Changing
	// the password, creating a token, and starting, turning off or
	// renewing two-factor authentication also take the current password in the
	// request: a stolen token alone can't change how the account logs in.
	account := loggedIn.Group("", auth.RequireAbilities("*"))
	account.Put("/password", web.H(h.ChangePassword)).Name("password.change")
	account.Get("/tokens", web.H(h.Tokens)).Name("tokens.index")
	account.Post("/tokens", web.H(h.CreateToken)).Name("tokens.create").Status(http.StatusCreated)
	account.Delete("/tokens/{id}", web.H(h.RevokeToken)).Name("tokens.delete")
	account.Get("/two-factor", web.H(h.TwoFactor)).Name("two-factor")
	account.Post("/two-factor", web.H(h.StartTwoFactor)).Name("two-factor.start")
	account.Post("/two-factor/confirm", web.H(h.ConfirmTwoFactor)).Name("two-factor.confirm")
	account.Post("/two-factor/recovery-codes", web.H(h.NewRecoveryCodes)).Name("two-factor.recovery-codes")
	account.Post("/two-factor/disable", web.H(h.DisableTwoFactor)).Name("two-factor.disable")
}

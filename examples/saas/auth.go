// SPDX-License-Identifier: Apache-2.0

package main

import (
	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/saas/app/handlers"
	"anetos.dev/anetos/examples/saas/app/models"
	"anetos.dev/anetos/examples/saas/routes"
)

// setupAuth adds accounts (anetos make:auth): the migrations of the
// api_tokens and social_accounts tables, package auth with the app's
// users (AUTH_* settings), sign-in with Google and GitHub (package
// social), and the account routes. setup calls it after the routes.
func setupAuth(app *anetos.App, r *web.Router, sessions *session.Manager) (*auth.Auth[*models.User], error) {
	runner, err := anetos.Resolve[*migrate.Runner](app)
	if err != nil {
		return nil, err
	}
	if err := runner.Add(auth.Migrations(), social.Migrations()); err != nil {
		return nil, err
	}
	// Signing in leads to the dashboard, unless AUTH_HOME_URL names
	// another page (or this default changes).
	a, err := auth.ForApp(app, models.Users, auth.DefaultHomeURL("/dashboard"))
	if err != nil {
		return nil, err
	}
	// Each provider is on once its SOCIAL_<NAME>_CLIENT_ID and
	// _CLIENT_SECRET are set; users come back to APP_URL/auth/<name>/callback.
	s, err := social.ForApp(app, a, handlers.SocialUser, social.Configured(app, social.Google(), social.GitHub()))
	if err != nil {
		return nil, err
	}
	routes.Auth(r, sessions, a, s)
	return a, nil
}

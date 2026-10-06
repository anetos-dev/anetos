// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"net/http"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/session"
)

// ActingAs signs u in for the requests that follow, as a password sign-in
// without remember-me would, so a test needn't post the login form. It
// replaces whoever was signed in, and drops a remember-me cookie. U is
// the user type given to
// auth.ForApp in setup (*models.User):
//
//	app := anetostest.New(t, setup)
//	ada := anetostest.Create(app, factories.Users)
//	anetostest.ActingAs(app, &ada).Get("/dashboard").AssertOK()
//
// It needs sessions (session.ForApp) and auth.ForApp in setup; a
// disabled user fails the test.
func ActingAs[U auth.Authenticatable](a *App, u U) *App {
	a.t.Helper()
	au, err := anetos.Resolve[*auth.Auth[U]](a.App)
	if err != nil {
		a.t.Fatalf("anetostest: ActingAs needs auth.ForApp in setup, with users of type %T: %v", u, err)
	}
	var loginErr error
	a.WithSession(func(s *session.Session) { loginErr = au.LoginSession(s, u) })
	if loginErr != nil {
		a.t.Fatalf("anetostest: ActingAs: %v", loginErr)
	}
	// An earlier user's remember-me cookie would sign them back in if
	// the session went.
	a.jar.set([]*http.Cookie{{Name: au.RememberCookie(), Path: "/", MaxAge: -1}})
	return a
}

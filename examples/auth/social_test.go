// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/db"
)

// region: test-social
func TestSocialLogin(t *testing.T) {
	// FakeSocial: Google and GitHub log in through a stand-in provider.
	app := anetostest.New(t, setup, anetostest.FakeSocial())
	app.Get("/login").AssertSee(`href="/auth/google/redirect"`, "Log in with Google")

	// A new account: a user is created, with the address verified.
	grace := anetostest.SocialAccount{ID: "g-1", Email: "grace@example.com", EmailVerified: true, Name: "Grace"}
	app.SocialLogin("/auth/google/redirect", grace).AssertRedirect("/dashboard") // AUTH_HOME_URL
	app.Get("/dashboard").AssertSee("Hello, Grace").AssertDontSee("Please verify")
	app.PostForm("/logout", nil)

	// The same account again, with a new address: the linked user.
	grace.Email = "grace@new.example"
	app.SocialLogin("/auth/google/redirect", grace).AssertRedirect("/dashboard")
	n, err := db.RawFirst[int64](app.Context(), "SELECT COUNT(*) FROM users")
	if err != nil || n != 1 {
		t.Errorf("users: %d, %v", n, err)
	}
}

// endregion

func TestSocialLinking(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeSocial())
	verified := createUser(t, app, "Ada", "ada@example.com", false)
	now := time.Now().UTC()
	if _, err := db.Query[User](app.Context()).Where(colID.Eq(verified.ID)).Update(colVerified.Set(&now)); err != nil {
		t.Fatal(err)
	}
	createUser(t, app, "Mallory", "victim@example.com", false) // registered first, never verified

	// A verified address finds the existing, verified user.
	app.SocialLogin("/auth/github/redirect", anetostest.SocialAccount{ID: "s-ada", Email: "ada@example.com", EmailVerified: true}).
		AssertRedirect("/dashboard")
	app.Get("/dashboard").AssertSee("Hello, Ada")
	app.PostForm("/logout", nil)

	// An unverified address at the provider finds no one.
	app.SocialLogin("/auth/github/redirect", anetostest.SocialAccount{ID: "s-x", Email: "ada@example.com"}).
		AssertRedirect("/login").Follow().AssertSee("has no verified email address")

	// Nor does an address no one verified here (a pre-registered account).
	app.SocialLogin("/auth/google/redirect", anetostest.SocialAccount{ID: "s-victim", Email: "victim@example.com", EmailVerified: true}).
		AssertRedirect("/login").Follow().AssertSee("An account with this email address exists")

	// Another GitHub account with Ada's address (reused) isn't linked to
	// her: she logs in with the first one.
	app.SocialLogin("/auth/github/redirect", anetostest.SocialAccount{ID: "s-other", Email: "ada@example.com", EmailVerified: true}).
		AssertRedirect("/login").Follow().AssertSee("logs in with another github account")
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/url"
	"testing"
	"time"

	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/anetostest"

	"anetos.dev/anetos/examples/saas/app/jobs"
	"anetos.dev/anetos/examples/saas/app/listeners"
	"anetos.dev/anetos/examples/saas/app/mailers"
	"anetos.dev/anetos/examples/saas/app/models"
	"anetos.dev/anetos/examples/saas/app/tasks"
)

// signUp registers ada and returns the app, signed in.
func signUp(t *testing.T, app *anetostest.App) {
	t.Helper()
	app.Get("/register")
	app.PostForm("/register", url.Values{
		"name": {"Ada"}, "email": {"ada@example.com"},
		"password": {"correct horse"}, "password_confirmation": {"correct horse"},
	}).AssertRedirect("/dashboard")
}

func TestWelcome(t *testing.T) {
	app := anetostest.New(t, setup) // QUEUE_DRIVER=sync in tests: jobs run when dispatched
	now := app.Freeze(time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	signUp(t, app)

	anetostest.AssertDispatched(app, func(j jobs.SendWelcome) bool { return j.UserID > 0 })
	anetostest.AssertMailSent(app, func(m mailers.Welcome) bool {
		return m.Email == "ada@example.com" && m.TrialEnds.Equal(now.AddDate(0, 0, models.TrialDays))
	})
	app.Get("/dashboard").AssertSee("Plan: trial, until October 16")
}

// A first sign-in with Google creates a user and welcomes them; the next
// sign-in doesn't.
func TestWelcomeAfterSocialSignIn(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeSocial())
	grace := anetostest.SocialAccount{ID: "g-1", Email: "grace@example.com", EmailVerified: true, Name: "Grace"}
	app.SocialSignIn("/auth/google/redirect", grace).AssertRedirect("/dashboard")
	app.PostForm("/logout", nil)
	app.SocialSignIn("/auth/google/redirect", grace).AssertRedirect("/dashboard")
	if sent := anetostest.Mailables[mailers.Welcome](app); len(sent) != 1 || sent[0].Name != "Grace" {
		t.Errorf("welcome emails: %+v", sent)
	}
}

func TestChangePlan(t *testing.T) {
	app := anetostest.New(t, setup)
	signUp(t, app)

	if err := listeners.ChangePlan(app.Context(), listeners.SubscriptionChanged{Email: "Ada@Example.com", Plan: "pro"}); err != nil {
		t.Fatal(err)
	}
	anetostest.AssertDatabaseHas[models.User](app, models.UserCols.Plan.Eq("pro"), models.UserCols.TrialEndsAt.IsNull())
	app.Get("/dashboard").AssertSee("Plan: pro")

	// Messages that can't apply go to the dead-letter topic at once.
	for _, m := range []listeners.SubscriptionChanged{{Email: "ada@example.com", Plan: "gold"}, {Email: "nobody@example.com", Plan: "pro"}} {
		if err := listeners.ChangePlan(app.Context(), m); !pubsub.IsPermanent(err) {
			t.Errorf("%+v: %v", m, err)
		}
	}
}

func TestEndTrials(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Freeze(time.Time{})
	signUp(t, app)

	app.Travel(models.TrialDays*24*time.Hour - time.Minute) // not yet
	if err := tasks.EndTrials(app.Context()); err != nil {
		t.Fatal(err)
	}
	anetostest.AssertDatabaseHas[models.User](app, models.UserCols.Plan.Eq("trial"))

	app.Travel(time.Minute)
	if err := tasks.EndTrials(app.Context()); err != nil {
		t.Fatal(err)
	}
	anetostest.AssertDatabaseHas[models.User](app, models.UserCols.Plan.Eq("free"), models.UserCols.TrialEndsAt.IsNull())
}

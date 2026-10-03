// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/i18n"
)

// region: test
func TestLanguages(t *testing.T) {
	app := anetostest.New(t, setup)

	// English by default; plurals follow the count.
	app.Get("/?plants=1").AssertOK().AssertSee(`lang="en"`, "Welcome, Ada!", "You have 1 plant.")
	app.Get("/?plants=3").AssertSee("You have 3 plants.")

	// The browser's languages.
	app.WithHeader("Accept-Language", "bn-BD, en;q=0.8")
	app.Get("/?name=Rafi&plants=2").AssertSee(`lang="bn"`, "স্বাগতম, Rafi!", "আপনার 2টি গাছ আছে।")

	// Validation messages and labels in the visitor's language.
	app.PostJSON("/signup", map[string]any{"email": "nope", "plant_count": 0}).AssertUnprocessable().
		AssertJSONPath("errors.name", "নাম দিতে হবে।").
		AssertJSONPath("errors.email", "ইমেইল একটি সঠিক ইমেইল ঠিকানা হতে হবে।").
		AssertJSONPath("errors.plant_count", "গাছের সংখ্যা কমপক্ষে 1 হতে হবে।")

	// A chosen language (?locale=, the switcher's links) beats the
	// browser's, and is remembered.
	app.Get("/?locale=en").AssertRedirect("/")
	app.Get("/").AssertSee(`lang="en"`, "Welcome, Ada!", `href="/?locale=bn"`)
}

// endregion

// TestCatalogs fails when Bangla lacks a key of English, or a key the
// code uses is in no catalog.
func TestCatalogs(t *testing.T) {
	app := anetostest.New(t, setup)
	var out bytes.Buffer
	if n := anetos.MustResolve[*i18n.Translator](app.App).Check(&out, nil); n > 0 {
		t.Errorf("lang:check:\n%s", out.String())
	}
}

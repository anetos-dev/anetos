// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/web/openapi"

	"bookmarks/routes"
)

// anetostest.New boots the app with test settings (.env.testing, not
// .env) and a migrated database, which the test leaves as it found it.
func TestWelcome(t *testing.T) {
	app := anetostest.New(t, setup)
	app.GetJSON("/api/v1").AssertOK().
		AssertJSONPath("name", "bookmarks").
		AssertJSONPath("message", "Welcome to the Bookmarks API.")
}

// Errors are JSON problem details, whatever the client accepts.
func TestNotFound(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Get("/api/v1/no-such-thing").AssertNotFound().
		AssertHeader("Content-Type", "application/problem+json").
		AssertJSONPath("status", 404)
}

// openapi.json describes the routes as they are: after changing a route
// or a handler's types, update it with `go run . openapi`.
func TestOpenAPI(t *testing.T) {
	app := anetostest.New(t, setup)
	if err := openapi.Check(app.Router(), routes.OpenAPI); err != nil {
		t.Fatal(err)
	}
}

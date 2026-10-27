// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/web/typescript"

	"spa/routes"
)

// frontend/src/types/api.ts, the SPA's client, matches the API: after
// changing a route or a handler's types, update it with `go run . types`
// (anetos dev does). Without it, the SPA's build would type-check against
// an API that isn't there.
func TestTypes(t *testing.T) {
	app := anetostest.New(t, setup)
	if err := typescript.Check(app.Router(), typescript.Config{Prefix: routes.OpenAPI.Prefix}); err != nil {
		t.Fatal(err)
	}
}

// The paths no route takes are the SPA's: a browser gets its page, whose
// router shows the path's view (index.html once the frontend is built;
// without the build, in public/spa, a page that says to build it). The
// API's paths are never the SPA's: a missing one is JSON's 404.
// region: spa-test
func TestSPAPages(t *testing.T) {
	app := anetostest.New(t, setup)
	app.WithHeader("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	for _, path := range []string{"/", "/archived", "/verify-email?token=x"} {
		app.Get(path).AssertHeader("Content-Type", "text/html; charset=utf-8")
	}
	app.Get("/api/v1/no-such-thing").AssertNotFound().
		AssertHeader("Content-Type", "application/problem+json")
	app.Get("/api/v1/bookmarks").AssertStatus(http.StatusUnauthorized).
		AssertHeader("Content-Type", "application/problem+json")
}

// endregion

// SPDX-License-Identifier: Apache-2.0

// Package handlers holds the HTTP handlers.
package handlers

import (
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/views"
)

// Home serves the home page.
type Home struct{}

// Show renders the home page, for guests; logged-in users go to their
// projects.
func (Home) Show(c *web.Ctx) error {
	if _, ok := auth.User[*models.User](c); ok {
		return c.RedirectRoute("projects.index")
	}
	return c.Render(http.StatusOK, views.Home())
}

// Package handlers holds the HTTP handlers.
package handlers

import (
	"net/http"

	"anetos.dev/anetos/web"

	"tracker/views"
)

// Home serves the home page.
type Home struct{}

// Show renders the home page.
func (Home) Show(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.Home())
}

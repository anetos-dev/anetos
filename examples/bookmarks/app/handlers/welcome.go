// SPDX-License-Identifier: Apache-2.0

// Package handlers holds the HTTP handlers.
package handlers

import (
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/web"
)

// Welcome answers the API's root.
type Welcome struct{}

// WelcomeResponse is GET /api/v1's JSON. Answer with structs of your own
// like this one, rather than models, so that what clients get is chosen
// field by field.
type WelcomeResponse struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Show responds to GET /api/v1. Routed with web.H, it is typed: its
// second parameter is the request's input (none here), and its result is
// written as JSON.
func (Welcome) Show(c *web.Ctx, _ struct{}) (WelcomeResponse, error) {
	return WelcomeResponse{
		Name:    "bookmarks",
		Message: i18n.T(c, "api.welcome", "app", "Bookmarks"),
	}, nil
}

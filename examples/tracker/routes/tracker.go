// SPDX-License-Identifier: Apache-2.0

package routes

import (
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/openapi"
	"anetos.dev/anetos/web/ratelimit"

	"anetos.dev/anetos/examples/tracker/app/handlers"
	"anetos.dev/anetos/examples/tracker/app/models"
)

// Tracker adds the tracker's pages, for logged-in users, and its JSON
// API, for API tokens. Each handler checks the user's role in the
// project (handlers.loadProject).
func Tracker(r *web.Router, sessions *session.Manager, a *auth.Auth[*models.User]) {
	pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
	pages.Get("/", handlers.Home{}.Show).Name("home")

	var projects handlers.Projects
	var issues handlers.Issues
	m := pages.Group("", a.Require) // guests go to the login page
	m.Get("/projects", projects.Index).Name("projects.index")
	m.Get("/projects/new", projects.New).Name("projects.new")
	m.With(ratelimit.Middleware("projects", ratelimit.PerMinute(10))).Post("/projects", web.H(projects.Create)).Name("projects.create")
	m.Get("/search", web.H(handlers.Search)).Name("search")

	p := m.Group("/p/{project}")
	p.Get("", web.H(projects.Show)).Name("projects.show")
	p.Get("/settings", web.H(projects.Settings)).Name("projects.settings")
	p.Put("/settings", web.H(projects.Update)).Name("projects.update")
	p.Post("/archive", web.H(projects.Archive)).Name("projects.archive")
	p.Post("/members", web.H(projects.AddMember)).Name("members.create")
	p.Put("/members/{user}", web.H(projects.UpdateMember)).Name("members.update")
	p.Delete("/members/{user}", web.H(projects.RemoveMember)).Name("members.delete")
	p.Post("/labels", web.H(projects.CreateLabel)).Name("labels.create")
	p.Delete("/labels/{label}", web.H(projects.DeleteLabel)).Name("labels.delete")

	p.Get("/issues/new", web.H(issues.New)).Name("issues.new")
	p.Post("/issues", web.H(issues.Create)).Name("issues.create")
	p.Get("/issues/{number}", web.H(issues.Show)).Name("issues.show")
	p.Get("/issues/{number}/edit", web.H(issues.Edit)).Name("issues.edit")
	p.Put("/issues/{number}", web.H(issues.Update)).Name("issues.update")
	p.Delete("/issues/{number}", web.H(issues.Delete)).Name("issues.delete")
	p.Post("/issues/{number}/status", web.H(issues.SetStatus)).Name("issues.status")
	p.With(ratelimit.Middleware("comments", ratelimit.PerMinute(30))).
		Post("/issues/{number}/comments", web.H(issues.Comment)).Name("comments.create")
	p.Post("/issues/{number}/files", web.H(issues.Attach)).Name("attachments.create")
	p.Get("/files/{id}", web.H(issues.Download)).Name("attachments.show")
	p.Delete("/files/{id}", web.H(issues.DeleteAttachment)).Name("attachments.delete")

	// region: api
	// The API's errors are JSON problem details, whatever the client
	// accepts, and a guest gets a 401, not the login page.
	var api handlers.API
	v1 := r.Group("/api", web.JSONErrors, a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
	v1.Get("/projects", web.H(api.Projects)).Name("api.projects")
	v1.Get("/projects/{project}/issues", web.H(api.Issues)).Name("api.issues")
	v1.Post("/projects/{project}/issues", web.H(api.CreateIssue)).Name("api.issues.create").Status(http.StatusCreated)
	v1.Get("/projects/{project}/issues/{number}", web.H(api.Issue)).Name("api.issues.show")
	// endregion
}

// region: openapi

// OpenAPI describes the API from its typed handlers: `go run . openapi`
// writes openapi.json, which api_test.go checks is up to date, and the
// app serves it at /api/openapi.json.
var OpenAPI = openapi.Config{
	Title:       "Tracker",
	Version:     "1.0.0",
	Description: "Projects and issues, for clients with an API token: create one on the tokens page.",
	Prefix:      "/api",
	Path:        "/api/openapi.json",
}

// endregion

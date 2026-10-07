// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"

	"anetos.dev/anetos/examples/tracker/app/handlers"
	"anetos.dev/anetos/examples/tracker/app/models"
)

// token issues an API token for u with the abilities, and sends it with
// the requests that follow.
func (w *world) token(t *testing.T, u models.User, abilities ...string) *anetostest.App {
	t.Helper()
	a := anetos.MustResolve[*auth.Auth[*models.User]](w.app.App)
	plain, _, err := a.CreateToken(w.app.Context(), &u, "test", abilities, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return w.app.WithHeader("Authorization", "Bearer "+plain)
}

func TestAPI(t *testing.T) {
	w := newSearchWorld(t)
	w.issue(func(i *models.Issue) { i.Title, i.AssigneeID = "Crash on save", &w.member.ID })
	w.issue(func(i *models.Issue) { i.Title, i.Status = "Old", models.Closed })
	if err := addLabel(w, 1, w.bug); err != nil {
		t.Fatal(err)
	}
	api := w.token(t, w.member, "*")

	var projects []handlers.APIProject
	api.GetJSON("/api/projects").AssertOK().JSON(&projects)
	if len(projects) != 1 || projects[0].Key != "WEB" {
		t.Errorf("projects: %+v", projects)
	}

	var page handlers.APIIssuePage
	api.GetJSON("/api/projects/WEB/issues").AssertOK().JSON(&page)
	if page.Total != 1 || page.Issues[0].Ref != "WEB-1" || page.Issues[0].Assignee != w.member.Email ||
		len(page.Issues[0].Labels) != 1 || page.Issues[0].Labels[0] != "bug" {
		t.Errorf("open issues: %+v", page)
	}
	api.GetJSON("/api/projects/WEB/issues?status=all").AssertJSONPath("total", float64(2))
	api.GetJSON("/api/projects/WEB/issues?q=crash").AssertJSONPath("total", float64(1))
	api.GetJSON("/api/projects/WEB/issues/2").AssertOK().AssertJSONPath("status", "closed")
	api.GetJSON("/api/projects/WEB/issues/9").AssertNotFound()
	api.Get("/api/projects/WEB/issues/9").AssertNotFound().AssertHeader("Content-Type", "application/problem+json")

	var created handlers.APIIssue
	api.PostJSON("/api/projects/WEB/issues", map[string]any{"title": "From the API", "labels": []string{"Idea", "bug"}}).
		AssertCreated().JSON(&created)
	if created.Ref != "WEB-3" || created.Priority != "normal" || created.Author != w.member.Email || len(created.Labels) != 2 {
		t.Errorf("created: %+v", created)
	}
	api.PostJSON("/api/projects/WEB/issues", map[string]any{"title": ""}).AssertUnprocessable()
	api.PostJSON("/api/projects/WEB/issues", map[string]any{"title": "x", "labels": []string{"nope"}}).AssertUnprocessable()

	// The project's roles apply: a viewer reads, doesn't open issues; an
	// outsider sees nothing.
	w.token(t, w.viewer, "*").PostJSON("/api/projects/WEB/issues", map[string]any{"title": "x"}).AssertForbidden()
	w.token(t, w.outsider, "*").GetJSON("/api/projects/WEB/issues").AssertNotFound()

	// A read-only token caps its user's permissions.
	readOnly := w.token(t, w.member, "issues.view")
	readOnly.GetJSON("/api/projects/WEB/issues").AssertOK()
	readOnly.PostJSON("/api/projects/WEB/issues", map[string]any{"title": "x"}).AssertForbidden()

	w.app.WithHeader("Authorization", "")
	w.app.GetJSON("/api/projects").AssertStatus(http.StatusUnauthorized)
	// A browser's request gets the same: the API's errors are JSON.
	w.app.Get("/api/projects").AssertStatus(http.StatusUnauthorized).
		AssertHeader("Content-Type", "application/problem+json").AssertJSONPath("status", 401)
}

// The dashboard makes read-only tokens.
func TestReadOnlyToken(t *testing.T) {
	w := newWorld(t)
	app := w.as(w.member)
	app.Get("/dashboard").AssertSee(`name="read_only"`)
	app.PostForm("/confirm-password", form("password", "correct horse")) // factories.Password
	app.PostForm("/tokens", form("name", "CI", "read_only", "1")).AssertRedirect("/dashboard")
	tokens, err := anetos.MustResolve[*auth.Auth[*models.User]](w.app.App).Tokens(w.app.Context(), &w.member)
	if err != nil || len(tokens) != 1 || len(tokens[0].Abilities) != 1 || tokens[0].Abilities[0] != "issues.view" {
		t.Errorf("tokens: %+v, %v", tokens, err)
	}
}

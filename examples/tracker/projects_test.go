// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/tracker/app/models"
)

func TestHomeRedirectsLoggedInUsers(t *testing.T) {
	w := newWorld(t)
	w.app.Get("/").AssertOK().AssertSee("Track your team")
	w.app.Get("/projects").AssertRedirect("/login")
	w.as(w.member).Get("/").AssertRedirectRoute("projects.index")
}

func TestCreateProject(t *testing.T) {
	w := newWorld(t)
	app := w.as(w.outsider)             // until another w.as
	app.Get("/projects/new").AssertOK() // where failed posts go back to

	// The key: capital letters and digits, from a letter, not taken.
	for _, key := range []string{"", "w", "web", "1WEB", "WEB", "TOOLONGKEY1", "WЕB", "Ü1"} { // Cyrillic Е

		app.PostForm("/projects", form("name", "Docs", "key", key)).
			AssertRedirect("/projects/new").
			AssertValidationErrors("key")
	}
	app.PostForm("/projects", form("name", "Docs", "key", "DOCS", "description", "The manual.")).
		AssertRedirect("/p/DOCS").
		Follow().
		AssertSee("Docs", "Project created.", "No issues here.", "New issue")
	anetostest.AssertDatabaseHas[models.Project](app, models.ProjectCols.Key.Eq("DOCS"))

	// Its maker owns it, and sees it in the list; others don't.
	app.Get("/p/DOCS/settings").AssertOK().AssertSee(w.outsider.Email)
	app.Get("/projects").AssertSee("Docs").AssertDontSee("Website")
	w.as(w.member).Get("/projects").AssertSee("Website").AssertDontSee("Docs")
	w.as(w.member).Get("/p/DOCS").AssertNotFound()
}

func TestProjectList(t *testing.T) {
	w := newWorld(t)
	w.issue()
	w.issue(func(i *models.Issue) { i.Status = models.Closed })
	w.as(w.viewer).Get("/projects").AssertOK().AssertSee("Website", "WEB", "1 open", "1 closed")
}

// Who sees and does what: a project is a 404 to those without a role
// in it, viewers read and comment, members open and edit issues, owners
// manage the project.
func TestProjectAccess(t *testing.T) {
	w := newWorld(t)
	issue := w.issue()
	for _, u := range []models.User{w.owner, w.member, w.viewer} {
		w.as(u).Get(w.path("")).AssertOK().AssertSee(issue.Title)
		w.as(u).Get(w.path("issues/1")).AssertOK()
	}
	w.as(w.outsider).Get(w.path("")).AssertNotFound()
	w.as(w.outsider).Get(w.path("issues/1")).AssertNotFound()
	w.as(w.outsider).PostForm(w.path("issues/1/comments"), form("body", "Hi")).AssertNotFound()

	w.as(w.viewer).Get(w.path("")).AssertDontSee("New issue", `href="/p/WEB/settings"`)
	w.as(w.viewer).Get(w.path("issues/new")).AssertForbidden()
	w.as(w.viewer).PostForm(w.path("issues"), form("title", "x", "priority", "normal")).AssertForbidden()
	w.as(w.viewer).Get(w.path("issues/1/edit")).AssertForbidden()
	w.as(w.member).Get(w.path("")).AssertSee("New issue").AssertDontSee(`href="/p/WEB/settings"`)
	w.as(w.member).Get(w.path("settings")).AssertForbidden()
	w.as(w.owner).Get(w.path("settings")).AssertOK()

	// Administrators see every project.
	if err := rbac.Assign(w.app.Context(), w.outsider.AuthID(), rbac.Global, "admin"); err != nil {
		t.Fatal(err)
	}
	w.as(w.outsider).Get("/projects").AssertSee("Website")
	w.as(w.outsider).Get(w.path("settings")).AssertOK()
	w.as(w.outsider).Get("/p/NOPE").AssertNotFound()
}

func TestMembers(t *testing.T) {
	w := newWorld(t)
	w.as(w.owner).Get(w.path("settings")).AssertSee("Olivia", "Max", "Vera")

	// Add by email address: only people with an account.
	w.as(w.owner).PostForm(w.path("members"), form("email", "nobody@example.com", "role", "member")).
		AssertValidationErrors("email")
	w.as(w.owner).PostForm(w.path("members"), form("email", w.outsider.Email, "role", "viewer")).
		AssertRedirect(w.path("settings")).
		AssertSessionHas("status", "Otto is a member now.")
	w.as(w.outsider).Get(w.path("")).AssertOK()

	// Change a role, remove someone.
	w.as(w.owner).PutForm(w.path("members/"+id(w.outsider.ID)), form("role", "member")).AssertRedirect(w.path("settings"))
	w.as(w.outsider).Get(w.path("issues/new")).AssertOK()
	w.as(w.owner).DeleteForm(w.path("members/"+id(w.outsider.ID)), nil).AssertRedirect(w.path("settings"))
	w.as(w.outsider).Get(w.path("")).AssertNotFound()

	// A project keeps an owner.
	w.as(w.owner).DeleteForm(w.path("members/"+id(w.owner.ID)), nil).AssertValidationErrors("role")
	w.as(w.owner).PutForm(w.path("members/"+id(w.owner.ID)), form("role", "viewer")).AssertValidationErrors("role")
	w.as(w.owner).PutForm(w.path("members/"+id(w.member.ID)), form("role", "owner")).AssertRedirect(w.path("settings"))
	w.as(w.owner).DeleteForm(w.path("members/"+id(w.owner.ID)), nil).AssertRedirect(w.path("settings")) // leaves
	w.as(w.owner).Get(w.path("")).AssertNotFound()

	// Removing someone who isn't a member is a 404.
	w.as(w.owner).DeleteForm(w.path("members/"+id(w.outsider.ID)), nil).AssertNotFound()

	// Only owners manage members; no one gives a role above their own.
	w.as(w.viewer).PostForm(w.path("members"), form("email", w.outsider.Email, "role", "owner")).AssertForbidden()
	w.as(w.member).PostForm(w.path("members"), form("email", w.outsider.Email, "role", "superuser")).AssertValidationErrors("role")
}

func TestLabels(t *testing.T) {
	w := newWorld(t)
	w.as(w.owner).PostForm(w.path("labels"), form("label", " Design ", "color", "#7C3AED")).
		AssertRedirect(w.path("settings")).
		Follow().
		AssertSee("design", "--label:#7c3aed")
	w.as(w.owner).PostForm(w.path("labels"), form("label", "BUG", "color", "#000000")).AssertValidationErrors("label")
	w.as(w.owner).PostForm(w.path("labels"), form("label", "x", "color", "red")).AssertValidationErrors("color")
	w.as(w.owner).PostForm(w.path("labels"), form("label", "x", "color", "#0;}a{b")).AssertValidationErrors("color")

	issue := w.issue()
	w.as(w.owner).PutForm(w.path("issues/1"), form("title", issue.Title, "priority", "normal", "labels", id(w.bug.ID))).AssertRedirect(w.path("issues/1"))
	w.as(w.owner).DeleteForm(w.path("labels/"+id(w.bug.ID)), nil).AssertRedirect(w.path("settings"))
	w.as(w.owner).Get(w.path("issues/1")).AssertDontSee(">bug<")
	w.as(w.owner).DeleteForm(w.path("labels/"+id(w.bug.ID)), nil).AssertNotFound()
}

func TestArchive(t *testing.T) {
	w := newWorld(t)
	w.issue()
	w.as(w.owner).PostForm(w.path("archive"), nil).AssertRedirect(w.path("settings")).AssertSessionHas("status", "Project archived.")

	// Read-only: no new issues, edits or comments; still readable.
	w.as(w.member).Get(w.path("")).AssertOK().AssertSee("This project is archived").AssertDontSee("New issue")
	w.as(w.member).Get(w.path("issues/1")).AssertOK().AssertDontSee("Add a comment", "Close issue")
	w.as(w.member).PostForm(w.path("issues"), form("title", "x", "priority", "normal")).AssertForbidden()
	w.as(w.member).PostForm(w.path("issues/1/comments"), form("body", "x")).AssertForbidden()
	w.as(w.member).PostForm(w.path("issues/1/status"), form("status", "closed")).AssertForbidden()
	// Owners too: only the settings (name, archiving) still change.
	w.as(w.owner).DeleteForm(w.path("issues/1"), nil).AssertForbidden()
	w.as(w.owner).PostForm(w.path("labels"), form("label", "x", "color", "#000000")).AssertForbidden()
	w.as(w.owner).DeleteForm(w.path("labels/"+id(w.bug.ID)), nil).AssertForbidden()
	w.as(w.owner).PostForm(w.path("members"), form("email", w.outsider.Email, "role", "viewer")).AssertForbidden()
	w.as(w.owner).Get(w.path("settings")).AssertOK().AssertSee("Bring the project back").AssertDontSee(`name="email"`)
	w.as(w.owner).PutForm(w.path("settings"), form("name", "Old website")).AssertRedirect(w.path("settings"))

	w.as(w.owner).PostForm(w.path("archive"), nil).AssertSessionHas("status", "Project brought back.")
	w.as(w.member).PostForm(w.path("issues/1/comments"), form("body", "Back")).AssertRedirect(w.path("issues/1"))
}

// Saving a project loaded before issues were opened keeps their numbers
// (LastNumber is read-only in the model).
func TestStaleProjectSave(t *testing.T) {
	w := newWorld(t)
	stale := w.project
	w.issue()
	w.issue()
	stale.Name = "Renamed"
	if err := db.Update(w.app.Context(), &stale); err != nil {
		t.Fatal(err)
	}
	w.as(w.member).PostForm(w.path("issues"), form("title", "Third", "priority", "normal")).AssertRedirect(w.path("issues/3"))
}

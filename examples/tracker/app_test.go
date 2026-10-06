// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/database/migrations"
)

func TestAdminIssues(t *testing.T) {
	w := newWorld(t)
	w.issue(func(i *models.Issue) { i.Title = "Broken link" })
	if err := rbac.Assign(w.app.Context(), w.outsider.AuthID(), rbac.Global, "admin"); err != nil {
		t.Fatal(err)
	}
	w.as(w.owner).DeleteForm(w.path("issues/1"), nil).AssertRedirect(w.path(""))

	admin := w.as(w.outsider)
	admin.Get("/admin/projects").AssertOK().AssertSee("WEB", "Website")
	admin.Get("/admin/issues").AssertOK().AssertDontSee("Broken link")
	admin.Get("/admin/issues/trash").AssertOK().AssertSee("WEB-1", "Broken link")
	admin.Get("/admin/activity").AssertOK().AssertSee("issues")
}

func TestSeedAndCatalogs(t *testing.T) {
	app := anetostest.New(t, setup)
	run := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := app.ExecuteArgs(app.Context(), args, &out, &errOut); code != 0 || errOut.Len() > 0 {
			t.Fatalf("%v: %d\n%s%s", args, code, &out, &errOut)
		}
		return out.String()
	}
	for _, s := range migrations.Seeders { // what db:seed runs
		if err := s.Run(app.Context()); err != nil {
			t.Fatal(err)
		}
	}
	anetostest.AssertDatabaseCount[models.Issue](app, 8)
	anetostest.AssertDatabaseHas[models.Project](app, models.ProjectCols.Key.Eq("API"), models.ProjectCols.LastNumber.Eq(3))
	ada, err := db.Query[models.User](app.Context()).Where(models.UserCols.Email.Eq("ada@example.com")).First()
	if err != nil {
		t.Fatal(err)
	}
	anetostest.ActingAs(app, &ada).Get("/p/WEB").AssertOK().AssertSee("WEB-5")

	// Every key the pages use is in the catalogs (a command: the last
	// thing the app does).
	if out := run("lang:check"); !strings.Contains(out, "en OK") {
		t.Errorf("lang:check: %s", out)
	}
}

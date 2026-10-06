// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
)

// newUser creates a user and returns them with an API token.
func newUser(t *testing.T, app *anetostest.App, name string) (*User, string) {
	t.Helper()
	u := &User{Name: name, Email: strings.ToLower(name) + "@example.com"}
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	a := anetos.MustResolve[*auth.Auth[*User]](app.App)
	token, _, err := a.CreateToken(app.Context(), u, "test", []string{"*"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return u, token
}

func as(app *anetostest.App, token string) *anetostest.App {
	return app.WithHeader("Authorization", "Bearer "+token)
}

func TestHistory(t *testing.T) {
	// region: test-history
	app := anetostest.New(t, setup)
	ada, token := newUser(t, app, "Ada")
	api := as(app, token)

	var doc Document
	api.PostJSON("/api/documents", NewDocument{Slug: "plan", Title: "Plan"}).AssertCreated().JSON(&doc)
	path := fmt.Sprintf("/api/documents/%d", doc.ID)
	api.PatchJSON(path, map[string]string{"title": "The plan", "status": "published"}).AssertOK()
	api.GetJSON(path).AssertOK() // a view: not in the log
	api.PostJSON("/api/documents/archive", nil).AssertOK().AssertJSONPath("archived", float64(1))
	api.Delete(path).AssertNoContent()
	api.PostJSON(path+"/restore", nil).AssertOK()

	var page HistoryPage
	api.GetJSON(path + "/history").AssertOK().JSON(&page)
	events := page.Events
	var actions []string
	for _, e := range events {
		actions = append(actions, e.Action)
		if e.Actor != "user:"+ada.AuthID() {
			t.Errorf("%s by %s", e.Action, e.Actor)
		}
	}
	want := []string{audit.Restored, audit.Deleted, audit.Updated, audit.Updated, audit.Created}
	if strings.Join(actions, " ") != strings.Join(want, " ") {
		t.Fatalf("history: %v, want %v", actions, want)
	}
	// endregion
	if events[0].Via != fmt.Sprintf("POST %s/restore", path) {
		t.Errorf("the restore was done via %q", events[0].Via)
	}
	// The archive was a bulk write; the edit records what changed.
	if events[2].Rows != 1 || events[2].Changes != nil {
		t.Errorf("the archive: %+v", events[2])
	}
	edit := events[3].Changes
	if edit.Old["title"] != "Plan" || edit.New["title"] != "The plan" || edit.New["status"] != "published" || len(edit.New) != 2 {
		t.Errorf("the edit: %+v", edit)
	}
	if created := events[4].Changes; created.New["share_token"] != audit.Redacted || created.New["view_count"] != nil {
		t.Errorf("the create: %+v", created)
	}
}

func TestExportIsRecorded(t *testing.T) {
	app := anetostest.New(t, setup)
	ada, token := newUser(t, app, "Ada")
	as(app, token).PostJSON("/api/documents", NewDocument{Slug: "a", Title: "A"}).AssertCreated()
	as(app, token).GetJSON("/api/documents/export").AssertOK()
	e, err := db.Query[audit.Entry](app.Context()).Where(db.C("action").Eq("documents.exported")).First()
	if err != nil {
		t.Fatal(err)
	}
	if e.Actor() != audit.User(ada.AuthID()) || e.SubjectID != ada.AuthID() || e.Properties["documents"] != float64(1) {
		t.Errorf("export entry: %+v", e)
	}
}

func TestSlugsOfDeletedDocumentsAreFree(t *testing.T) {
	app := anetostest.New(t, setup)
	_, token := newUser(t, app, "Ada")
	api := as(app, token)
	var doc Document
	api.PostJSON("/api/documents", NewDocument{Slug: "plan", Title: "Plan"}).AssertCreated().JSON(&doc)
	api.PostJSON("/api/documents", NewDocument{Slug: "plan", Title: "Another"}).AssertUnprocessable()
	api.Delete(fmt.Sprintf("/api/documents/%d", doc.ID)).AssertNoContent()
	api.PostJSON("/api/documents", NewDocument{Slug: "plan", Title: "Another"}).AssertCreated()
}

func TestPruneTrashed(t *testing.T) {
	app := anetostest.New(t, setup)
	_, token := newUser(t, app, "Ada")
	var doc Document
	as(app, token).PostJSON("/api/documents", NewDocument{Slug: "old", Title: "Old"}).AssertCreated().JSON(&doc)
	as(app, token).Delete(fmt.Sprintf("/api/documents/%d", doc.ID)).AssertNoContent()
	app.Travel(31 * 24 * time.Hour)
	done, err := db.PruneAllTrashed(app.Context())
	if err != nil || len(done) != 1 || done[0].Rows != 1 {
		t.Fatalf("PruneAllTrashed = %+v, %v", done, err)
	}
	// The permanent delete is in the log too, by the system.
	gone, err := db.Query[audit.BulkOp](app.Context()).Where(db.C("action").Eq(audit.ForceDeleted)).First()
	if err != nil || gone.Actor() != audit.System || len(gone.Before) != 1 {
		t.Errorf("prune entry: %+v, %v", gone, err)
	}
}

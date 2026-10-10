// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/url"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/factory"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/database/factories"
)

// world is the app with a project, WEB, and its people: an owner, a
// member, a viewer, and an outsider who has no role in it.
type world struct {
	app                             *anetostest.App
	owner, member, viewer, outsider models.User
	project                         models.Project
	bug, idea                       models.Label
}

func newWorld(t *testing.T, opts ...anetostest.Option) *world {
	t.Helper()
	app := anetostest.New(t, setup, opts...)
	named := func(name string) models.User {
		return anetostest.Create(app, factories.Users.With(func(u *models.User) { u.Name = name }))
	}
	w := &world{app: app, owner: named("Olivia"), member: named("Max"), viewer: named("Vera"), outsider: named("Otto")}
	w.project = anetostest.Create(app, factories.Projects.With(func(p *models.Project) { p.Key, p.Name = "WEB", "Website" }))
	for u, role := range map[*models.User]string{&w.owner: access.Owner, &w.member: access.Member, &w.viewer: access.Viewer} {
		if err := rbac.Assign(app.Context(), u.AuthID(), access.Project("WEB"), role); err != nil {
			t.Fatal(err)
		}
	}
	w.bug = models.Label{ProjectID: w.project.ID, Name: "bug", Color: "#dc2626"}
	w.idea = models.Label{ProjectID: w.project.ID, Name: "idea", Color: "#2563eb"}
	for _, l := range []*models.Label{&w.bug, &w.idea} {
		if err := db.Create(app.Context(), l); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// as logs u in for the requests that follow.
func (w *world) as(u models.User) *anetostest.App { return anetostest.ActingAs(w.app, &u) }

// issue creates an issue in the project, by the owner, with fn's changes.
func (w *world) issue(fn ...func(*models.Issue)) models.Issue {
	return anetostest.Create(w.app, issueIn(w.project, w.owner, fn...))
}

// path is a path under the project: w.path("issues/1").
func (w *world) path(rest string) string {
	if rest == "" || rest[0] == '?' {
		return "/p/WEB" + rest
	}
	return "/p/WEB/" + rest
}

func id(n int64) string { return fmt.Sprint(n) }

func form(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

// projectWithKey makes a project with the key, and no members.
func projectWithKey(key string) *factory.Factory[models.Project] {
	return factories.Projects.With(func(p *models.Project) { p.Key, p.Name = key, "Project "+key })
}

// issueIn makes an issue in the project by author, with fn's changes.
func issueIn(p models.Project, author models.User, fn ...func(*models.Issue)) *factory.Factory[models.Issue] {
	return factories.Issues.With(func(i *models.Issue) { i.ProjectID, i.AuthorID = p.ID, author.ID }).With(fn...)
}

// assign gives u the role in the project.
func assign(t *testing.T, w *world, u models.User, p models.Project, role string) {
	t.Helper()
	if err := rbac.Assign(w.app.Context(), u.AuthID(), access.Project(p.Key), role); err != nil {
		t.Fatal(err)
	}
}

// addLabel puts the label on the project's issue with the number.
func addLabel(w *world, number int, l models.Label) error {
	ctx := w.app.Context()
	issue, err := db.Query[models.Issue](ctx).Where(models.IssueCols.ProjectID.Eq(w.project.ID), models.IssueCols.Number.Eq(number)).First()
	if err != nil {
		return err
	}
	return db.Attach(ctx, &issue, models.IssueRels.Labels, l.ID)
}

// newSearchWorld is newWorld for tests that search. MySQL's and
// MariaDB's full-text indexes only see committed rows, so there the
// test commits its rows (anetostest.WithoutTransaction) and deletes them
// when it ends; elsewhere the test runs in a transaction, as usual.
// Which database the tests use comes from their settings (the
// environment, .env.testing), so a first app finds out.
func newSearchWorld(t *testing.T) *world {
	t.Helper()
	probe := anetostest.New(t, setup)
	d, err := db.From(probe.Context())
	if err != nil {
		t.Fatal(err)
	}
	if d.Dialect().Name() != "mysql" {
		return newWorld(t)
	}
	w := newWorld(t, anetostest.WithoutTransaction())
	firstProject, firstUser := w.project.ID, w.owner.ID
	t.Cleanup(func() { // before the app closes: cleanups run last first
		// The rows this test made: projects (and, ON DELETE CASCADE,
		// their issues, labels, comments and files), then its users,
		// their roles and tokens.
		ctx := w.app.Context()
		if _, err := db.Query[models.Project](ctx).Where(models.ProjectCols.ID.Gte(firstProject)).Delete(); err != nil {
			t.Error(err)
		}
		users, err := db.Query[models.User](ctx).Where(models.UserCols.ID.Gte(firstUser)).Get()
		if err != nil {
			t.Error(err)
		}
		a := anetos.MustResolve[*auth.Auth[*models.User]](w.app.App)
		for _, u := range users {
			if err := rbac.RemoveUser(ctx, u.AuthID()); err != nil {
				t.Error(err)
			}
			if err := a.RevokeAllTokens(ctx, &u); err != nil {
				t.Error(err)
			}
		}
		if _, err := db.Query[models.User](ctx).Where(models.UserCols.ID.Gte(firstUser)).Delete(); err != nil {
			t.Error(err)
		}
	})
	return w
}

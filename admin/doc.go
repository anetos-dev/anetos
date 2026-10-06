// SPDX-License-Identifier: Apache-2.0

// Package admin is an admin interface for Anetos apps: pages where staff
// list, search, filter, create, edit and delete the app's records, with
// permissions from package auth/rbac and every change in the audit log
// when the models are tracked (package audit).
//
// It is a library the app wires with code: `anetos make:admin` writes
// admin.go, which creates the [Panel] for the app's users, adds a
// [Resource] per model, and mounts it under ADMIN_PATH (/admin) or at
// ADMIN_HOST; `anetos make:admin:resource Post` writes a resource to
// change as the app needs:
//
//	p, err := admin.New(app, a) // a: the app's *auth.Auth[*models.User]
//	if err != nil {
//		return err
//	}
//	err = admin.Add(p, admin.Resource[models.Post, PostForm]{
//		Name:    "posts",
//		Columns: []admin.Column[models.Post]{admin.Field[models.Post]("Title", "title")},
//		Search:  []string{"title"},
//		Edit:    func(p models.Post) PostForm { return PostForm{Title: p.Title} },
//		Apply: func(ctx context.Context, in PostForm, p *models.Post) error {
//			p.Title = in.Title
//			return nil
//		},
//	})
//	…
//	err = p.Mount(r, sessions.Middleware, web.CSRF(), a.Middleware)
//
// A resource's form is a struct of its own (F), not the model: only its
// fields can be changed, its validate tags check them, and Apply copies
// them into the record. Fields are rendered by their Go type (strings,
// numbers, bools, [DateTime], anetos.Date) and admin tags:
// admin:"textarea", admin:"password", admin:"select=draft|published" (or
// choices from Resource.Choices), admin:"help=…", admin:"-".
//
// Only signed-in users with the permission [Access] get in. Each resource
// has four more, admin.<name>.view, .create, .update and .delete, which
// [New] and [Add] declare in the app's registry so roles (in code or in
// the database) can grant them. Models with db.SoftDeletes get a trash,
// with restore and delete forever.
//
// The pages are html/template, embedded, with htmx and a stylesheet of
// their own, under a strict Content-Security-Policy.
package admin

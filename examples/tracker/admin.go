// SPDX-License-Identifier: Apache-2.0

package main

import (
	"anetos.dev/anetos"
	"anetos.dev/anetos/admin"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	appadmin "anetos.dev/anetos/examples/tracker/app/admin"
	"anetos.dev/anetos/examples/tracker/app/models"
)

// setupAdmin adds the admin interface (anetos make:admin) at ADMIN_PATH
// (default /admin), or at ADMIN_HOST: the users and roles, and the
// resources of app/admin, which anetos make:admin:resource adds to. Only
// signed-in users with the permission admin.access get in; the admin
// role has every permission: `go run . rbac:assign <user-id> admin`. setup calls it after setupAuth.
func setupAdmin(app *anetos.App, r *web.Router, sessions *session.Manager, a *auth.Auth[*models.User]) error {
	// Roles and permissions (package auth/rbac): roles in code here, and
	// more in the database, made in the admin from its permissions.
	runner, err := anetos.Resolve[*migrate.Runner](app)
	if err != nil {
		return err
	}
	if err := runner.Add(rbac.Migrations()); err != nil {
		return err
	}
	// The tracker's permissions and roles are in app/access.
	if _, err := rbac.ForApp(app, access.Permissions, access.Roles...); err != nil {
		return err
	}
	p, err := admin.New(app, a, admin.UserName(func(u *models.User) string { return u.Name }))
	if err != nil {
		return err
	}
	if err := appadmin.Users(p, a); err != nil {
		return err
	}
	if err := admin.Roles(p); err != nil {
		return err
	}
	// The dashboard, and the operations' pages: the jobs, the scheduled
	// tasks.
	widgets := []admin.Widget{
		admin.SignUps[models.User]("Users", "created_at"),
	}
	if q, ok := anetos.Lookup[*queue.Queue](app); ok {
		widgets = append(widgets, admin.QueueHealth(q))
		if err := admin.Jobs(p, q); err != nil {
			return err
		}
	}
	if s, ok := anetos.Lookup[*schedule.Scheduler](app); ok {
		if err := admin.Schedule(p, s); err != nil {
			return err
		}
	}
	// The audit log (setup's audit.ForApp): the activity pages, and each
	// issue's history on its page.
	widgets = append(widgets, admin.RecentActivity(10))
	if err := admin.Activity(p); err != nil {
		return err
	}
	if err := p.Dashboard(widgets...); err != nil {
		return err
	}
	for _, add := range appadmin.Resources {
		if err := add(p); err != nil {
			return err
		}
	}
	// The middleware of the app's pages: sessions, CSRF protection, and
	// the signed-in user.
	return p.Mount(r, sessions.Middleware, web.CSRF(), a.Middleware)
}

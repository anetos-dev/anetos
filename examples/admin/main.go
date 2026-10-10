// SPDX-License-Identifier: Apache-2.0

// Command admin is a shop's back office (package admin): staff log in
// and manage the products and categories, with roles and permissions
// (package auth/rbac) and every change to a product in the audit log
// (package audit), and the staff's accounts and roles managed there. seed
// creates an administrator, an editor and support staff.
//
//	anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080
//	go run . migrate
//	go run . seed
//	go run .                      # then open http://localhost:8080/admin
package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"slices"

	"anetos.dev/anetos"
	"anetos.dev/anetos/admin"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// region: roles
// editor are the permissions of editors: they manage products, and may
// only look at categories. The admin declares its permissions too
// (admin.New, admin.Add); roles in code name them in the app's list.
var editor = slices.Concat(
	[]rbac.Permission{admin.Access},
	admin.PermissionsOf("products"),
	admin.PermissionsOf("categories", "view"),
)

// support are the permissions of support staff: they look after the
// staff's accounts, and may impersonate them to see what they see.
var support = slices.Concat(
	[]rbac.Permission{admin.Access, "admin.users.impersonate"},
	admin.PermissionsOf("users", "view", "update"),
)

// roles are the staff's roles: administrators may do everything.
var roles = []rbac.Role{
	{Name: "admin", Title: "Administrator", Super: true},
	{Name: "editor", Title: "Editor", Permissions: editor},
	{Name: "support", Title: "Support", Permissions: support},
}

// permissions are those of the roles, each once.
var permissions = slices.Compact(slices.Sorted(slices.Values(slices.Concat(editor, support))))

// endregion

// region: setup-admin
// setupAdmin adds the admin, after the routes of the app's pages, with
// their middleware.
func setupAdmin(app *anetos.App, r *web.Router, sessions *session.Manager, a *auth.Auth[*User]) error {
	p, err := admin.New(app, a, admin.Title("Shop admin"))
	if err != nil {
		return err
	}
	if err := admin.Add(p, products()); err != nil {
		return err
	}
	if err := admin.Add(p, categories()); err != nil {
		return err
	}
	// The staff and the roles (roles.assign gives roles).
	if err := addUsers(p, a); err != nil {
		return err
	}
	if err := admin.Roles(p); err != nil {
		return err
	}
	// The dashboard, and who did what (the audit log).
	if err := admin.Activity(p); err != nil {
		return err
	}
	err = p.Dashboard(lowStock(), admin.SignUps[Product]("New products", "created_at"), admin.RecentActivity(8))
	if err != nil {
		return err
	}
	return p.Mount(r, sessions.Middleware, web.CSRF(), a.Middleware)
}

// endregion

func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	sets := []*migrate.Set{Migrations, auth.Migrations(), rbac.Migrations(), audit.Migrations()}
	if _, err := migrate.New(app, sets); err != nil {
		return nil, err
	}
	if _, err := cache.New(app); err != nil { // login throttling
		return nil, err
	}
	trail, err := audit.New(app)
	if err != nil {
		return nil, err
	}
	if err := audit.Track[Product](trail); err != nil {
		return nil, err
	}
	a, err := auth.New(app, users)
	if err != nil {
		return nil, err
	}
	if _, err := rbac.New(app, permissions, roles...); err != nil {
		return nil, err
	}
	app.Command("seed", "Create an administrator (admin@example.com), an editor (editor@example.com) and support staff (support@example.com), password \"secret password\"",
		func(ctx context.Context, args *cmd.Args) error {
			if err := seed(ctx); err != nil {
				return err
			}
			fmt.Fprintln(args.Stdout, "Created admin@example.com, editor@example.com and support@example.com, password \"secret password\".")
			return nil
		})
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	pages(srv.Router(), sessions, a)
	if err := setupAdmin(app, srv.Router(), sessions, a); err != nil {
		return nil, err
	}
	return srv, nil
}

// pages are the login page and logging out.
func pages(r *web.Router, sessions *session.Manager, a *auth.Auth[*User]) {
	g := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
	// The app's home page: who is logged in, and the banner while
	// impersonating someone (admin.Banner).
	g.With(a.Require).Get("/", func(c *web.Ctx) error {
		u, err := auth.Current[*User](c)
		if err != nil {
			return err
		}
		banner, err := view.String(c, admin.Banner())
		if err != nil {
			return err
		}
		return c.Render(http.StatusOK, view.Template(homePage, "home", struct {
			Banner template.HTML
			Name   string
		}{template.HTML(banner), u.Name})) //nolint:gosec // the banner's own markup
	}).Name("home")
	g.Get("/login", func(c *web.Ctx) error {
		return c.Render(http.StatusOK, view.Template(loginPage, "login", c))
	}).Name("login")
	g.Post("/login", web.H(func(c *web.Ctx, in LoginInput) (web.Responder, error) {
		if _, err := a.Attempt(c, in.Email, in.Password, false); errors.Is(err, auth.ErrInvalidCredentials) {
			return nil, validate.Fail("email", "These credentials don't match our records.")
		} else if errors.Is(err, auth.ErrDisabled) {
			return nil, validate.Fail("email", "This account is disabled.")
		} else if err != nil {
			return nil, err
		}
		return web.Redirect(auth.Intended(c, "/admin")), nil
	}))
	g.Post("/logout", func(c *web.Ctx) error {
		if err := a.Logout(c); err != nil {
			return err
		}
		return c.Redirect(http.StatusSeeOther, "/login")
	})
}

// LoginInput is the login form.
type LoginInput struct {
	Email    string `json:"email" validate:"required|email"`
	Password string `json:"password" validate:"required"`
}

var homePage = template.Must(template.New("home").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Shop</title></head><body>
{{.Banner}}
<h1>Hello, {{.Name}}</h1>
<p><a href="/admin">The admin</a></p>
</body></html>`))

var loginPage = template.Must(template.New("login").Funcs(template.FuncMap{
	"csrf":  view.CSRFToken,
	"error": func(c *web.Ctx) string { return view.Errors(c).Get("email") },
	"old":   func(c *web.Ctx) string { return view.Old(c, "email") },
}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Log in</title></head><body>
<h1>Log in</h1>
<form method="post" action="/login">
<input type="hidden" name="_token" value="{{csrf .}}">
{{with error .}}<p>{{.}}</p>{{end}}
<label>Email <input type="email" name="email" value="{{old .}}"></label>
<label>Password <input type="password" name="password"></label>
<button>Log in</button>
</form></body></html>`))

func seed(ctx context.Context) error {
	hash, err := password.Hash("secret password")
	if err != nil {
		return err
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		for _, u := range []struct{ name, email, role string }{
			{"Ada", "admin@example.com", "admin"},
			{"Eve", "editor@example.com", "editor"},
			{"Sam", "support@example.com", "support"},
		} {
			user := User{Name: u.name, Email: u.email, Password: hash}
			if err := db.Create(ctx, &user); err != nil {
				return err
			}
			if err := rbac.Assign(ctx, user.AuthID(), rbac.Global, u.role); err != nil {
				return err
			}
		}
		for _, name := range []string{"Books", "Games"} {
			if err := db.Create(ctx, &Category{Name: name}); err != nil {
				return err
			}
		}
		return nil
	})
}

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute()
}

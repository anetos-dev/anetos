// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

// Roles adds the roles pages: every role, those declared in code and
// those stored in the database, with their permissions and how many users
// have them; and creating, editing and deleting the stored ones, with the
// permissions the signed-in user has. Permissions: admin.roles.view,
// .create, .update and .delete; giving roles to users is
// [AssignRoles], on their pages ([Users]).
func Roles(p *Panel) error {
	if p.reg == nil {
		return errors.New("admin: Roles needs the app's roles and permissions (rbac.ForApp)")
	}
	return p.add(&rolesRes{p: p, in: resInfo{Name: "roles", Title: "Roles", Singular: "Role"}})
}

// rolesRes is the roles pages.
type rolesRes struct {
	p  *Panel
	in resInfo
}

func (r *rolesRes) info() *resInfo { return &r.in }

func (r *rolesRes) count(ctx context.Context) (int64, error) {
	roles, err := rbac.Roles(ctx)
	return int64(len(roles)), err
}

func (r *rolesRes) url(suffix string) string { return r.p.base + "/roles" + suffix }

func (r *rolesRes) mount(g *web.Router) {
	need := func(kind string) *web.Router { return g.With(rbac.Require(r.in.perm(kind))) }
	need("view").Get("/", r.index).Name("admin.roles.index")
	need("create").Get("/new", r.p.confirmFirst(r.create)).Name("admin.roles.create")
	need("create").Post("/", r.p.confirmFirst(r.store)).Name("admin.roles.store")
	need("view").Get("/{name}", r.show).Name("admin.roles.show")
	need("update").Get("/{name}/edit", r.p.confirmFirst(r.edit)).Name("admin.roles.edit")
	need("update").Post("/{name}", r.p.confirmFirst(r.update)).Name("admin.roles.update")
	need("delete").Post("/{name}/delete", r.p.confirmFirst(r.destroy)).Name("admin.roles.destroy")
}

// roleRow is a role in the list.
type roleRow struct {
	Name, Title, URL string
	Custom           bool
	Permissions      string
	Users            int64
}

func (r *rolesRes) index(c *web.Ctx) error {
	roles, err := rbac.Roles(c)
	if err != nil {
		return err
	}
	counts, err := rbac.RoleCounts(c)
	if err != nil {
		return err
	}
	data := struct {
		Rows   []roleRow
		NewURL string
	}{}
	for _, role := range roles {
		perms := "All (super)"
		if !role.Super {
			perms = plural(len(role.Permissions), "permission")
		}
		data.Rows = append(data.Rows, roleRow{Name: role.Name, Title: role.DisplayName(), URL: r.url("/" + url.PathEscape(role.Name)),
			Custom: role.Custom, Permissions: perms, Users: counts[role.Name]})
	}
	if rbac.Can(c, r.in.perm("create")) {
		data.NewURL = r.url("/new")
	}
	return r.p.render(c, "roles", page{Title: "Roles", Crumbs: []navItem{{Title: r.p.cfg.Title, URL: r.p.URL()}}, Data: data})
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// permGroup is permissions sharing a prefix (admin.posts).
type permGroup struct {
	Name  string
	Perms []permView
}

type permView struct {
	Name    string
	Checked bool
	Kept    bool // on the role, but not the signed-in user's to give
}

// groups returns the permissions grouped by what comes before their last
// dot.
func groups(perms []rbac.Permission, checked func(rbac.Permission) bool, kept func(rbac.Permission) bool) []permGroup {
	var out []permGroup
	for _, p := range perms {
		name := string(p)
		g := ""
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			g = name[:i]
		}
		if len(out) == 0 || out[len(out)-1].Name != g {
			out = append(out, permGroup{Name: g})
		}
		out[len(out)-1].Perms = append(out[len(out)-1].Perms, permView{Name: name, Checked: checked(p), Kept: kept != nil && kept(p)})
	}
	return out
}

// sortedPerms returns perms by name.
func sortedPerms(perms []rbac.Permission) []rbac.Permission {
	out := slices.Clone(perms)
	slices.Sort(out)
	return out
}

func (r *rolesRes) find(c *web.Ctx) (rbac.Role, error) {
	role, err := rbac.FindRole(c, c.Param("name"))
	if errors.Is(err, rbac.ErrUnknownRole) {
		return role, db.ErrNotFound
	}
	return role, err
}

func (r *rolesRes) show(c *web.Ctx) error {
	role, err := r.find(c)
	if err != nil {
		return err
	}
	holders, err := rbac.Holders(c, role.Name, 100)
	if err != nil {
		return err
	}
	type holder struct{ User, Name, URL, Scope string }
	names := map[string]string{}
	if r.p.userLabels != nil {
		ids := make([]string, len(holders))
		for i, h := range holders {
			ids[i] = h.UserID
		}
		if names, err = r.p.userLabels(c, ids); err != nil {
			return err
		}
	}
	data := struct {
		Role    rbac.Role
		Groups  []permGroup
		Holders []holder
		More    bool
		EditURL string
		Delete  *buttonView
		CSRF    string
	}{Role: role, CSRF: csrfToken(c)}
	data.Groups = groups(sortedPerms(role.Permissions), func(rbac.Permission) bool { return true }, nil)
	for i, h := range holders {
		if i == 99 {
			data.More = true
			break
		}
		v := holder{User: h.UserID, Name: names[h.UserID], Scope: string(h.Scope)}
		if v.Name == "" {
			v.Name = "User " + h.UserID
		}
		if r.p.usersName != "" {
			v.URL = r.p.base + "/" + r.p.usersName + "/" + url.PathEscape(h.UserID)
		}
		data.Holders = append(data.Holders, v)
	}
	if role.Custom && r.mayGive(c, role) {
		if rbac.Can(c, r.in.perm("update")) {
			data.EditURL = r.url("/" + url.PathEscape(role.Name) + "/edit")
		}
		if rbac.Can(c, r.in.perm("delete")) {
			data.Delete = &buttonView{Name: "delete", Title: "Delete", URL: r.url("/" + url.PathEscape(role.Name) + "/delete"),
				Confirm: "Delete the role " + role.Name + "? Its users lose it.", Danger: true}
		}
	}
	return r.p.render(c, "role", page{Title: role.DisplayName(), Crumbs: r.crumbs(navItem{Title: role.DisplayName()}), Data: data})
}

func (r *rolesRes) crumbs(more ...navItem) []navItem {
	return append([]navItem{{Title: r.p.cfg.Title, URL: r.p.URL()}, {Title: "Roles", URL: r.url("")}}, more...)
}

// mayGive reports whether the signed-in user has every permission of role
// (everywhere), as making, changing or deleting it needs.
func (r *rolesRes) mayGive(ctx context.Context, role rbac.Role) bool {
	return rbac.AuthorizeRole(ctx, rbac.Global, role.Name) == nil
}

// roleForm is the create and edit form.
type roleForm struct {
	Action, Cancel, Submit string
	New                    bool
	Name, Title            string
	Groups                 []permGroup
	Errors                 map[string]string
	CSRF                   string
}

// declared returns the permissions of perms that are declared: a stored
// role may hold others, no longer declared, which allow nothing and go
// when it is saved.
func (r *rolesRes) declared(perms []rbac.Permission) []rbac.Permission {
	var out []rbac.Permission
	for _, p := range perms {
		if r.p.reg.Declared(p) {
			out = append(out, p)
		}
	}
	return out
}

// givable returns the permissions the signed-in user has everywhere.
func (r *rolesRes) givable(ctx context.Context) []rbac.Permission {
	var out []rbac.Permission
	for _, p := range sortedPerms(r.p.reg.Permissions()) {
		if rbac.Can(ctx, p) {
			out = append(out, p)
		}
	}
	return out
}

func (r *rolesRes) form(c *web.Ctx, title string, f roleForm, have []rbac.Permission, crumbs []navItem, status int) error {
	givable := r.givable(c)
	all := givable
	have = r.declared(have)
	for _, p := range have {
		if !slices.Contains(all, p) {
			all = append(all, p) // kept, as the user can't give it
		}
	}
	all = sortedPerms(all)
	f.Groups = groups(all, func(p rbac.Permission) bool { return slices.Contains(have, p) },
		func(p rbac.Permission) bool { return !slices.Contains(givable, p) })
	f.CSRF = csrfToken(c)
	pg := page{Title: title, Crumbs: crumbs, Data: f}
	if status != http.StatusOK {
		pg.Error = "Check the form."
	}
	return r.p.render(c, "roleform", pg)
}

func (r *rolesRes) create(c *web.Ctx) error {
	return r.form(c, "New role", roleForm{Action: r.url(""), Cancel: r.url(""), Submit: "Create", New: true}, nil,
		r.crumbs(navItem{Title: "New"}), http.StatusOK)
}

// submitted reads the form: the permissions checked, among those the
// user may give, plus those of the role they can't (kept as they were).
func (r *rolesRes) submitted(c *web.Ctx, before []rbac.Permission) (title string, perms []rbac.Permission, err error) {
	req := c.Request()
	if err := req.ParseForm(); err != nil {
		return "", nil, web.Error(http.StatusBadRequest, "")
	}
	givable := r.givable(c)
	for _, v := range req.PostForm["permissions"] {
		p := rbac.Permission(v)
		if !slices.Contains(givable, p) {
			continue
		}
		if !slices.Contains(perms, p) {
			perms = append(perms, p)
		}
	}
	for _, p := range r.declared(before) {
		if !slices.Contains(givable, p) && !slices.Contains(perms, p) {
			perms = append(perms, p)
		}
	}
	return strings.TrimSpace(req.PostForm.Get("title")), sortedPerms(perms), nil
}

func (r *rolesRes) store(c *web.Ctx) error {
	title, perms, err := r.submitted(c, nil)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(c.Request().PostForm.Get("name"))
	role := rbac.Role{Name: name, Title: title, Permissions: perms}
	err = db.Tx(c, func(ctx context.Context) error {
		if name == "new" {
			return fmt.Errorf("%w: the name new is the admin's (its page to make a role)", rbac.ErrInvalidRole)
		}
		if err := rbac.CreateRole(ctx, role); err != nil {
			return err
		}
		return record(ctx, "rbac.role_created", audit.Subject{Type: "rbac_roles", ID: name}, map[string]any{"permissions": perms})
	})
	if msg, ok := roleMessage(err); ok {
		return r.form(c, "New role", roleForm{Action: r.url(""), Cancel: r.url(""), Submit: "Create", New: true,
			Name: name, Title: title, Errors: map[string]string{"name": msg}}, perms, r.crumbs(navItem{Title: "New"}), http.StatusUnprocessableEntity)
	} else if err != nil {
		return err
	}
	return done(c, "Role "+name+" created.", r.url("/"+url.PathEscape(name)))
}

// roleMessage returns the message of package rbac's errors about a role
// the user gave (its name, its permissions).
func roleMessage(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, rbac.ErrInvalidRole), errors.Is(err, rbac.ErrRoleExists), errors.Is(err, rbac.ErrUnknownPermission):
		msg := strings.TrimPrefix(err.Error(), "rbac: ")
		return strings.ToUpper(msg[:1]) + msg[1:] + ".", true
	}
	return "", false
}

// editable returns the role of the path, its page, and why the signed-in
// user may not change it ("" if they may).
func (r *rolesRes) editable(c *web.Ctx) (role rbac.Role, show, refusal string, err error) {
	if role, err = r.find(c); err != nil {
		return role, "", "", err
	}
	show = r.url("/" + url.PathEscape(role.Name))
	switch {
	case !role.Custom:
		refusal = "This role is declared in the app's code: change it there."
	case !r.mayGive(c, role):
		refusal = "This role has permissions you don't: you can't change or delete it."
	}
	return role, show, refusal, nil
}

func (r *rolesRes) edit(c *web.Ctx) error {
	role, show, refusal, err := r.editable(c)
	if err != nil {
		return err
	}
	if refusal != "" {
		return failed(c, refusal, show)
	}
	return r.form(c, "Edit "+role.DisplayName(), roleForm{Action: show, Cancel: show, Submit: "Save", Name: role.Name, Title: role.Title},
		role.Permissions, r.crumbs(navItem{Title: role.DisplayName(), URL: show}, navItem{Title: "Edit"}), http.StatusOK)
}

func (r *rolesRes) update(c *web.Ctx) error {
	role, show, refusal, err := r.editable(c)
	if err != nil {
		return err
	}
	if refusal != "" {
		return failed(c, refusal, show)
	}
	title, perms, err := r.submitted(c, role.Permissions)
	if err != nil {
		return err
	}
	changed := rbac.Role{Name: role.Name, Title: title, Permissions: perms}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := rbac.UpdateRole(ctx, changed); err != nil {
			return err
		}
		return record(ctx, "rbac.role_updated", audit.Subject{Type: "rbac_roles", ID: role.Name},
			map[string]any{"title": title, "permissions": perms, "before": sortedPerms(role.Permissions)})
	})
	if msg, ok := roleMessage(err); ok {
		return r.form(c, "Edit "+role.DisplayName(), roleForm{Action: show, Cancel: show, Submit: "Save", Name: role.Name, Title: title,
			Errors: map[string]string{"name": msg}}, perms, r.crumbs(navItem{Title: role.DisplayName(), URL: show}, navItem{Title: "Edit"}),
			http.StatusUnprocessableEntity)
	} else if err != nil {
		return err
	}
	return done(c, "Saved.", show)
}

func (r *rolesRes) destroy(c *web.Ctx) error {
	role, show, refusal, err := r.editable(c)
	if err != nil {
		return err
	}
	if refusal != "" {
		return failed(c, refusal, show)
	}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := rbac.DeleteRole(ctx, role.Name); err != nil {
			return err
		}
		return record(ctx, "rbac.role_deleted", audit.Subject{Type: "rbac_roles", ID: role.Name},
			map[string]any{"permissions": sortedPerms(role.Permissions)})
	})
	if err != nil {
		return err
	}
	return done(c, "Role "+role.Name+" deleted.", r.url(""))
}

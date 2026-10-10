// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

// Choice is a value a filter or a select field offers, with its label.
type Choice struct {
	// Value is what the form sends.
	Value string
	// Label is what people see.
	Label string
}

// Choices returns a choice per value, each its own label.
func Choices(values ...string) []Choice {
	out := make([]Choice, len(values))
	for i, v := range values {
		out[i] = Choice{v, v}
	}
	return out
}

// Column is a column of a resource's list, or a line of a record's page.
type Column[T any] struct {
	// Title is the column's heading.
	Title string
	// Column is the database column it shows, for sorting and for the
	// value when Value is nil.
	Column string
	// Value returns what to show for a record: a string, number, bool,
	// time, or a view.Component or template.HTML for markup. Nil shows
	// the model's field for Column.
	Value func(row T) any
	// Sortable lets people sort the list by Column.
	Sortable bool
}

// Field returns a sortable column showing the model's field for column.
func Field[T any](title, column string) Column[T] {
	return Column[T]{Title: title, Column: column, Sortable: true}
}

// Filter narrows a resource's list to the records matching a choice.
type Filter[T any] struct {
	// Name is the filter's query parameter (?status=draft), unique in
	// the resource.
	Name string
	// Title is its label.
	Title string
	// Choices are the values offered; others are ignored.
	Choices []Choice
	// Apply narrows q to the records matching value.
	Apply func(q *db.Q[T], value string) *db.Q[T]
}

// Equals returns a filter keeping the records whose column equals the
// chosen value.
func Equals[T any](column, title string, choices ...Choice) Filter[T] {
	return Filter[T]{Name: column, Title: title, Choices: choices, Apply: func(q *db.Q[T], v string) *db.Q[T] {
		return q.Where(db.C(column).Eq(v))
	}}
}

// Action is something done to one record from its page: publish,
// refund, resend.
type Action[T any] struct {
	// Name identifies it in URLs: lowercase letters, digits and -.
	Name string
	// Title is its button's label.
	Title string
	// Confirm, if set, is asked before it runs ("Refund this order?").
	Confirm string
	// Permission is the resource's permission it needs: "update" (the
	// default), "view", "create" or "delete", or a permission of the
	// app's own (one with a dot).
	Permission string
	// Danger shows it as a destructive action, and asks for the user's
	// password first (ADMIN_CONFIRM).
	Danger bool
	// When reports whether a record offers it; nil: every record.
	When func(row T) bool
	// Run does it. A *validate.Errors or validate.Fail error is shown
	// to the user; other errors are server errors.
	Run func(ctx context.Context, row *T) error
	// Done is the message shown once it's done; default "<Title>: done."
	Done string

	// then is where to go once it's done; the record's page if nil.
	then func(c *web.Ctx, row T) string
	// sensitive asks for the password first, as Danger does.
	sensitive bool
}

// BulkAction is something done to the records selected in the list.
type BulkAction[T any] struct {
	// Name identifies it: lowercase letters, digits and -.
	Name string
	// Title is its button's label.
	Title string
	// Confirm, if set, is asked before it runs.
	Confirm string
	// Permission is as for [Action]; "update" by default.
	Permission string
	// Danger shows it as a destructive action, and asks for the user's
	// password first (ADMIN_CONFIRM).
	Danger bool
	// Run does it to the selected records (rows is a query for them,
	// within the resource's Query) and returns how many it changed.
	Run func(ctx context.Context, rows *db.Q[T]) (int64, error)
}

// Resource is a model the admin manages: model T, edited through form
// struct F (use struct{} for a resource that is only listed and shown).
type Resource[T any, F any] struct {
	// Name is the resource's URL segment and permission name: "posts"
	// gives /admin/posts and admin.posts.view, .create, .update and
	// .delete. Lowercase letters, digits and -.
	Name string
	// Title is its plural name ("Posts"), by default from Name.
	Title string
	// Singular is one record's name ("Post"), by default from Title.
	Singular string
	// Columns are the list's columns.
	Columns []Column[T]
	// Details are the lines of a record's page; Columns if empty.
	Details []Column[T]
	// Search are the text columns the list's search box looks in (LIKE,
	// without regard to ASCII case on SQLite, and to any case elsewhere).
	Search []string
	// Filters narrow the list.
	Filters []Filter[T]
	// Sort is the list's order: a column, "-" first for descending;
	// default newest key first.
	Sort string
	// PerPage overrides ADMIN_PER_PAGE.
	PerPage int
	// Query limits what the admin sees and changes of the model (a scope
	// applied to every query), nil for every record.
	Query func(q *db.Q[T]) *db.Q[T]
	// Label names a record in titles; default "#<key>".
	Label func(row T) string
	// Edit returns the form for a record (the zero T for a new one).
	// With Edit and Apply nil, records can't be created or edited here.
	Edit func(row T) F
	// Apply copies a submitted form, validated by F's validate tags,
	// into the record before it is saved; a validation error it returns
	// (validate.Fail) is shown next to the field.
	Apply func(ctx context.Context, in F, row *T) error
	// Choices are the choices of select fields of F whose values come
	// from the database, by JSON name ("author_id"); fields tagged
	// admin:"select=a|b" have theirs fixed.
	Choices map[string]func(ctx context.Context) ([]Choice, error)
	// Actions are done to one record, from its page.
	Actions []Action[T]
	// BulkActions are done to the records selected in the list.
	BulkActions []BulkAction[T]
	// NoCreate hides "New": records are edited, not created, here.
	NoCreate bool
	// NoDelete hides deleting.
	NoDelete bool
}

// resource is a Resource of any types, for the panel.
type resource interface {
	info() *resInfo
	mount(r *web.Router)
	count(ctx context.Context) (int64, error)
}

// resInfo is what the panel knows about a resource.
type resInfo struct {
	Name     string
	Title    string
	Singular string
	soft     bool     // the model embeds db.SoftDeletes
	custom   []string // the permissions' kinds, if not view, create, update and delete
	editable bool
	create   bool
	delete   bool
}

func (in *resInfo) perm(kind string) rbac.Permission {
	return rbac.Permission("admin." + in.Name + "." + kind)
}

func (in *resInfo) perms() []rbac.Permission { return PermissionsOf(in.Name, in.custom...) }

// Add adds a resource to the panel and declares its permissions. Add the
// resources before [Panel.Mount].
func Add[T, F any](p *Panel, r Resource[T, F]) error {
	res, err := build(p, r)
	if err != nil {
		return err
	}
	return p.add(res)
}

// build checks a resource and works out what the panel needs of it.
func build[T, F any](p *Panel, r Resource[T, F]) (*res[T, F], error) {
	if r.Name == "" {
		return nil, errors.New("admin: a resource needs a Name")
	}
	if r.Title == "" {
		r.Title = humanize(r.Name)
	}
	if r.Singular == "" {
		r.Singular = singular(r.Title)
	}
	if (r.Edit == nil) != (r.Apply == nil) {
		return nil, fmt.Errorf("admin: resource %s needs both Edit and Apply, or neither", r.Name)
	}
	cols, err := db.Columns[T]()
	if err != nil {
		return nil, err
	}
	table, key, err := db.KeyOf(new(T))
	if err != nil {
		return nil, fmt.Errorf("admin: resource %s: %w", r.Name, err)
	}
	fields, err := columnFields(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	check := func(what, col string) error {
		if col != "" && !slices.Contains(cols, col) {
			return fmt.Errorf("admin: resource %s: %s %q is not a column of %s", r.Name, what, col, table)
		}
		return nil
	}
	for _, c := range slices.Concat(r.Columns, r.Details) {
		if err := check("column", c.Column); err != nil {
			return nil, err
		}
		if c.Value == nil && c.Column == "" {
			return nil, fmt.Errorf("admin: resource %s: column %q needs a Column or a Value", r.Name, c.Title)
		}
	}
	for _, s := range r.Search {
		if err := check("search column", s); err != nil {
			return nil, err
		}
	}
	for _, f := range r.Filters {
		if f.Name == "" || f.Apply == nil || slices.Contains([]string{"q", "sort", "page"}, f.Name) {
			return nil, fmt.Errorf("admin: resource %s: a filter needs a Name (not q, sort or page) and Apply", r.Name)
		}
	}
	checkPerm := func(what, name, perm string) error {
		switch {
		case perm == "" || slices.Contains([]string{"view", "create", "update", "delete"}, perm):
			return nil
		case strings.Contains(perm, ".") && (p.reg == nil || p.reg.Declared(rbac.Permission(perm))):
			return nil
		}
		return fmt.Errorf("admin: resource %s: %s %q: permission %q is neither view, create, update or delete nor a declared permission of the app's", r.Name, what, name, perm)
	}
	for _, a := range r.Actions {
		if !nameRe.MatchString(a.Name) || a.Run == nil {
			return nil, fmt.Errorf("admin: resource %s: action %q needs a name (lowercase letters, digits, -) and Run", r.Name, a.Name)
		}
		if err := checkPerm("action", a.Name, a.Permission); err != nil {
			return nil, err
		}
	}
	for _, a := range r.BulkActions {
		if !nameRe.MatchString(a.Name) || a.Run == nil || a.Name == "delete" {
			return nil, fmt.Errorf("admin: resource %s: bulk action %q needs a name (lowercase letters, digits, -; not delete) and Run", r.Name, a.Name)
		}
		if err := checkPerm("bulk action", a.Name, a.Permission); err != nil {
			return nil, err
		}
	}
	soft, err := db.SoftDeleting[T]()
	if err != nil {
		return nil, err
	}
	var form []formSpec
	if r.Edit != nil {
		if form, err = formSpecs(reflect.TypeFor[F]()); err != nil {
			return nil, fmt.Errorf("admin: resource %s: %w", r.Name, err)
		}
	}
	if r.Sort == "" && fields.pk != "" {
		r.Sort = "-" + fields.pk
	}
	if s := strings.TrimPrefix(r.Sort, "-"); s != "" {
		if err := check("sort", s); err != nil {
			return nil, err
		}
	}
	names := map[string]bool{}
	for _, a := range r.Actions {
		if names[a.Name] {
			return nil, fmt.Errorf("admin: resource %s: two actions named %q", r.Name, a.Name)
		}
		names[a.Name] = true
	}
	res := &res[T, F]{
		Resource: r,
		p:        p,
		table:    table,
		keyType:  reflect.TypeOf(key),
		fields:   fields,
		form:     form,
		in: resInfo{
			Name: r.Name, Title: r.Title, Singular: r.Singular,
			soft: soft, editable: r.Edit != nil,
			create: r.Edit != nil && !r.NoCreate, delete: !r.NoDelete,
		},
	}
	return res, nil
}

// res is a Resource with what the panel works out from it.
type res[T, F any] struct {
	Resource[T, F]
	p       *Panel
	in      resInfo
	table   string
	keyType reflect.Type
	fields  modelFields
	form    []formSpec
	hooks   hooks[T]
}

// hooks are what a resource built on Add (the users) adds to it.
type hooks[T any] struct {
	// guard refuses an operation on a record ("update", "delete",
	// "action:<name>", or one of its own) with an error; a
	// *validate.Errors one is shown to the user.
	guard func(c *web.Ctx, row T, op string) error
	// sections are more parts of a record's page.
	sections func(c *web.Ctx, row T) ([]section, error)
	// routes adds routes to the resource's group.
	routes func(g *web.Router)
	// removed runs when a record is deleted for good, in its
	// transaction.
	removed func(ctx context.Context, row T) error
}

// section is a part of a record's page.
type section struct {
	Title string
	Body  template.HTML
}

// check runs the guard on an operation, if there is one.
func (r *res[T, F]) check(c *web.Ctx, row T, op string) error {
	if r.hooks.guard == nil {
		return nil
	}
	return r.hooks.guard(c, row, op)
}

// allowed reports whether the guard lets an operation be offered.
func (r *res[T, F]) allowed(c *web.Ctx, row T, op string) bool { return r.check(c, row, op) == nil }

// refused answers a guarded operation: a message on to, or the error.
func refused(c *web.Ctx, err error, to string) error {
	if msg, ok := userError(err); ok {
		return failed(c, msg, to)
	}
	return err
}

func (r *res[T, F]) info() *resInfo { return &r.in }

func (r *res[T, F]) tableName() string { return r.table }

func (r *res[T, F]) query(ctx context.Context) *db.Q[T] {
	q := db.Query[T](ctx)
	if r.Query != nil {
		q = r.Query(q)
	}
	return q
}

func (r *res[T, F]) count(ctx context.Context) (int64, error) { return r.query(ctx).Count() }

func (r *res[T, F]) mount(g *web.Router) {
	n := "admin." + r.Name + "."
	need := func(kind string) *web.Router { return g.With(rbac.Require(r.in.perm(kind))) }
	need("view").Get("/", r.index).Name(n + "index")
	if r.in.soft && r.in.delete {
		need("delete").Get("/trash", r.trash).Name(n + "trash")
		need("delete").Post("/{id}/restore", r.restore).Name(n + "restore")
		need("delete").Post("/{id}/force-delete", r.p.confirmFirst(r.forceDelete)).Name(n + "force-delete")
	}
	if r.in.create {
		need("create").Get("/new", r.newPage).Name(n + "new")
		need("create").Post("/", web.H(r.create)).Name(n + "create")
	}
	need("view").Get("/{id}", r.show).Name(n + "show")
	if r.in.editable {
		need("update").Get("/{id}/edit", r.edit).Name(n + "edit")
		need("update").Post("/{id}", web.H(r.update)).Name(n + "update")
	}
	if r.in.delete {
		need("delete").Post("/{id}/delete", r.p.confirmFirst(r.delete)).Name(n + "delete")
	}
	// Actions check their own permissions, and need the view one too.
	if len(r.Actions) > 0 {
		need("view").Post("/{id}/actions/{action}", r.action).Name(n + "action")
	}
	if len(r.BulkActions) > 0 || r.in.delete {
		need("view").Post("/bulk", r.bulk).Name(n + "bulk")
	}
	if r.hooks.routes != nil {
		r.hooks.routes(g)
	}
}

// can checks a permission of the resource ("view") or the app's own.
func (r *res[T, F]) can(ctx context.Context, perm string) bool {
	if perm == "" {
		perm = "update"
	}
	if strings.Contains(perm, ".") {
		return rbac.Can(ctx, rbac.Permission(perm))
	}
	return rbac.Can(ctx, r.in.perm(perm))
}

func (r *res[T, F]) authorize(ctx context.Context, perm string) error {
	if !r.can(ctx, perm) {
		return web.Error(http.StatusForbidden, "You may not do this.")
	}
	return nil
}

// url returns the resource's URL, with a suffix ("/7/edit").
func (r *res[T, F]) url(suffix string) string { return r.p.base + "/" + r.Name + suffix }

// key parses a record's key from the path.
func (r *res[T, F]) key(c *web.Ctx) (any, error) {
	k, ok := r.parseKey(c.Param("id"))
	if !ok {
		return nil, db.ErrNotFound
	}
	return k, nil
}

// find loads the record in the path, within the resource's scope.
func (r *res[T, F]) find(c *web.Ctx, trashed bool) (T, error) {
	var zero T
	k, err := r.key(c)
	if err != nil {
		return zero, err
	}
	q := r.query(c)
	if trashed {
		q = q.OnlyTrashed()
	}
	return q.Find(k)
}

func (r *res[T, F]) label(row T) string {
	if r.Label != nil {
		return r.Label(row)
	}
	_, k, _ := db.KeyOf(&row)
	return fmt.Sprintf("%s #%v", r.Singular, k)
}

func (r *res[T, F]) keyText(row T) string {
	_, k, _ := db.KeyOf(&row)
	return url.PathEscape(fmt.Sprint(k))
}

func (r *res[T, F]) crumbs(more ...navItem) []navItem {
	return append([]navItem{{Title: r.p.cfg.Title, URL: r.p.URL()}, {Title: r.Title, URL: r.url("")}}, more...)
}

// done flashes msg and sends the browser to to.
func done(c *web.Ctx, msg, to string) error {
	flash(c, "admin.status", msg)
	return c.Redirect(http.StatusSeeOther, to)
}

---
title: Add an admin panel
since: v0.3.0
---

# Add an admin panel

Give your staff pages to list, search, filter, create, edit and delete
the app's records, with the admin interface of module
`anetos.dev/anetos/admin`: no front-end build, permissions from
[roles](roles-and-permissions.md), and every change in the
[audit log](audit-log.md) for the models it tracks.

## Before you start

- Users who sign in with a session: [accounts with make:auth](accounts.md)
  or [authentication](authentication.md) of your own.
- [Roles and permissions](roles-and-permissions.md) (`rbac.ForApp`):
  `make:admin` sets them up when the app doesn't.

The code here comes from [`examples/admin`](../../../examples/admin), a
shop's back office.

## Steps

### 1. Add the admin

In a project made with `anetos new` and `anetos make:auth`:

```sh
go tool anetos make:admin
go run . migrate      # the roles tables
```

It adds the module to `go.mod` and writes `admin.go`, with `setupAdmin`,
which `setup` now calls after `setupAuth`: it sets up roles with an
`admin` role that may do everything, creates the admin for the app's
users, adds the resources of `app/admin`, and mounts it at `/admin` with
the middleware of the app's pages. By hand, it looks like this:

```go
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
	return p.Mount(r, sessions.Middleware, web.CSRF(), a.Middleware)
}
```

(Copied from [`examples/admin`](../../../examples/admin/main.go), region `setup-admin`.)

Only signed-in users with the permission `admin.access` get in: guests
are sent to sign in (`AUTH_LOGIN_URL`), others get 403. Let a user in
with the `admin` role:

```sh
go run . rbac:assign 1 admin
```

### 2. Add a resource per model

```sh
go tool anetos make:admin:resource Product
```

It writes `app/admin/products.go`, the resource for `models.Product`, from
the model's fields, and adds it to `app/admin/admin.go`. It's yours to
change. A resource's form is a struct of its own: only its fields can
be changed, its `validate` tags check them (see [validation](validation.md)),
and `Apply` copies them into the record before it is saved:

```go
// ProductForm is what the admin edits of a product: only these fields
// can be changed, checked by their validate tags.
type ProductForm struct {
	Name       string  `json:"name" validate:"required|max:255"`
	SKU        string  `json:"sku" label:"SKU" validate:"required|max:50|alpha_dash"`
	CategoryID int64   `json:"category_id" label:"Category" admin:"select" validate:"required"`
	Price      float64 `json:"price" validate:"required|min:0" admin:"help=In dollars."`
	Stock      int     `json:"stock" validate:"min:0"`
	Status     string  `json:"status" admin:"select=draft|active|archived" validate:"required|in:draft,active,archived"`
}
```

(Copied from [`examples/admin/admin.go`](../../../examples/admin/admin.go), region `form`.)

Fields are rendered by their Go type: text for strings, numbers, a
checkbox for bools, `admin.DateTime` for a date and time, `anetos.Date`
for a date. Each needs a `json` tag (or a `form` tag): the name it is
posted under. The `admin` tag changes that: `textarea`, `password`,
`select=a|b|c` (fixed choices) or `select` (choices from
`Resource.Choices`), `help=…`, and `-` to leave a field out; `label`
names it. A select's value must be one of its choices; a field left out
keeps the value `Edit` gave it, whatever a request sends.

The resource says what the list shows and how records are edited:

```go
// products is the admin's resource for products, at /admin/products.
func products() admin.Resource[Product, ProductForm] {
	return admin.Resource[Product, ProductForm]{
		Name: "products",
		Columns: []admin.Column[Product]{
			admin.Field[Product]("Name", "name"),
			admin.Field[Product]("SKU", "sku"),
			{Title: "Price", Column: "price", Sortable: true, Value: func(p Product) any { return dollars(p.Price) }},
			admin.Field[Product]("Stock", "stock"),
			admin.Field[Product]("Status", "status"),
		},
		Search:  []string{"name", "sku"},
		Filters: []admin.Filter[Product]{admin.Equals[Product]("status", "Status", admin.Choices("draft", "active", "archived")...)},
		Label:   func(p Product) string { return p.Name },
		Edit: func(p Product) ProductForm {
			return ProductForm{Name: p.Name, SKU: p.SKU, CategoryID: p.CategoryID,
				Price: float64(p.Price) / 100, Stock: p.Stock, Status: p.Status}
		},
		Apply: func(ctx context.Context, in ProductForm, p *Product) error {
			// Checks that need the database are done here.
			taken, err := db.Query[Product](ctx).WithTrashed().
				Where(db.C("sku").Eq(in.SKU), db.C("id").Ne(p.ID)).Exists()
			if err != nil {
				return err
			}
			if taken {
				return validate.Fail("sku", "Another product has this SKU.")
			}
			p.Name, p.SKU, p.CategoryID = in.Name, in.SKU, in.CategoryID
			p.Price, p.Stock, p.Status = int64(in.Price*100+0.5), in.Stock, in.Status
			return nil
		},
		// The categories to choose from, from the database.
		Choices: map[string]func(ctx context.Context) ([]admin.Choice, error){
			"category_id": categoryChoices,
		},
		Actions: []admin.Action[Product]{{
			Name: "archive", Title: "Archive", Confirm: "Archive this product?",
			When: func(p Product) bool { return p.Status != "archived" },
			Run: func(ctx context.Context, p *Product) error {
				p.Status = "archived"
				return db.Update(ctx, p)
			},
		}},
		BulkActions: []admin.BulkAction[Product]{{
			Name: "activate", Title: "Activate",
			Run: func(_ context.Context, q *db.Q[Product]) (int64, error) {
				return q.Update(db.C("status").Set("active"))
			},
		}},
	}
}
```

(Copied from [`examples/admin/admin.go`](../../../examples/admin/admin.go), region `products`.)

- `Columns` are the list's; `admin.Field` shows a column of the model,
  sortable. A column with `Value` shows what the function returns: text,
  a number, a time (in `APP_TIMEZONE`), or a `view.Component` or
  `template.HTML` for markup. `Details` are the lines of a record's page,
  `Columns` by default.
- `Search` are the text columns the search box looks in (`LIKE`,
  ignoring case; on SQLite, only for ASCII letters); `Filters` narrow the list (`admin.Equals` for a column's value);
  `Sort` is the default order (newest first otherwise).
- `Query` scopes everything the admin sees and changes of the model:
  `func(q *db.Q[Order]) *db.Q[Order] { return q.Where(…) }`. A record that
  `Apply` would put outside it isn't saved.
- `Edit` and `Apply` make records editable; without them, the resource is
  only listed and shown. `NoCreate` and `NoDelete` hide creating and
  deleting.
- `Actions` are buttons on a record's page; `BulkActions` act on the
  records selected in the list (always within `Query`). `Run` may return
  `validate.Fail`, which is shown to the user.

Models with `db.SoftDeletes` get a trash: deleting moves records there,
where they can be restored or deleted for good.

### 3. Give staff roles

Each resource has four permissions, `admin.<name>.view`, `.create`,
`.update` and `.delete`, which `admin.New` and `admin.Add` declare, so
roles stored in the database can grant them. Roles declared in code
name them with `admin.PermissionsOf`, among the app's permissions:

```go
// editor are the permissions of editors: they manage products, and may
// only look at categories. The admin declares its permissions too
// (admin.New, admin.Add); roles in code name them in the app's list.
var editor = slices.Concat(
	[]rbac.Permission{admin.Access},
	admin.PermissionsOf("products"),
	admin.PermissionsOf("categories", "view"),
)

// roles are the staff's roles: administrators may do everything.
var roles = []rbac.Role{
	{Name: "admin", Title: "Administrator", Super: true},
	{Name: "editor", Title: "Editor", Permissions: editor},
}
```

(Copied from [`examples/admin`](../../../examples/admin/main.go), region `roles`.)

Users see only the resources they may view, and only the buttons of
what they may do; the routes check the permissions too. An action needs
`update` unless its `Permission` names another one: `"view"`,
`"create"`, `"delete"`, or a permission of the app's own (with a dot).

## How it works

The admin is a library, not a plugin: its pages need the app's own types
(the user type, the models), which code in the app can name. Its pages
are `html/template`, embedded in the module, with htmx and a stylesheet
of their own, so there is nothing to build; they are served with a
strict Content-Security-Policy, kept out of frames and caches. Forms are
bound and validated as [forms](forms.md) are: a failed post goes back to
the form with the errors and what was typed. Writes go through package
`db`, so models tracked by the [audit log](audit-log.md) record who did
what. Routes are named `admin.*` (`admin.products.edit`).

To serve the admin on a host of its own, set `ADMIN_HOST`
(`admin.example.com`): it answers only there, at the root unless
`ADMIN_PATH` is set. Settings: [`ADMIN_*`](../reference/configuration.md#admin).

## Testing it

Sign in as the app's tests do, then use the admin's pages:

```go
func TestEditorManagesProducts(t *testing.T) {
	app := signIn(t, "editor@example.com")
	books, err := db.Query[Category](app.Context()).Where(db.C("name").Eq("Books")).First()
	if err != nil {
		t.Fatal(err)
	}
	app.Get("/admin").AssertOK().AssertSee("Shop admin", "Eve", "Products", "Categories")
	app.Get("/admin/products/new").AssertOK().AssertSee(`<option value="` + strconv.FormatInt(books.ID, 10) + `"`)

	form := url.Values{"name": {"Go in Action"}, "sku": {"go-1"}, "category_id": {strconv.FormatInt(books.ID, 10)},
		"price": {"39.99"}, "stock": {"12"}, "status": {"draft"}}
	app.PostForm("/admin/products", form).Follow().AssertSee("Product created.", "$39.99")
	p, err := db.Query[Product](app.Context()).Where(db.C("sku").Eq("go-1")).First()
	if err != nil || p.Price != 3999 || p.CategoryID != books.ID {
		t.Fatalf("created %+v, %v", p, err)
	}
	app.Get("/admin/products/new")
	app.PostForm("/admin/products", form).AssertValidationErrors("sku") // taken

	path := fmt.Sprintf("/admin/products/%d", p.ID)
	form.Set("price", "35")
	app.PostForm(path, form).Follow().AssertSee("Saved.", "$35.00")
	app.PostForm(path+"/actions/archive", nil).Follow().AssertSee("Archive: done.", "archived")
	app.Get("/admin/products?status=archived&q=GO").AssertSee("Go in Action")
	app.PostForm(path+"/delete", nil).Follow().AssertSee("Go in Action moved to the trash.")

	// Every change is in the audit log, by Eve.
	events, _, err := audit.History(app.Context(), audit.Subject{Type: "products", ID: strconv.FormatInt(p.ID, 10)}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range events {
		if e.Entry != nil {
			actions = append(actions, e.Entry.Action)
		}
	}
	if fmt.Sprint(actions) != "[deleted updated updated created]" {
		t.Errorf("history %v", actions)
	}

	// Categories: editors may look, not change.
	app.Get("/admin/categories").AssertOK().AssertSee("Books", "Games").AssertDontSee(`href="/admin/categories/new"`)
	app.PostForm("/admin/categories", url.Values{"name": {"Music"}}).AssertForbidden()
}
```

(Copied from [`examples/admin/main_test.go`](../../../examples/admin/main_test.go), region `test-products`.)

`make:admin` writes `admin_test.go`, which checks who gets in.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `admin: New needs the app's roles and permissions` | `rbac.ForApp` wasn't called before `admin.New` | Set up roles first (`make:admin` does) |
| `rbac: unknown permission "admin.posts.view", in role "editor"` | A role in code names an admin permission the app's list lacks | Add `admin.PermissionsOf("posts")` to the permissions passed to `rbac.ForApp` |
| 403 on every admin page | The user lacks `admin.access` | `go run . rbac:assign <user-id> admin`, or a role with `admin.Access` |
| A resource isn't in the menu | The user lacks its `view` permission | Give `admin.<name>.view` |
| `admin: resource posts: column "titel" is not a column of posts` | A column, search, or sort names a column the model doesn't have | Use the database's column name |
| `form field X: a … can't be edited in a form` | The form struct has a field of a type forms don't render | Use a string, number, bool, `admin.DateTime` or `anetos.Date`, or tag it `admin:"-"` |

## Next steps

- [Roles and permissions](roles-and-permissions.md)
- [Keep an audit log](audit-log.md): who changed what in the admin
- [CLI reference](../reference/cli.md#anetos-make): `make:admin` and
  `make:admin:resource`

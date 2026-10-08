---
title: Add an admin panel
since: v0.3.0
group: "Accounts and security"
weight: 309
---

# Add an admin panel

Give your staff pages to list, search, filter, create, edit and delete
the app's records, and to manage users and roles, with the admin
interface of module `anetos.dev/anetos/admin`: no front-end build,
permissions from [roles](roles-and-permissions.md), and every change in
the [audit log](audit-log.md) for the models it tracks.

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
users, adds the users (`app/admin/users.go`), the roles and the resources
of `app/admin`, and mounts it at `/admin` with the middleware of the
app's pages. By hand, it looks like this:

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

// support are the permissions of support staff: they look after the
// staff's accounts, and may act as them to see what they see.
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
```

(Copied from [`examples/admin`](../../../examples/admin/main.go), region `roles`.)

Users see only the resources they may view, and only the buttons of
what they may do; the routes check the permissions too. An action needs
`update` unless its `Permission` names another one: `"view"`,
`"create"`, `"delete"`, or a permission of the app's own (with a dot).

### 4. Manage users

`make:admin` writes `app/admin/users.go`, which adds the app's users with
`admin.Users`: a resource as any other (its columns, search and form),
and account management on each user's page. By hand:

```go
// UserForm is what the admin edits of a member of staff.
type UserForm struct {
	Name  string `json:"name" validate:"required|max:255"`
	Email string `json:"email" validate:"required|email|max:255"`
}

// addUsers adds the staff to the admin, with their accounts: disabling,
// signing out, API tokens, roles, acting as them.
func addUsers(p *admin.Panel, a *auth.Auth[*User]) error {
	return admin.Users(p, admin.Resource[User, UserForm]{
		Name:     "users",
		Title:    "Staff",
		Singular: "Member",
		Columns: []admin.Column[User]{
			admin.Field[User]("Name", "name"),
			admin.Field[User]("Email", "email"),
		},
		Search:   []string{"name", "email"},
		Label:    func(u User) string { return u.Name },
		NoCreate: true, // seed creates them; this shop has no sign-up
		Edit:     func(u User) UserForm { return UserForm{Name: u.Name, Email: u.Email} },
		Apply: func(_ context.Context, in UserForm, u *User) error {
			u.Name, u.Email = in.Name, strings.ToLower(in.Email)
			return nil
		},
	}, admin.Accounts[*User]{Auth: a, DisabledAt: "disabled_at"})
}
```

(Copied from [`examples/admin/admin.go`](../../../examples/admin/admin.go), region `users-resource`.)

On a user's page, as the `Accounts` allow:

- **Disable and enable.** Package `auth` refuses disabled users (its
  `Users.Disabled`): they are signed out at their next request, can't
  sign in, and their API tokens stop working.
- **Verification:** mark the address verified, or email the link again;
  email a password-reset link (`SendVerification`, `SendPasswordReset`,
  which `make:auth` exports from `app/handlers`).
- **Sign out everywhere:** every browser and device, at once. It replaces
  the user's session key (`Users.SessionKey`, `SetSessionKey`).
- **API tokens:** listed (name, abilities, last use), revoked one by one
  or all at once.
- **Roles and permissions,** by scope: given and taken away, by those
  with `admin.roles.assign`, and only roles they could hold themselves.
- **Act as user,** below.

Disabling and signing out everywhere need two columns of the users table,
which `make:auth` makes since v0.3, and the `auth.Users` that read them:

```go
// users tells package auth how to find users, which are disabled, and
// how to sign them out everywhere.
var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		return found(db.Find[User](ctx, n))
	},
	ByLogin: func(ctx context.Context, email string) (*User, error) {
		return found(db.Query[User](ctx).Where(db.C("email").Eq(strings.ToLower(email))).First())
	},
	Disabled:   func(u *User) bool { return u.DisabledAt != nil },
	SessionKey: func(u *User) string { return u.SessionKey },
	SetSessionKey: func(ctx context.Context, u *User, key string) error {
		_, err := db.Query[User](ctx).WhereKeys(u.ID).Update(db.C("session_key").Set(key))
		return err
	},
}
```

(Copied from [`examples/admin/models.go`](../../../examples/admin/models.go), region `users`.)

An app made with an older `make:auth` adds `disabled_at` (a nullable
timestamp) and `session_key` (a string) with a migration, the fields to
`User`, and those three functions to `models.Users`.

Changing a user needs every permission they have, in every scope: support
staff can't edit, disable or act as an administrator. No one disables,
deletes, acts as or changes the roles of themselves here. Each of these
is recorded in the [audit log](audit-log.md) (`user.disabled`,
`rbac.role_assigned`, …), when the app keeps one.

### 5. Manage roles

`admin.Roles(p)` adds the roles pages: every role, with its permissions
and who has it. Roles declared in code are shown; roles stored in the
database are created, edited and deleted here, from the permissions the
admin has (no one makes a role with more than they have). Permissions:
`admin.roles.view`, `.create`, `.update` and `.delete`.

### 6. Act as a user

To see what a user sees, an admin with `admin.users.impersonate` presses
**Act as user**: they are signed in as the user, and the app's pages show
a banner with a button that stops it. `make:admin` puts the banner in
`views/layout.templ`; in your own layout, put it first in `<body>`:

```templ
// illustrative
<body>
	@admin.Banner()
```

While it lasts, the audit log attributes what is done to the admin,
acting as the user (`ActingAs`); jobs it dispatches are attributed to the
admin alone. Its start and its stop are logged (`user.impersonated`,
`user.impersonation_ended`); signing out ends it too, unlogged. The admin
can't act as disabled users, as those with permissions they lack, or as
anyone while already acting as someone. Each request checks that the
admin may still sign in (not that they still have
`admin.users.impersonate`: taking it away ends nothing under way). The
admin's remember-me cookie goes when it starts. An admin at a host of
its own (`ADMIN_HOST`) doesn't offer it: the app's pages are on another
host, with another session.

### 7. Put widgets on the dashboard

The admin's first page shows widgets above the resources:
`p.Dashboard(widgets...)`, before `Mount`. A widget loads its content
when the page is shown: figures, a bar chart (drawn as SVG), a table
whose rows link to the admin's pages, any `view.Component`, and a link.
A widget that fails (or panics) says so in its place, the error is
logged, and the rest of the page works. A widget's `Permission` must be
declared, as a resource's are; the built-in ones' are declared for you.

```go
// lowStock is a dashboard widget: the active products running out.
func lowStock() admin.Widget {
	return admin.Widget{Title: "Low stock", Permission: "admin.products.view", Load: func(ctx context.Context) (admin.Content, error) {
		low, err := db.Query[Product](ctx).Where(db.C("status").Eq("active"), db.C("stock").Lt(5)).
			OrderBy(db.C("stock").Asc()).Limit(10).Get()
		if err != nil {
			return admin.Content{}, err
		}
		t := &admin.Table{Headers: []string{"Product", "In stock"}}
		for _, p := range low {
			t.Rows = append(t.Rows, []string{p.Name, strconv.Itoa(p.Stock)})
			t.Links = append(t.Links, fmt.Sprintf("products/%d", p.ID)) // the admin's page
		}
		return admin.Content{
			Stats: []admin.Stat{{Label: "Running out", Value: strconv.Itoa(len(low)), Warn: len(low) > 0}},
			Table: t,
			Link:  &admin.Link{Title: "Every active product", URL: "products?status=active&sort=stock"},
		}, nil
	}}
}
```

(Copied from [`examples/admin/admin.go`](../../../examples/admin/admin.go), region `widget`.)

Links and table rows without a leading `/` lead to the admin's own pages
(`products/7`), and are left out if there is no such page. Built in:

| Widget | Shows | Permission |
|---|---|---|
| `admin.SignUps[T](title, column)` | Model `T`'s records: in all, new in 7 and 30 days, and a bar a day for 30 days (UTC), by `column` (`"created_at"`) | `admin.access` |
| `admin.QueueHealth(q, queues...)` | The jobs waiting on the default queue and on `queues`, and the failed ones | `admin.jobs.view` |
| `admin.AIUsage()` | Tokens and cost of the app's AI calls in 24 hours and 30 days, and tokens a day (`ai.TrackUsage`) | `admin.access` |
| `admin.RecentActivity(n)` | The audit log's latest `n` entries | `admin.activity.view` |

### 8. See who did what

`admin.Activity(p)` adds the activity pages, over the app's
[audit log](audit-log.md): its entries and bulk writes, newest first,
filtered by who (`user:42`), what (`updated`), which kind of record
(`posts`) and which one, and when. An entry's page shows its changes,
field by field. The pages of a resource whose model the log tracks show
a record's latest history. Permission: `admin.activity.view`: it shows
what the log keeps, changed values included (fields the log leaves out,
such as passwords, aren't there), so give it to those who may see them.

`make:admin` adds `admin.Activity` and the `RecentActivity` widget to
`admin.go` when the app keeps an audit log. If you add the log later, add
them to `setupAdmin` yourself, before `p.Dashboard`:

```go
// illustrative
widgets = append(widgets, admin.RecentActivity(10))
if err := admin.Activity(p); err != nil {
	return err
}
```

### 9. Watch jobs and scheduled tasks

`admin.Jobs(p, q)` adds the jobs page: the queues' sizes, and the failed
jobs with their error and payload, to retry or forget one by one or all
at once (`admin.jobs.view`, `.update`). `admin.Schedule(p, s)` adds the
scheduled tasks: each one's schedule, next run and last run (kept in the
cache; see [Scheduling](scheduling.md#4-check-and-run-tasks)), and
**Run now**, which runs the task in the background, as the scheduler
does (`admin.schedule.view`, `.run`): a task running from this process
isn't started again until it ends, and the app's shutdown waits for it
until its deadline. Retrying, forgetting and running are recorded in the
audit log. A failed job's page shows its payload, so `admin.jobs.view`
is for those who may see what jobs carry.

`make:admin` adds these when the app has them: the queue and the
scheduler of `anetos new`, and the activity when the app keeps an audit
log.

### 10. Protect it

The admin asks for the password again before dangerous actions:
deleting, disabling, giving and taking roles and permissions, editing
roles, acting as a user, forgetting every failed job, and actions marked
`Danger`. After it, the user does the action again; the password holds
for `AUTH_CONFIRM_TTL` (15 minutes). Users who sign in without a
password (Google, GitHub) confirm by signing out and in again; set
`ADMIN_CONFIRM=false` to not ask at all.

```env
ADMIN_TWO_FACTOR=required          # only users with two-factor sign-in on
ADMIN_ALLOW_IPS=10.0.0.0/8,203.0.113.7   # only these addresses; others get 404
```

With `ADMIN_TWO_FACTOR=required`, users without
[two-factor sign-in](two-factor.md) are told to turn it on, at
`AUTH_TWO_FACTOR_URL` (`make:auth`'s page). With `ADMIN_ALLOW_IPS`, the
client's address is read behind trusted proxies only
(`HTTP_TRUSTED_PROXIES`). A user's page shows whether they have
two-factor sign-in on, and **Turn off two-factor sign-in** helps someone
who lost their phone (not oneself).

The user's name, at the top of every page, links to their account
settings in the app (`AUTH_SETTINGS_URL`, [`make:auth`](accounts.md)'s
`/settings`), when the app has that page: their password, two-factor
sign-in, language and time zone.

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
	// Deleting asks for the password again first (ADMIN_CONFIRM).
	app.PostForm(path+"/delete", nil).AssertRedirect("/admin/confirm?back=" + url.QueryEscape("/admin/products?status=archived&q=GO"))
	app.PostForm("/admin/confirm", url.Values{"password": {"secret password"}, "back": {path}}).AssertRedirect(path)
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

And the accounts:

```go
func TestStaffAccounts(t *testing.T) {
	app := signIn(t, "admin@example.com")
	eve, err := db.Query[User](app.Context()).Where(db.C("email").Eq("editor@example.com")).First()
	if err != nil {
		t.Fatal(err)
	}
	page := fmt.Sprintf("/admin/users/%d", eve.ID)

	// Acting as Eve: the app as she sees it, with the banner, once Ada
	// confirmed her password.
	app.PostForm("/admin/confirm", url.Values{"password": {"secret password"}}).AssertRedirect("/admin")
	app.PostForm(page+"/actions/impersonate", nil).AssertRedirect("/")
	app.Get("/").AssertSee("Hello, Eve", "acting as <strong>Eve</strong>")
	app.PostForm("/admin/impersonation/stop", nil).AssertRedirect(page)
	app.Get("/").AssertSee("Hello, Ada").AssertDontSee("acting as")

	// Disabled, Eve can't sign in. Back as herself, Ada confirms her
	// password again.
	app.PostForm("/admin/confirm", url.Values{"password": {"secret password"}})
	app.PostForm(page+"/actions/disable", nil).Follow().AssertSee("Account disabled.")
	app.PostForm("/logout", nil)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"editor@example.com"}, "password": {"secret password"}}).AssertValidationErrors("email")

	// Support staff look after accounts, but not those with permissions
	// they don't have, such as Eve's.
	app.PostForm("/login", url.Values{"email": {"support@example.com"}, "password": {"secret password"}}).AssertRedirect("/admin")
	app.PostForm(page+"/actions/enable", nil).Follow().AssertSee("You may not manage Eve")
}
```

(Copied from [`examples/admin/main_test.go`](../../../examples/admin/main_test.go), region `test-users`.)

`make:admin` writes `admin_test.go`, which checks who gets in.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `admin: New needs the app's roles and permissions` | `rbac.ForApp` wasn't called before `admin.New` | Set up roles first (`make:admin` does) |
| `rbac: unknown permission "admin.posts.view", in role "editor"` | A role in code names an admin permission the app's list lacks | Add `admin.PermissionsOf("posts")` to the permissions passed to `rbac.ForApp` |
| 403 on every admin page | The user lacks `admin.access` | `go run . rbac:assign <user-id> admin`, or a role with `admin.Access` |
| A resource isn't in the menu | The user lacks its `view` permission | Give `admin.<name>.view` |
| `admin: Users: auth.Users.Disabled doesn't report users whose disabled_at is set` | `Accounts.DisabledAt` names a column `auth` doesn't read | Add `Disabled` to the app's `auth.Users` |
| "You may not manage X: they have permissions you don't." | X has a permission the admin lacks somewhere | Have someone with more permissions do it |
| No "Disable" or "Sign out everywhere" on a user's page | The users table or `auth.Users` lack `disabled_at` or the session key | Add them (step 4) |
| `admin: resource posts: column "titel" is not a column of posts` | A column, search, or sort names a column the model doesn't have | Use the database's column name |
| `form field X: a … can't be edited in a form` | The form struct has a field of a type forms don't render | Use a string, number, bool, `admin.DateTime` or `anetos.Date`, or tag it `admin:"-"` |

## Next steps

- [Roles and permissions](roles-and-permissions.md)
- [Accounts with make:auth](accounts.md)
- [Keep an audit log](audit-log.md): who changed what in the admin
- [CLI reference](../reference/cli.md#anetos-make): `make:admin` and
  `make:admin:resource`

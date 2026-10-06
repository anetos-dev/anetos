# Admin: a shop's back office

Staff sign in and manage a shop's products and categories, with the
admin interface of module `anetos.dev/anetos/admin`:

- **Resources** ([`admin.go`](admin.go)): products and categories, each a
  list (columns, search, a status filter, sorting, pages), a record's page,
  and forms to create and edit. A form is a struct of its own
  (`ProductForm`): only its fields can be changed, its validate tags
  check them, and `Apply` copies them into the product (prices in
  dollars in the form, in cents in the database).
- **Choices from the database**: a product's category is chosen from the
  categories.
- **Actions**: archive a product from its page; activate the selected
  products from the list.
- **Trash**: products use soft deletes, so deleted ones go to the trash,
  where they can be restored or deleted for good.
- **Roles** ([`main.go`](main.go)): administrators may do everything;
  editors manage products and may only look at categories; support staff
  look after the staff's accounts, with the admin's permissions
  (`admin.PermissionsOf`). Roles stored in the database are made on the
  roles pages (`admin.Roles`).
- **Staff accounts** (`admin.Users`): disable and enable them, sign them
  out everywhere, revoke their API tokens, give and take away roles, and
  act as them (the app's home page shows the banner, `admin.Banner`).
  No one manages someone with permissions they lack: support can't touch
  an editor's account.
- **Audit log**: products are tracked (package `audit`), so every change
  made in the admin is logged with who made it.

## Run it

```sh
anetos key:generate >> .env   # APP_KEY, once (the anetos developer tool)
export APP_ENV=development HTTP_ADDR=:8080
go run . migrate
go run . seed                 # admin@, editor@ and support@example.com
go run .
```

Then open <http://localhost:8080/admin> and sign in as
`admin@example.com`, `editor@example.com` or `support@example.com`,
password `secret password`.

## Tests

`go test` signs in and uses the admin against a fresh SQLite database
with `anetostest`: [`main_test.go`](main_test.go).

The guide: [Add an admin panel](../../docs/site/guides/admin.md).

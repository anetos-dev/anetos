// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/url"
	"strconv"
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/db"
)

// signIn seeds the users and signs in with the email.
func signIn(t *testing.T, email string) *anetostest.App {
	t.Helper()
	app := anetostest.New(t, setup)
	if err := seed(app.Context()); err != nil {
		t.Fatal(err)
	}
	app.Get("/admin").AssertRedirect("/login")
	app.Get("/login").AssertOK()
	app.PostForm("/login", url.Values{"email": {email}, "password": {"secret password"}}).AssertRedirect("/admin")
	return app
}

// region: test-products
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

// endregion

func TestAdministrator(t *testing.T) {
	app := signIn(t, "admin@example.com")
	app.Get("/admin/categories/new").AssertOK()
	app.PostForm("/admin/categories", url.Values{"name": {"Music"}}).Follow().AssertSee("Category created.", "Music")
	for _, sku := range []string{"a", "b"} {
		if err := db.Create(app.Context(), &Product{Name: "P " + sku, SKU: sku, Status: "draft"}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := db.Pluck(db.Query[Product](app.Context()), db.Col[int64]("id"))
	if err != nil {
		t.Fatal(err)
	}
	app.PostForm("/admin/products/bulk", url.Values{"action": {"activate"}, "ids": {fmt.Sprint(ids[0]), fmt.Sprint(ids[1])}}).
		Follow().AssertSee("Activate: 2 of 2.")
	if n, _ := db.Query[Product](app.Context()).Where(db.C("status").Eq("active")).Count(); n != 2 {
		t.Errorf("%d active", n)
	}
	app.PostForm("/logout", nil).AssertRedirect("/login")
	app.Get("/admin").AssertRedirect("/login")
}

// region: test-users
func TestStaffAccounts(t *testing.T) {
	app := signIn(t, "admin@example.com")
	eve, err := db.Query[User](app.Context()).Where(db.C("email").Eq("editor@example.com")).First()
	if err != nil {
		t.Fatal(err)
	}
	page := fmt.Sprintf("/admin/users/%d", eve.ID)

	// Acting as Eve: the app as she sees it, with the banner.
	app.PostForm(page+"/actions/impersonate", nil).AssertRedirect("/")
	app.Get("/").AssertSee("Hello, Eve", "acting as <strong>Eve</strong>")
	app.PostForm("/admin/impersonation/stop", nil).AssertRedirect(page)
	app.Get("/").AssertSee("Hello, Ada").AssertDontSee("acting as")

	// Disabled, Eve can't sign in.
	app.PostForm(page+"/actions/disable", nil).Follow().AssertSee("Account disabled.")
	app.PostForm("/logout", nil)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"editor@example.com"}, "password": {"secret password"}}).AssertValidationErrors("email")

	// Support staff look after accounts, but not those with permissions
	// they don't have, such as Eve's.
	app.PostForm("/login", url.Values{"email": {"support@example.com"}, "password": {"secret password"}}).AssertRedirect("/admin")
	app.PostForm(page+"/actions/enable", nil).Follow().AssertSee("You may not manage Eve")
}

// endregion

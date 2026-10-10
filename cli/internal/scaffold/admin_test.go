// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// spaces are runs of spaces and tabs, which gofmt aligns.
var spaces = regexp.MustCompile(`[ \t]+`)

func TestMakeAdmin(t *testing.T) {
	fresh := func(auth bool) string {
		dir := filepath.Join(t.TempDir(), "shop")
		if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", Replace: "../../.."}); err != nil {
			t.Fatal(err)
		}
		if auth {
			if _, err := MakeAuth(dir, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	write := func(dir, name, src string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := MakeAdmin(fresh(false)); err == nil || !strings.Contains(err.Error(), "make:auth first") {
		t.Errorf("without make:auth: %v", err)
	}
	if _, err := MakeAdminResource(fresh(false), "Post"); err == nil || !strings.Contains(err.Error(), "make:admin first") {
		t.Errorf("resource without make:admin: %v", err)
	}

	dir := fresh(true)
	res, err := MakeAdmin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Created, []string{"admin.go", "app/admin/admin.go", "app/admin/users.go", "admin_test.go"}) || !res.Wired || !res.RBAC ||
		!slices.Equal(res.Env, []string{".env", ".env.example"}) || !res.Users || !res.Disabled || !res.SessionKey || !res.Banner {
		t.Errorf("result %+v", res)
	}
	if s := read(t, filepath.Join(dir, "app/admin/users.go")); !strings.Contains(s, `DisabledAt: "disabled_at",`) ||
		!strings.Contains(s, "handlers.SendPasswordReset(ctx, a, u)") || !strings.Contains(s, "u.EmailVerifiedAt = nil") {
		t.Errorf("users.go:\n%s", s)
	}
	if s := read(t, filepath.Join(dir, "views/layout.templ")); !strings.Contains(s, "\t\t\t@admin.Banner()") || !strings.Contains(s, "import (\n\t\"anetos.dev/anetos/admin\"\n") {
		t.Errorf("layout.templ:\n%s", s)
	}
	if _, ok := addBanner([]byte("package views\n\ntempl X() {\n<div></div>\n}\n")); ok {
		t.Error("addBanner without a body")
	}
	main := read(t, filepath.Join(dir, "main.go"))
	if !strings.Contains(main, adminCall) || strings.Contains(main, authCall) {
		t.Errorf("main.go:\n%s", main)
	}
	if s := read(t, filepath.Join(dir, "admin.go")); !strings.Contains(s, `appadmin "example.com/shop/app/admin"`) || !strings.Contains(s, "rbac.New(app, nil, roles...)") {
		t.Errorf("admin.go:\n%s", s)
	}
	if s := read(t, filepath.Join(dir, ".env.example")); strings.Count(s, "\nADMIN_PATH=\n") != 1 {
		t.Errorf(".env.example:\n%s", s)
	}
	if _, err := MakeAdmin(dir); err == nil || !strings.Contains(err.Error(), "setupAdmin") {
		t.Errorf("twice: %v", err)
	}

	// A resource, from the model's fields.
	write(dir, "app/models/post.go", `package models

import (
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
)

type Post struct {
	db.Model
	db.SoftDeletes
	Title       string     `+"`db:\"title\"`"+`
	Body        *string
	Views       int64      `+"`db:\"views\"`"+`
	Featured    bool
	PublishedAt *time.Time `+"`db:\"published_at\"`"+`
	EditedAt    time.Time
	Day         anetos.Date
	APIToken    string
	Meta        map[string]any `+"`db:\",json\"`"+`
	Skip        string `+"`db:\"-\"`"+`
	secret      string
}
`)
	files, err := MakeAdminResource(dir, "post")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"app/admin/posts.go", "app/admin/admin.go"}) {
		t.Errorf("files %v", files)
	}
	got := read(t, filepath.Join(dir, "app/admin/posts.go"))
	for _, want := range []string{
		"type PostForm struct {\n\tTitle string `json:\"title\"`\n\tBody *string `json:\"body\"`\n\tViews int64 `json:\"views\"`\n\tFeatured bool `json:\"featured\"`\n\tPublishedAt admin.DateTime `json:\"published_at\"`\n\tEditedAt admin.DateTime `json:\"edited_at\"`\n\tDay anetos.Date `json:\"day\"`\n}",
		`Name: "posts",`,
		`admin.Field[models.Post]("ID", "id"),`,
		`admin.Field[models.Post]("Title", "title"),`,
		`admin.Field[models.Post]("Created", "created_at"),`,
		`Search: []string{"title", "body"},`,
		"// Left out of the form: APIToken, Meta.",
		"EditedAt: admin.DateTime{Time: m.EditedAt},",
		"f.PublishedAt = admin.DateTime{Time: *m.PublishedAt}",
		"m.PublishedAt = &t",
		"m.EditedAt = in.EditedAt.Time",
		"m.Day = in.Day",
		`"anetos.dev/anetos"`,
		`"example.com/shop/app/models"`,
	} {
		if !strings.Contains(spaces.ReplaceAllString(got, " "), spaces.ReplaceAllString(want, " ")) {
			t.Errorf("posts.go lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "Skip") {
		t.Errorf("posts.go has unexported or db:\"-\" fields:\n%s", got)
	}
	if list := read(t, filepath.Join(dir, "app/admin/admin.go")); !strings.Contains(list, "error{\n\tPosts,\n}") {
		t.Errorf("admin.go:\n%s", list)
	}
	if _, err := MakeAdminResource(dir, "Post"); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("twice: %v", err)
	}
	if _, err := MakeAdminResource(dir, "Nope"); err == nil || !strings.Contains(err.Error(), "no struct type Nope") {
		t.Errorf("no model: %v", err)
	}
	// A model with no editable fields gets an empty form.
	write(dir, "app/models/category.go", "package models\n\nimport \"anetos.dev/anetos/db\"\n\ntype Category struct {\n\tdb.Model\n}\n")
	if _, err := MakeAdminResource(dir, "Category"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "app/admin/categories.go")); !strings.Contains(got, "return CategoryForm{}") || !strings.Contains(got, `Name: "categories"`) {
		t.Errorf("categories.go:\n%s", got)
	}
	// Without dates, the standard library's imports stay in a group of
	// their own; "an" before a vowel.
	write(dir, "app/models/order.go", "package models\n\nimport \"anetos.dev/anetos/db\"\n\ntype Order struct {\n\tdb.Model\n\tTotal int64\n}\n")
	if _, err := MakeAdminResource(dir, "Order"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "app/admin/orders.go")); !strings.Contains(got, "import (\n\t\"context\"\n\n\t\"anetos.dev/anetos/admin\"\n\n") ||
		!strings.Contains(got, "what the admin edits of an order:") {
		t.Errorf("orders.go:\n%s", got)
	}
	if list := read(t, filepath.Join(dir, "app/admin/admin.go")); !strings.Contains(list, "error{\n\tPosts,\n\tCategories,\n\tOrders,\n}") {
		t.Errorf("admin.go:\n%s", list)
	}
	if _, err := addResource([]byte("package admin\n"), "X"); err == nil {
		t.Error("addResource without a list")
	}
	if _, err := addResource([]byte("package admin\n\nvar Resources = []int{"), "X"); err == nil {
		t.Error("addResource to a file that doesn't parse")
	}
	for src, want := range map[string]string{
		"package admin\n\nvar Resources = []func() error{Posts}\n\nfunc f() {\n}\n": "var Resources = []func() error{Posts,\n\tTags,\n}\n\nfunc f() {\n}\n",
		"package admin\n\nvar Resources = []func() error{\n\tPosts, // posts\n}\n":  "var Resources = []func() error{\n\tPosts, // posts\n\tTags,\n}\n",
		"package admin\n\nvar Resources = []func() error{\n\tTags,\n}\n":            "var Resources = []func() error{\n\tTags,\n}\n",
	} {
		got, err := addResource([]byte(src), "Tags")
		if err != nil || !strings.HasSuffix(string(got), want) {
			t.Errorf("addResource(%q) = %q, %v", src, got, err)
		}
	}

	// An app with roles already: no rbac setup, no test.
	dir = fresh(true)
	write(dir, "roles.go", "package main\n\n// rbac.New(app, perms) is called elsewhere.\n")
	if res, err := MakeAdmin(dir); err != nil || res.RBAC || slices.Contains(res.Created, "admin_test.go") {
		t.Errorf("with rbac: %+v, %v", res, err)
	}
	if s := read(t, filepath.Join(dir, "admin.go")); strings.Contains(s, "rbac") && !strings.Contains(s, "give it with a role of the app's") {
		t.Errorf("admin.go:\n%s", s)
	}
	// A main.go make:auth didn't wire isn't changed.
	dir = fresh(true)
	main = strings.Replace(read(t, filepath.Join(dir, "main.go")), authCall, "\tif _, err := setupAuth(app, srv.Router(), sessions); err != nil { // mine\n\t\treturn nil, err\n\t}\n", 1)
	write(dir, "main.go", main)
	if res, err := MakeAdmin(dir); err != nil || res.Wired || read(t, filepath.Join(dir, "main.go")) != main {
		t.Errorf("unwired: %+v, %v", res, err)
	}
	// A User model of an older make:auth: no disabling.
	dir = fresh(true)
	user := read(t, filepath.Join(dir, "app/models/user.go"))
	user = strings.Replace(user, "\tDisabledAt      *time.Time `db:\"disabled_at\" json:\"disabled_at\"` // set: can't log in\n", "", 1)
	write(dir, "app/models/user.go", user)
	if res, err := MakeAdmin(dir); err != nil || res.Disabled || !res.Users {
		t.Errorf("an older User: %+v, %v", res, err)
	}
	if s := read(t, filepath.Join(dir, "app/admin/users.go")); strings.Contains(s, "DisabledAt") {
		t.Errorf("users.go of an older User:\n%s", s)
	}
	// A failed write leaves nothing behind.
	dir = fresh(true)
	layoutBefore := read(t, filepath.Join(dir, "views/layout.templ"))
	write(dir, "app/admin", "not a directory")
	if res, err := MakeAdmin(dir); err == nil || len(res.Created) != 0 {
		t.Errorf("failed write: %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "admin.go")); !os.IsNotExist(err) {
		t.Errorf("admin.go left behind: %v", err)
	}
	if strings.Contains(read(t, filepath.Join(dir, ".env")), "ADMIN_") {
		t.Error("the settings were added")
	}
	if read(t, filepath.Join(dir, "views/layout.templ")) != layoutBefore {
		t.Error("the layout was changed")
	}
}

func TestAdminReplace(t *testing.T) {
	dir := t.TempDir()
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for gomod, want := range map[string]string{
		"module x\n": "",
		"module x\nreplace anetos.dev/anetos => " + repo + "\n":                                          filepath.ToSlash(repo) + "/admin",
		"module x\nreplace anetos.dev/anetos => ../nowhere\n":                                            "",
		"module x\nreplace anetos.dev/anetos => " + repo + "\nreplace anetos.dev/anetos/admin => ../a\n": "replaced",
		"module x\nreplace anetos.dev/anetos => example.com/fork v1.0.0\n":                               "",
	} {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
			t.Fatal(err)
		}
		got, replaced, err := AdminReplace(dir)
		if replaced {
			got = "replaced"
		}
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", gomod, got, err, want)
		}
	}
}

// TestProjectCallsForApp: a project made before v0.5 calls rbac.ForApp,
// which make:admin recognizes like rbac.New.
func TestProjectCallsForApp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc setup() { rbac.ForApp(app, nil) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if none, err := noRBAC(root); err != nil || none {
		t.Errorf("noRBAC = %v, %v", none, err)
	}
	if found, err := projectCalls(root, "audit.New(", "audit.ForApp("); err != nil || found {
		t.Errorf("audit: %v, %v", found, err)
	}
}

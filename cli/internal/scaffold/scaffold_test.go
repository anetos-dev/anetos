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

	"golang.org/x/mod/modfile"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreate(t *testing.T) {
	for _, db := range Databases {
		t.Run(db, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "my-blog")
			files, err := Create(Project{Dir: dir, Module: "example.com/my-blog", DB: db, Replace: "../../.."})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"go.mod", "main.go", "main_test.go", "plugins.go", ".env", ".env.example", ".gitignore",
				"routes/web.go", "app/handlers/home.go", "views/layout.templ", "public/static/app.css", "database/migrations/migrations.go", "database/factories/factories.go"} {
				if !slices.Contains(files, want) {
					t.Errorf("missing %s in %v", want, files)
				}
			}
			env := read(t, filepath.Join(dir, ".env"))
			if !regexp.MustCompile(`APP_KEY=base64:[A-Za-z0-9+/]{43}=`).MatchString(env) || !strings.Contains(env, "DB_CONNECTION="+db) {
				t.Errorf(".env:\n%s", env)
			}
			if db != "sqlite" && !strings.Contains(env, "DB_DATABASE=my_blog") {
				t.Errorf(".env database name:\n%s", env)
			}
			if info, _ := os.Stat(filepath.Join(dir, ".env")); info.Mode().Perm() != 0o600 {
				t.Errorf(".env mode %v", info.Mode())
			}
			if db == "sqlite" {
				if slices.Contains(files, ".env.testing") {
					t.Error("SQLite projects need no .env.testing (tests use an in-memory database)")
				}
			} else if env := read(t, filepath.Join(dir, ".env.testing")); !strings.Contains(env, "DB_DATABASE=my_blog_test\n") || !strings.Contains(env, "DB_CONNECTION="+db) {
				t.Errorf(".env.testing:\n%s", env)
			}
			if ex := read(t, filepath.Join(dir, ".env.example")); strings.Contains(ex, "base64:") {
				t.Error(".env.example has a key")
			}
			mod := read(t, filepath.Join(dir, "go.mod"))
			if !strings.Contains(mod, "module example.com/my-blog") || !strings.Contains(mod, "drivers/"+db+" =>") {
				t.Errorf("go.mod:\n%s", mod)
			}
			main := read(t, filepath.Join(dir, "main.go"))
			if !strings.Contains(main, db+".Driver()") || !strings.Contains(main, `"example.com/my-blog/routes"`) {
				t.Errorf("main.go:\n%s", main)
			}
			if !strings.Contains(read(t, filepath.Join(dir, "views/layout.templ")), "My Blog") {
				t.Error("title not in layout")
			}
		})
	}
}

func TestCreateErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Project{
		{Dir: dir, DB: "sqlite"},                                           // not empty
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "oracle"},               // bad db
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Module: "/x"}, // bad module
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Module: "-x"},
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Module: "Blog."},
		{Dir: filepath.Join(t.TempDir(), `my "blog"`), DB: "sqlite"},
		{Dir: filepath.Join(t.TempDir(), "x{y}"), DB: "sqlite"},
		{Dir: filepath.Join(t.TempDir(), "fmt"), DB: "sqlite"},
		{Dir: filepath.Join(t.TempDir(), "embed"), DB: "sqlite"},
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Replace: t.TempDir()},
	} {
		if _, err := Create(p); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
}

func TestMake(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "database", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 30, 5, 0, time.UTC)
	cases := []struct {
		fn   func() (string, error)
		path string
		want []string
	}{
		{func() (string, error) { return MakeHandler(root, "blog-posts") }, "app/handlers/blog_posts.go", []string{"type BlogPosts struct{}", `pages.Get("/blog-posts", h.Index).Name("blog-posts.index")`}},
		{func() (string, error) { return MakeModel(root, "Category") }, "app/models/category.go", []string{"type Category struct", "db.Model", "categories table"}},
		{func() (string, error) { return MakeAgent(root, "order-support") }, "app/agents/order_support.go", []string{"var OrderSupport = ai.Agent{", `Name:         "order-support"`, "orderSupportLookup", "type orderSupportLookupInput struct"}},
		{func() (string, error) { return MakeMiddleware(root, "admin_only") }, "app/middleware/admin_only.go", []string{"func AdminOnly(next http.Handler) http.Handler"}},
		{func() (string, error) { return MakeMigration(root, "create_posts_table", now) }, "database/migrations/2026_10_01_123005_create_posts_table.go", []string{`All.AddFunc("2026_10_01_123005_create_posts_table"`, `s.Create("posts"`, `s.DropIfExists("posts")`}},
		{func() (string, error) { return MakeMigration(root, "AddViewsToPostsTable", now) }, "database/migrations/2026_10_01_123006_add_views_to_posts_table.go", []string{`s.Alter("posts"`}},
		{func() (string, error) { return MakeMigration(root, "backfill_slugs", now) }, "database/migrations/2026_10_01_123007_backfill_slugs.go", []string{"return nil"}},
		{func() (string, error) { return MakeMigration(root, "add_logged_in_at_to_users_table", now) }, "database/migrations/2026_10_01_123008_add_logged_in_at_to_users_table.go", []string{`s.Alter("users"`}},
	}
	for _, c := range cases {
		path, err := c.fn()
		if err != nil || path != c.path {
			t.Errorf("%s: %q %v", c.path, path, err)
			continue
		}
		src := read(t, filepath.Join(root, path))
		for _, w := range c.want {
			if !strings.Contains(src, w) {
				t.Errorf("%s: missing %q in:\n%s", path, w, src)
			}
		}
	}
	if _, err := MakeModel(root, "Category"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("overwrite: %v", err)
	}
	for _, bad := range []string{"", "9lives", "a b!"} {
		if _, err := MakeHandler(root, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := MakeMigration(t.TempDir(), "x", now); err == nil {
		t.Error("migration outside a project")
	}
	if ModelTable("person") != "people" {
		t.Error(ModelTable("person"))
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := FindRoot(filepath.Join(root, "app", "models")); err != nil || got != root {
		t.Errorf("FindRoot = %q %v", got, err)
	}
}

// The templ version of new projects is the one the examples use.
func TestTemplVersion(t *testing.T) {
	mod := read(t, "../../../examples/forms/go.mod")
	if !strings.Contains(mod, "github.com/a-h/templ "+TemplVersion) {
		t.Errorf("examples/forms uses another templ than %s", TemplVersion)
	}
}

func TestCreateHereWithQuotedReplace(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "my checkout")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module anetos.dev/anetos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "shop")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if _, err := Create(Project{Dir: ".", DB: "sqlite", Replace: checkout}); err != nil {
		t.Fatal(err)
	}
	mod := read(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(mod, "module shop") || !strings.Contains(mod, `=> "`+filepath.ToSlash(checkout)+`/cli"`) {
		t.Errorf("go.mod:\n%s", mod)
	}
	if _, err := modfile.Parse("go.mod", []byte(mod), nil); err != nil {
		t.Errorf("go.mod doesn't parse: %v", err)
	}
	if env := read(t, filepath.Join(dir, ".env")); !strings.Contains(env, "APP_NAME=shop") {
		t.Errorf(".env:\n%s", env)
	}
}

func TestMakeAuth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", Replace: "../../.."}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	res, err := MakeAuth(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app/models/user.go", "app/handlers/auth.go", "app/mailers/auth.go", "views/auth.templ",
		"views/auth_mail.templ", "routes/auth.go", "auth.go", "auth_test.go", "database/migrations/2030_01_02_030405_create_users_table.go"} {
		if !slices.Contains(res.Created, want) {
			t.Errorf("missing %s in %v", want, res.Created)
		}
	}
	if !res.Wired || !strings.Contains(read(t, filepath.Join(dir, "main.go")), "routes.Register(srv.Router(), sessions)\n"+authCall) {
		t.Errorf("main.go not wired:\n%s", read(t, filepath.Join(dir, "main.go")))
	}
	if h := read(t, filepath.Join(dir, "app/handlers/auth.go")); !strings.Contains(h, `"example.com/shop/app/models"`) {
		t.Errorf("handlers' imports:\n%s", h)
	}
	// The SOCIAL_* settings, once per file.
	if !slices.Equal(res.Env, []string{".env", ".env.example"}) {
		t.Errorf("Env = %v", res.Env)
	}
	for _, f := range res.Env {
		if env := read(t, filepath.Join(dir, f)); strings.Count(env, "\nSOCIAL_GOOGLE_CLIENT_ID=\n") != 1 || !strings.Contains(env, "\nSOCIAL_GITHUB_CLIENT_SECRET=\n") {
			t.Errorf("%s:\n%s", f, env)
		}
	}
	envFile := filepath.Join(t.TempDir(), ".env")
	for content, want := range map[string]string{
		"":                                   strings.TrimPrefix(socialSettings, "\n"),
		"A=1":                                "A=1\n" + socialSettings,
		"A=1\n":                              "A=1\n" + socialSettings,
		"A=1\n\n":                            "A=1\n\n" + strings.TrimPrefix(socialSettings, "\n"),
		"# SOCIAL_GOOGLE_CLIENT_ID=x\n":      "",
		"export SOCIAL_GOOGLE_CLIENT_ID = x": "",
	} {
		if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		added, err := addSettings(envFile, socialSettings)
		if err != nil || added != (want != "") {
			t.Errorf("addSettings to %q: %v, %v", content, added, err)
		}
		if got := read(t, envFile); want != "" && got != want {
			t.Errorf("addSettings to %q:\n%s", content, got)
		}
	}
	if added, err := addSettings(filepath.Join(t.TempDir(), "missing"), socialSettings); added || err != nil {
		t.Errorf("a missing file: %v, %v", added, err)
	}
	// Nothing is overwritten.
	if _, err := MakeAuth(dir, now); err == nil || !strings.Contains(err.Error(), "create_users_table.go exists") {
		t.Errorf("twice: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "database/migrations/2030_01_02_030405_create_users_table.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeAuth(dir, now); err == nil || !strings.Contains(err.Error(), "app/models/user.go exists") {
		t.Errorf("with a user.go: %v", err)
	}
	// A main.go without the routes.Register call isn't changed.
	other := filepath.Join(t.TempDir(), "other")
	if _, err := Create(Project{Dir: other, Module: "example.com/other", DB: "sqlite", Replace: "../../.."}); err != nil {
		t.Fatal(err)
	}
	main := strings.Replace(read(t, filepath.Join(other, "main.go")), "routes.Register(srv.Router(), sessions)", "routes.Register(srv.Router(), sessions) // mine", 1)
	main = strings.Replace(main, "routes.Register(", "myRoutes(", 1)
	if err := os.WriteFile(filepath.Join(other, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := MakeAuth(other, now); err != nil || res.Wired || read(t, filepath.Join(other, "main.go")) != main {
		t.Errorf("unwired: %+v, %v", res, err)
	}
	// Not an anetos new project.
	if _, err := MakeAuth(t.TempDir(), now); err == nil {
		t.Error("an empty directory: no error")
	}

	fresh := func() string {
		dir := filepath.Join(t.TempDir(), "p")
		if _, err := Create(Project{Dir: dir, Module: "example.com/p", DB: "sqlite", Replace: "../../.."}); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	write := func(dir, name, src string) {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A name the files declare is taken.
	p := fresh()
	write(p, "app/models/account.go", "package models\n\ntype User struct{}\n")
	if _, err := MakeAuth(p, now); err == nil || !strings.Contains(err.Error(), "app/models already declares User") {
		t.Errorf("User taken: %v", err)
	}
	write(p, "app/models/account.go", "package models\n")
	write(p, "views/pages.templ", "package views\n\ntempl Login() {\n}\n")
	if _, err := MakeAuth(p, now); err == nil || !strings.Contains(err.Error(), "views already declares Login") {
		t.Errorf("Login taken: %v", err)
	}
	// A failed write leaves nothing behind.
	p = fresh()
	write(p, "app/mailers", "not a directory")
	if res, err := MakeAuth(p, now); err == nil || len(res.Created) != 0 {
		t.Errorf("failed write: %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(p, "app/models/user.go")); !os.IsNotExist(err) {
		t.Errorf("user.go left behind: %v", err)
	}
	if strings.Contains(read(t, filepath.Join(p, ".env")), "SOCIAL_") {
		t.Error("the settings were added")
	}
	// A settings file that can't be read stops it before it writes.
	p = fresh()
	if err := os.Remove(filepath.Join(p, ".env.example")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(p, ".env.example"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res, err := MakeAuth(p, now); err == nil || len(res.Created) != 0 {
		t.Errorf("unreadable .env.example: %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(p, "auth.go")); !os.IsNotExist(err) {
		t.Errorf("auth.go written: %v", err)
	}
	// A comment isn't a call; a routes.Register moved out of setup isn't
	// wired into.
	p = fresh()
	main = strings.Replace(read(t, filepath.Join(p, "main.go")), "func setup(", "// TODO: setupAuth(app)\nfunc setup(", 1)
	write(p, "main.go", main)
	if res, err := MakeAuth(p, now); err != nil || !res.Wired || strings.Count(read(t, filepath.Join(p, "main.go")), "setupAuth(app, srv.Router(), sessions)") != 1 {
		t.Errorf("with a comment: %+v, %v", res, err)
	}
	p = fresh()
	main = strings.Replace(read(t, filepath.Join(p, "main.go")), "\troutes.Register(srv.Router(), sessions)\n", "\tif err := register(srv, sessions); err != nil {\n\t\treturn nil, err\n\t}\n", 1)
	main += "\nfunc register(srv *web.Server, sessions *session.Manager) error {\n\troutes.Register(srv.Router(), sessions)\n\treturn nil\n}\n"
	write(p, "main.go", main)
	if res, err := MakeAuth(p, now); err != nil || res.Wired || read(t, filepath.Join(p, "main.go")) != main {
		t.Errorf("routes in a helper: %+v, %v", res, err)
	}
}

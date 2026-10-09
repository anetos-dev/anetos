// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"io/fs"
	"maps"
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
				"Dockerfile", ".dockerignore", "deploy/my-blog.service", "deploy/production.env.example",
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
			// Deployment: the Dockerfile builds with the go line's Go, and
			// SQLite's database lives on the data volume.
			docker := read(t, filepath.Join(dir, "Dockerfile"))
			if !strings.Contains(mod, "\ngo "+goMinor+".") || !strings.Contains(docker, "FROM golang:"+goMinor+" AS build") ||
				!strings.Contains(docker, `ENTRYPOINT ["/app/my-blog"]`) || strings.Contains(docker, "DB_DATABASE") != (db == "sqlite") {
				t.Errorf("Dockerfile:\n%s", docker)
			}
			unit := read(t, filepath.Join(dir, "deploy/my-blog.service"))
			if !strings.Contains(unit, "ExecStart=/opt/my-blog/my-blog run") || strings.Contains(unit, "DB_DATABASE") != (db == "sqlite") {
				t.Errorf("unit:\n%s", unit)
			}
			prod := read(t, filepath.Join(dir, "deploy/production.env.example"))
			if !strings.Contains(prod, "APP_ENV=production") || !strings.Contains(prod, "DB_CONNECTION="+db) || strings.Contains(prod, "base64:") {
				t.Errorf("production.env.example:\n%s", prod)
			}
		})
	}
}

// The deploy files name the binary after the module, which may differ
// from the directory, and Docker after the directory in lower case.
func TestCreateDeployNames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "MyBlog")
	if _, err := Create(Project{Dir: dir, Module: "example.com/acme/shop/v2", DB: "postgres", Replace: "../../.."}); err != nil {
		t.Fatal(err)
	}
	if unit := read(t, filepath.Join(dir, "deploy", "MyBlog.service")); !strings.Contains(unit, "sudo install -D bin/shop /opt/MyBlog/MyBlog") {
		t.Errorf("unit:\n%s", unit)
	}
	if df := read(t, filepath.Join(dir, "Dockerfile")); !strings.Contains(df, "docker build -t myblog ") || strings.Contains(df, "DB_DATABASE") {
		t.Errorf("Dockerfile:\n%s", df)
	}
	if ignore := read(t, filepath.Join(dir, ".dockerignore")); !strings.Contains(ignore, "**/*.db\n") || !strings.Contains(ignore, "*.env\n") {
		t.Errorf(".dockerignore:\n%s", ignore)
	}
	for module, want := range map[string]string{"example.com/blog": "blog", "example.com/blog/v2": "blog", "blog": "blog", "v2": "v2", "example.com/v2x": "v2x"} {
		if got := BinaryName(module); got != want {
			t.Errorf("BinaryName(%q) = %q, want %q", module, got, want)
		}
	}
}

// Each design kit writes its stylesheet and views/ui, and a CSS
// framework's kit the framework's files as released; the layout and the
// pages are the same for every kit, and only views/ui has classes
// (design D292, D298).
func TestCreateKits(t *testing.T) {
	kits := map[string][]string{
		"":          {"--primary:"},
		"anetos":    {"--primary:"},
		"none":      {"The app's styles."},
		"pico":      {"after Pico's (pico.min.css, v" + KitVersions["pico"] + ")", "pico.min.css", "pico.LICENSE.txt"},
		"bootstrap": {"after Bootstrap's (bootstrap.min.css, v" + KitVersions["bootstrap"] + ")", "bootstrap.min.css", "bootstrap.bundle.min.js", "bootstrap.LICENSE.txt", "popper.LICENSE.txt", "theme.js"},
		"bulma":     {"after Bulma's (bulma.min.css, v" + KitVersions["bulma"] + ")", "bulma.min.css", "bulma.LICENSE.txt", "nav.js"},
	}
	if len(kits) != len(Kits)+1 {
		t.Fatalf("Kits %v: test each", Kits)
	}
	var layouts []string
	for css, want := range kits {
		dir := filepath.Join(t.TempDir(), "a")
		if _, err := Create(Project{Dir: dir, DB: "sqlite", CSS: css}); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(dir, "public", "static", "app.css")); !strings.Contains(got, strings.ReplaceAll(want[0], "\n", " ")) {
			t.Errorf("--css=%s: app.css:\n%s", css, got)
		}
		for _, f := range want[1:] {
			if fi, err := os.Stat(filepath.Join(dir, "public", "static", f)); err != nil || fi.Size() == 0 {
				t.Errorf("--css=%s: public/static/%s: %v", css, f, err)
			}
		}
		for _, f := range []string{"ui.go", "shell.templ", "page.templ", "form.templ", "data.templ"} {
			if _, err := os.Stat(filepath.Join(dir, "views", "ui", f)); err != nil {
				t.Errorf("--css=%s: %v", css, err)
			}
		}
		classless(t, dir, css == "none")
		layouts = append(layouts, read(t, filepath.Join(dir, "views", "layout.templ")))
	}
	for _, l := range layouts[1:] {
		if l != layouts[0] {
			t.Error("the layout differs between kits")
		}
	}
	if HasUI(t.TempDir()) {
		t.Error("HasUI of an empty directory")
	}
}

// The CSS frameworks' files a kit carries are the releases KitVersions
// names (scripts/update-kits.sh fetches them), each with its license.
func TestKitVersions(t *testing.T) {
	for _, kit := range Kits {
		dir := "templates/kits/" + kit + "/public/static"
		entries, err := templates.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var vendored []string
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".tmpl") {
				vendored = append(vendored, e.Name())
			}
		}
		version, ok := KitVersions[kit]
		if ok != (len(vendored) > 0) {
			t.Errorf("%s: KitVersions %q, files %v", kit, version, vendored)
			continue
		}
		for _, f := range vendored {
			b, err := fs.ReadFile(templates, dir+"/"+f)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(f, ".LICENSE.txt") {
				if !bytes.Contains(b, []byte("MIT License")) {
					t.Errorf("%s: %s isn't the MIT license", kit, f)
				}
				continue
			}
			if !bytes.Contains(b[:min(len(b), 200)], []byte(" v"+version+" ")) {
				t.Errorf("%s: %s isn't v%s: %q", kit, f, version, b[:min(len(b), 200)])
			}
		}
		if ok && !slices.Contains(vendored, kit+".LICENSE.txt") {
			t.Errorf("%s: no %s.LICENSE.txt", kit, kit)
		}
	}
}

// classless fails the test when a templ file outside views/ui (or, with
// all, any templ file) has a class attribute: the kit's components carry
// the markup that a kit styles.
func classless(t *testing.T, root string, all bool) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(root, "views"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".templ") {
			return err
		}
		if !all && filepath.Base(filepath.Dir(path)) == "ui" {
			return nil
		}
		if strings.HasSuffix(path, "_mail.templ") || strings.HasSuffix(path, "mail.templ") {
			return nil // emails carry their own inline styles
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if before, _, found := bytes.Cut(b, []byte("class=")); found {
			line := bytes.Count(before, []byte("\n")) + 1
			t.Errorf("%s:%d has a class attribute; call a views/ui component", path, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A project made before v0.5 has no views/ui: WriteUI writes the kit its
// stylesheet looks like, without touching the stylesheet.
func TestWriteUI(t *testing.T) {
	for kit, css := range map[string]string{"anetos": ".card { padding: 1rem }", "none": "body { margin: 0 }"} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "public", "static"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "public", "static", "app.css"), []byte(css), 0o644); err != nil {
			t.Fatal(err)
		}
		if HasUI(dir) || GuessKit(dir) != kit {
			t.Fatalf("%s: HasUI %v, GuessKit %q", kit, HasUI(dir), GuessKit(dir))
		}
		written, err := WriteUI(dir, "example.com/blog", kit)
		if err != nil || len(written) < 5 || !HasUI(dir) {
			t.Fatalf("%s: %v %v", kit, written, err)
		}
		if got := read(t, filepath.Join(dir, "public", "static", "app.css")); got != css {
			t.Errorf("%s: the stylesheet changed: %s", kit, got)
		}
		if ui := read(t, filepath.Join(dir, "views", "ui", "shell.templ")); !strings.Contains(ui, `"example.com/blog/public"`) {
			t.Errorf("%s: shell.templ:\n%s", kit, ui)
		}
		if _, err := WriteUI(dir, "example.com/blog", kit); err == nil {
			t.Errorf("%s: WriteUI wrote over views/ui", kit)
		}
	}
	if _, err := WriteUI(t.TempDir(), "m", "nope"); err == nil {
		t.Error("WriteUI of a kit that doesn't exist")
	}
}

// No two layers of a stack write the same file: a layer never overrides
// another's (design D259).
func TestStackLayersDisjoint(t *testing.T) {
	if !slices.Equal(slices.Sorted(maps.Keys(stackLayers)), slices.Sorted(slices.Values(Stacks))) {
		t.Fatalf("stackLayers %v, Stacks %v", stackLayers, Stacks)
	}
	for stack, layers := range stackLayers {
		from := map[string]string{} // file → layer
		for _, layer := range layers {
			if _, err := templates.ReadDir("templates/new/" + layer); err != nil {
				t.Fatalf("%s: layer %s: %v", stack, layer, err)
			}
		}
		err := walkStack(stack, func(src, rel string) error {
			layer := strings.Split(src, "/")[2]
			if other, ok := from[rel]; ok {
				t.Errorf("%s: %s is written by the layers %s and %s", stack, rel, other, layer)
			}
			from[rel] = layer
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// --stack=api: JSON only, no views, templ, static files or sessions.
func TestCreateAPI(t *testing.T) {
	for _, db := range Databases {
		t.Run(db, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "shop")
			files, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: db, Replace: "../../..", Stack: "api"})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"go.mod", "main.go", "main_test.go", "plugins.go", ".env", ".env.example", "Dockerfile",
				"deploy/shop.service", "deploy/production.env.example", "routes/api.go", "app/handlers/welcome.go",
				"locales/en/app.yaml", "database/migrations/migrations.go", "README.md"} {
				if !slices.Contains(files, want) {
					t.Errorf("missing %s in %v", want, files)
				}
			}
			for _, f := range files {
				if strings.HasPrefix(f, "views/") || strings.HasPrefix(f, "public/") || f == "routes/web.go" || f == "app/handlers/home.go" {
					t.Errorf("an API project has %s", f)
				}
			}
			if !IsAPI(dir) {
				t.Error("IsAPI = false")
			}
			main := read(t, filepath.Join(dir, "main.go"))
			for _, no := range []string{"session", "templ"} {
				if strings.Contains(main, no) {
					t.Errorf("main.go mentions %s:\n%s", no, main)
				}
			}
			if !strings.Contains(main, "routes.Register(srv.Router())") || !strings.Contains(main, "http://localhost:8080/api/v1") {
				t.Errorf("main.go:\n%s", main)
			}
			routes := read(t, filepath.Join(dir, "routes", "api.go"))
			if !strings.Contains(routes, "r.UseGlobal(web.JSONErrors)") || !strings.Contains(routes, `r.Group("/api/v1").As("api.")`) {
				t.Errorf("routes/api.go:\n%s", routes)
			}
			for _, f := range []string{".env", ".env.example", "deploy/production.env.example"} {
				env := read(t, filepath.Join(dir, f))
				if !strings.Contains(env, "\nHTTP_CORS_ORIGINS=\n") || strings.Contains(env, "SESSION_DRIVER") || strings.Contains(env, "LOCALE_URL") {
					t.Errorf("%s:\n%s", f, env)
				}
			}
			if readme := read(t, filepath.Join(dir, "README.md")); strings.Contains(readme, "views") || !strings.Contains(readme, "sign in with API tokens") || !strings.Contains(readme, "/api/v1") {
				t.Errorf("README.md:\n%s", readme)
			}
		})
	}
	// The web stack keeps what it had.
	dir := filepath.Join(t.TempDir(), "blog")
	if _, err := Create(Project{Dir: dir, DB: "sqlite", Stack: "web"}); err != nil {
		t.Fatal(err)
	}
	if IsAPI(dir) {
		t.Error("IsAPI(web project) = true")
	}
	if env := read(t, filepath.Join(dir, ".env")); strings.Contains(env, "HTTP_CORS_ORIGINS") || !strings.Contains(env, "SESSION_DRIVER=cookie") {
		t.Errorf("web .env:\n%s", env)
	}
	// A web project that adds an API stays a web project.
	if err := os.WriteFile(filepath.Join(dir, "routes", "api.go"), []byte("package routes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsAPI(dir) {
		t.Error("IsAPI(web project with routes/api.go) = true")
	}
}

// In an API project, make:handler and make:middleware write for the
// API, and the generators of pages refuse.
func TestMakeInAPIProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: root, DB: "sqlite", Stack: "api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeHandler(root, "orders"); err != nil {
		t.Fatal(err)
	}
	h := read(t, filepath.Join(root, "app", "handlers", "orders.go"))
	if !strings.Contains(h, "routes/api.go") || !strings.Contains(h, `api.Get("/orders", web.H(h.Index)).Name("orders.index")`) ||
		!strings.Contains(h, "GET /api/v1/orders") || !strings.Contains(h, "func (h Orders) Index(c *web.Ctx, _ struct{}) (OrdersResponse, error) {") {
		t.Errorf("handler:\n%s", h)
	}
	if _, err := MakeMiddleware(root, "audit"); err != nil {
		t.Fatal(err)
	}
	if m := read(t, filepath.Join(root, "app", "middleware", "audit.go")); !strings.Contains(m, "routes/api.go") || !strings.Contains(m, "\n\n// Audit is HTTP middleware.") {
		t.Errorf("middleware:\n%s", m)
	}
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	// The list endpoint's query parameters can't be fields.
	for _, f := range []string{"page:int", "per_page:int", "sort:string"} {
		if _, err := MakeCrud(root, "Item", []string{"name:string", f}, now); err == nil || !strings.Contains(err.Error(), "list endpoint") {
			t.Errorf("make:crud with %s: %v", f, err)
		}
	}
	// make:crud writes JSON endpoints, routed in the api group.
	crud, err := MakeCrud(root, "Product", []string{"name:string:unique", "price:float", "launch_on:date:optional", "notes:text:optional"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !crud.API || !crud.Routed || crud.Linked || crud.Path != "/api/v1/products" ||
		!slices.Equal(crud.Created, []string{"app/models/product.go", "database/migrations/2026_10_08_090000_create_products_table.go",
			"app/handlers/products.go", "routes/products.go", "products_test.go"}) {
		t.Errorf("make:crud: %+v", crud)
	}
	if r := read(t, filepath.Join(root, "routes", "api.go")); !strings.Contains(r, "\tProducts(api) // anetos make:crud\n}") {
		t.Errorf("routes/api.go:\n%s", r)
	}
	hc := read(t, filepath.Join(root, "app", "handlers", "products.go"))
	for _, want := range []string{"type ProductResponse struct", "LaunchOn  *anetos.Date `json:\"launch_on\"`", "Name     *string      `query:\"name\"`",
		`validate:"in:id,-id,created_at,-created_at,updated_at,-updated_at,name,-name,price,-price,launch_on,-launch_on"`,
		"(db.Page[ProductResponse], error)", "return db.MapPage(page, productResponse), nil"} {
		if !strings.Contains(hc, want) {
			t.Errorf("handlers: no %q in\n%s", want, hc)
		}
	}
	for _, no := range []string{`query:"price"`, `query:"notes"`} {
		if strings.Contains(hc, no) {
			t.Errorf("handlers: %q (floats and texts aren't filters)", no)
		}
	}
	if _, err := MakeAdmin(root); err == nil || !strings.Contains(err.Error(), "the admin is for web projects") {
		t.Errorf("make:admin: %v", err)
	}
	// make:auth writes the API's accounts, unless a name it declares is
	// taken.
	taken := filepath.Join(root, "app", "handlers", "mine.go")
	if err := os.WriteFile(taken, []byte("package handlers\n\ntype SignInResponse struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeAuth(root, now); err == nil || !strings.Contains(err.Error(), "app/handlers already declares SignInResponse") {
		t.Errorf("make:auth with a name taken: %v", err)
	}
	if err := os.Remove(taken); err != nil {
		t.Fatal(err)
	}
	res, err := MakeAuth(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.API || !res.Wired || res.Menu || !slices.Equal(res.Env, []string{".env", ".env.example", "deploy/production.env.example"}) {
		t.Errorf("make:auth: %+v", res)
	}
	for _, f := range []string{"app/handlers/auth.go", "app/mailers/auth.go", "app/mailers/auth.html", "routes/auth.go", "auth.go", "auth_test.go",
		"locales/en/auth.yaml", "database/factories/users.go", "database/migrations/2026_10_08_090001_create_users_table.go"} {
		if !slices.Contains(res.Created, f) {
			t.Errorf("make:auth didn't write %s: %v", f, res.Created)
		}
	}
	for _, f := range res.Created {
		if strings.HasPrefix(f, "views/") || strings.HasSuffix(f, ".templ") {
			t.Errorf("make:auth wrote %s in an API project", f)
		}
	}
	if m := read(t, filepath.Join(root, "main.go")); !strings.Contains(m, "\troutes.Register(srv.Router())\n\t// Accounts (anetos make:auth)") ||
		!strings.Contains(m, "setupAuth(app, srv.Router()); err != nil") {
		t.Errorf("main.go:\n%s", m)
	}
	if env := read(t, filepath.Join(root, ".env")); !strings.Contains(env, "\nAUTH_CLIENT_URL=http://localhost:5173\n") || strings.Contains(env, "SOCIAL_") {
		t.Errorf(".env:\n%s", env)
	}
	if env := read(t, filepath.Join(root, "deploy", "production.env.example")); !strings.Contains(env, "\nAUTH_CLIENT_URL=https://app.example.com\n") {
		t.Errorf("production.env.example:\n%s", env)
	}
	if _, err := MakeAuth(root, now); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("make:auth twice: %v", err)
	}
	// Templ views (for emails, say) don't make it a web project.
	if err := os.MkdirAll(filepath.Join(root, "views"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsAPI(root) {
		t.Error("IsAPI(API project with views/) = false")
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
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", CSS: "nope"},
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: "vue"}, // not yet
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: "api", CSS: "none"},
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: "api", CSS: "anetos"},
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
	classless(t, dir, false)
	if res.Kit != "" {
		t.Errorf("wrote views/ui (%s) in a project that has it", res.Kit)
	}
	if !res.Wired || !strings.Contains(read(t, filepath.Join(dir, "main.go")), "routes.Register(srv.Router(), sessions)\n"+authCall) {
		t.Errorf("main.go not wired:\n%s", read(t, filepath.Join(dir, "main.go")))
	}
	if h := read(t, filepath.Join(dir, "app/handlers/auth.go")); !strings.Contains(h, `"example.com/shop/app/models"`) {
		t.Errorf("handlers' imports:\n%s", h)
	}
	if f := read(t, filepath.Join(dir, "database/factories/users.go")); !strings.Contains(f, "var Users = factory.New(func(n int) models.User {") {
		t.Errorf("the users' factory:\n%s", f)
	}
	// The SOCIAL_* settings, once per file.
	if !slices.Equal(res.Env, []string{".env", ".env.example", "deploy/production.env.example"}) {
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

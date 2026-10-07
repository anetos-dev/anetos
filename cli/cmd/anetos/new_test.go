// SPDX-License-Identifier: Apache-2.0

package main

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNewProject creates a project against this checkout, adds code with
// the make commands, and checks that it builds, passes its test, and runs
// its commands. It downloads templ if the module cache doesn't have it.
func TestNewProject(t *testing.T) {
	if testing.Short() {
		t.Skip("creates and builds a project")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "blog")
	code, out, errOut := runCmd(t, "new", dir, "--module", "example.com/blog", "--replace", repo)
	if code != 0 {
		t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "go tool templ generate") || !strings.Contains(out, "go run . migrate") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "views", "home_templ.go")); err != nil {
		t.Errorf("views not generated: %v", err)
	}

	t.Chdir(filepath.Join(dir, "app")) // make commands find the root
	for _, args := range [][]string{
		{"make:model", "Post", "--migration"},
		{"make:handler", "Posts"},
		{"make:middleware", "Admin"},
		{"make:agent", "Support"},
	} {
		if code, out, errOut := runCmd(t, args...); code != 0 {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errOut)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "app", "models", "models_gen.go")); err != nil || !strings.Contains(string(b), "PostCols") {
		t.Errorf("models_gen.go: %v %s", err, b)
	}
	if code, _, _ := runCmd(t, "make:handler", "Posts"); code != 1 {
		t.Error("make:handler overwrote a file")
	}
	// Accounts: the generated code builds, and its tests (auth_test.go)
	// pass with the rest below.
	if code, out, errOut := runCmd(t, "make:auth"); code != 0 || !strings.Contains(out, "updated main.go: setup calls setupAuth") {
		t.Fatalf("make:auth: %d\n%s\n%s", code, out, errOut)
	}
	if code, _, errOut := runCmd(t, "make:auth"); code != 1 || !strings.Contains(errOut, "exists") {
		t.Errorf("make:auth twice: %d %s", code, errOut)
	}
	if b := read(t, filepath.Join(dir, "views", "layout.templ")); !strings.Contains(b, "\t\t\t\t\t</nav>\n\t\t\t\t\t@AccountMenu()\n") {
		t.Errorf("make:auth didn't add AccountMenu to the layout:\n%s", b)
	}
	// Pages for three models: their tests (articles_test.go…) pass with
	// the rest below; each is routed and in the nav.
	for _, args := range [][]string{
		{"make:crud", "Article", "title:string", "body:text", "published:bool", "views:int", "rating:float", "due_on:date:optional", "contact:email:unique", "notes:text:optional"},
		// Names that were package names, keywords, initialisms or YAML's.
		{"make:crud", "APIKey", "name:string:unique", "true:bool", "null:string:optional"},
		// No required string: rows are #ID.
		{"make:crud", "View", "type:int", "range:float", "default:text:optional", "image_url:string:optional", "taken_on:date"},
	} {
		code, out, errOut := runCmd(t, args...)
		if code != 0 || !strings.Contains(out, "updated routes/web.go: Register calls ") || !strings.Contains(out, "updated views/layout.templ: the nav links to ") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errOut)
		}
	}
	if b := read(t, filepath.Join(dir, "routes", "web.go")); !strings.Contains(b, "\tArticles(pages) // anetos make:crud\n\tAPIKeys(pages) // anetos make:crud\n\tViews(pages) // anetos make:crud\n}") {
		t.Errorf("routes/web.go:\n%s", b)
	}
	// The nav marks the list's link on all its pages.
	navTest := "package main\n\nimport (\n\t\"testing\"\n\n\t\"anetos.dev/anetos/anetostest\"\n)\n\n" +
		"func TestNavCurrent(t *testing.T) {\n\tapp := anetostest.New(t, setup)\n" +
		"\tapp.Get(\"/articles/new\").AssertOK().AssertSee(`<a href=\"/articles\" aria-current=\"page\">`)\n" +
		"\tapp.Get(\"/api-keys\").AssertOK().AssertSee(`<a href=\"/api-keys\" aria-current=\"page\">`).AssertDontSee(`<a href=\"/articles\" aria-current`)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "nav_test.go"), []byte(navTest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"make:crud", "Post", "title:string"}, "_create_posts_table.go exists"},
		{[]string{"make:crud", "Article", "title:string"}, "_create_articles_table.go exists"},
		{[]string{"make:crud", "Note", "title"}, "name:type"},
		{[]string{"make:crud", "Note", "title:uuid"}, "unknown type"},
		{[]string{"make:crud", "Note", "body:text:unique"}, "can't be unique"},
		{[]string{"make:crud", "Note", "id:int"}, "has it already"},
	} {
		if code, _, errOut := runCmd(t, c.args...); code != 1 || !strings.Contains(errOut, c.err) {
			t.Errorf("%v: %d %s", c.args, code, errOut)
		}
	}
	// The admin, with a resource for posts: it builds, and its test
	// (admin_test.go) passes with the rest below.
	if code, out, errOut := runCmd(t, "make:admin"); code != 0 || !strings.Contains(out, "updated main.go: setup calls setupAdmin") {
		t.Fatalf("make:admin: %d\n%s\n%s", code, out, errOut)
	}
	postGo := filepath.Join(dir, "app", "models", "post.go")
	post := strings.Replace(read(t, postGo), "\tdb.Model // id, created_at, updated_at\n",
		"\tdb.Model // id, created_at, updated_at\n\tTitle string `db:\"title\"`\n\tPublishedAt *time.Time `db:\"published_at\"`\n\tViews int `db:\"views\"`\n", 1)
	post = strings.Replace(post, `import "anetos.dev/anetos/db"`, "import (\n\t\"time\"\n\n\t\"anetos.dev/anetos/db\"\n)", 1)
	if err := os.WriteFile(postGo, []byte(post), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCmd(t, "make:admin:resource", "Post"); code != 0 || !strings.Contains(out, "created app/admin/posts.go") {
		t.Fatalf("make:admin:resource: %d\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut := runCmd(t, "gen"); code != 0 {
		t.Fatalf("gen: %d\n%s\n%s", code, out, errOut)
	}

	goRun := func(args ...string) string {
		t.Helper()
		c := exec.Command("go", args...)
		c.Dir = dir // go test: anetostest uses an in-memory database
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, b)
		}
		return string(b)
	}
	// anetos add, with the plugin from this checkout: --replace replaced
	// every module of the checkout.
	const postmark = "anetos.dev/anetos/plugins/postmark"
	if code, out, errOut := runCmd(t, "add", postmark); code != 0 ||
		!strings.Contains(out, "POSTMARK_WEBHOOK_USER, POSTMARK_WEBHOOK_PASSWORD") {
		t.Fatalf("add: %d\n%s\n%s", code, out, errOut)
	}
	if mod := read(t, filepath.Join(dir, "go.mod")); !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(postmark) + ` v\S+$`).MatchString(mod) {
		t.Errorf("go.mod doesn't require the plugin directly:\n%s", mod)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".env.example")); err != nil ||
		!strings.Contains(string(b), "\n# postmark plugin\nPOSTMARK_WEBHOOK_USER=\nPOSTMARK_WEBHOOK_PASSWORD=\n") {
		t.Errorf(".env.example: %v\n%s", err, b)
	}
	if code, _, errOut := runCmd(t, "add", postmark); code != 1 || !strings.Contains(errOut, "already") {
		t.Errorf("add twice: %d %s", code, errOut)
	}
	if code, _, errOut := runCmd(t, "add", "example.com/nope@v1.0.0"); code != 1 || !strings.Contains(errOut, "as they were") {
		t.Errorf("add of a missing module: %d %s", code, errOut)
	}
	// A plugin the app refuses (here, for its version requirement) isn't
	// added.
	tooNew := filepath.Join(t.TempDir(), "toonew")
	for name, src := range map[string]string{
		"go.mod": "module example.com/toonew\n\ngo 1.26\n\nrequire anetos.dev/anetos v0.0.0-00010101000000-000000000000\n",
		"toonew.go": "package toonew\n\nimport \"anetos.dev/anetos/ext\"\n\n" +
			"type p struct{}\n\nfunc (p) Name() string     { return \"toonew\" }\nfunc (p) Requires() string { return \">= v9.0.0\" }\n\n" +
			"// Plugin needs Anetos 9.\nfunc Plugin() ext.Plugin { return p{} }\n",
	} {
		if err := os.MkdirAll(tooNew, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tooNew, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goMod := read(t, filepath.Join(dir, "go.mod"))
	goRun("mod", "edit", "-replace", "example.com/toonew="+tooNew)
	goMod2 := read(t, filepath.Join(dir, "go.mod"))
	if code, _, errOut := runCmd(t, "add", "example.com/toonew@v0.0.0-00010101000000-000000000000"); code != 1 ||
		!strings.Contains(errOut, "requires Anetos >= v9.0.0") || !strings.Contains(errOut, "as they were") {
		t.Errorf("add of a plugin for Anetos 9: %d %s", code, errOut)
	}
	if got := read(t, filepath.Join(dir, "go.mod")); got != goMod2 {
		t.Errorf("go.mod changed:\n%s", got)
	}
	if strings.Contains(read(t, filepath.Join(dir, "plugins.go")), "toonew") {
		t.Error("plugins.go lists the refused plugin")
	}
	goRun("mod", "edit", "-dropreplace", "example.com/toonew")
	if got := read(t, filepath.Join(dir, "go.mod")); got != goMod {
		t.Errorf("go.mod after dropping the replace:\n%s", got)
	}

	goRun("vet", "./...")
	goRun("test", "./...")
	// The production build: one static binary, at bin/blog by default.
	if code, out, errOut := runCmd(t, "build", "--version=v9.9.9"); code != 0 || !strings.Contains(out, "built bin/blog (") {
		t.Fatalf("build: %d\n%s\n%s", code, out, errOut)
	}
	bin := filepath.Join(dir, "bin", "blog")
	// For another system: the generators still run on this one.
	if code, out, errOut := runCmd(t, "build", "--target=linux/arm64", "-o", filepath.Join(dir, "bin", "blog-arm64")); code != 0 || !strings.Contains(out, "built bin/blog-arm64 (linux/arm64, ") {
		t.Fatalf("build --target=linux/arm64: %d\n%s\n%s", code, out, errOut)
	}
	if f, err := elf.Open(filepath.Join(dir, "bin", "blog-arm64")); err != nil || f.Machine != elf.EM_AARCH64 {
		t.Errorf("bin/blog-arm64: %v %v", err, f)
	} else {
		f.Close()
	}
	if code, out, errOut := runCmd(t, "build", "--target=windows/amd64"); code != 0 || !strings.Contains(out, "built bin/blog.exe (windows/amd64, ") {
		t.Fatalf("build --target=windows/amd64: %d\n%s\n%s", code, out, errOut)
	}
	app := func(args ...string) string {
		t.Helper()
		c := exec.Command(bin, args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "DB_DATABASE="+filepath.Join(dir, "app.db"))
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, b)
		}
		return string(b)
	}
	if out := app("migrate"); !strings.Contains(out, "create_posts_table") || !strings.Contains(out, "create_postmark_suppressions") ||
		!strings.Contains(out, "create_users_table") || !strings.Contains(out, "create_api_tokens_table") {
		t.Errorf("migrate:\n%s", out)
	}
	// doctor: the app's checks, in production; and anetos doctor, with
	// the project's own, in development.
	c := exec.Command(bin, "doctor", "--strict")
	c.Dir = dir
	c.Env = append(os.Environ(), "DB_DATABASE="+filepath.Join(dir, "app.db"), "APP_ENV=production", "APP_DEBUG=false", "APP_URL=https://blog.acme.io", "MAIL_FROM_ADDRESS=hello@acme.io")
	b, err := c.CombinedOutput()
	if out := strings.Join(strings.Fields(string(b)), " "); err == nil || !strings.Contains(out, "Checking blog (APP_ENV=production).") ||
		!strings.Contains(out, "warning mail: MAIL_DRIVER=log in production") || !strings.Contains(out, "ok migrations") ||
		!strings.Contains(out, "ok session") || !strings.Contains(out, "0 problems, 1 warning") {
		t.Errorf("doctor --strict in production: %v\n%s", err, b)
	}
	if code, out, errOut := runCmd(t, "doctor"); code != 0 || !strings.Contains(out, "Checking the project (Anetos ") ||
		!regexp.MustCompile(`ok\s+\.env`).MatchString(out) || !strings.Contains(out, "Checking blog (APP_ENV=development).") {
		t.Errorf("anetos doctor: %d\n%s\n%s", code, out, errOut)
	}
	if err := os.Chmod(filepath.Join(dir, ".env"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCmd(t, "doctor", "--strict"); code != 1 || !strings.Contains(out, "warning  .env: other users of this machine can read it") {
		t.Errorf("anetos doctor --strict with a readable .env: %d\n%s\n%s", code, out, errOut)
	}
	if out := app("routes:list"); !regexp.MustCompile(`GET\s+/\s+home`).MatchString(out) ||
		!regexp.MustCompile(`DELETE\s+/api-keys/\{id\}\s+api-keys\.destroy`).MatchString(out) ||
		!regexp.MustCompile(`GET\s+/admin/posts/\{id\}/edit\s+admin\.posts\.edit`).MatchString(out) {
		t.Errorf("routes:list:\n%s", out)
	}
	if out := app("version"); !strings.HasPrefix(out, "blog v9.9.9\nAnetos ") {
		t.Errorf("version:\n%s", out)
	}
	// version needs no settings: main prints it before setup (outside
	// the project, without its .env and APP_KEY).
	c = exec.Command(bin, "version")
	c.Dir = t.TempDir()
	c.Env = append(os.Environ(), "APP_ENV=production", "APP_KEY=")
	if b, err := c.CombinedOutput(); err != nil || !strings.HasPrefix(string(b), "blog v9.9.9\n") {
		t.Errorf("version without settings: %v\n%s", err, b)
	}
	if out := app("help"); !strings.Contains(out, "migrate:status") || !strings.Contains(out, "serve") {
		t.Errorf("help:\n%s", out)
	}
	// Every key the pages use is in the catalogs (lang:check reads the
	// source from the project's folder).
	if out := app("lang:check"); !strings.Contains(out, "lang:check: en OK") {
		t.Errorf("lang:check:\n%s", out)
	}
	if out := app("schedule:list"); !strings.Contains(out, "No scheduled tasks.") {
		t.Errorf("schedule:list:\n%s", out)
	}
	if out := app("plugins:list"); !regexp.MustCompile(`postmark\s.*\s/postmark\s+config, migrations`).MatchString(out) {
		t.Errorf("plugins:list:\n%s", out)
	}
	if out := app("postmark:suppressions"); !strings.Contains(out, "No suppressed addresses.") {
		t.Errorf("postmark:suppressions:\n%s", out)
	}

	if code, out, errOut := runCmd(t, "remove", postmark); code != 0 || !strings.Contains(out, "Removed") {
		t.Fatalf("remove: %d\n%s\n%s", code, out, errOut)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "plugins.go")); err != nil || strings.Contains(string(b), "postmark") {
		t.Errorf("plugins.go after remove: %v\n%s", err, b)
	}
	if code, _, errOut := runCmd(t, "remove", postmark); code != 1 || !strings.Contains(errOut, "isn't in plugins.go") {
		t.Errorf("remove twice: %d %s", code, errOut)
	}
}

// TestNewAPIProject creates an API project (--stack=api) against this
// checkout: it builds, passes its tests, has no views or sessions, and
// the generators of pages refuse in it. PostgreSQL and MySQL projects
// build too.
func TestNewAPIProject(t *testing.T) {
	if testing.Short() {
		t.Skip("creates and builds a project")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "new", filepath.Join(t.TempDir(), "x"), "--stack=api", "--css=none", "--skip-install"); code != 1 || !strings.Contains(errOut, "--css is for the web stack") {
		t.Errorf("--stack=api --css=none: %d %s", code, errOut)
	}
	if code, _, errOut := runCmd(t, "new", filepath.Join(t.TempDir(), "x"), "--stack=vue", "--skip-install"); code != 1 || !strings.Contains(errOut, "--stack must be one of web, api") {
		t.Errorf("--stack=vue: %d %s", code, errOut)
	}
	dir := filepath.Join(t.TempDir(), "shop")
	code, out, errOut := runCmd(t, "new", dir, "--module", "example.com/shop", "--stack=api", "--replace", repo)
	if code != 0 {
		t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "templ") || !strings.Contains(out, "http://localhost:8080/api/v1") {
		t.Errorf("output:\n%s", out)
	}
	for _, no := range []string{"views", "public", "routes/web.go"} {
		if _, err := os.Stat(filepath.Join(dir, no)); err == nil {
			t.Errorf("an API project has %s", no)
		}
	}
	if mod := read(t, filepath.Join(dir, "go.mod")); strings.Contains(mod, "templ") {
		t.Errorf("go.mod:\n%s", mod)
	}

	t.Chdir(dir)
	if code, out, errOut := runCmd(t, "make:handler", "Orders"); code != 0 || !strings.Contains(out, "created app/handlers/orders.go") {
		t.Fatalf("make:handler: %d\n%s\n%s", code, out, errOut)
	}
	for _, args := range [][]string{{"make:auth"}, {"make:crud", "Product", "name:string"}} {
		if code, _, errOut := runCmd(t, args...); code != 1 || !strings.Contains(errOut, "this is an API project") {
			t.Errorf("%v: %d %s", args, code, errOut)
		}
	}
	if code, _, errOut := runCmd(t, "make:admin"); code != 1 || !strings.Contains(errOut, "the admin is for web projects") {
		t.Errorf("make:admin: %d %s", code, errOut)
	}
	goRun := func(args ...string) string {
		t.Helper()
		c := exec.Command("go", args...)
		c.Dir = dir
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, b)
		}
		return string(b)
	}
	goRun("vet", "./...")
	if out := goRun("test", "-v", "."); !strings.Contains(out, "--- PASS: TestWelcome") || !strings.Contains(out, "--- PASS: TestNotFound") {
		t.Errorf("go test:\n%s", out)
	}
	if code, out, errOut := runCmd(t, "build"); code != 0 || !strings.Contains(out, "built bin/shop (") {
		t.Fatalf("build: %d\n%s\n%s", code, out, errOut)
	}
	app := func(args ...string) string {
		t.Helper()
		c := exec.Command(filepath.Join(dir, "bin", "shop"), args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "DB_DATABASE="+filepath.Join(dir, "app.db"))
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, b)
		}
		return string(b)
	}
	if out := app("migrate"); !strings.Contains(out, "create_jobs_tables") || strings.Contains(out, "sessions") {
		t.Errorf("migrate:\n%s", out)
	}
	if out := app("routes:list"); !regexp.MustCompile(`GET\s+/api/v1\s+api\.welcome`).MatchString(out) {
		t.Errorf("routes:list:\n%s", out)
	}
	if out := app("lang:check"); !strings.Contains(out, "lang:check: en OK") {
		t.Errorf("lang:check:\n%s", out)
	}
	if out := app("doctor"); strings.Contains(out, "session") {
		t.Errorf("doctor:\n%s", out)
	}

	for _, d := range []string{"postgres", "mysql"} {
		t.Run(d, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "shop-"+d)
			if code, out, errOut := runCmd(t, "new", dir, "--db", d, "--stack=api", "--replace", repo); code != 0 {
				t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
			}
			c := exec.Command("go", "vet", "./...")
			c.Dir = dir
			if b, err := c.CombinedOutput(); err != nil {
				t.Fatalf("go vet: %v\n%s", err, b)
			}
		})
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// make:auth's and make:crud's code compiles in PostgreSQL and MySQL projects too (their
// tests need a server, so they only build here).
func TestMakeAuthServerDatabases(t *testing.T) {
	if testing.Short() {
		t.Skip("creates and builds projects")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"postgres", "mysql"} {
		t.Run(d, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "shop-"+d)
			if code, out, errOut := runCmd(t, "new", dir, "--module", "example.com/my.shop-"+d, "--db", d, "--replace", repo); code != 0 {
				t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
			}
			t.Chdir(dir)
			if code, out, errOut := runCmd(t, "make:auth"); code != 0 {
				t.Fatalf("make:auth: %d\n%s\n%s", code, out, errOut)
			}
			if code, out, errOut := runCmd(t, "make:crud", "Product", "name:string:unique", "price:float", "stock:int", "on_sale:bool", "launch_on:date:optional", "notes:text:optional"); code != 0 {
				t.Fatalf("make:crud: %d\n%s\n%s", code, out, errOut)
			}
			c := exec.Command("go", "vet", "./...")
			c.Dir = dir
			if b, err := c.CombinedOutput(); err != nil {
				t.Fatalf("go vet: %v\n%s", err, b)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
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
	bin := filepath.Join(dir, "tmp", "blog")
	goRun("build", "-o", bin, ".")
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
	if out := app("routes:list"); !regexp.MustCompile(`GET\s+/\s+home`).MatchString(out) {
		t.Errorf("routes:list:\n%s", out)
	}
	if out := app("help"); !strings.Contains(out, "migrate:status") || !strings.Contains(out, "serve") {
		t.Errorf("help:\n%s", out)
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

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// make:auth's code compiles in PostgreSQL and MySQL projects too (their
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
			c := exec.Command("go", "vet", "./...")
			c.Dir = dir
			if b, err := c.CombinedOutput(); err != nil {
				t.Fatalf("go vet: %v\n%s", err, b)
			}
		})
	}
}

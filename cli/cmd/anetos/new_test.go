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
	if out := app("migrate"); !strings.Contains(out, "create_posts_table") {
		t.Errorf("migrate:\n%s", out)
	}
	if out := app("routes:list"); !regexp.MustCompile(`GET\s+/\s+home`).MatchString(out) {
		t.Errorf("routes:list:\n%s", out)
	}
	if out := app("help"); !strings.Contains(out, "migrate:status") || !strings.Contains(out, "serve") {
		t.Errorf("help:\n%s", out)
	}
}

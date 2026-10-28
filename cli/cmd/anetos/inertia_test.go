// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/cli/internal/scaffold"
)

// A react or vue project keeps the starter theme: css:use names it, and
// refuses another framework (design D370).
func TestCSSUseInertia(t *testing.T) {
	for _, stack := range scaffold.InertiaStacks {
		t.Run(stack, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "shop")
			if _, err := scaffold.Create(scaffold.Project{Dir: dir, DB: "sqlite", Stack: stack}); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			if code, out, errOut := runCmd(t, "css:use"); code != 0 || !strings.Contains(out, "frontend/src/theme.css") {
				t.Errorf("css:use: %d %s %s", code, out, errOut)
			}
			if code, out, _ := runCmd(t, "css:use", "anetos"); code != 0 || !strings.Contains(out, "frontend/src/theme.css") {
				t.Errorf("css:use anetos: %d %s", code, out)
			}
			if code, _, errOut := runCmd(t, "css:use", "pico"); code != 1 || !strings.Contains(errOut, "v0.7") {
				t.Errorf("css:use pico: %d %s", code, errOut)
			}
		})
	}
}

func TestInstallStepsInertia(t *testing.T) {
	for _, stack := range scaffold.InertiaStacks {
		steps := installSteps("sqlite", stack, true, true)
		if !slices.Equal(steps[len(steps)-1], []string{"run", ".", "types"}) {
			t.Errorf("%s: %q", stack, steps)
		}
		for _, s := range steps {
			if slices.Equal(s, []string{"run", ".", "openapi"}) {
				t.Errorf("%s: %q", stack, steps)
			}
		}
	}
}

// With Node (ANETOS_TEST_NODE=1, as CI's latest Go cli part has), for
// each Inertia stack: anetos new --stack=react (or vue) installs the frontend's packages and writes its
// types; make:auth and make:crud add their pages and update the types;
// the project passes its tests, and anetos build type-checks the pages
// and builds them into public/build, which the binary embeds. A renamed
// route fails the type check (design D370–D376).
func TestNewProjectInertia(t *testing.T) {
	for _, stack := range scaffold.InertiaStacks {
		t.Run(stack, func(t *testing.T) { testNewProjectInertia(t, stack) })
	}
}

func testNewProjectInertia(t *testing.T, stack string) {
	if testing.Short() {
		t.Skip("creates and builds projects")
	}
	if os.Getenv("ANETOS_TEST_NODE") != "1" {
		t.Skip("set ANETOS_TEST_NODE=1 to install the frontend's packages with npm and build them with Vite")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	goIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	dir := filepath.Join(t.TempDir(), "shop")
	code, out, errOut := runCmd(t, "new", dir, "--stack="+stack, "--replace", repo)
	if code != 0 || !strings.Contains(out, "go run . types") || !strings.Contains(out, "npm install (frontend/)") {
		t.Fatalf("new --stack=%s: %d\n%s\n%s", stack, code, out, errOut)
	}
	layout := scaffold.InertiaLayout(stack)
	ext := filepath.Ext(layout)
	if pages := read(t, filepath.Join(dir, "frontend", "src", "types", "pages.ts")); !strings.Contains(pages, "'Home': HomeProps") {
		t.Errorf("pages.ts:\n%s", pages)
	}
	if routes := read(t, filepath.Join(dir, "frontend", "src", "types", "routes.ts")); !strings.Contains(routes, "'home': '/'") {
		t.Errorf("routes.ts:\n%s", routes)
	}
	goIn(dir, "test", "./...") // without the build: the tags are a comment
	t.Chdir(dir)
	code, out, errOut = runCmd(t, "make:auth")
	if code != 0 || !strings.Contains(out, "updated "+layout) || !strings.Contains(out, "updated app/handlers/pages.go") ||
		!strings.Contains(out, "Updated the frontend's types") || strings.Contains(out, "isn't a new project's") {
		t.Fatalf("make:auth: %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut = runCmd(t, "make:crud", "Task", "title:string", "notes:text:optional", "points:int", "weight:float:optional",
		"done:bool", "due:date:optional", "owner:email:optional")
	if code != 0 || !strings.Contains(out, "updated "+layout+": the nav links to /tasks") || !strings.Contains(out, "Updated the frontend's types") {
		t.Fatalf("make:crud: %d\n%s\n%s", code, out, errOut)
	}
	// Models with few kinds of fields: their pages import only what they
	// use (the type check refuses unused names).
	for _, args := range [][]string{{"Tag", "name:string"}, {"Note", "body:text", "done:bool"}} {
		if code, out, errOut := runCmd(t, append([]string{"make:crud"}, args...)...); code != 0 {
			t.Fatalf("make:crud %v: %d\n%s\n%s", args, code, out, errOut)
		}
	}
	if pages := read(t, filepath.Join(dir, "frontend", "src", "types", "pages.ts")); !strings.Contains(pages, "'Tasks/Index': TasksIndexProps") ||
		!strings.Contains(pages, "'Auth/Login': SocialProps") {
		t.Errorf("pages.ts:\n%s", pages)
	}
	goIn(dir, "vet", "./...")
	goIn(dir, "test", "./...")
	if code, out, errOut := runCmd(t, "build", "-o", "bin/shop"); code != 0 { // tsc (vue-tsc) checks every page
		t.Fatalf("build: %d\n%s\n%s", code, out, errOut)
	}
	entry := map[string]string{".tsx": "src/app.tsx", ".vue": "src/app.ts"}[ext]
	if manifest := read(t, filepath.Join(dir, "public", "build", "manifest.json")); !strings.Contains(manifest, `"`+entry+`"`) ||
		!strings.Contains(manifest, `"src/pages/Tasks/Index`+ext+`"`) {
		t.Errorf("manifest:\n%s", manifest)
	}
	writeFiles(t, filepath.Join(dir, "public"), map[string]string{"embed_test.go": embedTest})
	goIn(dir, "test", "./public")

	// The pages name routes: renaming one in Go fails the type check.
	routes := filepath.Join(dir, "routes", "tasks.go")
	src := read(t, routes)
	if !strings.Contains(src, `.Name("tasks.index")`) {
		t.Fatalf("routes/tasks.go:\n%s", src)
	}
	writeFiles(t, filepath.Join(dir, "routes"), map[string]string{"tasks.go": strings.Replace(src, `.Name("tasks.index")`, `.Name("tasks.list")`, 1)})
	goIn(dir, "run", ".", "types")
	if code, _, errOut := runCmd(t, "build", "-o", "bin/shop"); code != 1 || !strings.Contains(errOut, "tasks.index") {
		t.Errorf("build with a renamed route: %d\n%s", code, errOut)
	}
}

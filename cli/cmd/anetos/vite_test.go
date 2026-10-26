// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// With Node (ANETOS_TEST_NODE=1, as CI's latest Go cli part has): anetos
// new --vite installs the frontend's packages, the project builds and
// passes its tests without a frontend build, anetos build builds it into
// public/build and the binary, which embeds every file of the build;
// vite:install --js adds one to a project, which then builds and passes
// its tests (design D341–D345).
func TestNewProjectVite(t *testing.T) {
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
	code, out, errOut := runCmd(t, "new", dir, "--vite", "--replace", repo)
	if code != 0 || !strings.Contains(out, "npm install (frontend/)") {
		t.Fatalf("new --vite: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "frontend", "node_modules", ".bin", "vite")); err != nil {
		t.Fatalf("no vite installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "frontend", "package-lock.json")); err != nil {
		t.Errorf("no lockfile: %v", err)
	}
	goIn(dir, "test", "./...") // no build: the tags are a comment
	t.Chdir(dir)
	if code, _, errOut := runCmd(t, "vite:install"); code != 1 || !strings.Contains(errOut, "has frontend/ already") {
		t.Errorf("vite:install again: %d %s", code, errOut)
	}
	// Sources named with _, which //go:embed would leave out: a chunk and
	// an asset. The project's test checks that the binary embeds every
	// file of the manifest.
	writeFiles(t, filepath.Join(dir, "frontend", "src"), map[string]string{
		"main.ts":  "import './main.css'\nvoid import('./_lazy').then((m) => m.hi())\n",
		"_lazy.ts": "export function hi(): void {\n  console.log('hi')\n}\n",
		"main.css": "body { background: url(./_bg.svg); }\n",
		"_bg.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><!--` + strings.Repeat(" ", 6000) + `--></svg>` + "\n",
	})
	writeFiles(t, filepath.Join(dir, "public"), map[string]string{"embed_test.go": embedTest})
	if code, out, errOut := runCmd(t, "build", "-o", "bin/shop"); code != 0 {
		t.Fatalf("build: %d\n%s\n%s", code, out, errOut)
	}
	manifest := read(t, filepath.Join(dir, "public", "build", "manifest.json"))
	if !strings.Contains(manifest, `"src/main.ts"`) || !strings.Contains(manifest, `"isEntry": true`) ||
		!strings.Contains(manifest, `"file": "assets/lazy-`) || !strings.Contains(manifest, `"assets/bg-`) {
		t.Errorf("manifest:\n%s", manifest)
	}
	goIn(dir, "test", "./public")
	if _, err := os.Stat(filepath.Join(dir, "bin", "shop")); err != nil {
		t.Error(err)
	}
	// A type error fails the build.
	writeFiles(t, filepath.Join(dir, "frontend", "src"), map[string]string{"main.ts": "import './main.css'\nconst n: number = 'not a number'\nconsole.log(n)\n"})
	if code, _, errOut := runCmd(t, "build", "-o", "bin/shop"); code != 1 || !strings.Contains(errOut, "npm run build in frontend/") {
		t.Errorf("build with a type error: %d\n%s", code, errOut)
	}

	// vite:install --js in a project made without one.
	plain := filepath.Join(t.TempDir(), "plain")
	if code, out, errOut := runCmd(t, "new", plain, "--replace", repo); code != 0 {
		t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
	}
	t.Chdir(plain)
	code, out, errOut = runCmd(t, "vite:install", "--js")
	if code != 0 || !strings.Contains(out, "created frontend/src/main.js") || !strings.Contains(out, "updated views/layout.templ") ||
		strings.Contains(out, "Add by hand") {
		t.Fatalf("vite:install --js: %d\n%s\n%s", code, out, errOut)
	}
	// vite:install ran templ generate: the layout's Go has the tags.
	if !strings.Contains(read(t, filepath.Join(plain, "views", "layout_templ.go")), "vite.Tags(") {
		t.Error("views/layout_templ.go has no vite.Tags")
	}
	goIn(plain, "vet", "./...")
	goIn(plain, "test", "./...")
	if code, out, errOut := runCmd(t, "build", "-o", "bin/plain"); code != 0 {
		t.Fatalf("build: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(read(t, filepath.Join(plain, "public", "build", "manifest.json")), `"src/main.js"`) {
		t.Error("no src/main.js in the manifest")
	}
}

// embedTest checks that public.Files has every file of the manifest.
const embedTest = `package public

import (
	"encoding/json"
	"io/fs"
	"testing"
)

func TestEmbedsBuild(t *testing.T) {
	b, err := fs.ReadFile(Files, "build/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]struct {
		File   string   ` + "`json:\"file\"`" + `
		CSS    []string ` + "`json:\"css\"`" + `
		Assets []string ` + "`json:\"assets\"`" + `
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range m {
		for _, f := range append(append([]string{c.File}, c.CSS...), c.Assets...) {
			n++
			if _, err := fs.Stat(Files, "build/"+f); err != nil {
				t.Errorf("not embedded: %v", err)
			}
		}
	}
	if n < 4 {
		t.Errorf("%d files in the manifest", n)
	}
}
`

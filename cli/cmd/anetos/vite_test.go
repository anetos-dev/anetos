// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// With Node (ANETOS_TEST_NODE=1, as make test-frontend and CI's frontend job set): anetos
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

// With Node: an API project's single-page app (design D365). anetos new
// --stack=api --vite=js installs its packages and writes its client of
// the API; the project passes its tests without a build, and anetos build
// builds it into public/spa, which the binary embeds. vite:install after
// make:auth writes the login page, which type-checks against the client.
func TestNewAPIProjectSPA(t *testing.T) {
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
	code, out, errOut := runCmd(t, "new", dir, "--stack=api", "--vite=js", "--replace", repo)
	if code != 0 || !strings.Contains(out, "go run . types") || !strings.Contains(out, "npm install (frontend/)") {
		t.Fatalf("new --stack=api --vite=js: %d\n%s\n%s", code, out, errOut)
	}
	if api := read(t, filepath.Join(dir, "frontend", "src", "types", "api.ts")); !strings.Contains(api, "welcome: (") {
		t.Errorf("api.ts:\n%s", api)
	}
	goIn(dir, "test", "./...") // without the build: the SPA's page says so
	t.Chdir(dir)
	if code, out, errOut := runCmd(t, "build", "-o", "bin/shop"); code != 0 {
		t.Fatalf("build: %d\n%s\n%s", code, out, errOut)
	}
	if html := read(t, filepath.Join(dir, "public", "spa", "index.html")); !strings.Contains(html, `<script type="module" crossorigin src="/assets/index-`) {
		t.Errorf("index.html:\n%s", html)
	}
	writeFiles(t, filepath.Join(dir, "public"), map[string]string{"embed_test.go": spaEmbedTest})
	goIn(dir, "test", "./public")
	// build and check write the client from the Go code as it is, before
	// the frontend's build type-checks against it.
	stale := filepath.Join(dir, "frontend", "src", "types", "api.ts")
	for _, step := range []func() error{
		func() error {
			if code, out, errOut := runCmd(t, "build", "-o", "bin/shop"); code != 0 {
				return fmt.Errorf("build: %d\n%s\n%s", code, out, errOut)
			}
			return nil
		},
		func() error { return checkGenerate(context.Background(), dir, io.Discard) },
	} {
		if err := os.WriteFile(stale, []byte("// stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := step(); err != nil {
			t.Fatal(err)
		}
		if api := read(t, stale); !strings.Contains(api, "welcome: (") {
			t.Errorf("api.ts not rewritten:\n%s", api)
		}
	}

	// vite:install after make:auth: the login page.
	authDir := filepath.Join(t.TempDir(), "accounts")
	if code, out, errOut := runCmd(t, "new", authDir, "--stack=api", "--replace", repo); code != 0 {
		t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
	}
	t.Chdir(authDir)
	if code, out, errOut := runCmd(t, "make:auth"); code != 0 {
		t.Fatalf("make:auth: %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut = runCmd(t, "vite:install")
	if code != 0 || !strings.Contains(out, "created frontend/src/main.ts") || !strings.Contains(out, "updated main.go") ||
		!strings.Contains(out, "updated .env") || strings.Contains(out, "Add by hand") || !strings.Contains(out, "open http://localhost:5173") {
		t.Fatalf("vite:install: %d\n%s\n%s", code, out, errOut)
	}
	if api := read(t, filepath.Join(authDir, "frontend", "src", "types", "api.ts")); !strings.Contains(api, "loginTwoFactor: (") {
		t.Errorf("api.ts has no login:\n%s", api)
	}
	goIn(authDir, "vet", "./...")
	goIn(authDir, "test", "./...")
	if code, out, errOut := runCmd(t, "build", "-o", "bin/accounts"); code != 0 { // tsc checks main.ts
		t.Fatalf("build: %d\n%s\n%s", code, out, errOut)
	}
}

// spaEmbedTest checks that public.Files has the SPA's page and the
// files it loads.
const spaEmbedTest = `package public

import (
	"io/fs"
	"regexp"
	"testing"
)

func TestEmbedsSPA(t *testing.T) {
	b, err := fs.ReadFile(Files, "spa/index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(` + "`(?:src|href)=\"/(assets/[^\"]+)\"`" + `).FindAllSubmatch(b, -1)
	if len(refs) < 2 {
		t.Fatalf("index.html loads %d files:\n%s", len(refs), b)
	}
	for _, r := range refs {
		if _, err := fs.Stat(Files, "spa/"+string(r[1])); err != nil {
			t.Errorf("not embedded: %v", err)
		}
	}
}
`

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

// Every test that needs Node (ANETOS_TEST_NODE) is one the Makefile's
// test-frontend runs (FRONTEND_TESTS, CI's frontend job), which skips the
// others: one it didn't list would run nowhere (design D380).
func TestFrontendTestsListed(t *testing.T) {
	makefile := read(t, filepath.Join("..", "..", "..", "Makefile"))
	m := regexp.MustCompile(`(?m)^FRONTEND_TESTS = \^\(([^)]*)\)\$\$$`).FindStringSubmatch(makefile)
	if m == nil {
		t.Fatal("the Makefile has no FRONTEND_TESTS = ^(…)$$ line")
	}
	listed := strings.Split(m[1], "|")
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var needNode []string
	decl := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	for _, f := range files {
		src := read(t, f)
		funcs := decl.FindAllStringSubmatchIndex(src, -1)
		for i, fn := range funcs {
			end := len(src)
			if i+1 < len(funcs) {
				end = funcs[i+1][0]
			}
			// A test's code reaches to the next test: its helpers too.
			if name := src[fn[2]:fn[3]]; name != "TestFrontendTestsListed" && strings.Contains(src[fn[0]:end], `os.Getenv("ANETOS_TEST_NODE")`) {
				needNode = append(needNode, name)
			}
		}
	}
	slices.Sort(listed)
	slices.Sort(needNode)
	if !slices.Equal(listed, needNode) {
		t.Errorf("FRONTEND_TESTS lists %v; the tests that need Node are %v", listed, needNode)
	}
}

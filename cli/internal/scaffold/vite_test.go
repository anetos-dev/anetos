// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"encoding/json"
	"errors"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

// anetos new --vite: frontend/ in either language, and the app wired to
// it (design D341–D345).
func TestCreateVite(t *testing.T) {
	for _, lang := range Languages {
		for _, kit := range []string{"anetos", "tailwind"} {
			dir := filepath.Join(t.TempDir(), "shop")
			files, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", CSS: kit, Vite: lang, Agents: Agents})
			if err != nil {
				t.Fatalf("%s %s: %v", lang, kit, err)
			}
			want := []string{"frontend/package.json", "frontend/vite.config." + lang, "frontend/src/main." + lang, "frontend/src/main.css", "frontend/go.mod"}
			if lang == "ts" {
				want = append(want, "frontend/tsconfig.json")
			}
			for _, f := range want {
				if !slices.Contains(files, f) {
					t.Errorf("%s: %s not written: %v", lang, f, files)
				}
			}
			if lang == "js" && slices.Contains(files, "frontend/tsconfig.json") {
				t.Error("js: tsconfig.json written")
			}
			var pkg struct {
				Name    string            `json:"name"`
				Type    string            `json:"type"`
				Scripts map[string]string `json:"scripts"`
				Dev     map[string]string `json:"devDependencies"`
			}
			if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, "frontend", "package.json"))), &pkg); err != nil {
				t.Fatalf("%s: package.json: %v", lang, err)
			}
			wantBuild := "vite build"
			if lang == "ts" {
				wantBuild = "tsc --noEmit && vite build"
			}
			if pkg.Name != "shop-frontend" || pkg.Type != "module" || pkg.Scripts["dev"] != "vite" || pkg.Scripts["build"] != wantBuild ||
				pkg.Dev["vite"] != "^"+ViteVersion || (pkg.Dev["typescript"] != "") != (lang == "ts") {
				t.Errorf("%s: package.json %+v", lang, pkg)
			}
			cfg := read(t, filepath.Join(dir, "frontend", "vite.config."+lang))
			for _, s := range []string{"base: '/build/'", "outDir: '../public/build'", "manifest: 'manifest.json'", "input: ['src/main." + lang + "']",
				"chunkFileNames: (chunk) => `assets/${embeddable(chunk.name)}-[hash].js`"} {
				if !strings.Contains(cfg, s) {
					t.Errorf("%s: vite.config lacks %q:\n%s", lang, s, cfg)
				}
			}
			mod, err := modfile.Parse("go.mod", []byte(read(t, filepath.Join(dir, "frontend", "go.mod"))), nil)
			if err != nil || mod.Module.Mod.Path != "example.com/shop/frontend" {
				t.Errorf("%s: frontend/go.mod: %v", lang, err)
			}
			main := read(t, filepath.Join(dir, "main.go"))
			if !strings.Contains(main, "\tif _, err := vite.New(app, public.Files); err != nil {\n\t\treturn nil, err\n\t}\n\tsrv, err := web.NewServer(app)") ||
				!strings.Contains(main, "\t\"anetos.dev/anetos/view/vite\"\n\t\"anetos.dev/anetos/web\"\n") || !strings.Contains(main, "\t\"example.com/shop/public\"\n\t\"example.com/shop/routes\"\n") {
				t.Errorf("%s: main.go:\n%s", lang, main)
			}
			for _, f := range []string{"main.go", "routes/web.go"} {
				b := read(t, filepath.Join(dir, f))
				if out, err := format.Source([]byte(b)); err != nil || string(out) != b {
					t.Errorf("%s: %s isn't gofmt'd: %v", lang, f, err)
				}
			}
			if routes := read(t, filepath.Join(dir, "routes", "web.go")); !strings.Contains(routes, "web.WrapHandler(public.Assets))\n\tr.Get(\"/build/{path...}\", web.WrapHandler(vite.Handler()))\n") {
				t.Errorf("%s: routes/web.go:\n%s", lang, routes)
			}
			layout := read(t, filepath.Join(dir, "views", "layout.templ"))
			if !strings.Contains(layout, "@ui.Head()\n\t\t\t@vite.Tags(\"src/main."+lang+"\")\n") ||
				!strings.Contains(layout, "\t\"anetos.dev/anetos/view\"\n\t\"anetos.dev/anetos/view/vite\"\n\t\"anetos.dev/anetos/web\"\n") {
				t.Errorf("%s: layout:\n%s", lang, layout)
			}
			if gi := read(t, filepath.Join(dir, ".gitignore")); !strings.HasSuffix(gi, "/frontend/node_modules/\n/public/build/\n") {
				t.Errorf("%s: .gitignore:\n%s", lang, gi)
			}
			if di := read(t, filepath.Join(dir, ".dockerignore")); !strings.HasSuffix(di, "/frontend/node_modules/\n/public/build/\n") {
				t.Errorf("%s: .dockerignore:\n%s", lang, di)
			}
			df := read(t, filepath.Join(dir, "Dockerfile"))
			if strings.Index(df, "FROM node:22-alpine AS frontend") > strings.Index(df, "FROM golang:") || strings.Count(df, "AS frontend") != 1 ||
				!strings.Contains(df, "COPY . .\nCOPY --from=frontend /src/public/build ./public/build\n") ||
				!strings.Contains(df, "go tool anetos build --no-frontend -o /out/shop") || strings.Contains(df, "go tool anetos build -o") {
				t.Errorf("%s %s: Dockerfile:\n%s", lang, kit, df)
			}
			if agents := read(t, filepath.Join(dir, "AGENTS.md")); !strings.Contains(agents, "| `frontend/` | The frontend Vite builds") || !strings.Contains(agents, "go in `frontend/src`") {
				t.Errorf("%s: AGENTS.md says nothing of frontend/", lang)
			}
		}
	}
	// Another frontend, or one in an API project, is refused.
	dir := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, DB: "sqlite", Vite: "ts"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddVite(dir, "ts"); !errors.Is(err, ErrHasFrontend) {
		t.Errorf("twice: %v", err)
	}
	if _, err := Create(Project{Dir: filepath.Join(t.TempDir(), "api"), DB: "sqlite", Stack: "api", Vite: "ts"}); err == nil || !strings.Contains(err.Error(), "--vite is for the web stack") {
		t.Errorf("api: %v", err)
	}
	if _, err := Create(Project{Dir: filepath.Join(t.TempDir(), "x"), DB: "sqlite", Vite: "coffee"}); err == nil || !strings.Contains(err.Error(), "--vite must be one of ts, js") {
		t.Errorf("coffee: %v", err)
	}
	api := filepath.Join(t.TempDir(), "api")
	if _, err := Create(Project{Dir: api, DB: "sqlite", Stack: "api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddVite(api, "ts"); err == nil || !strings.Contains(err.Error(), "an api project") {
		t.Errorf("AddVite in an api project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(api, "frontend")); err == nil {
		t.Error("frontend/ written in an api project")
	}
}

// vite:install in a project whose files the app changed: what it can't
// patch safely, it leaves alone and says how to add.
func TestAddViteManual(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	main := strings.Replace(read(t, filepath.Join(dir, "main.go")), "srv, err := web.NewServer(app)", "srv, err := web.NewServer(app, web.WithLogger(nil))", 1)
	write(t, filepath.Join(dir, "main.go"), main)
	// The layout is left alone too: its tags need vite.New.
	layout := read(t, filepath.Join(dir, "views", "layout.templ"))
	if err := os.Remove(filepath.Join(dir, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	res, err := AddVite(dir, "js")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Updated, []string{"routes/web.go", ".gitignore", ".dockerignore"}) || res.Entry != "src/main.js" {
		t.Errorf("updated %v", res.Updated)
	}
	if len(res.Manual) != 3 || !strings.HasPrefix(res.Manual[0], "main.go: ") || !strings.HasPrefix(res.Manual[1], "views/layout.templ: ") ||
		!strings.Contains(res.Manual[1], `@vite.Tags("src/main.js")`) || !strings.Contains(res.Manual[1], "once main.go calls vite.New") ||
		!strings.HasPrefix(res.Manual[2], "Dockerfile: ") {
		t.Errorf("manual %q", res.Manual)
	}
	if read(t, filepath.Join(dir, "main.go")) != main || read(t, filepath.Join(dir, "views", "layout.templ")) != layout {
		t.Error("a file it couldn't patch changed")
	}
	if _, err := AddVite(dir, "rust"); err == nil {
		t.Error("rust")
	}
}

// A Windows checkout's CRLF files are patched, and keep their CRLF.
func TestAddViteCRLF(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	files := []string{"main.go", "routes/web.go", "views/layout.templ", ".gitignore", ".dockerignore", "Dockerfile"}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		write(t, p, strings.ReplaceAll(read(t, p), "\n", "\r\n"))
	}
	res, err := AddVite(dir, "ts")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Updated, files) || len(res.Manual) != 0 {
		t.Fatalf("updated %v, manual %q", res.Updated, res.Manual)
	}
	for _, f := range files {
		b := read(t, filepath.Join(dir, filepath.FromSlash(f)))
		if strings.Count(b, "\n") != strings.Count(b, "\r\n") {
			t.Errorf("%s has LF lines:\n%q", f, b)
		}
	}
	if !strings.Contains(read(t, filepath.Join(dir, "main.go")), "vite.New(app, public.Files)") {
		t.Error("main.go not patched")
	}
}

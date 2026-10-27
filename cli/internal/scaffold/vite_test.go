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
	"time"

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
	if _, err := Create(Project{Dir: filepath.Join(t.TempDir(), "x"), DB: "sqlite", Vite: "coffee"}); err == nil || !strings.Contains(err.Error(), "--vite must be one of ts, js") {
		t.Errorf("coffee: %v", err)
	}
}

// anetos new --stack=api --vite, and vite:install in an API project with
// make:auth: a single-page app, built into public/spa and served at /
// (design D365).
func TestAddViteSPA(t *testing.T) {
	for _, lang := range Languages {
		dir := filepath.Join(t.TempDir(), "shop")
		files, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", Stack: "api", Vite: lang, Agents: Agents})
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		want := []string{"frontend/package.json", "frontend/vite.config." + lang, "frontend/index.html", "frontend/src/main." + lang,
			"frontend/src/style.css", "frontend/go.mod", "public/public.go"}
		if lang == "ts" {
			want = append(want, "frontend/tsconfig.json")
		}
		for _, f := range want {
			if !slices.Contains(files, f) {
				t.Errorf("%s: %s not written: %v", lang, f, files)
			}
		}
		if slices.Contains(files, "frontend/src/main.css") || slices.Contains(files, "frontend/public.go") {
			t.Errorf("%s: a web project's files written: %v", lang, files)
		}
		cfg := read(t, filepath.Join(dir, "frontend", "vite.config."+lang))
		for _, s := range []string{"port: 5173", "strictPort: true", "proxy: { '/api': 'http://127.0.0.1:8080' }", "outDir: '../public/spa'",
			"chunkFileNames: (chunk) => `assets/${embeddable(chunk.name)}-[hash].js`"} {
			if !strings.Contains(cfg, s) {
				t.Errorf("%s: vite.config lacks %q:\n%s", lang, s, cfg)
			}
		}
		if strings.Contains(cfg, "base:") || strings.Contains(cfg, "manifest") {
			t.Errorf("%s: vite.config has a web project's base or manifest:\n%s", lang, cfg)
		}
		if html := read(t, filepath.Join(dir, "frontend", "index.html")); !strings.Contains(html, `<script type="module" src="/src/main.`+lang+`"></script>`) ||
			!strings.Contains(html, "<title>Shop</title>") {
			t.Errorf("%s: index.html:\n%s", lang, html)
		}
		// Without make:auth, the page shows the welcome.
		if m := read(t, filepath.Join(dir, "frontend", "src", "main."+lang)); !strings.Contains(m, "client.api.welcome()") || strings.Contains(m, "login") {
			t.Errorf("%s: main:\n%s", lang, m)
		}
		main := read(t, filepath.Join(dir, "main.go"))
		for _, s := range []string{
			"\tif err := openapi.Register(app, srv, routes.OpenAPI); err != nil {\n\t\treturn nil, err\n\t}\n\t// `go run . types`",
			"\tif err := typescript.Register(app, srv, typescript.Config{Prefix: routes.OpenAPI.Prefix}); err != nil {\n\t\treturn nil, err\n\t}\n",
			"\tspa, err := vite.SPA(app, public.Files, vite.Except(\"/api/\"))\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\tsrv.Router().Fallback(spa)\n",
			"\t\"anetos.dev/anetos/view/vite\"\n", "\t\"anetos.dev/anetos/web/typescript\"\n", "\t\"example.com/shop/public\"\n\t\"example.com/shop/routes\"\n",
		} {
			if !strings.Contains(main, s) {
				t.Errorf("%s: main.go lacks %q:\n%s", lang, s, main)
			}
		}
		if out, err := format.Source([]byte(main)); err != nil || string(out) != main {
			t.Errorf("%s: main.go isn't gofmt'd: %v", lang, err)
		}
		if pub := read(t, filepath.Join(dir, "public", "public.go")); !strings.Contains(pub, "//go:embed *\nvar Files embed.FS\n") {
			t.Errorf("%s: public.go:\n%s", lang, pub)
		}
		for _, f := range []string{".gitignore", ".dockerignore"} {
			if b := read(t, filepath.Join(dir, f)); !strings.HasSuffix(b, "/frontend/node_modules/\n/public/spa/\n") {
				t.Errorf("%s: %s:\n%s", lang, f, b)
			}
		}
		df := read(t, filepath.Join(dir, "Dockerfile"))
		if !strings.Contains(df, "built by Vite into public/spa, which the\n# Go stage embeds.") ||
			!strings.Contains(df, "COPY . .\nCOPY --from=frontend /src/public/spa ./public/spa\n") ||
			!strings.Contains(df, "go tool anetos build --no-frontend -o /out/shop") {
			t.Errorf("%s: Dockerfile:\n%s", lang, df)
		}
		for _, f := range []string{".env", ".env.example"} {
			if b := read(t, filepath.Join(dir, f)); !strings.Contains(b, "\nHTTP_SPA_ORIGINS=http://localhost:5173,http://localhost:8080\n") {
				t.Errorf("%s: %s:\n%s", lang, f, b)
			}
		}
		if b := read(t, filepath.Join(dir, "deploy", "production.env.example")); !strings.Contains(b, "\nHTTP_SPA_ORIGINS=https://example.com\n") {
			t.Errorf("%s: production.env.example:\n%s", lang, b)
		}
		if !ServesSPA(dir) || ServesSPA(t.TempDir()) {
			t.Errorf("%s: ServesSPA", lang)
		}
		if agents := read(t, filepath.Join(dir, "AGENTS.md")); !strings.Contains(agents, "| `frontend/` | The single-page app Vite builds") ||
			!strings.Contains(agents, "**The SPA** (`frontend/src`)") {
			t.Errorf("%s: AGENTS.md says nothing of the SPA", lang)
		}
		if p, err := ReadAgentProject(dir); err != nil || !p.Frontend {
			t.Errorf("%s: ReadAgentProject: %+v, %v", lang, p, err)
		}
	}

	// vite:install after make:auth: a login page, and the emails link to
	// the app's own address in production.
	dir := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", Stack: "api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeAuth(dir, time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	res, err := AddVite(dir, "ts")
	if err != nil {
		t.Fatal(err)
	}
	if !res.SPA || len(res.Manual) != 0 ||
		!slices.Equal(res.Updated, []string{"main.go", ".gitignore", ".dockerignore", "Dockerfile", ".env", ".env.example", "deploy/production.env.example"}) {
		t.Errorf("updated %v, manual %q", res.Updated, res.Manual)
	}
	m := read(t, filepath.Join(dir, "frontend", "src", "main.ts"))
	for _, s := range []string{"client.api.me()", "client.api.login({ email, password })", "client.api.loginTwoFactor({ code })",
		"client.api.register({ name, email, password, password_confirmation: password })", "client.api.logout()",
		"import { ApiError, createApi, type UserResponse } from './types/api'"} {
		if !strings.Contains(m, s) {
			t.Errorf("main.ts lacks %q:\n%s", s, m)
		}
	}
	if b := read(t, filepath.Join(dir, "deploy", "production.env.example")); !strings.Contains(b, "\nAUTH_CLIENT_URL=https://example.com\n") {
		t.Errorf("production.env.example:\n%s", b)
	}
	// Run again on a project that has it: refused, nothing changed.
	if _, err := AddVite(dir, "ts"); !errors.Is(err, ErrHasFrontend) {
		t.Errorf("twice: %v", err)
	}
	// Again, once frontend/ is gone: what is there already isn't asked for
	// again.
	if err := os.RemoveAll(filepath.Join(dir, "frontend")); err != nil {
		t.Fatal(err)
	}
	if res, err := AddVite(dir, "ts"); err != nil || len(res.Manual) != 0 || len(res.Updated) != 0 {
		t.Errorf("again: %+v, %v", res, err)
	}

	// make:auth after vite:install: the emails link to the SPA, at APP_URL.
	dir = filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", Stack: "api", Vite: "ts"}); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeAuth(dir, time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if b := read(t, filepath.Join(dir, "deploy", "production.env.example")); !strings.Contains(b, "\nAUTH_CLIENT_URL=https://example.com\n") {
		t.Errorf("production.env.example after make:auth:\n%s", b)
	}
}

// vite:install in an API project whose main.go and .env the app changed.
func TestAddViteSPAManual(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", Stack: "api"}); err != nil {
		t.Fatal(err)
	}
	main := strings.Replace(read(t, filepath.Join(dir, "main.go")), "openapi.Register(app, srv, routes.OpenAPI)", "openapi.Register(app, srv, routes.APIv1)", 1)
	write(t, filepath.Join(dir, "main.go"), main)
	env := strings.Replace(read(t, filepath.Join(dir, ".env")), "HTTP_SPA_ORIGINS=\n", "HTTP_SPA_ORIGINS=https://app.test\n", 1)
	write(t, filepath.Join(dir, ".env"), env)
	// One listed already is left as it is, quietly.
	example := strings.Replace(read(t, filepath.Join(dir, ".env.example")), "HTTP_SPA_ORIGINS=\n", "HTTP_SPA_ORIGINS=https://app.test, http://localhost:5173/\n", 1)
	write(t, filepath.Join(dir, ".env.example"), example)
	if err := os.Remove(filepath.Join(dir, "deploy", "production.env.example")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "public", "public.go"), "package public\n")
	res, err := AddVite(dir, "js")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Updated, []string{".gitignore", ".dockerignore", "Dockerfile"}) || slices.Contains(res.Created, "public/public.go") {
		t.Errorf("updated %v, created %v", res.Updated, res.Created)
	}
	if len(res.Manual) != 3 || !strings.HasPrefix(res.Manual[0], "main.go: ") || !strings.Contains(res.Manual[0], "srv.Router().Fallback(spa)") ||
		!strings.HasPrefix(res.Manual[1], ".env: list http://localhost:5173") || !strings.HasPrefix(res.Manual[2], "public/public.go: ") {
		t.Errorf("manual %q", res.Manual)
	}
	if read(t, filepath.Join(dir, "main.go")) != main || read(t, filepath.Join(dir, ".env")) != env || read(t, filepath.Join(dir, ".env.example")) != example {
		t.Error("a file it couldn't patch changed")
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

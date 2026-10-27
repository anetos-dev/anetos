// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// newReact creates a project of the react stack.
func newReact(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if _, err := Create(Project{Dir: dir, Module: "example.com/" + name, DB: "sqlite", Replace: "../../..", Stack: "react", Agents: Agents}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// classlessPages fails the test when a page component under
// frontend/src/pages has a class: pages are made of the components of
// src/components/ui, as views/ui's in the web stack (design D370).
func classlessPages(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(root, "frontend", "src", "pages"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, err := os.ReadFile(path); err != nil {
			return err
		} else if strings.Contains(string(b), "className") {
			t.Errorf("%s has a class", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --stack=react: the web stack's Go side, with pages that are React
// components which Inertia's client renders (design D370–D376).
func TestCreateReact(t *testing.T) {
	dir := newReact(t, "shop")
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil || len(files) == 0 {
		t.Fatal(files, err)
	}
	for _, want := range []string{"main.go", "main_test.go", "routes/web.go", "app/handlers/pages.go", "app/handlers/home.go",
		"views/app.templ", "public/public.go", "public/favicon.svg", "locales/en/app.yaml", "frontend/package.json",
		"frontend/vite.config.ts", "frontend/tsconfig.json", "frontend/go.mod", "frontend/src/app.tsx", "frontend/src/Layout.tsx",
		"frontend/src/i18n.ts", "frontend/src/theme.css", "frontend/src/app.css", "frontend/src/components/ui.tsx",
		"frontend/src/pages/Home.tsx", "frontend/src/pages/Error.tsx", "AGENTS.md", ".claude/skills/anetos-page/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	for _, no := range []string{"views/layout.templ", "views/ui", "views/home.templ", "public/static/app.css", KitRecordFile} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(no))); err == nil {
			t.Errorf("a react project has %s", no)
		}
	}
	classlessPages(t, dir)
	if InertiaFramework(dir) != "react" || IsAPI(dir) || ServesSPA(dir) {
		t.Errorf("InertiaFramework %q, IsAPI %v, ServesSPA %v", InertiaFramework(dir), IsAPI(dir), ServesSPA(dir))
	}
	if LayoutFile(dir) != "views/app.templ" {
		t.Errorf("LayoutFile = %s", LayoutFile(dir))
	}
	main := read(t, filepath.Join(dir, "main.go"))
	for _, want := range []string{`vite.New(app, public.Files, vite.WithReactRefresh())`, "inertia.WithShared(handlers.SharedProps)",
		"inertia.WithPages(handlers.Pages...)", `typescript.Config{Inertia: in, Routes: true, ExcludeRoutes: []string{"admin."}}`, "routes.Register(srv.Router(), sessions)"} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go lacks %q:\n%s", want, main)
		}
	}
	pkg := read(t, filepath.Join(dir, "frontend", "package.json"))
	for _, want := range []string{`"@inertiajs/react": "^` + InertiaVersion + `"`, `"react": "^` + ReactVersion + `"`,
		`"@types/react": "^` + ReactTypesVersion + `"`, `"@vitejs/plugin-react": "^` + ReactPluginVersion + `"`,
		`"vite": "^` + ViteVersion + `"`, `"typescript": "^` + TypeScriptVersion + `"`} {
		if !strings.Contains(pkg, want) {
			t.Errorf("package.json lacks %s:\n%s", want, pkg)
		}
	}
	if theme := read(t, filepath.Join(dir, "frontend", "src", "theme.css")); !strings.Contains(theme, "--surface-2:") || !strings.Contains(theme, "src/app.tsx") || strings.Contains(theme, "views/ui") {
		t.Errorf("theme.css isn't the starter theme's:\n%.300s", theme)
	}
	docker := read(t, filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(docker, "npm run build") || !strings.Contains(docker, "public/build") {
		t.Errorf("Dockerfile builds no frontend:\n%s", docker)
	}
	for _, f := range []string{".gitignore", ".dockerignore"} {
		if ig := read(t, filepath.Join(dir, f)); !strings.Contains(ig, "/frontend/node_modules/") || !strings.Contains(ig, "/public/build/") {
			t.Errorf("%s:\n%s", f, ig)
		}
	}
	// The agent files are the ones agent:update would write: none stale.
	if stale := mustStale(t, dir); len(stale) != 0 {
		t.Errorf("stale agent files: %v", stale)
	}
	if a := read(t, filepath.Join(dir, "AGENTS.md")); !strings.Contains(a, "React components") || strings.Contains(a, "views/ui") {
		t.Errorf("AGENTS.md:\n%s", a)
	}

	// Refused: another CSS framework (until v0.7), and --vite, which the
	// stack has.
	for _, p := range []Project{
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: "react", CSS: "pico"},
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: "react", CSS: "none"},
		{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: "react", Vite: "ts"},
	} {
		if _, err := Create(p); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
	if _, err := Create(Project{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: "react", CSS: "anetos"}); err != nil {
		t.Errorf("--css=anetos: %v", err)
	}
}

// InertiaFramework reads the adapter frontend/package.json depends on;
// an API project's SPA, or a web project's frontend, isn't one.
func TestInertiaFramework(t *testing.T) {
	root := t.TempDir()
	if InertiaFramework(root) != "" {
		t.Error("an empty directory has a framework")
	}
	// A web project with Inertia added by hand (no page function) is
	// the web stack's.
	writeFile(t, root, "frontend/package.json", `{"dependencies": {"@inertiajs/react": "^3.9.1"}}`)
	if InertiaFramework(root) != "" {
		t.Error("a web project with Inertia added by hand has a framework")
	}
	writeFile(t, root, "app/handlers/pages.go", "package handlers\n\nfunc page[P any](component string) {}\n")
	for pkg, want := range map[string]string{
		`{"dependencies": {"@inertiajs/react": "^3.9.1", "react": "^19.3.1"}}`: "react",
		`{"devDependencies": {"@inertiajs/react": "^3.9.1"}}`:                  "react",
		`{"dependencies": {"react": "^19.3.1"}}`:                               "",
		`not JSON`:                                                             "",
	} {
		writeFile(t, root, "frontend/package.json", pkg)
		if got := InertiaFramework(root); got != want {
			t.Errorf("%s: %q, want %q", pkg, got, want)
		}
	}
}

// make:auth in a react project: the web stack's Go, with Inertia's
// renders, and React pages; every page gets the user, and the layout's
// header the account's links.
func TestMakeAuthInertia(t *testing.T) {
	dir := newReact(t, "shop")
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	res, err := MakeAuth(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app/models/user.go", "app/handlers/auth.go", "app/handlers/settings.go", "views/auth_mail.templ",
		"routes/auth.go", "auth.go", "auth_test.go", "frontend/src/components/AccountMenu.tsx", "frontend/src/pages/Auth/Login.tsx",
		"frontend/src/pages/Auth/Settings.tsx", "frontend/src/pages/Auth/TwoFactor.tsx"} {
		if !slices.Contains(res.Created, want) {
			t.Errorf("missing %s in %v", want, res.Created)
		}
	}
	if slices.Contains(res.Created, "views/auth.templ") {
		t.Error("a react project got templ pages")
	}
	classlessPages(t, dir)
	if !res.Wired || !res.Shared || !res.Menu || res.API {
		t.Errorf("result %+v", res)
	}
	if p := read(t, filepath.Join(dir, "app", "handlers", "pages.go")); !strings.Contains(p, "\treturn authUser(c) // anetos make:auth") {
		t.Errorf("pages.go:\n%s", p)
	}
	layout := read(t, filepath.Join(dir, "frontend", "src", "Layout.tsx"))
	if !strings.Contains(layout, "import AccountMenu from './components/AccountMenu'\nimport { Flash } from './components/ui'") ||
		!strings.Contains(layout, "<AccountMenu />") || strings.Contains(layout, "make:auth adds them") {
		t.Errorf("Layout.tsx:\n%s", layout)
	}
	h := alignment.ReplaceAllString(read(t, filepath.Join(dir, "app", "handlers", "auth.go")), " ")
	if !strings.Contains(h, `loginPage = page[SocialProps]("Auth/Login")`) || strings.Contains(h, "views.") {
		t.Errorf("auth.go:\n%s", h)
	}
	if r := read(t, filepath.Join(dir, "routes", "auth.go")); !strings.Contains(r, "inertia.Middleware") {
		t.Errorf("routes/auth.go:\n%s", r)
	}

	// A project whose currentUser and layout aren't anetos new's: neither
	// is changed, and the result says so.
	other := newReact(t, "other")
	pages := strings.Replace(read(t, filepath.Join(other, "app", "handlers", "pages.go")), "return nil, nil", "return nil, nil // mine", 1)
	writeFile(t, other, "app/handlers/pages.go", pages)
	layoutFile := filepath.Join(other, "frontend", "src", "Layout.tsx")
	mine := strings.Replace(read(t, layoutFile), "{/* The account's links (go tool anetos make:auth adds them). */}", "", 1)
	writeFile(t, other, "frontend/src/Layout.tsx", mine)
	res, err = MakeAuth(other, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Shared || res.Menu {
		t.Errorf("result %+v", res)
	}
	if read(t, filepath.Join(other, "app", "handlers", "pages.go")) != pages || read(t, layoutFile) != mine {
		t.Error("changed a file that isn't anetos new's")
	}
	// A mention of authUser elsewhere isn't currentUser's call, and an
	// import of AccountMenu already there isn't added twice.
	mention := newReact(t, "mention")
	pagesFile := filepath.Join(mention, "app", "handlers", "pages.go")
	writeFile(t, mention, "app/handlers/pages.go", read(t, pagesFile)+"\n// later: return authUser(c)\n")
	layoutFile = filepath.Join(mention, "frontend", "src", "Layout.tsx")
	writeFile(t, mention, "frontend/src/Layout.tsx", "import AccountMenu from './components/AccountMenu'\n"+read(t, layoutFile))
	if res, err := MakeAuth(mention, now); err != nil || !res.Shared || !res.Menu {
		t.Fatalf("mention: %+v %v", res, err)
	}
	if p := read(t, pagesFile); !strings.Contains(p, "\treturn authUser(c) // anetos make:auth") {
		t.Errorf("pages.go:\n%s", p)
	}
	if l := read(t, layoutFile); strings.Count(l, "import AccountMenu") != 1 || !strings.Contains(l, "<AccountMenu />") {
		t.Errorf("Layout.tsx:\n%s", l)
	}
	// CRLF line endings are kept.
	crlf := newReact(t, "crlf")
	for _, f := range []string{"app/handlers/pages.go", "frontend/src/Layout.tsx"} {
		writeFile(t, crlf, f, strings.ReplaceAll(read(t, filepath.Join(crlf, filepath.FromSlash(f))), "\n", "\r\n"))
	}
	if res, err := MakeAuth(crlf, now); err != nil || !res.Shared || !res.Menu {
		t.Fatalf("CRLF: %+v %v", res, err)
	}
	for _, f := range []string{"app/handlers/pages.go", "frontend/src/Layout.tsx"} {
		if got := read(t, filepath.Join(crlf, filepath.FromSlash(f))); strings.Count(got, "\n") != strings.Count(got, "\r\n") {
			t.Errorf("%s has mixed line endings", f)
		}
	}
}

// make:crud in a react project: the handlers render the pages
// (frontend/src/pages/<Plural>), and the layout's nav links to the list.
func TestMakeCrudInertia(t *testing.T) {
	dir := newReact(t, "shop")
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	res, err := MakeCrud(dir, "blog-post", []string{"title:string", "body:text:optional", "views:int", "done:bool", "due_on:date:optional"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app/models/blog_post.go", "app/handlers/blog_posts.go", "routes/blog_posts.go", "blog_posts_test.go",
		"locales/en/blog_posts.yaml", "frontend/src/pages/BlogPosts/Index.tsx", "frontend/src/pages/BlogPosts/Show.tsx",
		"frontend/src/pages/BlogPosts/Form.tsx"} {
		if !slices.Contains(res.Created, want) {
			t.Errorf("missing %s in %v", want, res.Created)
		}
	}
	if slices.Contains(res.Created, "views/blog_posts.templ") {
		t.Error("a react project got a templ view")
	}
	if !res.Routed || !res.Linked {
		t.Errorf("result %+v", res)
	}
	classlessPages(t, dir)
	for file, wants := range map[string][]string{
		"app/handlers/blog_posts.go":            {`blogPostIndexPage = page[BlogPostsIndexProps]("BlogPosts/Index")`, "db.MapPage(page, blogPostItem)"},
		"frontend/src/Layout.tsx":               {`<NavLink route="home" label={t('nav.home')} />` + "\n" + `            <NavLink route="blog-posts.index" label={t('blog_posts.title')} />`},
		"frontend/src/pages/BlogPosts/Form.tsx": {"transform=", "<Checkbox", "<Textarea"},
		"frontend/src/pages/BlogPosts/Show.tsx": {"<Multiline", "<PostButton", `method="delete"`},
		"blog_posts_test.go":                    {`AssertInertia("BlogPosts/Index")`},
	} {
		got := read(t, filepath.Join(dir, filepath.FromSlash(file)))
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %q:\n%s", file, w, got)
			}
		}
	}
	// A second model's link goes after the first's; a model without text
	// or booleans imports neither's components.
	if _, err := MakeCrud(dir, "Tag", []string{"name:string"}, now); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "frontend", "src", "Layout.tsx")); !strings.Contains(got, "label={t('blog_posts.title')} />\n            <NavLink route=\"tags.index\" label={t('tags.title')} />\n") {
		t.Errorf("Layout.tsx:\n%s", got)
	}
	for _, f := range []string{"Index", "Show", "Form"} {
		got := read(t, filepath.Join(dir, "frontend", "src", "pages", "Tags", f+".tsx"))
		for _, no := range []string{"Badge", "Multiline", "Textarea", "Checkbox", "transform"} {
			if strings.Contains(got, no) {
				t.Errorf("Tags/%s.tsx has %s:\n%s", f, no, got)
			}
		}
	}
	// A layout without anetos new's nav: no link.
	other := newReact(t, "other")
	layout := filepath.Join(other, "frontend", "src", "Layout.tsx")
	mine := strings.Replace(read(t, layout), `<NavLink route="home" label={t('nav.home')} />`, `<a href="/">Home</a>`, 1)
	writeFile(t, other, "frontend/src/Layout.tsx", mine)
	if res, err := MakeCrud(other, "Tag", []string{"name:string"}, now); err != nil || res.Linked || read(t, layout) != mine {
		t.Errorf("unlinked: %+v %v", res, err)
	}
}

// make:admin in a react project: the impersonation banner goes in the
// root view, which every page's first visit renders.
func TestMakeAdminInertia(t *testing.T) {
	dir := newReact(t, "shop")
	if _, err := MakeAuth(dir, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	res, err := MakeAdmin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Banner || !res.Wired {
		t.Errorf("result %+v", res)
	}
	if v := read(t, filepath.Join(dir, "views", "app.templ")); !strings.Contains(v, "\t\t<body>\n\t\t\t@admin.Banner()") || !strings.Contains(v, "\"anetos.dev/anetos/admin\"") {
		t.Errorf("views/app.templ:\n%s", v)
	}
}

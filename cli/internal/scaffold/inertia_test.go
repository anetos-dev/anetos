// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// inertiaCase is what a test expects of an Inertia stack's project.
type inertiaCase struct {
	stack, name, ext, entry string
	ui                      string   // the components' file, or folder's index
	main                    string   // vite.New's call in main.go
	pkg                     []string // in frontend/package.json
	marker                  string   // the layout's line for the account's links
	accountImport           string   // the line importing AccountMenu
	uiImport                string   // the line importing Flash, which it goes before
	navLink                 string   // the layout's line linking to (route, key): fmt with route, key
	navIndent               string   // its indentation
}

var inertiaCases = []inertiaCase{
	{
		stack: "react", name: "React", ext: ".tsx", entry: "src/app.tsx", ui: "src/components/ui.tsx",
		main: `vite.New(app, public.Files, vite.WithReactRefresh())`,
		pkg: []string{`"@inertiajs/react": "^` + InertiaVersion + `"`, `"react": "^` + ReactVersion + `"`,
			`"@types/react": "^` + ReactTypesVersion + `"`, `"@vitejs/plugin-react": "^` + ReactPluginVersion + `"`,
			`"vite": "^` + ViteVersion + `"`, `"typescript": "^` + TypeScriptVersion + `"`, `"build": "tsc --noEmit && vite build"`},
		marker:        "{/* The account's links (go tool anetos make:auth adds them). */}",
		accountImport: "import AccountMenu from './components/AccountMenu'", uiImport: "import { Flash } from './components/ui'",
		navLink: `<NavLink route="%s" label={t('%s')} />`, navIndent: "            ",
	},
	{
		stack: "vue", name: "Vue", ext: ".vue", entry: "src/app.ts", ui: "src/components/ui/index.ts",
		main: `vite.New(app, public.Files)`,
		pkg: []string{`"@inertiajs/vue3": "^` + InertiaVersion + `"`, `"vue": "^` + VueVersion + `"`,
			`"@vitejs/plugin-vue": "^` + VuePluginVersion + `"`, `"vue-tsc": "^` + VueTscVersion + `"`,
			`"vite": "^` + ViteVersion + `"`, `"typescript": "~` + TypeScript6Version + `"`, `"build": "vue-tsc --noEmit && vite build"`},
		marker:        "<!-- The account's links (go tool anetos make:auth adds them). -->",
		accountImport: "import AccountMenu from './components/AccountMenu.vue'", uiImport: "import { Flash } from './components/ui'",
		navLink: `<NavLink route="%s" :label="t('%s')" />`, navIndent: "        ",
	},
	{
		stack: "svelte", name: "Svelte", ext: ".svelte", entry: "src/app.ts", ui: "src/components/ui/index.ts",
		main: `vite.New(app, public.Files)`,
		pkg: []string{`"@inertiajs/svelte": "^` + InertiaVersion + `"`, `"svelte": "^` + SvelteVersion + `"`,
			`"@sveltejs/vite-plugin-svelte": "^` + SveltePluginVersion + `"`, `"svelte-check": "^` + SvelteCheckVersion + `"`,
			`"vite": "^` + ViteVersion + `"`, `"typescript": "~` + TypeScript6Version + `"`, `"build": "svelte-check --tsconfig ./tsconfig.json && vite build"`},
		marker:        "<!-- The account's links (go tool anetos make:auth adds them). -->",
		accountImport: "  import AccountMenu from './components/AccountMenu.svelte'", uiImport: "  import { Flash } from './components/ui'", // as indented in <script>
		navLink: `<NavLink route="%s" label={t('%s')} />`, navIndent: "      ",
	},
}

// eachStack runs test for each Inertia stack, in a subtest.
func eachStack(t *testing.T, test func(t *testing.T, c inertiaCase)) {
	for _, c := range inertiaCases {
		t.Run(c.stack, func(t *testing.T) { test(t, c) })
	}
}

// newProject creates a project of the stack.
func (c inertiaCase) newProject(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if _, err := Create(Project{Dir: dir, Module: "example.com/" + name, DB: "sqlite", Replace: "../../..", Stack: c.stack, Agents: Agents}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// layout is the stack's layout, relative to the project.
func (c inertiaCase) layout() string { return "frontend/src/Layout" + c.ext }

// nav is the layout's line linking to route.
func (c inertiaCase) nav(route, key string) string { return fmt.Sprintf(c.navLink, route, key) }

// classAttr is a class in markup: React's className, Vue's class.
var classAttr = regexp.MustCompile(`\bclass(Name)?=`)

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
		} else if classAttr.Match(b) {
			t.Errorf("%s has a class", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --stack=react, vue and svelte: the web stack's Go side, with pages that are
// the framework's components, which Inertia's client renders (design
// D370–D376).
func TestCreateInertia(t *testing.T) {
	eachStack(t, func(t *testing.T, c inertiaCase) {
		dir := c.newProject(t, "shop")
		for _, want := range []string{"main.go", "main_test.go", "routes/web.go", "app/handlers/pages.go", "app/handlers/home.go",
			"views/app.templ", "public/public.go", "public/favicon.svg", "locales/en/app.yaml", "frontend/package.json",
			"frontend/vite.config.ts", "frontend/tsconfig.json", "frontend/go.mod", "frontend/" + c.entry, c.layout(),
			"frontend/src/i18n.ts", "frontend/src/theme.css", "frontend/src/app.css", "frontend/" + c.ui,
			"frontend/src/pages/Home" + c.ext, "frontend/src/pages/Error" + c.ext, "AGENTS.md", ".claude/skills/anetos-page/SKILL.md"} {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
				t.Errorf("missing %s", want)
			}
		}
		for _, no := range []string{"views/layout.templ", "views/ui", "views/home.templ", "public/static/app.css", KitRecordFile} {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(no))); err == nil {
				t.Errorf("a %s project has %s", c.stack, no)
			}
		}
		classlessPages(t, dir)
		if InertiaFramework(dir) != c.stack || IsAPI(dir) || ServesSPA(dir) {
			t.Errorf("InertiaFramework %q, IsAPI %v, ServesSPA %v", InertiaFramework(dir), IsAPI(dir), ServesSPA(dir))
		}
		if LayoutFile(dir) != "views/app.templ" || InertiaLayout(c.stack) != c.layout() {
			t.Errorf("LayoutFile = %s, InertiaLayout = %s", LayoutFile(dir), InertiaLayout(c.stack))
		}
		main := read(t, filepath.Join(dir, "main.go"))
		for _, want := range []string{c.main + "; err != nil", "inertia.WithShared(handlers.SharedProps)",
			"inertia.WithPages(handlers.Pages...)", `typescript.Config{Inertia: in, Routes: true, ExcludeRoutes: []string{"admin."}}`, "routes.Register(srv.Router(), sessions)"} {
			if !strings.Contains(main, want) {
				t.Errorf("main.go lacks %q:\n%s", want, main)
			}
		}
		if v := read(t, filepath.Join(dir, "views", "app.templ")); !strings.Contains(v, `@vite.Tags("`+c.entry+`")`) {
			t.Errorf("views/app.templ:\n%s", v)
		}
		pkg := read(t, filepath.Join(dir, "frontend", "package.json"))
		for _, want := range c.pkg {
			if !strings.Contains(pkg, want) {
				t.Errorf("package.json lacks %s:\n%s", want, pkg)
			}
		}
		if theme := read(t, filepath.Join(dir, "frontend", "src", "theme.css")); !strings.Contains(theme, "--surface-2:") || !strings.Contains(theme, c.entry) || strings.Contains(theme, "views/ui") {
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
		if l := read(t, filepath.Join(dir, filepath.FromSlash(c.layout()))); !strings.Contains(l, c.marker) || !strings.Contains(l, c.navIndent+c.nav("home", "nav.home")+"\n") {
			t.Errorf("%s:\n%s", c.layout(), l)
		}
		for _, f := range []string{"app/handlers/home.go", "app/handlers/pages.go", "locales/en/app.yaml"} {
			if got := read(t, filepath.Join(dir, filepath.FromSlash(f))); !strings.Contains(got, c.ext) || strings.Contains(c.ext, ".vue") && strings.Contains(got, ".tsx") {
				t.Errorf("%s doesn't name the stack's files:\n%s", f, got)
			}
		}
		// The agent files are the ones agent:update would write: none stale.
		if stale := mustStale(t, dir); len(stale) != 0 {
			t.Errorf("stale agent files: %v", stale)
		}
		if a := read(t, filepath.Join(dir, "AGENTS.md")); !strings.Contains(a, c.name+" components") || strings.Contains(a, "views/ui") {
			t.Errorf("AGENTS.md:\n%s", a)
		}

		// Refused: another CSS framework (until v0.7), and --vite, which the
		// stack has.
		for _, p := range []Project{
			{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: c.stack, CSS: "pico"},
			{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: c.stack, CSS: "none"},
			{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: c.stack, Vite: "ts"},
		} {
			if _, err := Create(p); err == nil {
				t.Errorf("%+v accepted", p)
			}
		}
		if _, err := Create(Project{Dir: filepath.Join(t.TempDir(), "a"), DB: "sqlite", Stack: c.stack, CSS: "anetos"}); err != nil {
			t.Errorf("--css=anetos: %v", err)
		}
	})
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
		`{"dependencies": {"@inertiajs/vue3": "^3.9.1", "vue": "^3.5.44"}}`:    "vue",
		`{"dependencies": {"react": "^19.3.1"}}`:                               "",
		`not JSON`:                                                             "",
	} {
		writeFile(t, root, "frontend/package.json", pkg)
		if got := InertiaFramework(root); got != want {
			t.Errorf("%s: %q, want %q", pkg, got, want)
		}
	}
}

// make:auth in a react, vue or svelte project: the web stack's Go, with
// Inertia's renders, and the framework's pages; every page gets the
// user, and the layout's header the account's links.
func TestMakeAuthInertia(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	eachStack(t, func(t *testing.T, c inertiaCase) {
		dir := c.newProject(t, "shop")
		res, err := MakeAuth(dir, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"app/models/user.go", "app/handlers/auth.go", "app/handlers/settings.go", "views/auth_mail.templ",
			"routes/auth.go", "auth.go", "auth_test.go", "frontend/src/components/AccountMenu" + c.ext, "frontend/src/pages/Auth/Login" + c.ext,
			"frontend/src/pages/Auth/Settings" + c.ext, "frontend/src/pages/Auth/TwoFactor" + c.ext} {
			if !slices.Contains(res.Created, want) {
				t.Errorf("missing %s in %v", want, res.Created)
			}
		}
		if slices.Contains(res.Created, "views/auth.templ") {
			t.Error("an Inertia project got templ pages")
		}
		classlessPages(t, dir)
		if !res.Wired || !res.Shared || !res.Menu || res.API {
			t.Errorf("result %+v", res)
		}
		if p := read(t, filepath.Join(dir, "app", "handlers", "pages.go")); !strings.Contains(p, "\treturn authUser(c) // anetos make:auth") {
			t.Errorf("pages.go:\n%s", p)
		}
		layout := read(t, filepath.Join(dir, filepath.FromSlash(c.layout())))
		if !strings.Contains(layout, c.accountImport+"\n"+c.uiImport) ||
			!strings.Contains(layout, "<AccountMenu />") || strings.Contains(layout, "make:auth adds them") {
			t.Errorf("%s:\n%s", c.layout(), layout)
		}
		h := alignment.ReplaceAllString(read(t, filepath.Join(dir, "app", "handlers", "auth.go")), " ")
		if !strings.Contains(h, `loginPage = page[SocialProps]("Auth/Login")`) || strings.Contains(h, "views.") {
			t.Errorf("auth.go:\n%s", h)
		}
		if r := read(t, filepath.Join(dir, "routes", "auth.go")); !strings.Contains(r, "inertia.Middleware") {
			t.Errorf("routes/auth.go:\n%s", r)
		}
		if st := read(t, filepath.Join(dir, "app", "handlers", "settings.go")); !strings.Contains(st, "frontend/src/pages/Auth/Settings"+c.ext) {
			t.Errorf("settings.go:\n%s", st)
		}

		// A project whose currentUser and layout aren't anetos new's: neither
		// is changed, and the result says so.
		other := c.newProject(t, "other")
		pages := strings.Replace(read(t, filepath.Join(other, "app", "handlers", "pages.go")), "return nil, nil", "return nil, nil // mine", 1)
		writeFile(t, other, "app/handlers/pages.go", pages)
		layoutFile := filepath.Join(other, filepath.FromSlash(c.layout()))
		mine := strings.Replace(read(t, layoutFile), c.marker, "", 1)
		writeFile(t, other, c.layout(), mine)
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
		mention := c.newProject(t, "mention")
		pagesFile := filepath.Join(mention, "app", "handlers", "pages.go")
		writeFile(t, mention, "app/handlers/pages.go", read(t, pagesFile)+"\n// later: return authUser(c)\n")
		layoutFile = filepath.Join(mention, filepath.FromSlash(c.layout()))
		writeFile(t, mention, c.layout(), strings.Replace(read(t, layoutFile), c.uiImport, c.accountImport+"\n"+c.uiImport, 1))
		if res, err := MakeAuth(mention, now); err != nil || !res.Shared || !res.Menu {
			t.Fatalf("mention: %+v %v", res, err)
		}
		if p := read(t, pagesFile); !strings.Contains(p, "\treturn authUser(c) // anetos make:auth") {
			t.Errorf("pages.go:\n%s", p)
		}
		if l := read(t, layoutFile); strings.Count(l, "import AccountMenu") != 1 || !strings.Contains(l, "<AccountMenu />") {
			t.Errorf("%s:\n%s", c.layout(), l)
		}
		// Imports indented otherwise (tabs, in a Svelte layout's <script>):
		// AccountMenu's gets the Flash import's indentation.
		tabs := c.newProject(t, "tabs")
		layoutFile = filepath.Join(tabs, filepath.FromSlash(c.layout()))
		writeFile(t, tabs, c.layout(), strings.ReplaceAll(read(t, layoutFile), "\n  import ", "\n\timport "))
		if res, err := MakeAuth(tabs, now); err != nil || !res.Menu {
			t.Fatalf("tabs: %+v %v", res, err)
		}
		indent := "" // React's and Vue's imports aren't indented
		if strings.HasPrefix(c.uiImport, " ") {
			indent = "\t"
		}
		if l := read(t, layoutFile); !strings.Contains(l, "\n"+indent+strings.TrimSpace(c.accountImport)+"\n"+indent+strings.TrimSpace(c.uiImport)) || strings.Count(l, "import AccountMenu") != 1 {
			t.Errorf("tabs: %s:\n%s", c.layout(), l)
		}
		// CRLF line endings are kept.
		crlf := c.newProject(t, "crlf")
		for _, f := range []string{"app/handlers/pages.go", c.layout()} {
			writeFile(t, crlf, f, strings.ReplaceAll(read(t, filepath.Join(crlf, filepath.FromSlash(f))), "\n", "\r\n"))
		}
		if res, err := MakeAuth(crlf, now); err != nil || !res.Shared || !res.Menu {
			t.Fatalf("CRLF: %+v %v", res, err)
		}
		for _, f := range []string{"app/handlers/pages.go", c.layout()} {
			if got := read(t, filepath.Join(crlf, filepath.FromSlash(f))); strings.Count(got, "\n") != strings.Count(got, "\r\n") {
				t.Errorf("%s has mixed line endings", f)
			}
		}
	})
}

// make:crud in a react, vue or svelte project: the handlers render the pages
// (frontend/src/pages/<Plural>), and the layout's nav links to the list.
func TestMakeCrudInertia(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	eachStack(t, func(t *testing.T, c inertiaCase) {
		dir := c.newProject(t, "shop")
		res, err := MakeCrud(dir, "blog-post", []string{"title:string", "body:text:optional", "views:int", "done:bool", "due_on:date:optional"}, now)
		if err != nil {
			t.Fatal(err)
		}
		pages := "frontend/src/pages/BlogPosts/"
		for _, want := range []string{"app/models/blog_post.go", "app/handlers/blog_posts.go", "routes/blog_posts.go", "blog_posts_test.go",
			"locales/en/blog_posts.yaml", pages + "Index" + c.ext, pages + "Show" + c.ext, pages + "Form" + c.ext} {
			if !slices.Contains(res.Created, want) {
				t.Errorf("missing %s in %v", want, res.Created)
			}
		}
		if slices.Contains(res.Created, "views/blog_posts.templ") {
			t.Error("an Inertia project got a templ view")
		}
		if !res.Routed || !res.Linked {
			t.Errorf("result %+v", res)
		}
		classlessPages(t, dir)
		for file, wants := range map[string][]string{
			"app/handlers/blog_posts.go": {`blogPostIndexPage = page[BlogPostsIndexProps]("BlogPosts/Index")`, "db.MapPage(page, blogPostItem)"},
			c.layout():                   {c.nav("home", "nav.home") + "\n" + c.navIndent + c.nav("blog-posts.index", "blog_posts.title") + "\n"},
			pages + "Form" + c.ext:       {"transform=", "<Checkbox", "<Textarea"},
			pages + "Show" + c.ext:       {"<Multiline", "<PostButton", `method="delete"`},
			"blog_posts_test.go":         {`AssertInertia("BlogPosts/Index")`},
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
		if got := read(t, filepath.Join(dir, filepath.FromSlash(c.layout()))); !strings.Contains(got, c.nav("blog-posts.index", "blog_posts.title")+"\n"+c.navIndent+c.nav("tags.index", "tags.title")+"\n") {
			t.Errorf("%s:\n%s", c.layout(), got)
		}
		for _, f := range []string{"Index", "Show", "Form"} {
			got := read(t, filepath.Join(dir, "frontend", "src", "pages", "Tags", f+c.ext))
			for _, no := range []string{"Badge", "Multiline", "Textarea", "Checkbox", "transform"} {
				if strings.Contains(got, no) {
					t.Errorf("Tags/%s%s has %s:\n%s", f, c.ext, no, got)
				}
			}
		}
		if got := read(t, filepath.Join(dir, "frontend", "src", "pages", "Tags", "Index"+c.ext)); strings.Contains(got, "useFormat") {
			t.Errorf("Tags/Index%s formats nothing, but has useFormat:\n%s", c.ext, got)
		}
		// A layout without anetos new's nav: no link.
		other := c.newProject(t, "other")
		layout := filepath.Join(other, filepath.FromSlash(c.layout()))
		mine := strings.Replace(read(t, layout), c.nav("home", "nav.home"), `<a href="/">Home</a>`, 1)
		writeFile(t, other, c.layout(), mine)
		if res, err := MakeCrud(other, "Tag", []string{"name:string"}, now); err != nil || res.Linked || read(t, layout) != mine {
			t.Errorf("unlinked: %+v %v", res, err)
		}
	})
}

// make:admin in a react, vue or svelte project: the impersonation banner goes in
// the root view, which every page's first visit renders.
func TestMakeAdminInertia(t *testing.T) {
	eachStack(t, func(t *testing.T, c inertiaCase) {
		dir := c.newProject(t, "shop")
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
	})
}

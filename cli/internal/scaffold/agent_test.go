// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/cli/internal/docpages"
	"anetos.dev/anetos/docs"
)

func rels(files []AgentFile) []string {
	var r []string
	for _, f := range files {
		r = append(r, f.Rel)
	}
	return r
}

func TestRenderAgentFiles(t *testing.T) {
	web := AgentProject{Name: "blog", Module: "example.com/shop", Stack: "web", DB: "postgres", Kit: "pico", HasUI: true}
	files, err := RenderAgentFiles(web, Agents)
	if err != nil {
		t.Fatal(err)
	}
	got := rels(files)
	for _, want := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md", ".agents/skills/anetos-page/SKILL.md", ".claude/skills/anetos-page/SKILL.md"} {
		if !slices.Contains(got, want) {
			t.Errorf("no %s in %v", want, got)
		}
	}
	if slices.Contains(got, ".agents/skills/anetos-api/SKILL.md") {
		t.Error("a web project got the API skill")
	}
	agents := string(files[0].Content)
	for _, want := range []string{agentsBegin, agentsEnd, "Pico CSS", "PostgreSQL", "`bin/shop`", "make:admin", "go tool anetos check", "@view.CSRFField"} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md lacks %q", want)
		}
	}
	for _, f := range files {
		if strings.Contains(string(f.Content), "<no value>") || strings.Contains(string(f.Content), "[[") {
			t.Errorf("%s has a template leftover", f.Rel)
		}
		if strings.HasSuffix(f.Rel, "SKILL.md") {
			name := filepath.Base(filepath.Dir(f.Rel))
			if !strings.HasPrefix(string(f.Content), "---\nname: "+name+"\ndescription: ") {
				t.Errorf("%s's front matter:\n%.120s", f.Rel, f.Content)
			}
			_, after, _ := strings.Cut(string(f.Content), "description: ")
			desc, _, _ := strings.Cut(after, "\n")
			if len(desc) > 1024 || strings.ContainsAny(desc, ":") && !strings.HasPrefix(desc, `"`) && strings.Contains(desc, ": ") {
				t.Errorf("%s's description isn't plain YAML under 1024 characters: %q", f.Rel, desc)
			}
		}
	}

	api := AgentProject{Name: "shop", Stack: "api", DB: "sqlite"}
	files, err = RenderAgentFiles(api, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	got = rels(files)
	if slices.Contains(got, "CLAUDE.md") || slices.Contains(got, ".claude/skills/anetos-api/SKILL.md") ||
		!slices.Contains(got, ".agents/skills/anetos-api/SKILL.md") || slices.Contains(got, ".agents/skills/anetos-page/SKILL.md") {
		t.Errorf("codex, api: %v", got)
	}
	if a := string(files[0].Content); !strings.Contains(a, "/api/v1") || strings.Contains(a, "views/ui") || strings.Contains(a, "make:admin") {
		t.Errorf("the API's AGENTS.md:\n%s", a)
	}
	if _, err := RenderAgentFiles(api, []string{"vim"}); err == nil {
		t.Error("an unknown agent: no error")
	}

	// An Inertia stack's: the pages are the framework's components.
	react := AgentProject{Name: "shop", Stack: "react", DB: "sqlite", Kit: "anetos", Frontend: true, Framework: "react"}
	files, err = RenderAgentFiles(react, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	got = rels(files)
	if !slices.Contains(got, ".claude/skills/anetos-page/SKILL.md") || slices.Contains(got, ".claude/skills/anetos-api/SKILL.md") {
		t.Errorf("react: %v", got)
	}
	for _, f := range files {
		c := string(f.Content)
		if strings.Contains(c, "[[") || strings.Contains(c, "<no value>") || strings.Contains(c, "views/ui") ||
			strings.Contains(c, "CSRFField") || strings.Contains(c, "@vite.Tags") || strings.Contains(c, "AssertSee") {
			t.Errorf("react's %s:\n%s", f.Rel, c)
		}
	}
	a := string(files[0].Content)
	for _, want := range []string{"React components", "`frontend/src/theme.css`", "src/components/ui.tsx", "go run . types",
		"views/app.templ", `page[HomeProps]("Home")`, `AssertInertia("Home")`, "make:admin"} {
		if !strings.Contains(a, want) {
			t.Errorf("react's AGENTS.md lacks %q:\n%s", want, a)
		}
	}
	for _, f := range files {
		switch f.Rel {
		case ".claude/skills/anetos-page/SKILL.md":
			if c := string(f.Content); !strings.Contains(c, "frontend/src/pages/Reports/Show.tsx") || !strings.Contains(c, "transform") {
				t.Errorf("react's page skill:\n%s", c)
			}
		case ".claude/skills/anetos-crud/SKILL.md":
			if c := string(f.Content); !strings.Contains(c, "frontend/src/pages/<Models>/") {
				t.Errorf("react's crud skill:\n%s", c)
			}
		}
	}

	// The svelte stack's.
	svelte := AgentProject{Name: "shop", Stack: "svelte", DB: "sqlite", Kit: "anetos", Frontend: true, Framework: "svelte"}
	files, err = RenderAgentFiles(svelte, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		c := string(f.Content)
		if strings.Contains(c, "[[") || strings.Contains(c, "<no value>") || strings.Contains(c, ".tsx") || strings.Contains(c, ".vue") ||
			strings.Contains(c, "dangerouslySetInnerHTML") || strings.Contains(c, "defineProps") {
			t.Errorf("svelte's %s:\n%s", f.Rel, c)
		}
		for rel, wants := range map[string][]string{
			"AGENTS.md":                               {"Svelte components", "src/components/ui/", "Layout.svelte"},
			".claude/skills/anetos-page/SKILL.md":     {"$props()", "src/routes.ts", "Show.svelte"},
			".claude/skills/anetos-security/SKILL.md": {"`{@html}`"},
		} {
			for _, w := range wants {
				if f.Rel == rel && !strings.Contains(c, w) {
					t.Errorf("svelte's %s lacks %q:\n%s", rel, w, c)
				}
			}
		}
	}

	// The vue stack's: its components, its files and its syntax.
	vue := AgentProject{Name: "shop", Stack: "vue", DB: "sqlite", Kit: "anetos", Frontend: true, Framework: "vue"}
	files, err = RenderAgentFiles(vue, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		c := string(f.Content)
		if strings.Contains(c, "[[") || strings.Contains(c, "<no value>") || strings.Contains(c, ".tsx") || strings.Contains(c, "dangerouslySetInnerHTML") {
			t.Errorf("vue's %s:\n%s", f.Rel, c)
		}
		for rel, wants := range map[string][]string{
			"AGENTS.md":                               {"Vue components", "src/components/ui/", "Layout.vue"},
			".claude/skills/anetos-page/SKILL.md":     {"defineProps<ReportProps>()", `:action="route('reports.create')"`, "Show.vue"},
			".claude/skills/anetos-security/SKILL.md": {"`v-html`", "{{ value }}"},
		} {
			for _, w := range wants {
				if f.Rel == rel && !strings.Contains(c, w) {
					t.Errorf("vue's %s lacks %q:\n%s", rel, w, c)
				}
			}
		}
	}
}

// TestAgentDocsCommands checks that each `go tool anetos docs <page>` the
// agent files mention prints one page.
func TestAgentDocsCommands(t *testing.T) {
	pages, err := docpages.Load(docs.Site)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("go tool anetos docs ([a-z0-9][a-z0-9/_-]*)")
	seen := map[string]bool{}
	for _, p := range []AgentProject{{Name: "a", Stack: "web", DB: "sqlite", Kit: "anetos"}, {Name: "b", Stack: "api", DB: "mysql"},
		{Name: "c", Stack: "react", DB: "sqlite", Kit: "anetos", Frontend: true, Framework: "react"},
		{Name: "d", Stack: "vue", DB: "sqlite", Kit: "anetos", Frontend: true, Framework: "vue"},
		{Name: "e", Stack: "svelte", DB: "sqlite", Kit: "anetos", Frontend: true, Framework: "svelte"}} {
		files, err := RenderAgentFiles(p, Agents)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			for _, m := range re.FindAllStringSubmatch(string(f.Content), -1) {
				if seen[m[1]] {
					continue
				}
				seen[m[1]] = true
				if found := docpages.Find(pages, m[1]); len(found) != 1 {
					t.Errorf("%s: go tool anetos docs %s finds %d pages", f.Rel, m[1], len(found))
				}
			}
		}
	}
	if len(seen) < 15 {
		t.Errorf("only %d docs commands found", len(seen))
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallAgentFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "notes")
	writeFile(t, root, "go.mod", "module example.com/notes\n\ngo 1.26\n\nrequire (\n\tanetos.dev/anetos v0.5.0\n\tanetos.dev/anetos/drivers/mysql v0.5.0\n)\n")
	writeFile(t, root, "routes/api.go", "package routes\n")
	writeFile(t, root, "CLAUDE.md", "# Notes\n\nOur own rules.\n")
	writeFile(t, root, "AGENTS.md", "Our agents' notes.\n")
	writeFile(t, root, ".agents/skills/our-skill/SKILL.md", "---\nname: our-skill\ndescription: ours\n---\n")

	if InstalledAgents(root) != nil || mustStale(t, root) != nil {
		t.Error("agent files found before any were written")
	}
	p, err := ReadAgentProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if p != (AgentProject{Name: "notes", Module: "example.com/notes", Stack: "api", DB: "mysql"}) { // the binary's name: no deploy unit
		t.Errorf("ReadAgentProject = %+v", p)
	}
	changed, err := InstallAgentFiles(root, p, []string{"claude", "cursor"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changed, "AGENTS.md") || !slices.Contains(changed, "CLAUDE.md") || slices.Contains(changed, "GEMINI.md") {
		t.Errorf("changed %v", changed)
	}
	agents := readFile(t, root, "AGENTS.md")
	if !strings.HasPrefix(agents, agentsBegin) || !strings.HasSuffix(agents, agentsEnd+"\n\nOur agents' notes.\n") {
		t.Errorf("AGENTS.md:\n%s", agents)
	}
	if got := readFile(t, root, "CLAUDE.md"); got != "@AGENTS.md\n\n# Notes\n\nOur own rules.\n" {
		t.Errorf("CLAUDE.md = %q", got)
	}
	if got := readFile(t, root, ".agents/skills/our-skill/SKILL.md"); !strings.Contains(got, "ours") {
		t.Error("the developer's skill changed")
	}
	if got := InstalledAgents(root); !slices.Equal(got, []string{"claude", "cursor"}) { // cursor: its .cursor/mcp.json
		t.Errorf("InstalledAgents = %v", got)
	}
	if stale := mustStale(t, root); len(stale) != 0 {
		t.Errorf("stale right after installing: %v", stale)
	}
	// Twice: nothing changes.
	if changed, err := InstallAgentFiles(root, p, []string{"claude", "cursor"}, false); err != nil || len(changed) != 0 {
		t.Errorf("second install: %v %v", changed, err)
	}
	// Edits inside the markers, and a missing skill, are stale; the
	// developer's text outside them isn't.
	agents = strings.Replace(agents, "Our agents' notes.", "Our agents' notes, longer.", 1)
	writeFile(t, root, "AGENTS.md", agents)
	if stale := mustStale(t, root); len(stale) != 0 {
		t.Errorf("an edit outside the markers is stale: %v", stale)
	}
	writeFile(t, root, "AGENTS.md", strings.Replace(agents, "## Commands", "## Our commands", 1))
	if err := os.Remove(filepath.Join(root, ".claude", "skills", "anetos-test", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if stale := mustStale(t, root); !slices.Equal(stale, []string{"AGENTS.md", ".claude/skills/anetos-test/SKILL.md"}) {
		t.Errorf("stale = %v", stale)
	}
	if _, err := InstallAgentFiles(root, p, InstalledAgents(root), false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, root, "AGENTS.md"); !strings.Contains(got, "## Commands") || !strings.HasSuffix(got, "Our agents' notes, longer.\n") {
		t.Errorf("AGENTS.md after the update:\n%s", got)
	}
	if stale := mustStale(t, root); len(stale) != 0 {
		t.Errorf("stale after the update: %v", stale)
	}
}

func mustStale(t *testing.T, root string) []string {
	t.Helper()
	stale, err := StaleAgentFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return stale
}

func TestCreateAgentFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "blog")
	files, err := Create(Project{Dir: dir, DB: "sqlite", Agents: Agents})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files, "AGENTS.md") || !slices.Contains(files, ".claude/skills/anetos-crud/SKILL.md") {
		t.Errorf("files: %v", files)
	}
	if stale := mustStale(t, dir); len(stale) != 0 {
		t.Errorf("a new project's agent files are stale: %v", stale)
	}
	dir = filepath.Join(t.TempDir(), "plain")
	files, err = Create(Project{Dir: dir, DB: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(files, "AGENTS.md") {
		t.Error("agent files without Agents")
	}
}

func TestAgentFilesEdgeCases(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blog")
	if _, err := Create(Project{Dir: root, DB: "sqlite", Module: "example.com/blog", Agents: Agents}); err != nil {
		t.Fatal(err)
	}
	// Another directory name (a clone, a worktree): the name comes from
	// the deploy unit, so nothing is stale.
	moved := filepath.Join(t.TempDir(), "blog-worktree-7f3a")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	root = moved
	if stale := mustStale(t, root); len(stale) != 0 {
		t.Errorf("stale in a renamed directory: %v", stale)
	}
	if p, _ := ReadAgentProject(root); p.Name != "blog" || !p.HasUI || p.Kit != "anetos" {
		t.Errorf("ReadAgentProject = %+v", p)
	}
	// CRLF line endings (git's autocrlf) aren't stale, and an update keeps them.
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", ".claude/skills/anetos-crud/SKILL.md"} {
		b := readFile(t, root, rel)
		writeFile(t, root, rel, strings.ReplaceAll(b, "\n", "\r\n"))
	}
	if stale := mustStale(t, root); len(stale) != 0 {
		t.Errorf("stale with CRLF: %v", stale)
	}
	p, err := ReadAgentProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := InstallAgentFiles(root, p, InstalledAgents(root), false); err != nil || len(changed) != 0 {
		t.Errorf("update with CRLF: %v %v", changed, err)
	}
	agents := readFile(t, root, "AGENTS.md")
	writeFile(t, root, "AGENTS.md", strings.Replace(agents, "## Commands", "## Our commands", 1)+"Ours.\r\n")
	if changed, err := InstallAgentFiles(root, p, InstalledAgents(root), false); err != nil || !slices.Equal(changed, []string{"AGENTS.md"}) {
		t.Fatalf("update of an edited CRLF AGENTS.md: %v %v", changed, err)
	}
	if got := readFile(t, root, "AGENTS.md"); strings.Count(got, "\n") != strings.Count(got, "\r\n") || !strings.HasSuffix(got, agentsEnd+"\r\nOurs.\r\n") {
		t.Errorf("mixed line endings or lost text:\n%q", got[len(got)-80:])
	}
	// The end marker mentioned above the block, and a missing end marker.
	writeFile(t, root, "AGENTS.md", "Note: "+agentsEnd+" ends it.\n\n"+strings.ReplaceAll(readFile(t, root, "AGENTS.md"), "\r\n", "\n"))
	if changed, err := InstallAgentFiles(root, p, InstalledAgents(root), false); err != nil || len(changed) != 0 || strings.Count(readFile(t, root, "AGENTS.md"), beginPrefix) != 1 {
		t.Errorf("an end marker above the block: %v %v", changed, err)
	}
	writeFile(t, root, "AGENTS.md", agentsBegin+"\nhalf\n")
	if _, err := InstallAgentFiles(root, p, InstalledAgents(root), false); err == nil || !strings.Contains(err.Error(), "without") {
		t.Errorf("a begin marker without an end: %v", err)
	}
	if _, err := StaleAgentFiles(root); err == nil {
		t.Error("StaleAgentFiles: no error for a begin marker without an end")
	}
	writeFile(t, root, "AGENTS.md", "mine\n")
	if _, err := InstallAgentFiles(root, p, []string{"copilot"}, true); err != nil {
		t.Fatal(err)
	}
	// Pruning: only copilot's folder is left, and only it is checked.
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "anetos-crud")); !os.IsNotExist(err) {
		t.Errorf("claude's skills weren't removed: %v", err)
	}
	// CLAUDE.md alone isn't claude, nor GEMINI.md alone gemini (its
	// .gemini/settings.json lost the server): copilot, by its
	// .vscode/mcp.json.
	if got := InstalledAgents(root); !slices.Equal(got, []string{"copilot"}) {
		t.Errorf("InstalledAgents after pruning = %v", got)
	}
	if a := readFile(t, root, "AGENTS.md"); !strings.Contains(a, "Task guides, in `.agents/skills/`, loaded") || strings.Contains(a, ".claude/skills") {
		t.Errorf("AGENTS.md names the wrong skill folders")
	}
	if stale := mustStale(t, root); len(stale) != 0 {
		t.Errorf("stale after pruning: %v", stale)
	}
}

// TestAgentFilesOldProject: a web project made before views/ui gets no
// guidance about its components.
func TestAgentFilesOldProject(t *testing.T) {
	files, err := RenderAgentFiles(AgentProject{Name: "old", Stack: "web", DB: "sqlite", Kit: "anetos"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(string(f.Content), "@ui.") || strings.Contains(string(f.Content), "views/ui") {
			t.Errorf("%s mentions views/ui in a project without it", f.Rel)
		}
	}
}

// TestAgentFilesAPIs checks that the package-level functions the agent
// files name (web.Render, queue.Dispatch…) are in the API listing, and
// that c.Render has its status.
func TestAgentFilesAPIs(t *testing.T) {
	listing, err := os.ReadFile("../../../api/anetos.txt")
	if err != nil {
		t.Fatal(err)
	}
	pkgs := map[string]string{
		"web": "web", "db": "db", "auth": "auth", "rbac": "auth/rbac", "queue": "queue", "events": "events",
		"schedule": "schedule", "mailer": "mailer", "anetostest": "anetostest", "view": "view", "i18n": "i18n",
		"cache": "cache", "storage": "storage", "anetos": "", "migrate": "db/migrate",
	}
	re := regexp.MustCompile(`\b(web|db|auth|rbac|queue|events|schedule|mailer|anetostest|view|i18n|cache|storage|anetos|migrate)\.([A-Z]\w*)[(\[]`)
	checked := 0
	for _, p := range []AgentProject{{Name: "a", Stack: "web", DB: "sqlite", Kit: "anetos", HasUI: true}, {Name: "b", Stack: "api", DB: "postgres"}} {
		files, err := RenderAgentFiles(p, Agents)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			for _, m := range re.FindAllStringSubmatch(string(f.Content), -1) {
				path := "anetos.dev/anetos"
				if pkgs[m[1]] != "" {
					path += "/" + pkgs[m[1]]
				}
				decl := regexp.MustCompile(`(?m)^pkg ` + regexp.QuoteMeta(path) + `, (func|type) ` + m[2] + `[(\[ ]`)
				if !decl.Match(listing) {
					t.Errorf("%s: %s.%s isn't in %s's API", f.Rel, m[1], m[2], path)
				}
				checked++
			}
			if regexp.MustCompile(`c\.Render\(views`).MatchString(string(f.Content)) {
				t.Errorf("%s: c.Render takes the status first", f.Rel)
			}
		}
	}
	if checked < 20 {
		t.Errorf("only %d functions checked", checked)
	}
}

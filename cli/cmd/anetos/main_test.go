// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"anetos.dev/anetos/cli/internal/testmod"
)

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCommands(t *testing.T) {
	cases := []struct {
		args []string
		code int
		want string
	}{
		{nil, 2, "Usage: anetos"},
		{[]string{"help"}, 0, "Commands:"},
		{[]string{"help", "make:crud"}, 0, "Usage: anetos make:crud"},
		{[]string{"vite:install", "-h"}, 0, "Usage: anetos vite:install"},
		{[]string{"v:i", "x"}, 2, "Usage: anetos vite:install"},
		{[]string{"new", "x", "--vite=coffee"}, 2, `invalid value "coffee" for flag -vite: want ts or js`},
		{[]string{"help", "generate"}, 0, "Usage: anetos generate"},
		{[]string{"version"}, 0, "anetos "},
		{[]string{"key:generate", "--show"}, 0, "APP_KEY=base64:"},
		{[]string{"key:generate", "-h"}, 0, "Usage: anetos key:generate"},
		{[]string{"key:generate", "x"}, 2, "Usage: anetos key:generate"},
		{[]string{"nope"}, 2, `unknown command "nope"`},
		{[]string{"gen", "-h"}, 0, "Usage: anetos generate"},
		{[]string{"g", "-h"}, 0, "Usage: anetos generate"},
		{[]string{"k:g", "--show"}, 0, "APP_KEY=base64:"},
		{[]string{"help", "m:admin"}, 0, "Usage: anetos make:admin"},
		{[]string{"d"}, 2, `"d" could be dev, docs, doctor`},
		{[]string{"docs"}, 0, "guides/queues"},
		{[]string{"docs", "guides/queues"}, 0, "# Queues"},
		{[]string{"docs", "tutorial"}, 0, "# Tutorial"},
		{[]string{"docs", "guides/nope"}, 1, "no page"},
		{[]string{"docs", "a", "b"}, 2, "one page at a time"},
		{[]string{"docs", "--search", "retry", "a", "failed", "job"}, 0, "anetos docs guides/queues"},
		{[]string{"docs", "--search", "zzqqxx"}, 1, "No page mentions"},
		{[]string{"docs", "--search"}, 2, "needs words"},
		{[]string{"docs", "--search", "-n", "0", "queue"}, 2, "1 or more"},
		{[]string{"docs", "--search", "-n=-1", "queue"}, 2, "1 or more"},
		{[]string{"docs", "testing"}, 2, "could be"},
		{[]string{"docs", "../guides/mail.md"}, 0, "# Send email"},
		{[]string{"docs", "upgrade/README.md"}, 0, "# "},
		{[]string{"check", "-h"}, 0, "Usage: anetos check"},
		{[]string{"model:list", "-h"}, 0, "Usage: anetos model:list"},
		{[]string{"security:check", "-h"}, 0, "Usage: anetos security:check"},
		{[]string{"m:l", "-h"}, 0, "Usage: anetos model:list"},
		{[]string{"check", "x"}, 2, "Usage: anetos check"},
		{[]string{"agent:install", "-h"}, 0, "Usage: anetos agent:install"},
		{[]string{"agent:install", "--agent=vim"}, 2, `unknown agent "vim"`},
		{[]string{"agent:install", "--agent="}, 2, "no agent given"},
		{[]string{"agent:update", "-h"}, 0, "Usage: anetos agent:update"},
		{[]string{"a:u", "x"}, 2, "Usage: anetos agent:update"},
		{[]string{"m:m"}, 2, `"m:m" could be make:middleware, make:migration, make:model`},
		{[]string{"generate", "-h"}, 0, "Usage: anetos generate"},
		{[]string{"lang:add", "-h"}, 2, "lang:add was renamed locale:add in v0.5, and the former name is no longer accepted"},
		{[]string{"add", "lang", "-h"}, 2, "add lang was renamed locale:add in v0.5"},
		{[]string{"make:admin:resource", "-h"}, 2, "make:admin:resource was renamed make:admin-resource in v0.5"},
		{[]string{"add", "-h"}, 0, "Usage: anetos add"},
		{[]string{"add"}, 2, "Usage: anetos add"},
		{[]string{"add", "not a module"}, 2, "malformed module path"},
		{[]string{"remove", "-h"}, 0, "Usage: anetos remove"},
		{[]string{"remove", "a", "b"}, 2, "Usage: anetos remove"},
		{[]string{"generate", "-bogus"}, 2, "flag provided but not defined"},
		{[]string{"generate", "--check", "../../internal/modelgen/internal/..."}, 0, ""},
		{[]string{"css:build", "-h"}, 0, "Usage: anetos css:build"},
		{[]string{"css:build", "x"}, 2, "Usage: anetos css:build"},
		{[]string{"css:build"}, 1, "only Tailwind CSS's stylesheet"},
		{[]string{"css:use", "-h"}, 0, "Usage: anetos css:use"},
		{[]string{"css:use", "a", "b"}, 2, "Usage: anetos css:use"},
		{[]string{"css:use", "pico"}, 1, "no views/"},
	}
	for _, c := range cases {
		code, out, errOut := runCmd(t, c.args...)
		if code != c.code || !strings.Contains(out+errOut, c.want) {
			t.Errorf("anetos %v: code %d, output %q%q", c.args, code, out, errOut)
		}
	}
}

func TestGenWritesAndChecks(t *testing.T) {
	dir := t.TempDir()
	core, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	goMod, goSum, err := testmod.Files(core)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": goMod,
		"go.sum": goSum,
		"models/post.go": "package models\n\nimport \"anetos.dev/anetos/db\"\n\n" +
			"type Post struct {\n\tdb.Model\n\tTitle string\n}\n",
	}
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	code, _, errOut := runCmd(t, "generate", "--check")
	if code != 1 || !strings.Contains(errOut, filepath.Join("models", "models_gen.go")+" is out of date") {
		t.Errorf("check before gen: %d %q", code, errOut)
	}
	code, out, errOut := runCmd(t, "generate")
	if code != 0 || out != "wrote "+filepath.Join("models", "models_gen.go")+" (Post)\n" {
		t.Errorf("gen: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := runCmd(t, "generate", "--check"); code != 0 || out+errOut != "" {
		t.Errorf("check after gen: %d %q %q", code, out, errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, "models/post.go"), []byte("package models\n\ntype Post struct{ X string `db:\"x,bad\"` }\n\nfunc (Post) TableName() string { return \"p\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "generate"); code != 1 || !strings.Contains(errOut, `unknown db tag option "bad"`) {
		t.Errorf("gen with a bad model: %d %q", code, errOut)
	}
}

// In an app that writes the frontend's TypeScript types
// (typescript.Register), generate runs its types command, --check too.
func TestGenerateTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module typesapp\n\ngo 1.26\n",
		"main.go": `package main

import (
	"fmt"
	"os"
)

// typescript stands in for web/typescript, main for an app whose setup
// calls typescript.Register.
var typescript struct{ Register func(...any) }

func main() {
	if typescript.Register != nil {
		typescript.Register()
	}
	if len(os.Args) < 2 || os.Args[1] != "types" {
		return
	}
	if len(os.Args) > 2 && os.Args[2] == "--check" {
		if b, _ := os.ReadFile("types.ts"); string(b) != "types\n" {
			fmt.Fprintln(os.Stderr, "types.ts is out of date")
			os.Exit(1)
		}
		fmt.Println("types.ts is up to date")
		return
	}
	_ = os.WriteFile("types.ts", []byte("types\n"), 0o644)
	fmt.Println("wrote types.ts")
}
`,
	}
	writeFiles(t, dir, files)
	t.Chdir(dir)
	if code, out, errOut := runCmd(t, "generate", "--check"); code != 1 || !strings.Contains(errOut, "types.ts is out of date") || !strings.Contains(errOut, "go run . types") {
		t.Errorf("check before: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := runCmd(t, "generate"); code != 0 || out != "wrote types.ts\n" {
		t.Errorf("generate: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := runCmd(t, "generate", "--check"); code != 0 || out != "types.ts is up to date\n" {
		t.Errorf("check after: %d %q %q", code, out, errOut)
	}
}

// commandNames, which short forms expand to, are exactly the commands
// of run's switch: a command missing from the list would be reached by
// its whole name no more (make:admin would be a short form of
// make:admin-resource).
func TestCommandNames(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cases []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || types.ExprString(sw.Tag) != "args[0]" {
				return true
			}
			for _, st := range sw.Body.List {
				for _, e := range st.(*ast.CaseClause).List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, _ := strconv.Unquote(lit.Value); !strings.HasPrefix(v, "-") {
							cases = append(cases, v)
						}
					}
				}
			}
			return false
		})
		return false
	})
	got, want := slices.Sorted(slices.Values(cases)), slices.Sorted(slices.Values(commandNames))
	if !slices.Equal(got, want) || len(slices.Compact(slices.Clone(want))) != len(want) {
		t.Errorf("run's switch has %v,\ncommandNames %v", got, want)
	}
	for _, name := range commandNames {
		if code, out, errOut := runCmd(t, name, "-h"); code == 2 || strings.Contains(out+errOut, "unknown command") {
			t.Errorf("%s -h = %d %q", name, code, errOut)
		}
	}
}

// TestAgentCommands runs agent:install and agent:update in a project.
func TestAgentCommands(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shop")
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/shop\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "app"))
	if code, _, errOut := runCmd(t, "agent:update"); code != 1 || !strings.Contains(errOut, "agent:install") {
		t.Errorf("agent:update without files: %d %s", code, errOut)
	}
	code, out, errOut := runCmd(t, "agent:install", "--agent=gemini,codex")
	if code != 0 || !strings.Contains(out, "wrote AGENTS.md") || !strings.Contains(out, "wrote GEMINI.md") || strings.Contains(out, "CLAUDE.md") {
		t.Fatalf("agent:install: %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ := runCmd(t, "agent:update"); code != 0 || !strings.Contains(out, "up to date") {
		t.Errorf("agent:update right after: %d %s", code, out)
	}
	skill := filepath.Join(root, ".agents", "skills", "anetos-crud", "SKILL.md")
	if err := os.WriteFile(skill, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f := agentFinding(t, root); f.severity != "warning" || !strings.Contains(f.message, ".agents/skills/anetos-crud/SKILL.md") || !strings.Contains(f.message, "agent:update") {
		t.Errorf("doctor's agents finding for an old skill: %+v", f)
	}
	if code, out, _ := runCmd(t, "agent:update"); code != 0 || out != "wrote .agents/skills/anetos-crud/SKILL.md\n" {
		t.Errorf("agent:update of an old skill: %d %q", code, out)
	}
	if f := agentFinding(t, root); f.severity != "ok" {
		t.Errorf("doctor's agents finding after the update: %+v", f)
	}
	// Exactly the agents listed: codex and gemini's folder goes.
	if code, out, _ := runCmd(t, "agent:install", "--agent=claude"); code != 0 || !strings.Contains(out, "removed .agents/skills/anetos-crud/") || !strings.Contains(out, "wrote CLAUDE.md") {
		t.Errorf("agent:install --agent=claude: %d\n%s", code, out)
	}
}

// agentFinding is doctor's agents finding for the project in root.
func agentFinding(t *testing.T, root string) projectFinding {
	t.Helper()
	for _, f := range projectChecks(t.Context(), root) {
		if f.name == "agents" {
			return f
		}
	}
	return projectFinding{}
}

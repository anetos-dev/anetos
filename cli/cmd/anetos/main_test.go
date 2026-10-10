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
		{[]string{"d"}, 2, `"d" could be dev, doctor`},
		{[]string{"m:m"}, 2, `"m:m" could be make:middleware, make:migration, make:model`},
		{[]string{"generate", "-h"}, 0, "Usage: anetos generate"},
		{[]string{"lang:add", "-h"}, 0, "lang:add is now locale:add"},
		{[]string{"add", "lang", "-h"}, 0, "add lang is now locale:add"},
		{[]string{"help", "add", "lang"}, 0, "Usage: anetos locale:add"},
		{[]string{"make:admin:resource", "-h"}, 0, "make:admin:resource is now make:admin-resource"},
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

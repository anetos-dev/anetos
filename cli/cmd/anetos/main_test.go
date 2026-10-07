// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
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
		{[]string{"help", "gen"}, 0, "Usage: anetos gen"},
		{[]string{"version"}, 0, "anetos "},
		{[]string{"key:generate"}, 0, "APP_KEY=base64:"},
		{[]string{"key:generate", "-h"}, 0, "Usage: anetos key:generate"},
		{[]string{"key:generate", "x"}, 2, "Usage: anetos key:generate"},
		{[]string{"nope"}, 2, `unknown command "nope"`},
		{[]string{"gen", "-h"}, 0, "Usage: anetos gen"},
		{[]string{"add", "-h"}, 0, "Usage: anetos add"},
		{[]string{"add"}, 2, "Usage: anetos add"},
		{[]string{"add", "not a module"}, 2, "malformed module path"},
		{[]string{"remove", "-h"}, 0, "Usage: anetos remove"},
		{[]string{"remove", "a", "b"}, 2, "Usage: anetos remove"},
		{[]string{"gen", "-bogus"}, 2, "flag provided but not defined"},
		{[]string{"gen", "-check", "../../internal/modelgen/internal/..."}, 0, ""},
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

	code, _, errOut := runCmd(t, "gen", "-check")
	if code != 1 || !strings.Contains(errOut, filepath.Join("models", "models_gen.go")+" is out of date") {
		t.Errorf("check before gen: %d %q", code, errOut)
	}
	code, out, errOut := runCmd(t, "gen")
	if code != 0 || out != "wrote "+filepath.Join("models", "models_gen.go")+" (Post)\n" {
		t.Errorf("gen: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := runCmd(t, "gen", "-check"); code != 0 || out+errOut != "" {
		t.Errorf("check after gen: %d %q %q", code, out, errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, "models/post.go"), []byte("package models\n\ntype Post struct{ X string `db:\"x,bad\"` }\n\nfunc (Post) TableName() string { return \"p\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "gen"); code != 1 || !strings.Contains(errOut, `unknown db tag option "bad"`) {
		t.Errorf("gen with a bad model: %d %q", code, errOut)
	}
}

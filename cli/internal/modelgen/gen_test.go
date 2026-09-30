// SPDX-License-Identifier: Apache-2.0

package modelgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// module writes a temporary module requiring the core module from this
// repository, with the given files, and returns its directory.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	core, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/app\n\ngo 1.26\n\nrequire anetos.dev/anetos v0.0.0\n\nreplace anetos.dev/anetos => " + core + "\n"
	for name, src := range files {
		write(t, filepath.Join(dir, name), src)
	}
	return dir
}

func write(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func generate(t *testing.T, dir string, patterns ...string) []Change {
	t.Helper()
	changes, err := Generate(dir, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	return changes
}

func TestFixturesUpToDate(t *testing.T) {
	if changes := generate(t, ".", "./internal/..."); len(changes) != 0 {
		t.Errorf("fixtures are out of date (run go run ./cmd/anetos gen ./internal/modelgen/internal/... in cli): %v", changes[0].Path)
	}
}

const postV1 = `package models

import "anetos.dev/anetos/db"

type Post struct {
	db.Model
	Title string
}
`

func TestLifecycle(t *testing.T) {
	dir := module(t, map[string]string{
		"models/post.go": postV1,
		"other/other.go": "package other\n\nfunc F() {}\n",
	})
	gen := filepath.Join(dir, "models", FileName)
	changes := generate(t, dir)
	if len(changes) != 1 || changes[0].Path != gen || strings.Join(changes[0].Models, ",") != "Post" {
		t.Fatalf("changes = %+v", changes)
	}
	src := string(changes[0].Content)
	if !strings.HasPrefix(src, Header+"\n") || !strings.Contains(src, `db.Col[string]("title")`) {
		t.Errorf("generated:\n%s", src)
	}
	check(t, Apply(changes))
	vet(t, dir)
	if changes := generate(t, dir); len(changes) != 0 {
		t.Errorf("regenerating changed %v", changes[0].Path)
	}
	if changes := generate(t, filepath.Join(dir, "models"), "."); len(changes) != 0 {
		t.Errorf("regenerating from the package directory changed %v", changes[0].Path)
	}

	// Code using a column that the model no longer has doesn't stop
	// generation, and neither does the stale generated file.
	write(t, filepath.Join(dir, "models/post.go"), strings.Replace(postV1, "Title string", "Heading string", 1))
	write(t, filepath.Join(dir, "models/use.go"), "package models\n\nvar _ = PostCols.Title\n")
	changes = generate(t, dir)
	if len(changes) != 1 || !strings.Contains(string(changes[0].Content), `db.Col[string]("heading")`) {
		t.Fatalf("changes = %+v", changes)
	}
	check(t, Apply(changes))

	// With no models left, the file is removed.
	write(t, filepath.Join(dir, "models/post.go"), "package models\n\ntype Post struct{ Title string }\n")
	check(t, os.Remove(filepath.Join(dir, "models/use.go")))
	changes = generate(t, dir)
	if len(changes) != 1 || changes[0].Content != nil {
		t.Fatalf("changes = %+v", changes)
	}
	check(t, Apply(changes))
	if _, err := os.Stat(gen); !os.IsNotExist(err) {
		t.Errorf("%s not removed: %v", gen, err)
	}
	if changes := generate(t, dir); len(changes) != 0 {
		t.Errorf("changes after removal: %+v", changes)
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	const head = "package models\n\nimport \"anetos.dev/anetos/db\"\n\n"
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"duplicate column", map[string]string{"models/m.go": head +
			"type P struct {\n\tdb.Model\n\tA string `db:\"x\"`\n\tB string `db:\"x\"`\n}\n"}, `two fields map to column "x"`},
		{"unknown option", map[string]string{"models/m.go": head +
			"type P struct {\n\tdb.Model\n\tA string `db:\"x,unique\"`\n}\n"}, `unknown db tag option "unique"`},
		{"two pks", map[string]string{"models/m.go": head +
			"type P struct {\n\tdb.Model\n\tA string `db:\"a,pk\"`\n}\n"}, "more than one pk"},
		{"go name clash", map[string]string{"models/m.go": head +
			"type Inner struct{ Name string `db:\"inner_name\"` }\n\ntype P struct {\n\tdb.Model\n\tInner\n\tName string\n}\n"},
			"both come from fields named Name"},
		{"name taken", map[string]string{"models/m.go": head +
			"type P struct{ db.Model }\n\nvar PCols = 1\n"}, "PCols is already declared"},
		{"unknown directive", map[string]string{"models/m.go": head +
			"//anetos:modle\ntype P struct{ db.Model }\n"}, "unknown directive //anetos:modle"},
		{"not a struct", map[string]string{"models/m.go": head +
			"//anetos:model\ntype P int\n\nvar _ db.Model\n"}, "not a model struct"},
		{"generic", map[string]string{"models/m.go": head +
			"//anetos:model\ntype P[T any] struct{ db.Model }\n"}, "generic types and aliases can't be models"},
		{"self embedding", map[string]string{"models/m.go": head +
			"type P struct {\n\tdb.Model\n\t*P\n}\n"}, "embeds itself"},
		{"type error", map[string]string{"models/m.go": head +
			"type P struct {\n\tdb.Model\n\tX Undefined\n}\n"}, "field types must compile"},
		{"syntax error", map[string]string{"models/m.go": head + "type P struct {\n"}, "expected"},
		{"unnameable type", map[string]string{
			"dep/dep.go":  "package dep\n\nimport \"anetos.dev/anetos/db\"\n\ntype Base struct {\n\tdb.Model\n\tKind kind\n}\n\ntype kind string\n",
			"models/m.go": "package models\n\nimport \"example.com/app/dep\"\n\ntype P struct{ dep.Base }\n",
		}, "type dep.kind, unexported in package example.com/app/dep"},
		{"internal type", map[string]string{
			"lib/base/base.go":    "package base\n\nimport (\n\t\"anetos.dev/anetos/db\"\n\t\"example.com/app/lib/internal/x\"\n)\n\ntype Base struct {\n\tdb.Model\n\tKind x.Kind\n}\n",
			"lib/internal/x/x.go": "package x\n\ntype Kind string\n",
			"models/m.go":         "package models\n\nimport \"example.com/app/lib/base\"\n\ntype P struct{ base.Base }\n",
		}, "from internal package example.com/app/lib/internal/x"},
		{"anonymous struct with unexported fields", map[string]string{
			"dep/dep.go":  "package dep\n\nimport \"anetos.dev/anetos/db\"\n\ntype Base struct {\n\tdb.Model\n\tMeta struct{ x int } `db:\"meta,json\"`\n}\n",
			"models/m.go": "package models\n\nimport \"example.com/app/dep\"\n\ntype P struct{ dep.Base }\n",
		}, "unexported fields of package example.com/app/dep"},
		{"embedded type error", map[string]string{"models/m.go": head +
			"//anetos:skip\ntype Base struct {\n\tdb.Model\n\tX undefinedType\n}\n\ntype P struct{ Base }\n"}, "field types must compile"},
		{"build constraint", map[string]string{"models/m_linux.go": head + "type P struct{ db.Model }\n",
			"models/doc.go": "package models\n"}, "build constraints aren't supported"},
		{"go:build line", map[string]string{"models/m.go": "//go:build !nope\n\n" + head + "type P struct{ db.Model }\n"}, "build constraints aren't supported"},
		{"embedded struct from a constrained file", map[string]string{"models/m.go": head +
			"type P struct{ Base }\n", "models/base_linux.go": head + "//anetos:skip\ntype Base struct{ db.Model }\n",
			"models/base_other.go": "//go:build !linux\n\n" + head + "//anetos:skip\ntype Base struct{ db.Model }\n"},
			"declared in a file with build constraints"},
		{"not ours, excluded from the build", map[string]string{"models/m.go": head + "type P struct{ db.Model }\n",
			"models/models_gen.go": "//go:build ignore\n\npackage models\n"}, "was not written by anetos gen"},
		{"not ours", map[string]string{"models/m.go": head + "type P struct{ db.Model }\n",
			"models/models_gen.go": "package models\n"}, "was not written by anetos gen"},
		{"no packages", map[string]string{"models/m.go": head}, "nope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := module(t, c.files)
			pattern := "./..."
			if c.name == "no packages" {
				pattern = "./nope"
			}
			_, err := Generate(dir, pattern)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestImportNames(t *testing.T) {
	dir := module(t, map[string]string{
		"models/m.go": `package models

import (
	dbpkg "anetos.dev/anetos/db"
	"example.com/app/a/uuid"
	uuid2 "example.com/app/b/uuid"
)

var db = "a package-level name"

type P struct {
	dbpkg.Model
	A uuid.ID
	B uuid2.ID
}
`,
		"a/uuid/uuid.go": "package uuid\n\ntype ID [16]byte\n",
		"b/uuid/uuid.go": "package uuid\n\ntype ID string\n",
	})
	changes := generate(t, dir)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v", changes)
	}
	src := string(changes[0].Content)
	for _, want := range []string{`db2 "anetos.dev/anetos/db"`, `"example.com/app/a/uuid"`,
		`uuid2 "example.com/app/b/uuid"`, "db2.Column[uuid.ID]", "db2.Col[uuid2.ID](\"b\")"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
	check(t, Apply(changes))
	vet(t, dir)
}

// vet checks that the module, generated files included, compiles.
func vet(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
}

func TestTestFilesAndLineEndings(t *testing.T) {
	dir := module(t, map[string]string{
		"models/m.go":           "package models\n\nimport \"time\"\n\ntype P struct{ At time.Time }\n\nfunc (P) TableName() string { return \"p\" }\n",
		"models/helper_test.go": "package models\n\nimport \"database/sql\"\n\nvar db *sql.DB\n\nvar _ = db\n",
		"models/ext_test.go":    "package models_test\n\nvar time = 1\n\nvar _ = time\n",
	})
	changes := generate(t, dir)
	if len(changes) != 1 || !strings.Contains(string(changes[0].Content), `db2 "anetos.dev/anetos/db"`) ||
		!strings.Contains(string(changes[0].Content), "\t\"time\"") {
		t.Fatalf("changes = %+v", changes)
	}
	check(t, Apply(changes))
	vet(t, dir) // includes the test files

	// Names declared in files for other platforms are avoided too.
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	write(t, filepath.Join(dir, "models/x_"+other+".go"), "package models\n\nvar time = 1\n")
	if changes := generate(t, dir); len(changes) != 1 || !strings.Contains(string(changes[0].Content), `time2 "time"`) {
		t.Fatalf("changes = %+v", changes)
	}
	check(t, os.Remove(filepath.Join(dir, "models/x_"+other+".go")))

	// A checkout with Windows line endings is up to date.
	gen := changes[0].Path
	src, err := os.ReadFile(gen)
	check(t, err)
	check(t, os.WriteFile(gen, []byte(strings.ReplaceAll(string(src), "\n", "\r\n")), 0o644))
	if changes := generate(t, dir); len(changes) != 0 {
		t.Errorf("CRLF file reported out of date")
	}

	// A test file declaring the generated name is a conflict.
	write(t, filepath.Join(dir, "models/cols_test.go"), "package models\n\nvar PCols = 1\n")
	if _, err := Generate(dir); err == nil || !strings.Contains(err.Error(), "PCols is already declared") {
		t.Errorf("error = %v", err)
	}
}

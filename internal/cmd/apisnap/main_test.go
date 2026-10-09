// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollect(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.26\n",
		"web/web.go": `package web

import (
	"context"
	"errors"
)

// Look is a button's look.
type Look int

const (
	Primary Look = iota // the main action
	Secondary
	hidden
)

const MaxBody = 1 << 20

const Name = "web"

var ErrNotFound = errors.New("web: not found")

var Pages = &Router{}

const Layout = MaxBody

var Default Look

// Router routes.
type Router struct {
	Name   string ` + "`json:\"name\"`" + ` // its name
	a, B   int
	*Base
	inner
}

type Base struct{}

type inner struct{}

type Handler interface {
	Serve(ctx context.Context, path string) error
	io()
	Base
}

type Alias = Router

type HandlerFunc func(ctx context.Context, n int) error

type Set[T comparable] struct{ Items []T }

func New(names ...string) *Router { return nil }

func Map[In, Out any](f func(in In) (Out, error), a, b In) []Out { return nil }

func (r *Router) Get(path string, h func(c *Router, n int)) (*Router, error) { return r, nil }

func (s *Set[T]) Add(v T) {}

func (inner) Exported() {}

func helper() {}
`,
		"web/web_test.go":        "package web\n\nfunc TestOnly() {}\n",
		"web/tagged.go":          "//go:build !linux && !ignore_x\n\npackage web\n\nfunc Tagged() {}\n",
		"web/api/api.go":         "package api\n\nfunc Public() {}\n",
		"_old/old.go":            "package old\n\nfunc Old() {}\n",
		"api/x.go":               "package api\n\nfunc NotHere() {}\n",
		"web/ignore.go":          "//go:build ignore\n\npackage web\n\nfunc Ignored() {}\n",
		"internal/x/x.go":        "package x\n\nfunc Hidden() {}\n",
		"cmd/tool/main.go":       "package main\n\nfunc Tool() {}\n",
		"examples/e/e.go":        "package e\n\nfunc Example() {}\n",
		"drivers/db/go.mod":      "module example.com/m/drivers/db\n",
		"drivers/db/db.go":       "package db\n\nfunc Open() error { return nil }\n",
		"drivers/db/sub/sub.go":  "package sub\n\nvar X int\n",
		"cli/go.mod":             "module example.com/m/cli\n",
		"cli/cmd/anetos/main.go": "package main\n\nfunc main() {}\n",
		"bench/go.mod":           "module example.com/m/bench\n",
		"bench/bench.go":         "package bench\n\nfunc Bench() {}\n",
	})
	apis, err := collect(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range apis {
		names = append(names, n)
	}
	slices.Sort(names)
	if want := []string{"anetos.txt", "drivers-db.txt"}; !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	p := "pkg example.com/m/web, "
	want := []string{
		p + "const Layout = MaxBody",
		p + "const MaxBody = 1 << 20",
		p + "const Name = \"web\"",
		p + "const Primary Look = 0",
		p + "const Secondary Look = 1",
		p + "func Map[In, Out any](func(In) (Out, error), In, In) []Out",
		p + "func New(...string) *Router",
		p + "func Tagged()",
		p + "method (*Router) Get(string, func(*Router, int)) (*Router, error)",
		p + "method (*Set[T]) Add(T)",
		p + "type Alias = Router",
		p + "type Base struct",
		p + "type Handler interface",
		p + "type Handler interface, Serve(context.Context, string) error",
		p + "type Handler interface, embedded Base",
		p + "type Handler interface, unexported methods",
		p + "type HandlerFunc func(context.Context, int) error",
		p + "type Look int",
		p + "type Router struct",
		p + "type Router struct, B int",
		p + "type Router struct, Name string",
		p + "type Router struct, embedded *Base",
		p + "type Router struct, embedded inner",
		p + "type Set[T comparable] struct",
		p + "type Set[T comparable] struct, Items []T",
		p + "var Default Look",
		p + "var ErrNotFound error",
		p + "var Pages *Router",
		"pkg example.com/m/web/api, func Public()",
	}
	if got := apis["anetos.txt"].lines; !slices.Equal(got, want) {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got, want := apis["drivers-db.txt"].lines, []string{
		"pkg example.com/m/drivers/db, func Open() error",
		"pkg example.com/m/drivers/db/sub, var X int",
	}; !slices.Equal(got, want) {
		t.Errorf("driver lines %q, want %q", got, want)
	}

	// Written, they match; changed, they don't.
	dir := filepath.Join(root, "api")
	if err := write(dir, apis); err != nil {
		t.Fatal(err)
	}
	if !compare(dir, apis) {
		t.Error("compare after write")
	}
	// Checked out with CRLF line endings (git on Windows), they match.
	f := filepath.Join(dir, "anetos.txt")
	b, _ := os.ReadFile(f)
	if err := os.WriteFile(f, []byte(strings.ReplaceAll(string(b), "\n", "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if !compare(dir, apis) {
		t.Error("compare with CRLF")
	}
	// A file no module has fails the comparison.
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if compare(dir, apis) {
		t.Error("compare with a stale file")
	}
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	apis["anetos.txt"].lines = apis["anetos.txt"].lines[1:]
	if compare(dir, apis) {
		t.Error("compare with a removed line")
	}
	delete(apis, "drivers-db.txt")
	if err := write(dir, apis); err == nil || !strings.Contains(err.Error(), "drivers-db.txt") {
		t.Errorf("a stale file: %v", err)
	}
}

func TestCollectNeedsTypes(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":   "module example.com/m\n",
		"web/w.go": "package web\n\nimport \"time\"\n\nvar Timeout = time.Duration(5)\n",
	})
	if _, err := collect(root); err == nil || !strings.Contains(err.Error(), "give var Timeout an explicit type") {
		t.Errorf("collect: %v", err)
	}
}

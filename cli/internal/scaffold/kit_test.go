// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func newKitProject(t *testing.T, kit string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	if _, err := Create(Project{Dir: dir, DB: "sqlite", CSS: kit}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A new project records its kit: every file the kit wrote, with its
// digest, and a project without a record has none.
func TestKitRecord(t *testing.T) {
	for _, kit := range Kits {
		dir := newKitProject(t, kit)
		r, err := ReadKitRecord(dir)
		if err != nil || r.Kit != kit || r.Version != KitVersions[kit] {
			t.Fatalf("%s: %+v, %v", kit, r, err)
		}
		files, _ := renderKit(kit, projectData{Module: "app", Kit: kit})
		if len(r.Files) != len(files) {
			t.Errorf("%s: %d files recorded, the kit has %d", kit, len(r.Files), len(files))
		}
		for rel, sum := range r.Files {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil || digest(b) != sum {
				t.Errorf("%s: %s: %v", kit, rel, err)
			}
		}
		if GuessKit(dir) != kit {
			t.Errorf("%s: GuessKit = %s", kit, GuessKit(dir))
		}
	}
	if _, err := ReadKitRecord(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no record: %v", err)
	}
}

// A development build's views/ui/kit.json is read, and css:use replaces
// it with css.json.
func TestFormerKitRecord(t *testing.T) {
	dir := newKitProject(t, "pico")
	b, err := os.ReadFile(filepath.Join(dir, "views", "ui", "css.json"))
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(b), `"framework":`, `"kit":`, 1)
	if err := os.WriteFile(filepath.Join(dir, "views", "ui", "kit.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "views", "ui", "css.json")); err != nil {
		t.Fatal(err)
	}
	if r, err := ReadKitRecord(dir); err != nil || r.Kit != "pico" || len(r.Files) == 0 {
		t.Fatalf("kit.json: %+v, %v", r, err)
	}
	if _, err := UseKit(dir, "app", "bulma", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "views", "ui", "kit.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("kit.json kept: %v", err)
	}
	if r, err := ReadKitRecord(dir); err != nil || r.Kit != "bulma" {
		t.Errorf("css.json: %+v, %v", r, err)
	}
}

// css:use switches kits: the new kit's files are written, the old one's
// it lacks removed, the layout and the app's own files kept.
func TestUseKit(t *testing.T) {
	dir := newKitProject(t, "pico")
	layout := read(t, filepath.Join(dir, "views", "layout.templ"))
	write(t, filepath.Join(dir, "views", "ui", "mine.templ"), "package ui\n")
	write(t, filepath.Join(dir, "views", "ui", "shell_templ.go"), "// generated\n")

	c, err := UseKit(dir, "app", "bootstrap", false)
	if err != nil {
		t.Fatalf("%+v: %v", c, err)
	}
	if c.From != "pico" || c.To != "bootstrap" || len(c.Changed) != 0 {
		t.Errorf("change: %+v", c)
	}
	if !slices.Equal(c.Removed, []string{"public/static/pico.LICENSE.txt", "public/static/pico.min.css"}) {
		t.Errorf("removed %v", c.Removed)
	}
	if !slices.Equal(c.Own, []string{"views/ui/mine.templ"}) {
		t.Errorf("own %v", c.Own)
	}
	for _, f := range []string{"bootstrap.min.css", "bootstrap.bundle.min.js", "theme.js"} {
		if !exists(filepath.Join(dir, "public", "static", f)) {
			t.Errorf("no %s", f)
		}
	}
	if exists(filepath.Join(dir, "public", "static", "pico.min.css")) || !exists(filepath.Join(dir, "views", "ui", "mine.templ")) {
		t.Error("pico.min.css kept, or mine.templ removed")
	}
	if !strings.Contains(read(t, filepath.Join(dir, "views", "ui", "shell.templ")), "navbar") {
		t.Error("shell.templ isn't Bootstrap's")
	}
	if read(t, filepath.Join(dir, "views", "layout.templ")) != layout {
		t.Error("the layout changed")
	}
	if r, _ := ReadKitRecord(dir); r.Kit != "bootstrap" {
		t.Errorf("record: %+v", r)
	}

	// To Tailwind and back to the starter theme: its source comes and goes.
	if _, err := UseKit(dir, "app", "tailwind", false); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "views", "ui", "tailwind.css")) || exists(filepath.Join(dir, "public", "static", "theme.js")) {
		t.Error("tailwind: tailwind.css missing, or theme.js kept")
	}
	// Tailwind's app.css is compiled: a different one isn't a change.
	write(t, filepath.Join(dir, "public", "static", "app.css"), "/* compiled with more classes */")
	if c, err := UseKit(dir, "app", "none", false); err != nil {
		t.Fatalf("%+v: %v", c, err)
	}
	if exists(filepath.Join(dir, "views", "ui", "tailwind.css")) || exists(filepath.Join(dir, "views", "ui", "classes.go")) {
		t.Error("none: tailwind.css or classes.go kept")
	}
	if _, err := UseKit(dir, "app", "anetos", false); err != nil {
		t.Fatal(err)
	}
	fresh := newKitProject(t, "anetos")
	for _, f := range []string{"views/ui/shell.templ", "views/ui/classes.go", "public/static/app.css"} {
		if read(t, filepath.Join(dir, f)) != read(t, filepath.Join(fresh, f)) {
			t.Errorf("after the round trip, %s isn't a new project's", f)
		}
	}

	// The same kit again: nothing to write.
	if c, err := UseKit(dir, "app", "anetos", false); err != nil || len(c.Written) != 0 || len(c.Removed) != 0 {
		t.Errorf("again: %+v, %v", c, err)
	}
	if _, err := UseKit(dir, "app", "nope", false); err == nil {
		t.Error("a kit that doesn't exist")
	}
}

// A kit file changed since, or a file in the new kit's way, stops the
// switch unless forced; a project without a record needs force.
func TestUseKitRefuses(t *testing.T) {
	dir := newKitProject(t, "anetos")
	page := filepath.Join(dir, "views", "ui", "page.templ")
	css := filepath.Join(dir, "public", "static", "app.css")
	write(t, page, read(t, page)+"\n// mine\n")
	write(t, css, read(t, css)+"\n:root { --primary: #c00; }\n")
	before := read(t, filepath.Join(dir, "views", "ui", "shell.templ"))

	c, err := UseKit(dir, "app", "pico", false)
	if !errors.Is(err, ErrKitChanged) || !slices.Equal(c.Changed, []string{"public/static/app.css", "views/ui/page.templ"}) {
		t.Fatalf("changed: %+v, %v", c, err)
	}
	if read(t, filepath.Join(dir, "views", "ui", "shell.templ")) != before {
		t.Error("a refused switch wrote files")
	}
	// The same kit (an update) is refused too.
	if _, err := UseKit(dir, "app", "anetos", false); !errors.Is(err, ErrKitChanged) {
		t.Errorf("same kit: %v", err)
	}
	if c, err := UseKit(dir, "app", "pico", true); err != nil || len(c.Changed) != 2 {
		t.Fatalf("forced: %+v, %v", c, err)
	}
	if strings.Contains(read(t, page), "// mine") {
		t.Error("forced: page.templ kept")
	}

	// A file the new kit writes, which the old one didn't.
	write(t, filepath.Join(dir, "public", "static", "theme.js"), "// mine")
	if c, err := UseKit(dir, "app", "bootstrap", false); !errors.Is(err, ErrKitChanged) || !slices.Equal(c.Changed, []string{"public/static/theme.js"}) {
		t.Errorf("in the way: %+v, %v", c, err)
	}

	// No record.
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(KitRecordFile))); err != nil {
		t.Fatal(err)
	}
	if _, err := UseKit(dir, "app", "none", false); !errors.Is(err, ErrNoKitRecord) {
		t.Errorf("no record: %v", err)
	}
	if c, err := UseKit(dir, "app", "none", true); err != nil || c.From != "" {
		t.Errorf("no record, forced: %+v, %v", c, err)
	}
	if r, _ := ReadKitRecord(dir); r.Kit != "none" {
		t.Errorf("record: %+v", r)
	}
}

func TestAddNavMenu(t *testing.T) {
	const menu = `menu: "Menu"`
	for in, want := range map[string]string{
		"nav:\n  label: \"Main\"\n  home: \"Home\"\nerrors:\n  home: x\n": "nav:\n  " + menu + "\n  label: \"Main\"\n  home: \"Home\"\nerrors:\n  home: x\n",
		"nav:\r\n    home: \"Home\"\r\n":                                  "nav:\r\n    " + menu + "\r\n    home: \"Home\"\r\n",
		"home:\n  title: x":                                               "home:\n  title: x\nnav:\n  " + menu + "\n",
		"nav: # the header\n  home: x\n":                                  "nav: # the header\n  " + menu + "\n  home: x\n",
		"nav:   \n  home: x\n":                                            "nav:   \n  " + menu + "\n  home: x\n",
		"\ufeffnav:\n  home: x\n":                                         "\ufeffnav:\n  " + menu + "\n  home: x\n",
		"nav:\n  account:\n    login: x\n  home: y\n":                     "nav:\n  " + menu + "\n  account:\n    login: x\n  home: y\n",
		"nav:\n  sub: {menu: x}\n":                                        "nav:\n  " + menu + "\n  sub: {menu: x}\n",
		"nav:\n\n  home: x\n":                                             "nav:\n\n  " + menu + "\n  home: x\n",
		"":                                                                "nav:\n  " + menu + "\n",
		"nav:\n  menu: \"Menü\"\n":                                        "",
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, "locales", "en", "app.yaml")
		write(t, p, in)
		added, err := AddNavMenu(dir)
		if err != nil || added != (want != "") {
			t.Errorf("%q: %v, %v", in, added, err)
		}
		if want == "" {
			want = in
		}
		if got := read(t, p); got != want {
			t.Errorf("%q:\n%q, want\n%q", in, got, want)
		}
	}
	// Left alone, with what to add: a nav it can't edit safely.
	for _, in := range []string{"nav: {label: Main}\n", "nav: x\n", "- a\n", "nav: [\n"} {
		dir := t.TempDir()
		p := filepath.Join(dir, "locales", "en", "app.yaml")
		write(t, p, in)
		if added, err := AddNavMenu(dir); added || err == nil || !strings.Contains(err.Error(), "yourself") {
			t.Errorf("%q: %v, %v", in, added, err)
		}
		if read(t, p) != in {
			t.Errorf("%q: changed", in)
		}
	}
	if added, err := AddNavMenu(t.TempDir()); added || err != nil {
		t.Errorf("no locales: %v, %v", added, err)
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "locales", "en", "app.yaml"), "nav:\n  menu: x\n")
	write(t, filepath.Join(dir, "locales", "bn", "app.yaml"), "nav:\n  home: x\n")
	write(t, filepath.Join(dir, "locales", "fr", "app.yaml"), "nav:\n  menu: x\n")
	if got := LocalesWithoutNavMenu(dir); !slices.Equal(got, []string{"bn"}) {
		t.Errorf("LocalesWithoutNavMenu = %v", got)
	}
}

// A record can only name files of views/ui and public/static: css:use
// removes the old kit's, and a hand-edited one mustn't reach others.
func TestKitRecordPaths(t *testing.T) {
	dir := newKitProject(t, "anetos")
	recPath := filepath.Join(dir, filepath.FromSlash(KitRecordFile))
	good := read(t, recPath)
	for _, bad := range []string{"go.mod", "../victim.txt", "/etc/passwd", "views/ui/../../go.mod", "views/ui/kit.json", "views/ui/css.json", "public/static", "views/uix/a.go", `views\\ui\\a.go`} {
		write(t, recPath, strings.Replace(good, `"files": {`, `"files": {"`+bad+`": "00",`, 1))
		if _, err := ReadKitRecord(dir); !errors.Is(err, ErrBadKitRecord) {
			t.Errorf("%s: %v", bad, err)
		}
		if _, err := UseKit(dir, "app", "pico", false); !errors.Is(err, ErrBadKitRecord) {
			t.Errorf("%s: UseKit %v", bad, err)
		}
	}
	write(t, recPath, `{"kit": "anetos", "files": {"go.mod": "00"}}`)
	if c, err := UseKit(dir, "app", "pico", true); err != nil || c.From != "" || !exists(filepath.Join(dir, "go.mod")) {
		t.Errorf("forced over a bad record: %+v, %v", c, err)
	}
	if c, err := UseKit(dir, "app", "pico", false); err != nil {
		t.Errorf("then: %+v, %v", c, err)
	}
	write(t, recPath, "{bad")
	if _, err := ReadKitRecord(dir); !errors.Is(err, ErrBadKitRecord) {
		t.Errorf("not JSON: %v", err)
	}
}

// Line endings aren't changes (git on Windows), symbolic links are.
func TestUseKitTextAndLinks(t *testing.T) {
	dir := newKitProject(t, "anetos")
	for _, f := range []string{"views/ui/page.templ", "public/static/app.css"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		write(t, p, strings.ReplaceAll(read(t, p), "\n", "\r\n"))
	}
	if c, err := UseKit(dir, "app", "anetos", false); err != nil || len(c.Changed) != 0 || len(c.Written) != 0 {
		t.Errorf("CRLF: %+v, %v", c, err)
	}
	if c, err := UseKit(dir, "app", "pico", false); err != nil || len(c.Changed) != 0 {
		t.Fatalf("CRLF, to pico: %+v, %v", c, err)
	}
	shared := filepath.Join(t.TempDir(), "shell.templ")
	write(t, shared, "shared")
	shell := filepath.Join(dir, "views", "ui", "shell.templ")
	if err := os.Remove(shell); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, shell); err != nil {
		t.Skip(err)
	}
	if c, err := UseKit(dir, "app", "bulma", false); !errors.Is(err, ErrKitChanged) || !slices.Contains(c.Changed, "views/ui/shell.templ") {
		t.Errorf("link: %+v, %v", c, err)
	}
	if _, err := UseKit(dir, "app", "bulma", true); err != nil {
		t.Fatal(err)
	}
	if read(t, shared) != "shared" {
		t.Error("written through the link")
	}
	if fi, err := os.Lstat(shell); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("shell.templ: %v %v", fi, err)
	}
}

// Tailwind to Tailwind keeps the compiled app.css; without a record, the
// old kit's static files are listed, not removed.
func TestUseKitTailwindAndOthers(t *testing.T) {
	dir := newKitProject(t, "tailwind")
	css := filepath.Join(dir, "public", "static", "app.css")
	write(t, css, "/* compiled with the app's classes */")
	if c, err := UseKit(dir, "app", "tailwind", false); err != nil || slices.Contains(c.Written, "public/static/app.css") {
		t.Errorf("tailwind again: %+v, %v", c, err)
	}
	if read(t, css) != "/* compiled with the app's classes */" {
		t.Error("app.css replaced")
	}

	dir = newKitProject(t, "bootstrap")
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(KitRecordFile))); err != nil {
		t.Fatal(err)
	}
	c, err := UseKit(dir, "app", "pico", true)
	if err != nil || !slices.Contains(c.Others, "public/static/bootstrap.min.css") || slices.Contains(c.Others, "public/static/pico.min.css") {
		t.Errorf("no record: %+v, %v", c, err)
	}
}

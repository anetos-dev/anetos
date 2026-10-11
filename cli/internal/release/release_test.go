// SPDX-License-Identifier: Apache-2.0

package release

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/semver"
)

// repo writes a small repository of modules: the core, cli (without a
// replace), admin and a driver (with replaces), and an example.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		t.Helper()
		p = filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module anetos.dev/anetos\n\ngo 1.26.0\n")
	write("CHANGELOG.md", "# Changelog\n\n## [Unreleased]\n\n## [0.5.0] - 2026-10-20\n")
	write("cli/go.mod", "module anetos.dev/anetos/cli\n\ngo 1.26.0\n\nrequire anetos.dev/anetos v0.4.1-0.20261010215428-426a3a3dfbe9\n\nretract [v0.1.0, v0.4.0]\n")
	write("cli/go.sum", "anetos.dev/anetos v0.4.1-0.20261010215428-426a3a3dfbe9 h1:x=\nanetos.dev/anetos v0.4.1-0.20261010215428-426a3a3dfbe9/go.mod h1:y=\ngolang.org/x/mod v0.41.0 h1:z=\n")
	write("admin/go.mod", "module anetos.dev/anetos/admin\n\ngo 1.26.0\n\nrequire (\n\tanetos.dev/anetos v0.0.0-00010101000000-000000000000\n\tanetos.dev/anetos/drivers/sqlite v0.0.0-00010101000000-000000000000\n)\n\nreplace (\n\tanetos.dev/anetos => ..\n\tanetos.dev/anetos/drivers/sqlite => ../drivers/sqlite\n)\n\nretract [v0.1.0, v0.4.0]\n")
	write("drivers/sqlite/go.mod", "module anetos.dev/anetos/drivers/sqlite\n\ngo 1.26.0\n\nrequire anetos.dev/anetos v0.0.0-00010101000000-000000000000\n\nreplace anetos.dev/anetos => ../..\n")
	write("drivers/sqlite/CHANGELOG.md", "## [0.5.0] - 2026-10-20\n")
	write("plugins/postmark/go.mod", "module anetos.dev/anetos/plugins/postmark\n\ngo 1.26.0\n\nrequire anetos.dev/anetos v0.0.0-00010101000000-000000000000\n\nreplace anetos.dev/anetos => ../..\n")
	write("plugins/postmark/CHANGELOG.md", "## [Unreleased]\n")
	write("examples/app/go.mod", "module anetos.dev/anetos/examples/app\n\ngo 1.26.0\n\nrequire (\n\tanetos.dev/anetos v0.0.0-00010101000000-000000000000\n\tgithub.com/a-h/templ v0.3.1070\n)\n\nreplace anetos.dev/anetos => ../..\n")
	return root
}

func read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPublished(t *testing.T) {
	root := repo(t)
	mods, err := Published(root)
	if err != nil {
		t.Fatal(err)
	}
	tags := Tags(mods, "v0.5.0")
	want := []string{"v0.5.0", "cli/v0.5.0", "admin/v0.5.0", "drivers/sqlite/v0.5.0", "plugins/postmark/v0.5.0"}
	if !slices.Equal(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func TestCheckVersion(t *testing.T) {
	for _, v := range []string{"v0.5.0", "v0.5.0-rc.1", "v1.2.3"} {
		if err := CheckVersion(v); err != nil {
			t.Errorf("CheckVersion(%q) = %v", v, err)
		}
	}
	for _, v := range []string{"0.5.0", "v0.5", "v0.5.0+meta", "latest", "", "v2.0.0", "v0.3.1", "v0.4.0", "v0.1.0"} {
		if CheckVersion(v) == nil {
			t.Errorf("CheckVersion(%q) = nil, want an error", v)
		}
	}
}

func TestPrepare(t *testing.T) {
	root := repo(t)
	locales := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(locales, []byte("module anetos.dev/locales\n\ngo 1.26.0\n\nrequire anetos.dev/anetos v0.0.0-00010101000000-000000000000\n\nreplace anetos.dev/anetos => ../anetos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Prepare(root, "v0.5.0-rc.1", locales)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 6 { // all but the core's go.mod
		t.Errorf("changed %d files, want 6: %v", len(changed), changed)
	}
	for _, p := range []string{"cli/go.mod", "admin/go.mod", "drivers/sqlite/go.mod", "examples/app/go.mod"} {
		s := read(t, filepath.Join(root, p))
		if strings.Contains(s, "v0.0.0-00010101000000") || strings.Contains(s, "v0.4.1-") {
			t.Errorf("%s keeps an old version:\n%s", p, s)
		}
		if !strings.Contains(s, "anetos.dev/anetos v0.5.0-rc.1") {
			t.Errorf("%s doesn't require the core at v0.5.0-rc.1:\n%s", p, s)
		}
	}
	if s := read(t, filepath.Join(root, "admin/go.mod")); !strings.Contains(s, "anetos.dev/anetos/drivers/sqlite v0.5.0-rc.1") || !strings.Contains(s, "=> ../drivers/sqlite") {
		t.Errorf("admin/go.mod: the driver's version or its replace:\n%s", s)
	}
	if s := read(t, filepath.Join(root, "examples/app/go.mod")); !strings.Contains(s, "github.com/a-h/templ v0.3.1070") {
		t.Errorf("examples/app/go.mod lost another requirement:\n%s", s)
	}
	if s := read(t, locales); !strings.Contains(s, "anetos.dev/anetos v0.5.0-rc.1") {
		t.Errorf("the locales' go.mod:\n%s", s)
	}
	// cli requires the core without a replace: its old sums go.
	if s := read(t, filepath.Join(root, "cli/go.sum")); s != "golang.org/x/mod v0.41.0 h1:z=\n" {
		t.Errorf("cli/go.sum = %q", s)
	}
	// Twice is a no-op.
	if changed, err := Prepare(root, "v0.5.0-rc.1", locales); err != nil || len(changed) != 0 {
		t.Errorf("second Prepare = %v, %v", changed, err)
	}
	if _, err := Prepare(root, "0.5.0"); err == nil {
		t.Error("Prepare with a version without v: no error")
	}
}

func TestCheckChangelogs(t *testing.T) {
	root := repo(t)
	err := CheckChangelogs(root, "v0.5.0")
	if err == nil || !strings.Contains(err.Error(), "plugins/postmark/CHANGELOG.md") || strings.Contains(err.Error(), "drivers/sqlite") {
		t.Errorf("CheckChangelogs(v0.5.0) = %v, want postmark's alone", err)
	}
	if err := CheckChangelogs(root, "v0.5.0-rc.1"); err != nil {
		t.Errorf("a pre-release: %v", err)
	}
	if err := CheckChangelogs(root, "v0.6.0"); err == nil || !strings.Contains(err.Error(), "CHANGELOG.md") {
		t.Errorf("CheckChangelogs(v0.6.0) = %v", err)
	}
}

func TestCheckPublishable(t *testing.T) {
	root := repo(t)
	if err := checkPublishable(filepath.Join(root, "admin/go.mod"), false); err == nil {
		t.Error("a placeholder requirement: no error")
	}
	if err := checkPublishable(filepath.Join(root, "cli/go.mod"), true); err != nil {
		t.Errorf("cli: %v", err)
	}
	if _, err := Prepare(root, "v0.5.0"); err != nil {
		t.Fatal(err)
	}
	if err := checkPublishable(filepath.Join(root, "admin/go.mod"), false); err != nil {
		t.Errorf("admin after Prepare: %v", err)
	}
	if err := checkPublishable(filepath.Join(root, "admin/go.mod"), true); err == nil {
		t.Error("installed with replaces: no error")
	}
}

func TestCheckProject(t *testing.T) {
	p := filepath.Join(t.TempDir(), "go.mod")
	for _, c := range []struct{ mod, err string }{
		{"module x\n\nrequire anetos.dev/anetos v0.5.0\n", ""},
		{"module x\n\nrequire anetos.dev/anetos v0.4.0\n", "not v0.5.0"},
		{"module x\n\nrequire anetos.dev/anetos v0.5.0\n\nreplace anetos.dev/anetos => ../a\n", "replace"},
		{"module x\n", "doesn't require"},
	} {
		if err := os.WriteFile(p, []byte(c.mod), 0o644); err != nil {
			t.Fatal(err)
		}
		err := checkProject(p, "v0.5.0")
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%q: %v, want %q", c.mod, err, c.err)
		}
	}
}

func TestRehearsalVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "version.go"), []byte("package anetos\n\nconst develVersion = \"v0.5.0-dev\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := rehearsalVersion(dir, time.Date(2026, 10, 11, 12, 0, 0, 7, time.UTC))
	if err != nil || v != "v0.5.0-rehearsal.20261011120000.7" {
		t.Errorf("rehearsalVersion = %q, %v", v, err)
	}
	if semver.Compare(v, "v0.5.0-rc.1") <= 0 || semver.Compare(v, "v0.5.0") >= 0 {
		t.Errorf("%s isn't between v0.5.0-rc.1 and v0.5.0", v)
	}
}

func TestUntracked(t *testing.T) {
	for _, c := range []struct {
		rel  string
		dir  bool
		mode os.FileMode
		want bool
	}{
		{"storage", true, 0, false}, // the package
		{"examples/files/storage", true, 0, true},
		{".git", true, 0, true},
		{".github", true, 0, false},
		{"cli/cmd/anetos/anetos", false, 0o755, true},
		{"scripts/update-kits.sh", false, 0o755, false},
		{"examples/notes/notes.db", false, 0o644, true},
		{".env.example", false, 0o644, false},
		{".env", false, 0o644, true},
		{"coverage.out", false, 0o644, true},
		{"go.mod", false, 0o644, false},
	} {
		if got := untracked(c.rel, c.dir, c.mode); got != c.want {
			t.Errorf("untracked(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

// TestRepository checks the repository itself: its published modules,
// and that cli, which go install installs, has no replace or exclude
// directive and requires a published core.
func TestRepository(t *testing.T) {
	mods, err := Published("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) < 14 || mods[0].Path != CoreModule || mods[1].Path != CoreModule+"/cli" {
		t.Errorf("published modules: %v", mods)
	}
	if err := checkPublishable("../../go.mod", true); err != nil {
		t.Error(err)
	}
	for _, m := range mods {
		f, err := readMod(filepath.Join("../../..", m.Dir, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Retract) != 1 || f.Retract[0].Low != Retracted[0] || f.Retract[0].High != Retracted[1] {
			t.Errorf("%s/go.mod doesn't retract [%s, %s]", m.Dir, Retracted[0], Retracted[1])
		}
	}
}

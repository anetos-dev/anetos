// SPDX-License-Identifier: Apache-2.0

// Package release prepares a release of the repository's modules and
// rehearses it: what `make release` and `make release-rehearsal` run
// (docs/contributing/releasing.md, design D319).
//
// Every published module (the core, cli, admin, drivers/*, plugins/*) is
// released with one version. Inside the repository the modules build
// against each other through replace directives (the drivers, admin,
// plugins, examples, bench) or cli/go.work (cli, which go install
// refuses with a replace); outside, only the require lines count, so a
// release sets every requirement of a published module to the version
// being released.
package release

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// CoreModule is the framework's module path; the other modules are
// below it.
const CoreModule = "anetos.dev/anetos"

// Module is a published module of the repository.
type Module struct {
	Dir  string // relative to the repository's root, "." for the core
	Path string // module path
}

// Tag is the git tag of m's version v: v for the core, dir/v otherwise.
func (m Module) Tag(v string) string {
	if m.Dir == "." {
		return v
	}
	return m.Dir + "/" + v
}

// Published lists the repository's published modules: the core, cli,
// admin and each module under drivers/ and plugins/, in that order.
func Published(root string) ([]Module, error) {
	dirs := []string{".", "cli", "admin"}
	for _, parent := range []string{"drivers", "plugins"} {
		entries, err := os.ReadDir(filepath.Join(root, parent))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join(root, parent, e.Name(), "go.mod")); e.IsDir() && err == nil {
				dirs = append(dirs, parent+"/"+e.Name())
			}
		}
	}
	var mods []Module
	for _, d := range dirs {
		f, err := readMod(filepath.Join(root, d, "go.mod"))
		if err != nil {
			return nil, err
		}
		mods = append(mods, Module{Dir: d, Path: f.Module.Mod.Path})
	}
	return mods, nil
}

// Retracted are the versions every published module retracts: the
// tags from before Anetos was announced.
var Retracted = [2]string{"v0.1.0", "v0.4.0"}

// CheckVersion reports whether v is a version a release can have:
// canonical semver (v0.5.0, v0.5.0-rc.1) without build metadata, major
// version 0 or 1 (v2 would need /v2 module paths), and not in the
// retracted range.
func CheckVersion(v string) error {
	if !semver.IsValid(v) || semver.Canonical(v) != v || semver.Build(v) != "" {
		return fmt.Errorf("release: %q isn't a version like v0.5.0 or v0.5.0-rc.1", v)
	}
	if m := semver.Major(v); m != "v0" && m != "v1" {
		return fmt.Errorf("release: %s needs module paths ending in /%s", v, m)
	}
	if semver.Compare(v, Retracted[0]) >= 0 && semver.Compare(v, Retracted[1]) <= 0 {
		return fmt.Errorf("release: %s is in the retracted range [%s, %s]", v, Retracted[0], Retracted[1])
	}
	return nil
}

// Prepare sets, in every go.mod under root and in the extra go.mod files
// (another repository's, such as the locales'), each requirement of a
// published module to version v. In a module that requires a published
// module without replacing it (cli), it drops that module's lines from
// go.sum: they name the old version, and the new one has no checksum
// until it's published. It returns the files it changed, relative to
// the current directory.
func Prepare(root, v string, extra ...string) ([]string, error) {
	if err := CheckVersion(v); err != nil {
		return nil, err
	}
	mods, err := Published(root)
	if err != nil {
		return nil, err
	}
	published := map[string]bool{}
	for _, m := range mods {
		published[m.Path] = true
	}
	files, err := goModFiles(root)
	if err != nil {
		return nil, err
	}
	files = append(files, extra...)
	var changed []string
	for _, p := range files {
		ok, err := setRequirements(p, v, published)
		if err != nil {
			return changed, err
		}
		if ok {
			changed = append(changed, p)
		}
	}
	return changed, nil
}

// goModFiles lists the go.mod files under root, skipping hidden
// directories, testdata and tmp.
func goModFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root {
			n := d.Name()
			if strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || n == "testdata" || n == "tmp" || n == "node_modules" {
				return filepath.SkipDir
			}
		}
		if !d.IsDir() && d.Name() == "go.mod" {
			files = append(files, p)
		}
		return nil
	})
	return files, err
}

func readMod(p string) (*modfile.File, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	f, err := modfile.Parse(p, data, nil)
	if err != nil {
		return nil, err
	}
	if f.Module == nil {
		return nil, fmt.Errorf("%s: no module line", p)
	}
	return f, nil
}

// setRequirements rewrites the go.mod at p, and its go.sum when needed.
func setRequirements(p, v string, published map[string]bool) (bool, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return false, err
	}
	f, err := modfile.Parse(p, data, nil)
	if err != nil {
		return false, err
	}
	replaced := map[string]bool{}
	for _, r := range f.Replace {
		replaced[r.Old.Path] = true
	}
	var unreplaced []string
	changed := false
	for _, r := range f.Require {
		if !published[r.Mod.Path] || r.Mod.Version == v {
			continue
		}
		if err := f.AddRequire(r.Mod.Path, v); err != nil {
			return false, err
		}
		changed = true
		if !replaced[r.Mod.Path] {
			unreplaced = append(unreplaced, r.Mod.Path)
		}
	}
	if !changed {
		return false, nil
	}
	f.Cleanup()
	out, err := f.Format()
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(p, out, 0o644); err != nil {
		return false, err
	}
	if len(unreplaced) > 0 {
		if err := dropSums(filepath.Join(filepath.Dir(p), "go.sum"), unreplaced); err != nil {
			return true, err
		}
	}
	return true, nil
}

// dropSums removes the lines of paths' modules from the go.sum at p.
func dropSums(p string, paths []string) error {
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if mod, _, ok := strings.Cut(line, " "); ok && slices.Contains(paths, mod) {
			continue
		}
		out.WriteString(line + "\n")
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return os.WriteFile(p, out.Bytes(), 0o644)
}

// changelogSection matches a release's heading in a CHANGELOG.md.
var changelogSection = regexp.MustCompile(`(?m)^## \[([^\]]+)\]`)

// CheckChangelogs reports the CHANGELOG.md files with no section for v
// (without its "v": "## [0.5.0]"): the repository's, which covers the
// core, cli and admin, and those of the modules that have their own (the
// drivers and plugins). A pre-release
// (v0.5.0-rc.1) is checked against nothing: its changes stay under
// "Unreleased" until the release.
func CheckChangelogs(root, v string) error {
	if semver.Prerelease(v) != "" {
		return nil
	}
	mods, err := Published(root)
	if err != nil {
		return err
	}
	want := strings.TrimPrefix(v, "v")
	var missing []string
	for _, m := range mods {
		p := filepath.Join(root, m.Dir, "CHANGELOG.md")
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) && m.Dir != "." {
			continue // in the repository's CHANGELOG.md
		}
		if err != nil {
			return err
		}
		found := false
		for _, s := range changelogSection.FindAllSubmatch(data, -1) {
			if string(s[1]) == want {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, filepath.Join(m.Dir, "CHANGELOG.md"))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("release: no \"## [%s]\" section in %s", want, strings.Join(missing, ", "))
	}
	return nil
}

// Tags returns the git tags of version v for the modules, in their order.
func Tags(mods []Module, v string) []string {
	tags := make([]string, len(mods))
	for i, m := range mods {
		tags[i] = m.Tag(v)
	}
	return tags
}

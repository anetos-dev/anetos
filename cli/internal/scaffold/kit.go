// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"anetos.dev/anetos/cli/internal/tailwind"
)

// KitRecordFile is where a project records its design kit: which kit,
// which release of its framework, and the digest of every file the kit
// wrote, so anetos css:use can tell the files the developer changed
// (design D304).
const KitRecordFile = "views/ui/kit.json"

// KitRecord is the content of views/ui/kit.json.
type KitRecord struct {
	Kit     string            `json:"kit"`
	Version string            `json:"version,omitempty"` // the CSS framework's (KitVersions)
	Anetos  string            `json:"anetos,omitempty"`  // the CLI that wrote it
	Files   map[string]string `json:"files"`             // path (slashes) → SHA-256, hex
}

// newKitRecord records files, a kit's as rendered.
func newKitRecord(kit string, files []KitFile) KitRecord {
	r := KitRecord{Kit: kit, Version: KitVersions[kit], Anetos: cliVersion(), Files: map[string]string{}}
	for _, f := range files {
		r.Files[f.Rel] = digest(f.Content)
	}
	return r
}

func (r KitRecord) file() KitFile {
	b, err := json.MarshalIndent(r, "", "\t") // map keys sorted: a stable file
	if err != nil {
		panic(err) // strings only
	}
	return KitFile{KitRecordFile, append(b, '\n')}
}

// digest is a file's SHA-256 with its lines ending in \n: git on Windows
// may check text files out with \r\n, which isn't a change.
func digest(b []byte) string {
	sum := sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

// sameText reports whether two files are equal but for line endings.
func sameText(a, b []byte) bool {
	return bytes.Equal(bytes.ReplaceAll(a, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
}

// cliVersion is this CLI's module version, "(devel)" when built from a
// checkout.
func cliVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// ErrBadKitRecord is ReadKitRecord's error for a kit.json it can't use:
// not JSON, no kit, or a file outside the kit's folders.
var ErrBadKitRecord = errors.New("not a usable kit record")

// ReadKitRecord reads the project's views/ui/kit.json; an error wrapping
// fs.ErrNotExist when it has none (made before it existed, or by hand),
// ErrBadKitRecord when it can't be used. Its files can only be in
// views/ui or public/static: css:use removes those of an old kit.
func ReadKitRecord(root string) (KitRecord, error) {
	var r KitRecord
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(KitRecordFile)))
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("%s: %w: %w", KitRecordFile, ErrBadKitRecord, err)
	}
	if r.Kit == "" {
		return r, fmt.Errorf("%s: %w: it names no kit", KitRecordFile, ErrBadKitRecord)
	}
	for rel := range r.Files {
		if !kitPath(rel) {
			return r, fmt.Errorf("%s: %w: %q isn't a file of views/ui or public/static", KitRecordFile, ErrBadKitRecord, rel)
		}
	}
	return r, nil
}

// kitPath reports whether rel can be a kit's file: a clean, local path
// with slashes in views/ui or public/static, not the record itself.
func kitPath(rel string) bool {
	return path.Clean(rel) == rel && filepath.IsLocal(filepath.FromSlash(rel)) && rel != KitRecordFile &&
		(strings.HasPrefix(rel, "views/ui/") || strings.HasPrefix(rel, "public/static/"))
}

// KitChange is what UseKit did, or would do.
type KitChange struct {
	From, To string   // the kits ("" when the project had no usable record)
	Written  []string // the kit's files written (paths with slashes)
	Removed  []string // the old kit's files it no longer has
	Changed  []string // files that differed from what the old kit wrote, or weren't its: overwritten or removed with force
	Own      []string // the app's own files in views/ui, left as they are
	Others   []string // without a record: public/static's files that aren't the new kit's, left as they are
}

// ErrKitChanged is UseKit's error when kit files were changed since the
// kit wrote them (or a file it would write isn't the old kit's): without
// force, it writes nothing.
var ErrKitChanged = errors.New("files changed since the kit wrote them")

// ErrNoKitRecord is UseKit's error for a project without views/ui/kit.json
// and without force.
var ErrNoKitRecord = errors.New("the project has no " + KitRecordFile + ", so changed files can't be told apart")

// UseKit replaces the project's design kit (in root, of module module)
// with kit: it writes kit's views/ui files and public/static files,
// removes those of the old kit that kit has not, and records the new kit
// in views/ui/kit.json. The layout, the pages and the app's own files
// are left alone. A file the old kit wrote that changed since, or a file
// in the new kit's way that the old kit didn't write (or a symbolic
// link), stops it with ErrKitChanged (listed in the change) unless
// force; so does a project without a usable record (ErrNoKitRecord,
// ErrBadKitRecord), which force treats as recording nothing. With the
// kit the project already has, it updates the kit's files to this
// CLI's. Each file is written through a temporary file renamed into
// place, the record last: a switch that fails part way can be run again.
func UseKit(root, module, kit string, force bool) (KitChange, error) {
	var c KitChange
	if !slices.Contains(Kits, kit) {
		return c, fmt.Errorf("no design kit %q (kits: %s)", kit, strings.Join(Kits, ", "))
	}
	c.To = kit
	old, err := ReadKitRecord(root)
	recorded := err == nil
	switch {
	case recorded:
		c.From = old.Kit
	case (errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrBadKitRecord)) && force:
		old = KitRecord{Files: map[string]string{}}
	case errors.Is(err, fs.ErrNotExist):
		return c, ErrNoKitRecord
	default:
		return c, err
	}
	files, err := renderKit(kit, projectData{Module: module, Kit: kit})
	if err != nil {
		return c, err
	}
	next := map[string][]byte{}
	for _, f := range files {
		next[f.Rel] = f.Content
	}
	// Tailwind to Tailwind: the compiled app.css stays (css:build updates
	// it), with the classes of the app's own pages.
	keepCSS := old.Kit == "tailwind" && kit == "tailwind"
	// What would be lost: a recorded file that changed (written over or
	// removed), and a file in the way that the old kit didn't write.
	changed := func(rel string) bool {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return true // written through, it would change another file
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return false // gone: nothing to lose
		}
		want, recorded := old.Files[rel]
		if nb, ok := next[rel]; ok && sameText(b, nb) {
			return false // already the new kit's
		}
		if old.Kit == "tailwind" && rel == tailwind.Output {
			return false // compiled from the kit's files, which are checked
		}
		return !recorded || digest(b) != want
	}
	for rel := range next {
		if changed(rel) {
			c.Changed = append(c.Changed, rel)
		}
	}
	for rel := range old.Files {
		if _, kept := next[rel]; !kept {
			if changed(rel) {
				c.Changed = append(c.Changed, rel)
			}
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				c.Removed = append(c.Removed, rel)
			}
		}
	}
	sort.Strings(c.Changed)
	sort.Strings(c.Removed)
	if len(c.Changed) > 0 && !force {
		return c, ErrKitChanged
	}
	for _, f := range files {
		if keepCSS && f.Rel == tailwind.Output {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(f.Rel))
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			if b, err := os.ReadFile(p); err == nil && sameText(b, f.Content) {
				continue
			}
		}
		if err := writeReplace(p, f.Content); err != nil {
			return c, err
		}
		c.Written = append(c.Written, f.Rel)
	}
	for _, rel := range c.Removed {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return c, err
		}
		if base, ok := strings.CutSuffix(p, ".templ"); ok { // its generated code too
			_ = os.Remove(base + "_templ.go")
		}
	}
	rec := newKitRecord(kit, files).file()
	if err := writeReplace(filepath.Join(root, filepath.FromSlash(rec.Rel)), rec.Content); err != nil {
		return c, err
	}
	// The app's own files in views/ui: not the kit's, not generated.
	entries, _ := os.ReadDir(filepath.Join(root, "views", "ui"))
	for _, e := range entries {
		rel := "views/ui/" + e.Name()
		if _, ok := next[rel]; ok || e.IsDir() || rel == KitRecordFile || strings.HasSuffix(rel, "_templ.go") {
			continue
		}
		c.Own = append(c.Own, rel)
	}
	if !recorded { // the old kit's files can't be told from the app's
		entries, _ := os.ReadDir(filepath.Join(root, "public", "static"))
		for _, e := range entries {
			if rel := "public/static/" + e.Name(); !e.IsDir() && next[rel] == nil {
				c.Others = append(c.Others, rel)
			}
		}
	}
	return c, nil
}

// writeReplace writes content to file through a temporary file renamed
// into place, replacing a file (or a symbolic link) there.
func writeReplace(file string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // after the rename, there's nothing to remove
	_, err = tmp.Write(content)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// AddNavMenu adds nav.menu ("Menu", the name of the header's menu button
// in the Bootstrap and Bulma kits) to locales/en/app.yaml when it lacks
// it, as projects made before v0.5 do; it reports whether it did. A file
// it can't edit safely (nav in flow style, say) is left as it is, with
// an error saying what to add; so is one the edit wouldn't leave loading.
func AddNavMenu(root string) (bool, error) {
	p := filepath.Join(root, "locales", "en", "app.yaml")
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	manual := errors.New(`add nav.menu ("Menu") to locales/en/app.yaml yourself`)
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return false, fmt.Errorf("locales/en/app.yaml: %w; %w", err, manual)
	}
	nl := "\n"
	if bytes.Contains(b, []byte("\r\n")) {
		nl = "\r\n"
	}
	var out []byte
	switch {
	case len(doc.Content) == 0: // empty
		out = []byte("nav:" + nl + `  menu: "Menu"` + nl)
	case doc.Content[0].Kind != yaml.MappingNode:
		return false, fmt.Errorf("locales/en/app.yaml isn't a mapping; %w", manual)
	}
	if out == nil {
		top := doc.Content[0]
		var nav *yaml.Node
		for i := 0; i+1 < len(top.Content); i += 2 {
			if top.Content[i].Value == "nav" {
				nav = top.Content[i+1]
			}
		}
		switch {
		case nav == nil:
			out = bytes.Clone(b)
			if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
				out = append(out, nl...)
			}
			out = append(out, "nav:"+nl+`  menu: "Menu"`+nl...)
		case nav.Kind != yaml.MappingNode || nav.Style&yaml.FlowStyle != 0 || len(nav.Content) == 0:
			return false, fmt.Errorf("locales/en/app.yaml's nav isn't a block of keys; %w", manual)
		default:
			for i := 0; i < len(nav.Content); i += 2 {
				if nav.Content[i].Value == "menu" {
					return false, nil
				}
			}
			// Before nav's first key, at its column.
			first := nav.Content[0]
			lines := strings.SplitAfter(string(b), "\n")
			if first.Line < 1 || first.Line > len(lines) {
				return false, manual
			}
			line := strings.Repeat(" ", first.Column-1) + `menu: "Menu"` + nl
			out = []byte(strings.Join(slices.Insert(lines, first.Line-1, line), ""))
		}
	}
	// The result must load, with nav.menu.
	var check struct {
		Nav struct {
			Menu string `yaml:"menu"`
		} `yaml:"nav"`
	}
	if err := yaml.Unmarshal(out, &check); err != nil || check.Nav.Menu != "Menu" {
		return false, fmt.Errorf("locales/en/app.yaml: %w", manual)
	}
	return true, os.WriteFile(p, out, 0o644)
}

// LocalesWithoutNavMenu lists the project's locales other than en whose
// app.yaml has no nav.menu (lang:check reports them).
func LocalesWithoutNavMenu(root string) []string {
	dirs, _ := os.ReadDir(filepath.Join(root, "locales"))
	var missing []string
	for _, d := range dirs {
		if !d.IsDir() || d.Name() == "en" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, "locales", d.Name(), "app.yaml"))
		if err != nil {
			continue
		}
		var check struct {
			Nav struct {
				Menu *string `yaml:"menu"`
			} `yaml:"nav"`
		}
		if yaml.Unmarshal(b, &check) == nil && check.Nav.Menu == nil {
			missing = append(missing, d.Name())
		}
	}
	return missing
}

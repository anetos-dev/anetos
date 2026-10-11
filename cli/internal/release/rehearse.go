// SPDX-License-Identifier: Apache-2.0

package release

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/mod/zip"
)

// Rehearsal is a release rehearsed against a module proxy on disk: the
// repository's committed files, with every published module set to a
// made-up version, zipped as the module proxy would serve them, then
// installed and used as a user would.
type Rehearsal struct {
	Root    string    // the repository
	Out     io.Writer // progress and the go commands' output
	Keep    bool      // keep the temporary directory and the module cache's copies, for a look after
	Stacks  []string  // the stacks of `anetos new` to try; default web and api
	Version string    // default <develVersion's release>-rehearsal.<UTC time>, v0.5.0-rehearsal.…
}

// Run rehearses the release: it exports the committed files (git
// archive; the working tree minus untracked-looking files outside a git
// checkout), runs [Prepare] on them, writes each published module to a
// file GOPROXY, installs the anetos tool from it with go install, makes
// a project per stack with anetos new (no --replace) and, in the web
// one, runs make:auth and make:admin and adds the postmark plugin, then
// builds, vets and tests each project. Other modules come from the
// GOPROXY in effect.
func (r Rehearsal) Run(ctx context.Context) error {
	if r.Out == nil {
		r.Out = io.Discard
	}
	if len(r.Stacks) == 0 {
		r.Stacks = []string{"web", "api"}
	}
	tmp, err := os.MkdirTemp("", "anetos-rehearsal-")
	if err != nil {
		return err
	}
	if r.Keep {
		fmt.Fprintln(r.Out, "rehearsal: keeping", tmp)
	} else {
		defer os.RemoveAll(tmp)
	}
	src, proxy, bin, apps := filepath.Join(tmp, "src"), filepath.Join(tmp, "proxy"), filepath.Join(tmp, "bin"), filepath.Join(tmp, "apps")
	for _, d := range []string{src, proxy, bin, apps} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	how, err := export(ctx, r.Root, src)
	if err != nil {
		return fmt.Errorf("rehearsal: exporting the repository: %w", err)
	}
	if r.Version == "" {
		if r.Version, err = rehearsalVersion(src, time.Now()); err != nil {
			return fmt.Errorf("rehearsal: %w", err)
		}
	}
	fmt.Fprintf(r.Out, "rehearsal: %s exported (%s), version %s\n", r.Root, how, r.Version)
	if _, err := Prepare(src, r.Version); err != nil {
		return fmt.Errorf("rehearsal: %w", err)
	}
	mods, err := Published(src)
	if err != nil {
		return err
	}
	for _, m := range mods {
		if err := checkPublishable(filepath.Join(src, m.Dir, "go.mod"), m.Path == CoreModule+"/cli"); err != nil {
			return fmt.Errorf("rehearsal: %w", err)
		}
		if err := addToProxy(proxy, filepath.Join(src, m.Dir), m.Path, r.Version); err != nil {
			return fmt.Errorf("rehearsal: zipping %s: %w", m.Path, err)
		}
	}
	fmt.Fprintf(r.Out, "rehearsal: %d modules in the file GOPROXY\n", len(mods))

	env, err := rehearsalEnv(ctx, proxy, bin)
	if err != nil {
		return fmt.Errorf("rehearsal: %w", err)
	}
	if !r.Keep {
		defer forget(ctx, mods, r.Version)
	}
	run := func(dir string, name string, args ...string) error {
		fmt.Fprintf(r.Out, "rehearsal: (%s) %s %s\n", filepath.Base(dir), filepath.Base(name), strings.Join(args, " "))
		c := exec.CommandContext(ctx, name, args...)
		c.Dir, c.Env, c.Stdout, c.Stderr = dir, env, r.Out, r.Out
		return c.Run()
	}
	if err := run(apps, "go", "install", CoreModule+"/cli/cmd/anetos@"+r.Version); err != nil {
		return fmt.Errorf("rehearsal: go install the anetos tool: %w", err)
	}
	tool := filepath.Join(bin, "anetos")
	for _, stack := range r.Stacks {
		name := "rehearsal" + stack
		if err := run(apps, tool, "new", "--stack="+stack, name); err != nil {
			return fmt.Errorf("rehearsal: anetos new --stack=%s: %w", stack, err)
		}
		dir := filepath.Join(apps, name)
		if err := checkProject(filepath.Join(dir, "go.mod"), r.Version); err != nil {
			return fmt.Errorf("rehearsal: the %s project: %w", stack, err)
		}
		steps := [][]string{{"make:auth"}}
		if stack == "web" {
			// The modules a project adds later: the admin and a plugin,
			// at the tool's version (no version given).
			steps = append(steps, []string{"make:admin"}, []string{"add", "--yes", CoreModule + "/plugins/postmark"})
		}
		for _, args := range steps {
			if err := run(dir, tool, args...); err != nil {
				return fmt.Errorf("rehearsal: anetos %s in the %s project: %w", args[0], stack, err)
			}
		}
		if err := checkProject(filepath.Join(dir, "go.mod"), r.Version); err != nil {
			return fmt.Errorf("rehearsal: the %s project: %w", stack, err)
		}
		for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}, {"test", "./..."}} {
			if err := run(dir, "go", args...); err != nil {
				return fmt.Errorf("rehearsal: go %s in the %s project: %w", args[0], stack, err)
			}
		}
	}
	fmt.Fprintln(r.Out, "rehearsal: ok")
	return nil
}

var develVersion = regexp.MustCompile(`(?m)^const develVersion = "(v[0-9]+\.[0-9]+\.[0-9]+)-dev"$`)

// rehearsalVersion is the version of a rehearsal of the tree at src: the
// release its version.go is preparing, as a pre-release with the time
// (v0.5.0-rehearsal.20261011120000.123456789), above the earlier
// pre-releases and below the release, so the plugins' Requires ranges
// accept it.
func rehearsalVersion(src string, now time.Time) (string, error) {
	data, err := os.ReadFile(filepath.Join(src, "version.go"))
	if err != nil {
		return "", err
	}
	m := develVersion.FindSubmatch(data)
	if m == nil {
		return "", errors.New("version.go: no develVersion like \"v0.5.0-dev\"")
	}
	now = now.UTC()
	v := fmt.Sprintf("%s-rehearsal.%s.%d", m[1], now.Format("20060102150405"), now.Nanosecond())
	if !semver.IsValid(v) {
		return "", fmt.Errorf("%s isn't a valid version", v)
	}
	return v, nil
}

// rehearsalEnv is the environment of the rehearsal's go commands: the
// file GOPROXY first, then the one in effect for everything else (which
// must not be off); every module through a proxy (no GOPRIVATE or
// GONOPROXY, which would skip the file GOPROXY for anetos.dev); no
// checksum database for anetos.dev, whose rehearsal versions it doesn't
// know; no workspace, and the tools installed in bin.
func rehearsalEnv(ctx context.Context, proxy, bin string) ([]string, error) {
	upstream := goEnv(ctx, "GOPROXY")
	if upstream == "" {
		upstream = "https://proxy.golang.org,direct"
	}
	if upstream == "off" {
		return nil, errors.New("GOPROXY=off: the rehearsal needs the module proxy for the other modules")
	}
	nosumdb := "anetos.dev"
	if v := goEnv(ctx, "GONOSUMDB"); v != "" {
		nosumdb += "," + v
	}
	set := map[string]string{
		"GOPROXY":     "file://" + filepath.ToSlash(proxy) + "," + upstream,
		"GONOPROXY":   "", // not "none", which breaks finding a nested module's package
		"GOPRIVATE":   "",
		"GONOSUMDB":   nosumdb,
		"GOFLAGS":     "-modcacherw",
		"GOWORK":      "off",
		"GOBIN":       bin,
		"GOTOOLCHAIN": "local",
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !hasKey(set, k) {
			env = append(env, kv)
		}
	}
	for k, v := range set {
		env = append(env, k+"="+v)
	}
	return env, nil
}

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

func goEnv(ctx context.Context, key string) string {
	out, err := exec.CommandContext(ctx, "go", "env", key).Output()
	if err != nil {
		return os.Getenv(key)
	}
	return strings.TrimSpace(string(out))
}

// forget removes the rehearsal's version of the modules from the module
// cache (the extracted module and its cache/download files), so
// rehearsals don't fill it.
func forget(ctx context.Context, mods []Module, v string) {
	cache := goEnv(ctx, "GOMODCACHE")
	if cache == "" {
		return
	}
	for _, m := range mods {
		esc, err := module.EscapePath(m.Path)
		if err != nil {
			continue
		}
		os.RemoveAll(filepath.Join(cache, filepath.FromSlash(esc)+"@"+v))
		dl := filepath.Join(cache, "cache", "download", filepath.FromSlash(esc), "@v")
		for _, ext := range []string{".info", ".mod", ".zip", ".ziphash", ".lock"} {
			os.Remove(filepath.Join(dl, v+ext))
		}
	}
}

// export copies the repository's committed files into dst: git archive
// of HEAD in a git checkout, else the working tree without what is
// usually untracked.
func export(ctx context.Context, root, dst string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return "working tree", copyTree(root, dst)
	}
	c := exec.CommandContext(ctx, "git", "-C", root, "archive", "--format=tar", "HEAD")
	c.Stderr = os.Stderr
	out, err := c.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := c.Start(); err != nil {
		return "", err
	}
	err = untar(out, dst)
	if err != nil {
		_, _ = io.Copy(io.Discard, out)
	}
	if werr := c.Wait(); err == nil {
		err = werr
	}
	return "git archive HEAD", err
}

// untar extracts the directories and regular files of a tar stream into
// dst; links and other entries are skipped.
func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		p := filepath.Join(dst, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(p, filepath.Clean(dst)+string(filepath.Separator)) {
			return fmt.Errorf("%s: outside the archive's directory", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o755|0o644)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}

// untracked reports whether a file or directory of the working tree (rel,
// slash-separated) is one the repository's .gitignore files keep out of
// git: what a working-tree export leaves out.
func untracked(rel string, dir bool, mode fs.FileMode) bool {
	name := path.Base(rel)
	if dir {
		return strings.HasPrefix(name, ".") && name != ".github" ||
			name == "tmp" || name == "bin" || name == "node_modules" || name == "Claude outputs" ||
			name == "storage" && strings.HasPrefix(rel, "examples/") // an example's files, not the package
	}
	ext := filepath.Ext(name)
	return mode&0o111 != 0 && ext == "" || // a built binary
		ext == ".test" || ext == ".out" || ext == ".db" ||
		strings.HasSuffix(name, ".db-shm") || strings.HasSuffix(name, ".db-wal") ||
		strings.HasPrefix(name, "coverage.") ||
		name == ".env" || strings.HasPrefix(name, ".env.") && name != ".env.example"
}

func copyTree(root, dst string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel != "." && untracked(filepath.ToSlash(rel), true, 0) {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || untracked(filepath.ToSlash(rel), false, info.Mode()) {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, info.Mode().Perm())
	})
}

// addToProxy writes module modPath at version v, from dir, to the file
// GOPROXY at proxy: its zip (as the module proxy makes it: without
// nested modules), go.mod, info, and the version list.
func addToProxy(proxy, dir, modPath, v string) error {
	esc, err := module.EscapePath(modPath)
	if err != nil {
		return err
	}
	d := filepath.Join(proxy, filepath.FromSlash(esc), "@v")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(d, v+".zip"))
	if err != nil {
		return err
	}
	err = zip.CreateFromDir(f, module.Version{Path: modPath, Version: v}, dir)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(d, v+".mod"), mod, 0o644); err != nil {
		return err
	}
	info, _ := json.Marshal(map[string]string{"Version": v, "Time": time.Now().UTC().Format(time.RFC3339)})
	if err := os.WriteFile(filepath.Join(d, v+".info"), info, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "list"), []byte(v+"\n"), 0o644)
}

// checkPublishable fails on a published go.mod that names a version of
// an anetos.dev module that won't exist (a pseudo-version of the
// placeholder kind) or doesn't retract the pre-announcement versions,
// or, for cli (installed with go install), has any replace or exclude
// directive.
func checkPublishable(p string, installed bool) error {
	f, err := readMod(p)
	if err != nil {
		return err
	}
	for _, r := range f.Require {
		if strings.HasPrefix(r.Mod.Path, "anetos.dev/") && strings.HasPrefix(r.Mod.Version, "v0.0.0-00010101000000-") {
			return fmt.Errorf("%s requires %s %s, a version that isn't published", p, r.Mod.Path, r.Mod.Version)
		}
	}
	if installed && (len(f.Replace) > 0 || len(f.Exclude) > 0) {
		return fmt.Errorf("%s has replace or exclude directives, which go install refuses", p)
	}
	for _, r := range f.Retract {
		if r.Low == Retracted[0] && r.High == Retracted[1] {
			return nil
		}
	}
	return fmt.Errorf("%s doesn't retract [%s, %s]", p, Retracted[0], Retracted[1])
}

// checkProject fails when a project anetos new made has a replace
// directive, or requires the core, or one of its modules, at another
// version than v.
func checkProject(p, v string) error {
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	f, err := modfile.Parse(p, data, nil)
	if err != nil {
		return err
	}
	if len(f.Replace) > 0 {
		return fmt.Errorf("go.mod has replace directives")
	}
	core := false
	for _, r := range f.Require {
		if r.Mod.Path != CoreModule && !strings.HasPrefix(r.Mod.Path, CoreModule+"/") {
			continue
		}
		if r.Mod.Version != v {
			return fmt.Errorf("go.mod requires %s %s, not %s", r.Mod.Path, r.Mod.Version, v)
		}
		core = core || r.Mod.Path == CoreModule
	}
	if !core {
		return fmt.Errorf("go.mod doesn't require %s", CoreModule)
	}
	return nil
}

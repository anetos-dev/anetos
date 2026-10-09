// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/modfile"

	"anetos.dev/anetos/cli/internal/modelgen"
	"anetos.dev/anetos/cli/internal/scaffold"
	"anetos.dev/anetos/cli/internal/tailwind"
)

const buildUsage = `Usage: anetos build [-o file] [--target=os/arch] [--version=v1.2.0] [--cgo] [-- go build flags]

Builds the app for production: one binary with everything in it (the
migrations, the templ views, the files in public/ and locales/). It runs
templ generate and anetos gen first (and, in a project of the tailwind
kit, Tailwind CSS: anetos css:build), then go build with -trimpath and
-ldflags=-s -w, and CGO_ENABLED=0 (a static binary; SQLite works without
cgo) unless --cgo. --target builds for another system (linux/amd64,
linux/arm64, windows/amd64…); GOOS and GOARCH work too when the tool
isn't run with go tool. Build in a git repository for the version and
commit that "<app> version" prints (Go records them; tag releases:
v1.2.0), or give the version with --version (in a container without the
repository's history). Flags after -- go to go build (-tags=…; an
-ldflags is added to the build's own).
`

func build(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos build", flag.ContinueOnError)
	out := fs.String("o", "", "the binary to write (default bin/<module name>)")
	target := fs.String("target", "", "the system to build for, os/arch (default: this one, or GOOS and GOARCH)")
	cgo := fs.Bool("cgo", false, "build with cgo (CGO_ENABLED=1)")
	ver := fs.String("version", "", "the app's version, for its version command (default: from git)")
	var extra []string
	if i := indexOf(args, "--"); i >= 0 {
		args, extra = args[:i], args[i+1:]
	}
	pos, code := parse(fs, args, stderr, buildUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "anetos build: unexpected argument %q\n\n%s", pos[0], buildUsage)
		return 2
	}
	if !validVersion(*ver) {
		fmt.Fprintf(stderr, "anetos build: --version %q: only letters, digits and . + - _ ~ /\n", *ver)
		return 2
	}
	var goos, goarch string
	if *target != "" {
		var ok bool
		goos, goarch, ok = strings.Cut(*target, "/")
		if !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
			fmt.Fprintf(stderr, "anetos build: --target %q: want os/arch, like linux/arm64\n", *target)
			return 2
		}
	}
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(stderr, "anetos build:", err)
		return 1
	}
	b := appBuild{root: root, out: *out, goos: goos, goarch: goarch, cgo: *cgo, version: *ver, extra: extra}
	bin, err := b.run(ctx, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "anetos build:", err)
		return 1
	}
	size := ""
	if st, err := os.Stat(bin); err == nil && st.Mode().IsRegular() {
		size = fmt.Sprintf(", %.1f MB", float64(st.Size())/(1<<20))
	}
	rel := bin
	if r, err := filepath.Rel(root, bin); err == nil && !strings.HasPrefix(r, "..") {
		rel = r
	}
	fmt.Fprintf(stdout, "built %s (%s/%s%s)\n", rel, b.goos, b.goarch, size)
	return 0
}

// validVersion reports whether v is safe in -ldflags: letters, digits
// and . + - _ ~ / (a tag, a pseudo-version, a branch name).
func validVersion(v string) bool {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(".+-_~/", r):
		default:
			return false
		}
	}
	return true
}

// appBuild is a production build of the app in root.
type appBuild struct {
	root, out    string
	goos, goarch string // the target; empty: go env's
	cgo          bool
	version      string
	extra        []string // go build flags
}

// run generates the code and builds the binary, returning its path. It
// sets b.goos and b.goarch to the target.
func (b *appBuild) run(ctx context.Context, stderr io.Writer) (string, error) {
	if b.goos == "" || b.goarch == "" {
		goos, goarch, err := goTarget(ctx, b.root)
		if err != nil {
			return "", err
		}
		if b.goos == "" {
			b.goos = goos
		}
		if b.goarch == "" {
			b.goarch = goarch
		}
	}
	out := b.out
	if out == "" {
		mod, err := os.ReadFile(filepath.Join(b.root, "go.mod"))
		if err != nil {
			return "", err
		}
		name := scaffold.BinaryName(modfile.ModulePath(mod))
		if b.goos == "windows" {
			name += ".exe"
		}
		out = filepath.Join(b.root, "bin", name)
	} else if !filepath.IsAbs(out) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		out = filepath.Join(wd, out)
	}
	// The generators run here: go tool builds templ for GOOS and GOARCH.
	host := []string{"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH}
	if hasTempl(b.root) {
		if err := runIn(ctx, b.root, host, stderr, "go", "tool", "templ", "generate", "-log-level=warn"); err != nil {
			return "", fmt.Errorf("templ generate: %w", err)
		}
	}
	changes, err := modelgen.Generate(b.root, "./...")
	if err == nil {
		err = modelgen.Apply(changes)
	}
	if err != nil {
		return "", fmt.Errorf("anetos gen: %w", err)
	}
	if tailwind.Uses(b.root) {
		if err := buildCSS(ctx, b.root, stderr, "anetos build"); err != nil {
			return "", err
		}
	}
	env := []string{"CGO_ENABLED=0", "GOOS=" + b.goos, "GOARCH=" + b.goarch}
	if b.cgo {
		env[0] = "CGO_ENABLED=1"
	}
	ldflags := "-s -w"
	if b.version != "" {
		ldflags += " -X anetos.dev/anetos.buildVersion=" + b.version
	}
	ldflags, extra := mergeLDFlags(ldflags, b.extra)
	args := append([]string{"build", "-trimpath", "-ldflags=" + ldflags, "-o", out}, extra...)
	if err := runIn(ctx, b.root, env, stderr, "go", append(args, ".")...); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}
	return out, nil
}

// goTarget is the system go build builds for: GOOS and GOARCH from the
// environment or go env -w.
func goTarget(ctx context.Context, dir string) (goos, goarch string, err error) {
	c := exec.CommandContext(ctx, "go", "env", "GOOS", "GOARCH")
	c.Dir = dir
	b, err := c.Output()
	if err != nil {
		return "", "", fmt.Errorf("go env: %w", err)
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return "", "", fmt.Errorf("go env GOOS GOARCH: %q", b)
	}
	return f[0], f[1], nil
}

// mergeLDFlags moves the -ldflags of go build flags into ours (go build
// keeps only the last), returning both.
func mergeLDFlags(ours string, flags []string) (string, []string) {
	var rest []string
	for i := 0; i < len(flags); i++ {
		f := flags[i]
		name, val, hasVal := strings.Cut(strings.TrimPrefix(f, "-"), "=")
		if name != "-ldflags" && name != "ldflags" {
			rest = append(rest, f)
			continue
		}
		if !hasVal && i+1 < len(flags) {
			i++
			val = flags[i]
		}
		if val != "" {
			ours += " " + val
		}
	}
	return ours, rest
}

// runIn runs a command in dir, with env added, its output to w.
func runIn(ctx context.Context, dir string, env []string, w io.Writer, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	c.Stdout, c.Stderr = w, w
	return c.Run()
}

// hasTempl reports whether the project has templ files (outside the
// directories of build output and dependencies).
func hasTempl(root string) bool {
	found := errors.New("found")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if d.IsDir() && p != root {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "bin", "tmp", "storage":
				return filepath.SkipDir
			}
		}
		if !d.IsDir() && strings.HasSuffix(p, ".templ") {
			return found
		}
		return nil
	})
	return errors.Is(err, found)
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

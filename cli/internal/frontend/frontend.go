// SPDX-License-Identifier: Apache-2.0

// Package frontend runs the Node side of a project with a frontend/
// folder (design D341–D345, D365): it checks Node's version, installs the
// packages with the package manager the lockfile names (npm without
// one), builds with Vite (into public/build, or an API project's
// single-page app into public/spa), and starts Vite's development server
// for anetos dev.
package frontend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos/cli/internal/scaffold"
)

// Dir is the frontend's folder in a project.
const Dir = "frontend"

// Uses reports whether the project in root has a frontend that Vite
// builds: frontend/package.json in a web project (its pages' scripts and
// styles, into public/build), or in an API project that serves it as a
// single-page app (vite.SPA: into public/spa). An API project's other
// frontend/ is an app of its own, which anetos dev, build and check leave
// alone.
func Uses(root string) bool {
	if scaffold.IsAPI(root) {
		return scaffold.ServesSPA(root)
	}
	_, err := os.Stat(filepath.Join(root, Dir, "package.json"))
	return err == nil
}

// SPA reports whether the project in root serves a single-page app that
// Vite builds ([scaffold.ServesSPA]): in development, the app is Vite's
// address, which forwards /api to the app.
func SPA(root string) bool { return scaffold.ServesSPA(root) }

// UsesTypes reports whether the app in root writes the frontend's
// TypeScript types: a Go file of its main package calls
// typescript.Register, which adds the app's types command.
func UsesTypes(root string) bool {
	files, _ := filepath.Glob(filepath.Join(root, "*.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for line := range strings.Lines(string(b)) {
			if code, _, _ := strings.Cut(line, "//"); strings.Contains(code, "typescript.Register(") {
				return true // a call, not a comment's
			}
		}
	}
	return false
}

// Manager is a package manager: the command and its arguments to install
// the packages, and to run a package.json script.
type Manager struct {
	// Name is the command: npm, pnpm, yarn or bun.
	Name string
	// Lockfile is its lockfile, "" for npm without one.
	Lockfile string
	// Install installs the packages the lockfile pins, failing if
	// package.json asks for others: for builds.
	Install []string
	// Update installs the packages package.json asks for, updating the
	// lockfile: in development, after an edit of package.json.
	Update []string
}

var managers = []Manager{
	{Name: "pnpm", Lockfile: "pnpm-lock.yaml", Install: []string{"install", "--frozen-lockfile"}, Update: []string{"install"}},
	{Name: "yarn", Lockfile: "yarn.lock", Install: []string{"install"}, Update: []string{"install"}},
	{Name: "bun", Lockfile: "bun.lock", Install: []string{"install", "--frozen-lockfile"}, Update: []string{"install"}},
	{Name: "bun", Lockfile: "bun.lockb", Install: []string{"install", "--frozen-lockfile"}, Update: []string{"install"}},
	{Name: "npm", Lockfile: "package-lock.json", Install: []string{"ci"}, Update: []string{"install"}},
}

// ManagerOf returns the package manager of the frontend in dir, by its
// lockfile: npm (npm install) when it has none.
func ManagerOf(dir string) Manager {
	for _, m := range managers {
		if _, err := os.Stat(filepath.Join(dir, m.Lockfile)); err == nil {
			return m
		}
	}
	return Manager{Name: "npm", Install: []string{"install"}, Update: []string{"install"}}
}

// MinNode is the Node.js the frontend's Vite needs: 20.19 or later in
// 20, or 22.12 or later.
const MinNode = "20.19 or 22.12"

var nodeVersion = regexp.MustCompile(`^v(\d+)\.(\d+)\.`)

// CheckNode returns an error unless node is on the PATH in a version Vite
// runs on, saying what to install.
func CheckNode(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "node", "--version").Output()
	if err != nil {
		return fmt.Errorf("the frontend (frontend/) needs Node.js %s or later, which isn't installed (https://nodejs.org)", MinNode)
	}
	return checkNodeVersion(strings.TrimSpace(string(out)))
}

// checkNodeVersion checks `node --version`'s output ("v22.12.0").
func checkNodeVersion(v string) error {
	m := nodeVersion.FindStringSubmatch(v)
	if m == nil {
		return nil // an unusual build: let Vite say
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 20 || major == 20 && minor < 19 || major == 21 || major == 22 && minor < 12 {
		return fmt.Errorf("the frontend (frontend/) needs Node.js %s or later; this is %s (https://nodejs.org)", MinNode, v)
	}
	return nil
}

// NeedsInstall reports whether the frontend in dir needs its packages
// installed: it has no node_modules, or its lockfile (or package.json)
// changed after them.
func NeedsInstall(dir string) bool {
	nm, err := os.Stat(filepath.Join(dir, "node_modules"))
	if err != nil {
		return true
	}
	m := ManagerOf(dir)
	for _, f := range []string{"package.json", m.Lockfile} {
		if f == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, f)); err == nil && st.ModTime().After(nm.ModTime()) {
			return true
		}
	}
	return false
}

// Install installs the packages of the frontend in dir with its package
// manager, its output going to w, and marks node_modules current. With
// update (development), it installs what package.json asks for and
// updates the lockfile; otherwise (builds), what the lockfile pins.
func Install(ctx context.Context, dir string, w io.Writer, update bool) error {
	if err := CheckNode(ctx); err != nil {
		return err
	}
	m := ManagerOf(dir)
	if _, err := exec.LookPath(m.Name); err != nil {
		return fmt.Errorf("the frontend's lockfile (%s) is %s's, which isn't installed", m.Lockfile, m.Name)
	}
	args := m.Install
	if update {
		args = m.Update
	}
	if err := run(ctx, dir, w, m.Name, args...); err != nil {
		return fmt.Errorf("%s %s: %w", m.Name, strings.Join(args, " "), err)
	}
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, "node_modules"), now, now) // newer than the lockfile it installed
	return nil
}

// Build installs the packages if they need it, then runs the frontend's
// build script (vite build: into public/build, or public/spa), its
// output going to w.
func Build(ctx context.Context, dir string, w io.Writer) error {
	if err := CheckNode(ctx); err != nil {
		return err
	}
	if NeedsInstall(dir) {
		if err := Install(ctx, dir, w, false); err != nil {
			return err
		}
	}
	m := ManagerOf(dir)
	if err := run(ctx, dir, w, m.Name, "run", "build"); err != nil {
		return fmt.Errorf("%s run build in frontend/: %w", m.Name, err)
	}
	return nil
}

func run(ctx context.Context, dir string, w io.Writer, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Stdout, c.Stderr = w, w
	c.Env = append(os.Environ(), "NO_COLOR=1", "CI=1")
	return c.Run()
}

// ViteScript returns the frontend's Vite, node_modules/vite/bin/vite.js,
// which anetos dev runs with node (every package manager installs it
// there), or an error saying to install it.
func ViteScript(dir string) (string, error) {
	p := filepath.Join(dir, "node_modules", "vite", "bin", "vite.js")
	if _, err := os.Stat(p); err != nil {
		return "", errors.New("frontend/node_modules has no vite: add it to frontend/package.json's devDependencies")
	}
	return p, nil
}

// SPADevArgs are the arguments anetos dev runs the development server of
// an API project's single-page app with: at the address its vite.config
// sets (the one HTTP_SPA_ORIGINS lists), without clearing the terminal.
func SPADevArgs(script string) []string {
	return []string{script, "--clearScreen", "false"}
}

// DevArgs are the arguments anetos dev runs Vite's development server
// with: on 127.0.0.1 and the port it chose, whatever vite.config says,
// and without clearing the terminal, which shows the app's output too.
func DevArgs(script string, port int) []string {
	return []string{script, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--strictPort", "--clearScreen", "false"}
}

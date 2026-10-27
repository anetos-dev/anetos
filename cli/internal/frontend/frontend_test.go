// SPDX-License-Identifier: Apache-2.0

package frontend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckNodeVersion(t *testing.T) {
	for v, ok := range map[string]bool{
		"v20.19.0": true, "v20.18.3": false, "v21.7.0": false, "v22.11.0": false, "v22.12.0": true,
		"v24.1.0": true, "v18.20.0": false, "v16.0.0": false, "something else": true,
	} {
		err := checkNodeVersion(v)
		if (err == nil) != ok || err != nil && !strings.Contains(err.Error(), "needs Node.js 20.19 or 22.12 or later; this is "+v) {
			t.Errorf("%s: %v", v, err)
		}
	}
}

// The lockfile names the package manager; npm without one.
func TestManagerOf(t *testing.T) {
	for lock, want := range map[string]string{"": "npm install", "package-lock.json": "npm ci", "pnpm-lock.yaml": "pnpm install --frozen-lockfile",
		"yarn.lock": "yarn install", "bun.lock": "bun install --frozen-lockfile", "bun.lockb": "bun install --frozen-lockfile"} {
		dir := t.TempDir()
		if lock != "" {
			write(t, filepath.Join(dir, lock), "")
		}
		m := ManagerOf(dir)
		if got := m.Name + " " + strings.Join(m.Install, " "); got != want || m.Lockfile != lock {
			t.Errorf("%q: %s (%s)", lock, got, m.Lockfile)
		}
	}
}

// Packages are installed when node_modules is missing or older than
// package.json or the lockfile.
func TestNeedsInstall(t *testing.T) {
	dir := t.TempDir()
	if Uses(dir) {
		t.Error("Uses without frontend/package.json")
	}
	// An API project's frontend/ is an app of its own.
	api := t.TempDir()
	write(t, filepath.Join(api, "routes", "api.go"), "package routes\n")
	write(t, filepath.Join(api, Dir, "package.json"), "{}")
	if Uses(api) || SPA(api) {
		t.Error("Uses in an api project")
	}
	// One that serves it as its SPA (vite.SPA in main.go): it is the
	// app's.
	write(t, filepath.Join(api, "main.go"), "package main\n\n// vite.SPA(app, public.Files) in setup\n")
	if Uses(api) || SPA(api) {
		t.Error("Uses in an api project that names vite.SPA in a comment")
	}
	write(t, filepath.Join(api, "main.go"), "package main\n\nfunc setup() {\n\tspa, err := vite.SPA(app, public.Files)\n}\n")
	if !Uses(api) || !SPA(api) || SPA(dir) {
		t.Error("an api project's SPA")
	}
	if got := strings.Join(SPADevArgs("vite.js"), " "); got != "vite.js --clearScreen false" {
		t.Errorf("SPADevArgs: %s", got)
	}
	fe := filepath.Join(dir, Dir)
	write(t, filepath.Join(fe, "package.json"), "{}")
	write(t, filepath.Join(fe, "package-lock.json"), "{}")
	if !Uses(dir) || !NeedsInstall(fe) {
		t.Fatal("no node_modules")
	}
	write(t, filepath.Join(fe, "node_modules", ".package-lock.json"), "{}")
	old := time.Now().Add(-time.Hour)
	for _, f := range []string{"package.json", "package-lock.json"} {
		if err := os.Chtimes(filepath.Join(fe, f), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if NeedsInstall(fe) {
		t.Error("node_modules newer than both")
	}
	now := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(fe, "package-lock.json"), now, now); err != nil {
		t.Fatal(err)
	}
	if !NeedsInstall(fe) {
		t.Error("a lockfile newer than node_modules")
	}
	if _, err := ViteScript(fe); err == nil || !strings.Contains(err.Error(), "has no vite") {
		t.Errorf("ViteScript without vite: %v", err)
	}
	script := filepath.Join(fe, "node_modules", "vite", "bin", "vite.js")
	write(t, script, "")
	if p, err := ViteScript(fe); err != nil || p != script {
		t.Errorf("ViteScript: %q, %v", p, err)
	}
	if got := strings.Join(DevArgs("vite.js", 5173), " "); got != "vite.js --host 127.0.0.1 --port 5173 --strictPort --clearScreen false" {
		t.Errorf("DevArgs: %s", got)
	}
}

// UsesTypes: an app whose main package calls typescript.Register.
func TestUsesTypes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main_test.go"), "package main // typescript.Register(")
	write(t, filepath.Join(dir, "main.go"), "package main\n\n// typescript.Register(app, srv, cfg) later\n")
	if UsesTypes(dir) {
		t.Error("a test file or a comment counted")
	}
	write(t, filepath.Join(dir, "types.go"), "package main\n\nvar _ = typescript.Register(app, srv, cfg)\n")
	if !UsesTypes(dir) {
		t.Error("not found")
	}
}

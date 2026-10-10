// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretFile(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, "deploy/production.env": true, ".env.production": true, "config/.env.local": true,
		".env.example": false, ".env.testing": false, "deploy/production.env.example": false, ".env.sample": false, "env.go": false, "README.md": false,
	} {
		if got := secretFile(name); got != want {
			t.Errorf("secretFile(%q) = %v", name, got)
		}
	}
}

func TestProjectChecksGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	git("init", "-q")
	write := func(name, s string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".env", "APP_KEY=x\n", 0o600)
	write("deploy/production.env", "DB_PASSWORD=x\n", 0o600)
	write("deploy/production.env.example", "DB_PASSWORD=\n", 0o644)
	write(".env.testing", "DB_NAME=test\n", 0o644)
	git("add", "deploy", ".env.testing")

	text := func() string {
		var b strings.Builder
		for _, f := range projectChecks(t.Context(), dir) {
			b.WriteString(f.severity + " " + f.name + ": " + f.message + "\n")
		}
		return b.String()
	}
	got := text()
	if !strings.Contains(got, "problem git: deploy/production.env is in git") || strings.Contains(got, "production.env.example is") ||
		!strings.Contains(got, "warning git: .env isn't ignored") || !strings.Contains(got, "ok .env") {
		t.Errorf("before:\n%s", got)
	}
	write(".gitignore", ".env\n*.env\n", 0o644)
	git("rm", "-q", "--cached", "deploy/production.env")
	if got := text(); !strings.Contains(got, "ok git") {
		t.Errorf("after:\n%s", got)
	}
	// A tracked .env is a problem, reported once.
	git("add", "-f", ".env")
	if got := text(); !strings.Contains(got, "problem git: .env is in git") || strings.Contains(got, "isn't ignored") {
		t.Errorf("a tracked .env:\n%s", got)
	}
}

func TestInteractive(t *testing.T) {
	saved := os.Stdin
	t.Cleanup(func() { os.Stdin = saved })
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	os.Stdin = null
	if interactive() {
		t.Error("the null device is interactive")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	os.Stdin = r
	if interactive() {
		t.Error("a pipe is interactive")
	}
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/cli/internal/secscan"
)

func TestSecurityCheckPaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wd, _ := os.Getwd()
	got := absPaths([]string{"--out=r.sarif", "--ignore", "ci/ignore.yaml", "--format=sarif", "--out=-", "-out", filepath.Join(wd, "x")})
	want := []string{"--out=" + filepath.Join(wd, "r.sarif"), "--ignore", filepath.Join(wd, "ci", "ignore.yaml"), "--format=sarif", "--out=-", "-out", filepath.Join(wd, "x")}
	if !slices.Equal(got, want) {
		t.Errorf("absPaths:\n got %q\nwant %q", got, want)
	}

	if securityLevel([]string{"--format=json", "--level", "advanced"}) != secscan.Advanced || securityLevel([]string{"-level=advanced"}) != secscan.Advanced ||
		securityLevel([]string{"--strict"}) != secscan.Basic {
		t.Error("securityLevel")
	}

	for _, bad := range [][]string{{"--no-vuln=maybe"}, {"--findings=x.json"}} {
		var out, errOut strings.Builder
		if code := securityCheck(t.Context(), bad, &out, &errOut); code != 2 {
			t.Errorf("%v: %d %s", bad, code, errOut.String())
		}
	}
	if securityLevel([]string{"--level=advanced", "--level=basic"}) != secscan.Basic {
		t.Error("securityLevel: the last --level counts")
	}

	// Without a .env, it says how to write one before building anything.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_KEY", "")
	var out, errOut strings.Builder
	if code := securityCheck(t.Context(), nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "cp .env.example .env") {
		t.Errorf("without a .env: %d %s%s", code, out.String(), errOut.String())
	}
}

// TestSecurityCheckLiveOnly scans a local site without a project: the
// report is the app's, with the ignore file and the exit status.
func TestSecurityCheckLiveOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.git/HEAD" {
			_, _ = w.Write([]byte("ref: refs/heads/main\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Shop</h1>"))
	}))
	defer srv.Close()
	t.Chdir(t.TempDir()) // outside a project
	var out, errOut strings.Builder
	code := securityCheck(t.Context(), []string{"--url=" + srv.URL + "/", "--format=json"}, &out, &errOut)
	var rep struct {
		Findings []struct{ Rule, Severity, Where, Docs string }
		Summary  map[string]int
	}
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatalf("%v\n%s%s", err, out.String(), errOut.String())
	}
	found := false
	for _, f := range rep.Findings {
		if f.Rule == "live.exposed" && f.Where == "/.git/HEAD" && f.Severity == "problem" && strings.HasSuffix(f.Docs, "#liveexposed") {
			found = true
		}
	}
	if code != 1 || !found {
		t.Errorf("exit %d: %s%s", code, out.String(), errOut.String())
	}
	// Ignored with a reason, it passes.
	if err := os.WriteFile("security-ignore.yaml", []byte("- rule: live.exposed\n  reason: a test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := securityCheck(t.Context(), []string{"--url", srv.URL + "/", "--live-only"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Ignored (security-ignore.yaml)") {
		t.Errorf("ignored: %d %s", code, out.String())
	}
	// Another host needs --allow-host.
	if code := securityCheck(t.Context(), []string{"--url=https://example.org/", "--live-only"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "--allow-host=example.org") {
		t.Errorf("another host: %d %s", code, errOut.String())
	}
	if code := securityCheck(t.Context(), []string{"--live-only"}, &out, &errOut); code != 2 {
		t.Errorf("--live-only alone: %d", code)
	}
}

// The project's ignore file and APP_URL count from a folder of it.
func TestSecurityCheckLiveInProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.git/HEAD" {
			_, _ = w.Write([]byte("ref: refs/heads/main\n"))
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":               "module example.com/shop\n\ngo 1.26\n",
		".env":                 "APP_URL=\"https://shop.test\" # production\n",
		"security-ignore.yaml": "- rule: live.exposed\n  reason: a test\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(dir, "app"))
	t.Setenv("APP_URL", "")
	var out, errOut strings.Builder
	if code := securityCheck(t.Context(), []string{"--url=" + srv.URL + "/", "--live-only"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "1 ignored") {
		t.Errorf("from a folder: %d %s%s", code, out.String(), errOut.String())
	}
	lo := liveOpts{root: dir}
	if hosts := lo.allowed(); !slices.Contains(hosts, "shop.test") {
		t.Errorf("APP_URL from .env: %v", hosts)
	}
	// A host that isn't allowed is refused before anything is built.
	if code := securityCheck(t.Context(), []string{"--url=https://example.org/"}, &out, &errOut); code != 2 {
		t.Errorf("project mode, another host: %d", code)
	}
}

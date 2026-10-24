// SPDX-License-Identifier: Apache-2.0

package securitycheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRelAndRepoPrefix(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir()) // macOS's /var is /private/var
	if err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(repo, "services", "shop")
	sub := filepath.Join(mod, "routes")
	for _, d := range []string{filepath.Join(repo, ".git"), sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub) // a subfolder of the module: files are still the module's
	root := mod
	cases := map[string]string{
		filepath.Join(root, "routes", "web.go"):   "routes/web.go",
		filepath.Join(root, "main.go"):            "main.go",
		filepath.Join(repo, "other", "x.go"):      filepath.ToSlash(filepath.Join(repo, "other", "x.go")),
		"anetos.dev/anetos@v0.5.0/admin/panel.go": "anetos.dev/anetos@v0.5.0/admin/panel.go",
		"": "",
	}
	for in, want := range cases {
		if got := Rel(in); got != want {
			t.Errorf("Rel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := RepoPrefix(); got != "services/shop/" {
		t.Errorf("RepoPrefix() = %q, want services/shop/", got)
	}
	if got := RuleDocs("route.rate-limit"); got != Docs+"#routerate-limit" {
		t.Errorf("RuleDocs = %q", got)
	}
	if !InMainModule("main.setup") || InMainModule("example.com/other.F") {
		t.Error("InMainModule")
	}
}

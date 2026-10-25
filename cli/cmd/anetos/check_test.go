// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"anetos.dev/anetos/cli/internal/testmod"
)

type checkReport struct {
	OK    bool        `json:"ok"`
	Error string      `json:"error"`
	Steps []checkStep `json:"steps"`
}

func runCheckJSON(t *testing.T, args ...string) (int, checkReport, string) {
	t.Helper()
	code, out, errOut := runCmd(t, append([]string{"check", "--json"}, args...)...)
	var r checkReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("check --json printed no JSON (%v):\n%s\n%s", err, out, errOut)
	}
	return code, r, errOut
}

// TestCheckFails: check stops at the first failing step, and says so.
func TestCheckFails(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build")
	}
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":  "module example.com/broken\n\ngo 1.26\n",
		"main.go": "package main\n\nfunc main() { undefinedThing() }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	code, r, _ := runCheckJSON(t)
	if code != 1 || r.OK || len(r.Steps) != 2 {
		t.Fatalf("check: %d %+v", code, r)
	}
	if s := r.Steps[0]; s.Step != "generate" || !s.OK {
		t.Errorf("generate: %+v", s)
	}
	if s := r.Steps[1]; s.Step != "build" || s.OK || !strings.Contains(s.Output, "undefinedThing") {
		t.Errorf("build: %+v", s)
	}
	code, out, _ := runCmd(t, "check")
	if code != 1 || !strings.Contains(out, "build    FAILED") || strings.Contains(out, "vet") || !strings.Contains(out, "Fix the failure above") {
		t.Errorf("check: %d\n%s", code, out)
	}
}

// TestCheckOutsideProject: --json prints JSON even when check can't start.
func TestCheckOutsideProject(t *testing.T) {
	t.Chdir(t.TempDir())
	code, r, errOut := runCheckJSON(t)
	if code != 1 || r.OK || r.Error == "" || r.Steps == nil || !strings.Contains(errOut, "anetos check:") {
		t.Errorf("check outside a project: %d %+v %s", code, r, errOut)
	}
}

func TestTailText(t *testing.T) {
	if got := tailText("short", 10); got != "short" {
		t.Errorf("tailText = %q", got)
	}
	text := strings.Repeat("é", 10) + "end" // 'é' is two bytes
	for n := 3; n < len(text); n++ {
		got := tailText(text, n)
		if !utf8.ValidString(got) || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "end") || len(got)-len("…") > n {
			t.Errorf("tailText(%d) = %q", n, got)
		}
	}
}

// TestCheckSecurityWithoutEnv: without a .env, check's security step
// checks the code, and a problem fails it.
func TestCheckSecurityWithoutEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and loads a project")
	}
	dir := t.TempDir()
	core, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	goMod, goSum, err := testmod.Files(core)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": goMod, "go.sum": goSum,
		"main.go": "package main\n\nimport (\n\t\"anetos.dev/anetos/db\"\n\t\"anetos.dev/anetos/web\"\n)\n\n" +
			"type Post struct {\n\tdb.Model\n\tTitle string\n}\n\n" +
			"func search(c *web.Ctx) error {\n\t_, err := db.Raw[Post](c, \"SELECT * FROM posts WHERE title = '\"+c.Query(\"q\")+\"'\")\n\treturn err\n}\n\n" +
			"func main() { _ = search }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	t.Setenv("APP_KEY", "")
	code, r, _ := runCheckJSON(t)
	if code != 1 || r.OK || len(r.Steps) != 6 {
		t.Fatalf("check: %d %+v", code, r)
	}
	if s := r.Steps[5]; s.Step != "security" || s.OK || !strings.Contains(s.Output, "problem  source.sql  main.search  main.go:14") ||
		!strings.Contains(s.Output, "there's no .env") {
		t.Errorf("security: %+v", s)
	}
	// The project's ignore file counts here too.
	if err := os.WriteFile(filepath.Join(dir, "security-ignore.yaml"), []byte("- rule: source.sql\n  where: main.search\n  reason: reviewed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, r, _ := runCheckJSON(t); code != 0 || !r.OK || !strings.Contains(r.Steps[5].Output, "1 ignored") {
		t.Errorf("ignored: %d %+v", code, r.Steps)
	}
}

func TestWarningLines(t *testing.T) {
	report := "Checking blog.\n  ok       app\n  warning  mail: MAIL_DRIVER=log in production\n  note     cache: CACHE_DRIVER=memory\n" +
		"\nwarning  route.rate-limit  POST /register  main.go:5\n         it creates accounts\n         A07:2025 · https://…\n" +
		"\nnote     ignore.unused  security-ignore.yaml\n         the entry for x matches no finding\n\nproblem  source.sql  main.f\n         SQL\n"
	want := "warning  mail: MAIL_DRIVER=log in production\nnote     cache: CACHE_DRIVER=memory\n" +
		"warning  route.rate-limit  POST /register  main.go:5\n  it creates accounts\n" +
		"note     ignore.unused  security-ignore.yaml\n  the entry for x matches no finding"
	if got := warningLines(report); got != want {
		t.Errorf("warningLines:\n%s\nwant:\n%s", got, want)
	}
}

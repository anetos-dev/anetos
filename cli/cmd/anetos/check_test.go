// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

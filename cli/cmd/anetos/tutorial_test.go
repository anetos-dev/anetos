// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTutorialProject checks that examples/tutorial is what a reader of
// the tutorial (docs/site/getting-started/tutorial) has: the project of
// `anetos new tracker` and `anetos make:auth`, with the tutorial's
// changes. The files the tutorial doesn't change are as the generators
// write them, and the lines it tells readers to add code after are still
// in the generated files. The tutorial's code itself is examples/tutorial,
// which make check builds and tests.
func TestTutorialProject(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a project")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	example := filepath.Join(repo, "examples", "tutorial")
	dir := filepath.Join(t.TempDir(), "tracker")
	if code, out, errOut := runCmd(t, "new", dir, "--replace", repo); code != 0 {
		t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
	}
	t.Chdir(dir)
	if code, out, errOut := runCmd(t, "make:auth"); code != 0 {
		t.Fatalf("make:auth: %d\n%s\n%s", code, out, errOut)
	}

	// The generated files the tutorial changes, with the lines it adds
	// code after (or replaces).
	anchors := map[string][]string{
		"routes/auth.go":                  {`	members.Post("/confirm-password", web.H(h.ConfirmPassword))` + "\n"},
		"views/layout.templ":              {`			<header><a href={ web.URL(ctx, "home") }>Tracker</a></header>` + "\n"},
		"main.go":                         {"\tif _, err := events.ForApp(app); err != nil {\n\t\treturn nil, err\n\t}\n"},
		"database/factories/factories.go": {"package factories\n"},
		"app/models/models_gen.go":        nil, // anetos gen's, for the new models
	}
	stamp := regexp.MustCompile(`\d{4}_\d{2}_\d{2}_\d{6}`)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		switch {
		case rel == ".env", rel == "go.mod", rel == "go.sum", strings.HasSuffix(rel, "_templ.go"):
			return nil // a fresh key; replace directives; generated
		}
		got := read(t, p)
		if lines, ok := anchors[rel]; ok {
			for _, l := range lines {
				if !strings.Contains(got, l) {
					t.Errorf("%s no longer has the line the tutorial adds code after:\n%s", rel, l)
				}
			}
			return nil
		}
		want := filepath.Join(example, filepath.FromSlash(rel))
		if stamp.MatchString(rel) { // a migration: its name has the time it was made
			matches, _ := filepath.Glob(filepath.Join(example, filepath.Dir(rel), "*"+stamp.ReplaceAllString(filepath.Base(rel), "")))
			if len(matches) != 1 {
				t.Errorf("examples/tutorial has no %s", rel)
				return nil
			}
			want = matches[0]
			got = stamp.ReplaceAllString(got, "")
		}
		b, rerr := os.ReadFile(want)
		if rerr != nil {
			t.Errorf("examples/tutorial lacks %s, which the generators write: %v", rel, rerr)
			return nil
		}
		if stamp.ReplaceAllString(string(b), "") != stamp.ReplaceAllString(got, "") {
			t.Errorf("examples/tutorial/%s differs from the generated file: update the example (and the tutorial if it shows the file)", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

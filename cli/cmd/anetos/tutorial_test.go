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
		"views/layout.templ":              {"\t\t\t\t\t@navLink(\"home\", i18n.T(ctx, \"nav.home\"))\n", "\t\t\t\t}\n\t\t\t\t@AccountMenu()\n"},
		"main.go":                         {"\tif _, err := events.ForApp(app); err != nil {\n\t\treturn nil, err\n\t}\n"},
		"database/factories/factories.go": {"package factories\n"},
		"app/models/models_gen.go":        nil, // anetos gen's, for the new models
	}
	compareExample(t, dir, example, anchors, nil)
}

// compareExample checks the example against the generated project in
// dir: each generated file is the example's, with the tutorial's changes
// in undo (the example's text, then the generated text it replaced)
// undone, but those in anchors, whose lines (the ones the tutorial adds
// code after) must still be in the generated file. Migrations match
// whatever their timestamp; an example's SPDX header (one the generators
// don't write) is left out. Files the tutorial adds aren't checked.
func compareExample(t *testing.T, dir, example string, anchors map[string][]string, undo map[string][][2]string) {
	t.Helper()
	name := "examples/" + filepath.Base(example)
	stamp := regexp.MustCompile(`\d{4}_\d{2}_\d{2}_\d{6}`)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		switch {
		case rel == ".env", rel == "go.mod", rel == "go.sum", strings.HasSuffix(rel, "_templ.go"), strings.HasSuffix(rel, ".db"):
			return nil // a fresh key; replace directives; generated; a database
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
				t.Errorf("%s has no %s", name, rel)
				return nil
			}
			want = matches[0]
			got = stamp.ReplaceAllString(got, "")
		}
		b, rerr := os.ReadFile(want)
		if rerr != nil {
			t.Errorf("%s lacks %s, which the generators write: %v", name, rel, rerr)
			return nil
		}
		have := strings.TrimPrefix(string(b), "// SPDX-License-Identifier: Apache-2.0\n\n")
		for _, e := range undo[rel] { // the tutorial's changes, undone
			if r, ok := strings.CutPrefix(e[0], "drop "); ok { // "drop region: name": a block the tutorial adds
				i := strings.Index(have, "// "+r+"\n")
				j := strings.Index(have[max(i, 0):], "// endregion\n")
				if i < 0 || j < 0 {
					t.Errorf("%s/%s has no %s", name, rel, r)
					continue
				}
				have = have[:i] + have[i+j+len("// endregion\n"):]
				have = strings.Replace(have, "\n\n\n", "\n\n", 1)
				if strings.HasSuffix(have, "\n\n") {
					have = strings.TrimRight(have, "\n") + "\n"
				}
				continue
			}
			if !strings.Contains(have, e[0]) {
				t.Errorf("%s/%s no longer has the tutorial's change:\n%s", name, rel, e[0])
			}
			have = strings.Replace(have, e[0], e[1], 1)
		}
		if stamp.ReplaceAllString(have, "") != stamp.ReplaceAllString(got, "") {
			t.Errorf("%s/%s differs from the generated file: update the example (and the tutorial if it shows the file)", name, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestAPITutorialProject is TestTutorialProject for examples/bookmarks,
// the project of "Tutorial: build an API": `anetos new bookmarks
// --stack=api`, `make:auth` and `make:crud Bookmark …`, with the
// tutorial's changes (and SPDX headers, which the generators don't
// write).
func TestAPITutorialProject(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a project")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	example := filepath.Join(repo, "examples", "bookmarks")
	dir := filepath.Join(t.TempDir(), "bookmarks")
	if code, out, errOut := runCmd(t, "new", dir, "--stack=api", "--replace", repo); code != 0 {
		t.Fatalf("new: %d\n%s\n%s", code, out, errOut)
	}
	t.Chdir(dir)
	for _, args := range [][]string{{"make:auth"}, {"make:crud", "Bookmark", "url:string", "title:string", "notes:text:optional", "archived:bool"}} {
		if code, out, errOut := runCmd(t, args...); code != 0 {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errOut)
		}
	}
	// The files the tutorial rewrites (their lines it keeps are still
	// generated), and its changes to the others.
	anchors := map[string][]string{
		"routes/bookmarks.go":      {"func Bookmarks(r *web.Router) {\n", "\tvar h handlers.Bookmarks\n"},
		"app/models/models_gen.go": nil, // anetos gen's, for the new column
		"bookmarks_test.go":        nil, // the tutorial's tests replace make:crud's
		"openapi.json":             nil, // the document of the changed routes
	}
	region := func(name, body string) string {
		return "// region: " + name + "\n" + body + "// endregion\n"
	}
	findRow := "\trow, err := find(c, in.ID)\n"
	genRow := "\trow, err := db.Find[models.Bookmark](c, in.ID)\n"
	undo := map[string][][2]string{
		"routes/api.go": {{"\tapi.Get(\"/\", web.H(handlers.Welcome{}.Show)).Name(\"welcome\")\n}",
			"\tapi.Get(\"/\", web.H(handlers.Welcome{}.Show)).Name(\"welcome\")\n\tBookmarks(api) // anetos make:crud\n}"}},
		"routes/auth.go": {{"\t" + region("me", "\tBookmarks(me) // the user's bookmarks, with their token\n\t"), ""}},
		"app/models/bookmark.go": {
			{"// region: model\n\n", ""},
			{"\tUserID   int64  `db:\"user_id\"` // whose bookmark\n", ""},
			{"\n\n// endregion\n", "\n"},
		},
		"app/handlers/bookmarks.go": {
			{"\t\"anetos.dev/anetos/auth\"\n", ""},
			{"`json:\"url\" validate:\"required|url|max:255\"`", "`json:\"url\" validate:\"required|max:255\"`"},
			{"drop region: owner", ""},
			{"drop region: archive", ""},
			{"\t" + region("index", "\tuserID, err := owner(c)\n\tif err != nil {\n\t\treturn db.Page[BookmarkResponse]{}, err\n\t}\n"+
				"\tq := db.Query[models.Bookmark](c).Where(models.BookmarkCols.UserID.Eq(userID))\n\t"),
				"\tq := db.Query[models.Bookmark](c)\n"},
			{findRow, genRow}, {findRow, genRow}, {findRow, genRow},
			{"\t" + region("create", "\tuserID, err := owner(c)\n\tif err != nil {\n\t\treturn BookmarkResponse{}, err\n\t}\n"+
				"\trow := models.Bookmark{UserID: userID}\n\t"),
				"\tvar row models.Bookmark\n"},
		},
	}
	mig, _ := filepath.Glob(filepath.Join(dir, "database", "migrations", "*_create_bookmarks_table.go"))
	if len(mig) != 1 {
		t.Fatalf("migrations: %v", mig)
	}
	rel, _ := filepath.Rel(dir, mig[0])
	undo[filepath.ToSlash(rel)] = [][2]string{{"\t\t\t\t" + region("user-id",
		"\t\t\t\tt.ForeignID(\"user_id\").Constrained().CascadeOnDelete() // whose bookmark\n\t\t\t\t"), ""}}
	compareExample(t, dir, example, anchors, undo)
}

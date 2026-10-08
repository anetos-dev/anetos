// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMakeCrud(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	if _, err := Create(Project{Dir: dir, Module: "example.com/shop", DB: "sqlite", Replace: "../../.."}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	res, err := MakeCrud(dir, "blog-post", []string{"title:string", "image_url:string:optional", "due_on:date:optional", "email:email:unique"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app/models/blog_post.go", "database/migrations/2030_01_02_030405_create_blog_posts_table.go",
		"app/handlers/blog_posts.go", "views/blog_posts.templ", "routes/blog_posts.go", "blog_posts_test.go", "locales/en/blog_posts.yaml"} {
		if !slices.Contains(res.Created, want) {
			t.Errorf("missing %s in %v", want, res.Created)
		}
	}
	if !res.Routed || !res.Linked || res.Plural != "BlogPosts" || res.Path != "/blog-posts" {
		t.Errorf("result %+v", res)
	}
	for file, wants := range map[string][]string{
		"app/models/blog_post.go": {"type BlogPost struct", "ImageURL string `db:\"image_url\"`", "DueOn anetos.Date `db:\"due_on\"`"},
		"database/migrations/2030_01_02_030405_create_blog_posts_table.go": {`s.Create("blog_posts"`, `t.String("title", 255)`, `t.Date("due_on").Nullable()`, `t.String("email", 255).Unique()`},
		"app/handlers/blog_posts.go":                                       {"type BlogPostInput struct", `validate:"required|max:255"`, `json:"image_url" validate:"max:255"`, `validate:"required|email|max:255|unique:blog_posts,email,ID"`, `"example.com/shop/views"`},
		"routes/blog_posts.go":                                             {`r.Delete("/blog-posts/{id}", web.H(h.Delete)).Name("blog-posts.destroy")`},
		"routes/web.go":                                                    {"\tBlogPosts(pages) // anetos make:crud\n}"},
		"views/layout.templ":                                               {"\t\t\t\t\t\t@navLink(\"home\", i18n.T(ctx, \"nav.home\"))\n\t\t\t\t\t\t@navLink(\"blog-posts.index\", i18n.T(ctx, \"blog_posts.title\"))\n\t\t\t\t\t</nav>"},
		"locales/en/blog_posts.yaml":                                       {"blog_posts:\n title: \"Blog posts\"", `new: "New blog post"`, `"image_url": "Image URL"`},
		"blog_posts_test.go":                                               {"func TestBlogPosts(", `AssertValidationErrors("title", "email")`, `"due_on": {"2026-10-06"}`},
	} {
		got := alignment.ReplaceAllString(read(t, filepath.Join(dir, filepath.FromSlash(file))), " ") // gofmt's alignment
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %q:\n%s", file, w, got)
			}
		}
	}
	// A second model: its routes and link go after the first's.
	if _, err := MakeCrud(dir, "Tag", []string{"name:string"}, now); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "routes", "web.go")); !strings.Contains(got, "\tBlogPosts(pages) // anetos make:crud\n\tTags(pages) // anetos make:crud\n}") {
		t.Errorf("routes/web.go:\n%s", got)
	}
	if got := read(t, filepath.Join(dir, "views", "layout.templ")); !strings.Contains(got, "\"blog_posts.title\"))\n\t\t\t\t\t\t@navLink(\"tags.index\", i18n.T(ctx, \"tags.title\"))\n\t\t\t\t\t</nav>") {
		t.Errorf("layout:\n%s", got)
	}
	// Nothing is overwritten, and bad fields are refused before writing.
	if _, err := MakeCrud(dir, "Tag", []string{"name:string"}, now); err == nil || !strings.Contains(err.Error(), "create_tags_table.go exists") {
		t.Errorf("twice: %v", err)
	}
	for _, args := range [][]string{nil, {"title"}, {"Title:string"}, {"title:string", "title:text"}, {"created_at:date"}, {"x:uuid"},
		{"x:bool:unique"}, {"x:int:unique"}, {"x:string:optional:unique"}, {"x:string:big"}, {"model:string"}, {"id_:int"}, {"a__b:int"}, {"a_:int"}, {"_a:int"},
		{"a_1:int", "a1:int"}} {
		if _, err := MakeCrud(dir, "Note", args, now); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "models", "note.go")); err == nil {
		t.Error("a refused make:crud wrote note.go")
	}
	// A plural that is the name, and the framework's tables.
	if _, err := MakeCrud(dir, "News", []string{"title:string"}, now); err == nil || !strings.Contains(err.Error(), "plural") {
		t.Errorf("News: %v", err)
	}
	if _, err := MakeCrud(dir, "ApiToken", []string{"name:string"}, now); err == nil || !strings.Contains(err.Error(), "api_tokens table") {
		t.Errorf("ApiToken: %v", err)
	}
	// A name the files declare is taken.
	if err := os.WriteFile(filepath.Join(dir, "views", "mine.templ"), []byte("package views\n\ntempl NotesPage() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeCrud(dir, "Note", []string{"title:string"}, now); err == nil || !strings.Contains(err.Error(), "views already declares NotesPage") {
		t.Errorf("NotesPage taken: %v", err)
	}
}

// alignment is gofmt's: runs of spaces.
var alignment = regexp.MustCompile(` +`)

// The routes call and the nav link go only where anetos new's code is.
func TestCrudPatches(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	write := func(src string) {
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for src, want := range map[string]bool{
		"package routes\n\nfunc Register(r *web.Router) {\n\tpages := r.Group(\"\")\n\t_ = pages\n}\n":    true,
		"package routes\n\nfunc Register(r *web.Router) {\n\tr.Get(\"/\", nil)\n}\n":                      false, // no pages group
		"package routes\n\nfunc Register(r *web.Router) {\n\tpages := r.Group(\"\")\n\tPosts(pages)\n}\n": true,  // already
		"package routes\n\nfunc Routes() {}\n":                                                            false,
		"package routes\n\nfunc Register(r *web.Router) { pages := r; _ = pages }\n":                      false, // one line
		"not go": false,
	} {
		write(src)
		got, err := addRoutesCall(file, "Posts", "pages")
		if err != nil || got != want {
			t.Errorf("%q: %v, %v", src, got, err)
		}
		if want && !strings.Contains(read(t, file), "Posts(pages)") {
			t.Errorf("%q:\n%s", src, read(t, file))
		}
	}
	for src, want := range map[string]bool{
		"templ Layout() {\n\t<header>\n\t\t<nav>\n\t\t\t@navLink(\"home\", x)\n\t\t</nav>\n\t</header>\n}\n\ntempl navLink(route, label string) {\n}\n": true,
		"templ Layout() {\n\t<header>\n\t\t<nav><a></a></nav>\n\t</header>\n}\n\ntempl navLink(route, label string) {\n}\n":                             false,
		"templ Layout() {\n\t<header>\n\t\t<nav>\n\t\t</nav>\n\t</header>\n}\n":                                                                         false, // no navLink
		"templ Layout() {\n\t<nav>\n\t</nav>\n}\n\ntempl navLink(route, label string) {\n}\n":                                                           false, // no header
	} {
		write(src)
		got, err := addNavLink(file, "posts.index", "posts.title")
		if err != nil || got != want {
			t.Errorf("%q: %v, %v", src, got, err)
		}
		if want && !strings.Contains(read(t, file), "\t\t\t@navLink(\"home\", x)\n\t\t\t@navLink(\"posts.index\", i18n.T(ctx, \"posts.title\"))\n\t\t</nav>") {
			t.Errorf("%q:\n%s", src, read(t, file))
		}
	}
	for src, want := range map[string]bool{
		"<header>\n\t<nav>\n\t</nav>\n</header>\n": true,
		"<header><nav></nav></header>\n":           false,
		"<nav>\n</nav>\n":                          false,
	} {
		write(src)
		got, err := addAccountMenu(file)
		if err != nil || got != want {
			t.Errorf("%q: %v, %v", src, got, err)
		}
		if want && read(t, file) != "<header>\n\t<nav>\n\t</nav>\n\t@AccountMenu()\n</header>\n" {
			t.Errorf("%q:\n%s", src, read(t, file))
		}
	}
}

func TestCrudNames(t *testing.T) {
	for in, want := range map[string][2]string{
		"title":     {"Title", "Title"},
		"due_on":    {"DueOn", "Due on"},
		"image_url": {"ImageURL", "Image URL"},
		"user_id":   {"UserID", "User ID"},
		"a1_b":      {"A1B", "A1 b"},
	} {
		if g, l := goName(in), label(in); g != want[0] || l != want[1] {
			t.Errorf("%s: %s, %s", in, g, l)
		}
	}
}

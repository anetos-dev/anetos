// SPDX-License-Identifier: Apache-2.0

package typescript_test

import (
	"bytes"
	"context"
	"flag"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/inertia"
	"anetos.dev/anetos/web/typescript"
)

var update = flag.Bool("update", false, "rewrite testdata's golden files")

// Post is a post, as the API writes it.
type Post struct {
	ID        int64          `json:"id"`
	Title     string         `json:"title" description:"The post's title."`
	Tags      []string       `json:"tags"`
	Author    *Author        `json:"author"`
	Published *time.Time     `json:"published_at,omitempty"`
	Status    string         `json:"status"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type Author struct {
	Name string `json:"name"`
}

type PostID struct {
	ID int64 `path:"id"`
}

type ListPosts struct {
	Page int      `query:"page"`
	Tags []string `query:"tag"`
	Lang string   `header:"Accept-Language"`
}

type PostInput struct {
	Title  string   `json:"title" validate:"required|max:100"`
	Status string   `json:"status" validate:"in:draft,published"`
	Tags   []string `json:"tags"`
}

type UpdatePost struct {
	PostID
	PostInput
}

type Upload struct {
	PostID
	File    *multipart.FileHeader `form:"file"`
	Caption string                `form:"caption" validate:"required"`
}

type Page[T any] struct {
	Data []T `json:"data"`
	Next int `json:"next"`
}

func apiRouter() *web.Router {
	r := web.NewRouter()
	api := r.Group("/api/v1", web.JSONErrors)
	api.Get("/posts", web.H(func(c *web.Ctx, in ListPosts) (Page[Post], error) { return Page[Post]{}, nil })).Name("posts.index")
	api.Get("/posts/{id}", web.H(func(c *web.Ctx, in PostID) (Post, error) { return Post{}, nil })).Name("posts.show")
	api.Post("/posts", web.H(func(c *web.Ctx, in PostInput) (Post, error) { return Post{}, nil })).Name("posts.create").Status(http.StatusCreated)
	api.Put("/posts/{id}", web.H(func(c *web.Ctx, in UpdatePost) (Post, error) { return Post{}, nil })).Name("posts.update")
	api.Delete("/posts/{id}", web.H(func(c *web.Ctx, in PostID) (web.Empty, error) { return web.Empty{}, nil })).Name("posts.delete")
	api.Post("/posts/{id}/files", web.H(func(c *web.Ctx, in Upload) (web.Empty, error) { return web.Empty{}, nil })).Name("posts.upload")
	api.Get("/ping", web.H(func(c *web.Ctx, _ struct{}) (string, error) { return "pong", nil }))
	// A page's form: its input's type, no client function.
	r.Post("/notes", web.H(func(c *web.Ctx, in NoteInput) (web.Responder, error) { return web.Back(), nil }))
	r.Get("/plain", func(c *web.Ctx) error { return nil })
	return r
}

type NoteInput struct {
	Body string `json:"body" validate:"required"`
}

type PostsIndex struct {
	Posts  []Post                  `json:"posts"`
	Filter string                  `json:"filter,omitempty"`
	Stats  inertia.Prop[PostStats] `json:"stats"`
}

type PostStats struct {
	Count int `json:"count"`
}

type Shared struct {
	AppName string  `json:"appName"`
	User    *Author `json:"user"`
}

var (
	postsIndex = inertia.NewPage[PostsIndex]("Posts/Index")
	debugPage  = inertia.NewPage[inertia.Props]("Debug")
)

func tsConfig(t *testing.T) typescript.Config {
	root := view.ComponentFunc(func(context.Context, io.Writer) error { return nil })
	in, err := inertia.NewWithRoot(root, inertia.WithPages(postsIndex, debugPage),
		inertia.WithShared(func(*web.Ctx) (Shared, error) { return Shared{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	return typescript.Config{Dir: "testdata", Prefix: "/api/v1", Inertia: in}
}

// The files of a router and pages, compared with testdata's.
func TestGenerate(t *testing.T) {
	files, warnings, err := typescript.Generate(apiRouter(), tsConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files %v", files)
	}
	for path, b := range files {
		if *update {
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil || string(want) != string(b) {
			t.Errorf("%s differs (go test -update):\n%s", path, b)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("warnings %q", warnings)
	}
	if err := typescript.Check(apiRouter(), tsConfig(t)); err != nil {
		t.Error(err)
	}
	// Types only: no Prefix, no Inertia.
	files, _, err = typescript.Generate(apiRouter(), typescript.Config{})
	api := string(files[filepath.Join("frontend", "src", "types", "api.ts")])
	if err != nil || len(files) != 1 || strings.Contains(api, "createApi") || !strings.Contains(api, "export interface NoteInput {\n  body: string\n}") {
		t.Errorf("types only: %v\n%s", err, api)
	}
}

// Register adds the types command, which writes the files (those that
// changed) or checks them.
func TestRegister(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{}), anetos.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().Get("/api/v1/posts/{id}", web.H(func(c *web.Ctx, in PostID) (Post, error) { return Post{}, nil })).Name("posts.show")
	cfg := tsConfig(t)
	cfg.Dir = filepath.Join(t.TempDir(), "types")
	if err := typescript.Register(app, srv, cfg); err != nil {
		t.Fatal(err)
	}
	if err := typescript.Register(app, srv, cfg); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("twice: %v", err)
	}
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := app.ExecuteArgs(context.Background(), append([]string{"types"}, args...), &out, &out)
		return code, out.String()
	}
	if code, out := run("--check"); code != 1 || !strings.Contains(out, "api.ts doesn't exist: write it with `go run . types`") {
		t.Errorf("check without the files: %d %s", code, out)
	}
	if code, out := run(); code != 0 || !strings.Contains(out, "wrote "+filepath.Join(cfg.Dir, "api.ts")) || !strings.Contains(out, "pages.ts") {
		t.Errorf("write: %d %s", code, out)
	}
	if code, out := run(); code != 0 || strings.Contains(out, "wrote") {
		t.Errorf("unchanged files written again: %d %s", code, out)
	}
	if code, out := run("--check"); code != 0 || !strings.Contains(out, "up to date") {
		t.Errorf("check: %d %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, "pages.ts"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := run("--check"); code != 1 || !strings.Contains(out, "pages.ts is out of date") {
		t.Errorf("check an old file: %d %s", code, out)
	}
	if code, out := run("extra"); code != 2 || !strings.Contains(out, "unexpected argument") {
		t.Errorf("an argument: %d %s", code, out)
	}
}

type Edge struct {
	Inline struct {
		A int `json:"a"`
	} `json:"inline"`
	MaybeInline *struct {
		B string `json:"b"`
	} `json:"maybe_inline"`
	Odd    string            `json:"odd-name" description:"Says */ and\nmore."`
	Level  int               `json:"level" validate:"in:1,2,3"`
	Raw    any               `json:"raw"`
	Data   []byte            `json:"data"`
	Unions []map[string]bool `json:"unions"`
	Date   *time.Time        `json:"date"`
}

type EdgeIn struct {
	Name  string          `json:"name" validate:"in:a,b"`
	Codes []string        `json:"codes" validate:"max:3"`
	Flags map[string]bool `json:"flags" validate:"min:1"`
}

// Operation IDs that are both a function and a group; unusual types.
func TestEdges(t *testing.T) {
	r := web.NewRouter()
	g := r.Group("/api")
	g.Get("/edge", web.H(func(*web.Ctx, struct{}) (Edge, error) { return Edge{}, nil })).Name("edge")
	g.Get("/edge/sub", web.H(func(*web.Ctx, struct{}) (Edge, error) { return Edge{}, nil })).Name("edge.sub")
	g.Post("/edge", web.H(func(*web.Ctx, EdgeIn) (web.Responder, error) { return nil, nil }))
	g.Get("/any", web.H(func(*web.Ctx, struct{}) (any, error) { return nil, nil }))
	in, _ := inertia.NewWithRoot(view.ComponentFunc(func(context.Context, io.Writer) error { return nil }),
		inertia.WithShared(func(*web.Ctx) (inertia.Props, error) { return nil, nil }))
	files, _, err := typescript.Generate(r, typescript.Config{Dir: "x", Prefix: "/api", Inertia: in})
	if err != nil {
		t.Fatal(err)
	}
	api, pages := string(files[filepath.Join("x", "api.ts")]), string(files[filepath.Join("x", "pages.ts")])
	for _, want := range []string{
		"  inline: {\n    a: number\n  }\n",
		"  maybe_inline: {\n    b: string\n  } | null\n",
		"  /** Says * / and more. */\n  \"odd-name\": string\n",
		"  level: number\n", "  raw: unknown\n", "  data: string\n", "  unions: Array<Record<string, boolean>>\n", "  date: string | null\n",
		"  name?: \"a\" | \"b\" | \"\"\n", "  codes?: string[]\n", "  flags?: Record<string, boolean> | Record<string, never>\n",
		"    edge: (input: EdgeInput = {}, init?: RequestInit): Promise<Edge> =>",
		"    edgeSub: (input: EdgeSubInput = {}, init?: RequestInit): Promise<Edge> =>",
		"    postApiEdge: (input: PostApiEdgeInput = {}, init?: RequestInit): Promise<unknown> =>",
		"Promise<unknown>",
	} {
		if !strings.Contains(api, want) {
			t.Errorf("api.ts lacks %q:\n%s", want, api)
		}
	}
	if !strings.Contains(pages, "export type SharedProps = Record<string, unknown>\n") || strings.Contains(pages, "from './api'") {
		t.Errorf("pages.ts:\n%s", pages)
	}
	if err := typescript.Check(r, typescript.Config{Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Errorf("Check without the files: %v", err)
	}
}

// Names TypeScript can't have, or that clash: Go types of one name in two
// packages, types named as TypeScript's or the files' own, route names
// that aren't identifiers, a generic handler, two inputs of one name.
type (
	URL struct {
		Host string `json:"host"`
	}
	Record struct {
		A int `json:"a"`
	}
	Operation struct {
		B int `json:"b"`
	}
	Pages struct {
		C int `json:"c"`
	}
	PostsCreateInput struct {
		D int `json:"d"`
	}
	Quoted struct {
		ID   int    `json:"id,string" validate:"required"`
		Text string `json:"text" description:"The text."`
	}
	FormUpload struct {
		Meta  struct{ X int }       `json:"meta"`
		Title string                `json:"title"`
		File  *multipart.FileHeader `form:"file"`
	}
	Tags struct {
		Tags []string `header:"X-Tags"`
	}
)

func list[T any](*web.Ctx, struct{}) ([]T, error) { return nil, nil }

var declared = regexp.MustCompile(`(?m)^export (?:interface|type|class|function) (\S+?)[ <({]`)

func TestNames(t *testing.T) {
	r := web.NewRouter()
	g := r.Group("/api")
	g.Get("/urls", web.H(func(*web.Ctx, struct{}) (URL, error) { return URL{}, nil })).Name("urls")
	g.Get("/url", web.H(func(*web.Ctx, struct{}) (url.URL, error) { return url.URL{}, nil })).Name("url")
	g.Get("/record", web.H(func(*web.Ctx, struct{}) (Record, error) { return Record{}, nil })).Name("record")
	g.Get("/operation", web.H(func(*web.Ctx, struct{}) (Operation, error) { return Operation{}, nil })).Name("operation")
	g.Get("/pages", web.H(func(*web.Ctx, struct{}) (Pages, error) { return Pages{}, nil })).Name("pages")
	g.Post("/posts", web.H(func(*web.Ctx, Quoted) (PostsCreateInput, error) { return PostsCreateInput{}, nil })).Name("posts.create")
	g.Post("/posts2", web.H(func(*web.Ctx, Quoted) (PostsCreateInput, error) { return PostsCreateInput{}, nil })).Name("posts_create")
	g.Get("/docs/{path...}", web.H(func(*web.Ctx, struct{}) (web.Empty, error) { return web.Empty{}, nil })).Name("1weird:name")
	g.Get("/list", web.H(list[Record]))
	g.Post("/upload", web.H(func(*web.Ctx, FormUpload) (web.Empty, error) { return web.Empty{}, nil })).Name("upload")
	g.Get("/tags", web.H(func(*web.Ctx, Tags) (web.Empty, error) { return web.Empty{}, nil })).Name("tags")
	props := inertia.NewPage[Pages]("Pages")
	in, err := inertia.NewWithRoot(view.ComponentFunc(func(context.Context, io.Writer) error { return nil }), inertia.WithPages(props))
	if err != nil {
		t.Fatal(err)
	}
	files, warnings, err := typescript.Generate(r, typescript.Config{Dir: "x", Prefix: "/api", Inertia: in})
	if err != nil {
		t.Fatal(err)
	}
	api, pages := string(files[filepath.Join("x", "api.ts")]), string(files[filepath.Join("x", "pages.ts")])
	seen := map[string]bool{}
	for _, file := range []string{api, pages} {
		for _, m := range declared.FindAllStringSubmatch(file, -1) {
			if !regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`).MatchString(m[1]) || seen[m[1]] {
				t.Errorf("declared %q", m[1])
			}
			seen[m[1]] = true
		}
	}
	for _, name := range []string{"Record", "Operation", "Pages", "Array", "Promise", "Blob"} {
		if strings.Contains(api, "export interface "+name+" ") {
			t.Errorf("api.ts declares %s", name)
		}
	}
	for _, want := range []string{
		"export interface UrlURL {", "export interface TypescriptTestURL {", "export interface Record2 {", "export interface Operation2 {",
		"export interface Pages2 {", "export interface PostsCreateInput {\n  d: number\n}",
		"export interface PostsCreateInput2 {\n  id: string\n  /** The text. */\n  text?: string\n}",
		"export interface PostsCreateInput3 {", "export interface _1weirdNameInput {",
		"export interface Quoted {\n  id: string\n",
		"    postsCreate: (input: PostsCreateInput3, init?: RequestInit): Promise<PostsCreateInput> =>",
		"      create: (input: PostsCreateInput2, init?: RequestInit): Promise<PostsCreateInput> =>",
		"    \"1weirdName\": (input: _1weirdNameInput, init?: RequestInit): Promise<void> =>",
		"export interface _1weirdNameInput {\n  path: string\n}",
		"    getApiList: (input: GetApiListInput = {}, init?: RequestInit): Promise<Record2[]> =>",
		"path: '/api/upload', form: ['title', 'file'] }",
		"Record<string,",
	} {
		if !strings.Contains(api, want) {
			t.Errorf("api.ts lacks %q:\n%s", want, api)
		}
	}
	if !strings.Contains(pages, "import type { Pages2 } from './api'") && !strings.Contains(pages, "export type Pages3 = {") {
		t.Errorf("pages.ts:\n%s", pages)
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "header X-Tags is a list") }) {
		t.Errorf("warnings: %q", warnings)
	}
}

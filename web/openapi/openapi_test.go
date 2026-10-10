// SPDX-License-Identifier: Apache-2.0

package openapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/openapi"
	"anetos.dev/anetos/web/openapi/internal/other"
)

var update = flag.Bool("update", false, "rewrite testdata/*.json")

type Products struct{}

type ProductResponse struct {
	ID       int64        `json:"id"`
	Name     string       `json:"name"`
	Price    float64      `json:"price"`
	Tags     []string     `json:"tags"`
	LaunchOn *anetos.Date `json:"launch_on"`
	Notes    string       `json:"notes,omitempty" description:"Free text."`
	Created  time.Time    `json:"created_at"`
	Owner    *Owner       `json:"owner"`
}

type Owner struct {
	Name    string `json:"name"`
	Manager *Owner `json:"manager,omitempty"` // recursive
}

type ProductList struct {
	Page    int      `query:"page"`
	PerPage int      `query:"per_page" validate:"min:0|max:100"`
	Sort    string   `query:"sort" validate:"in:name,-name"`
	Name    *string  `query:"name"`
	Tags    []string `query:"tag"`
	Trace   string   `header:"X-Trace-ID" description:"Echoed in the logs."`
}

type ProductInput struct {
	ID       int64             `path:"id"`
	Name     string            `json:"name" validate:"required|max:255"`
	Price    float64           `json:"price" validate:"required|min:0"`
	Stock    int64             `json:"stock,string"`
	Email    string            `json:"email" validate:"email"`
	Kind     string            `json:"kind" validate:"in:a,b"`
	Tags     []string          `json:"tags" validate:"max:5|distinct"`
	Code     string            `json:"code" validate:"alpha|size:3"`
	Meta     map[string]string `json:"meta"`
	LaunchOn *anetos.Date      `json:"launch_on"`
	Owner    Owner             `json:"owner"`
}

type ProductID struct {
	ID int64 `path:"id"`
}

type Upload struct {
	Title string                `json:"title" validate:"required"`
	File  *multipart.FileHeader `form:"file" validate:"required"`
}

func (Products) Index(*web.Ctx, ProductList) (db.Page[ProductResponse], error) {
	return db.Page[ProductResponse]{}, nil
}
func (Products) Show(*web.Ctx, ProductID) (ProductResponse, error) { return ProductResponse{}, nil }
func (Products) Create(*web.Ctx, ProductInput) (ProductResponse, error) {
	return ProductResponse{}, nil
}
func (Products) Delete(*web.Ctx, ProductID) (web.Empty, error)    { return web.Empty{}, nil }
func (Products) Upload(*web.Ctx, Upload) (web.Empty, error)       { return web.Empty{}, nil }
func (Products) Legacy(*web.Ctx, struct{}) (web.Responder, error) { return nil, nil }
func (Products) Echo(*web.Ctx, ProductResponse) (ProductResponse, error) {
	return ProductResponse{}, nil
}

// token is a middleware asking for a bearer token, as auth's Require.
func token(next http.Handler) http.Handler {
	return web.Documented(next, web.MiddlewareDoc{Security: "bearer",
		Responses: map[int]string{http.StatusUnauthorized: "No token."}})
}

func admin(next http.Handler) http.Handler {
	return web.Documented(next, web.MiddlewareDoc{Security: "bearer", Scopes: []string{"*"},
		Responses: map[int]string{http.StatusForbidden: "Not an admin."}})
}

// shop is a router with an API under /api/v1; plain adds a route there
// the document can't describe.
func shop(plain ...bool) *web.Router {
	r := web.NewRouter()
	r.Get("/health", func(c *web.Ctx) error { return nil }) // outside the prefix
	api := r.Group("/api/v1").As("api.")
	var h Products
	api.Get("/products", web.H(h.Index)).Name("products.index")
	api.Get("/products/{id}", web.H(h.Show)).Name("products.show")
	loggedIn := api.Group("", token)
	loggedIn.Put("/products/{id}", web.H(h.Create)).Name("products.update")
	loggedIn.With(admin).Delete("/products/{id}", web.H(h.Delete)).Name("products.delete")
	loggedIn.Post("/uploads", web.H(h.Upload)).Status(http.StatusAccepted)
	api.Get("/legacy", web.H(h.Legacy))
	api.Post("/echo", web.H(h.Echo)).Status(http.StatusCreated)
	if len(plain) > 0 && plain[0] {
		api.Get("/plain", func(c *web.Ctx) error { return nil })
	}
	api.Get("/files/{path...}", web.H(func(*web.Ctx, struct{}) (web.Empty, error) { return web.Empty{}, nil }))
	return r
}

var shopConfig = openapi.Config{Title: "Shop", Prefix: "/api/v1", Path: "/api/v1/openapi.json", File: "testdata/shop.json"}

func TestSpec(t *testing.T) {
	spec, warnings, err := openapi.Spec(shop(true), shopConfig)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("testdata/shop.json", spec, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := openapi.Check(shop(), shopConfig); err != nil {
		t.Error(err)
	}
	// A route under the prefix the document leaves out fails the check.
	if err := openapi.Check(shop(true), shopConfig); err == nil || !strings.Contains(err.Error(), "can't describe GET /api/v1/plain, under /api/v1") {
		t.Errorf("a plain route: %v", err)
	}
	if !json.Valid(spec) {
		t.Fatal("not JSON")
	}
	want := []string{
		"GET /api/v1/legacy: its result is a web.Responder, whose status and body are chosen as it runs: described as any 2XX",
		"GET /api/v1/plain: not a typed handler (web.H, registered as web.H returns it): left out",
	}
	if strings.Join(warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings:\n%s", strings.Join(warnings, "\n"))
	}
	// The same routes give the same bytes.
	again, _, _ := openapi.Spec(shop(true), shopConfig)
	if !bytes.Equal(spec, again) {
		t.Error("two documents of the same routes differ")
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	cfg := openapi.Config{Title: "Shop", Prefix: "/api/v1", File: filepath.Join(dir, "openapi.json")}
	if err := openapi.Check(shop(), cfg); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Errorf("missing: %v", err)
	}
	spec, _, _ := openapi.Spec(shop(), cfg)
	if err := os.WriteFile(cfg.File, bytes.Replace(spec, []byte(`"Shop"`), []byte(`"Old"`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := openapi.Check(shop(), cfg); err == nil || !strings.Contains(err.Error(), "out of date (line 4 differs)") {
		t.Errorf("stale: %v", err)
	}
	if _, _, err := openapi.Spec(shop(), openapi.Config{}); err == nil {
		t.Error("no title accepted")
	}
}

func TestUnknownScheme(t *testing.T) {
	r := web.NewRouter()
	key := func(next http.Handler) http.Handler {
		return web.Documented(next, web.MiddlewareDoc{Security: "key"})
	}
	r.With(key).Get("/x", web.H(func(*web.Ctx, struct{}) (web.Empty, error) { return web.Empty{}, nil }))
	if _, _, err := openapi.Spec(r, openapi.Config{Title: "X"}); err == nil || !strings.Contains(err.Error(), `"key"`) {
		t.Errorf("unknown scheme: %v", err)
	}
	cfg := openapi.Config{Title: "X", SecuritySchemes: map[string]openapi.SecurityScheme{"key": {Type: "apiKey", In: "header", Name: "X-Key"}}}
	spec, _, err := openapi.Spec(r, cfg)
	if err != nil || !bytes.Contains(spec, []byte(`"X-Key"`)) {
		t.Errorf("%v\n%s", err, spec)
	}
}

// Register serves the document and adds the openapi command, which
// writes the file or checks it.
func TestAppNew(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{}), anetos.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	r := srv.Router()
	r.Get("/api/v1/products/{id}", web.H(Products{}.Show)).Name("products.show")
	cfg := openapi.Config{Title: "Shop", Prefix: "/api/v1", Path: "/api/v1/openapi.json", File: filepath.Join(t.TempDir(), "openapi.json")}
	if err := openapi.Register(app, srv, cfg); err != nil {
		t.Fatal(err)
	}
	if err := openapi.Register(app, srv, openapi.Config{}); err == nil {
		t.Error("no title accepted")
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	spec, _, _ := openapi.Spec(r, cfg)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || !bytes.Equal(rec.Body.Bytes(), spec) {
		t.Errorf("served: %d %s\n%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if bytes.Contains(spec, []byte("openapi.json")) {
		t.Error("the document describes its own route")
	}

	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := app.ExecuteArgs(context.Background(), append([]string{"openapi"}, args...), &out, &out)
		return code, out.String()
	}
	if code, out := run("--check"); code != 1 || !strings.Contains(out, "doesn't exist") {
		t.Errorf("check without the file: %d %s", code, out)
	}
	if code, out := run(); code != 0 || !strings.Contains(out, "wrote "+cfg.File) {
		t.Errorf("write: %d %s", code, out)
	}
	if b, _ := os.ReadFile(cfg.File); !bytes.Equal(b, spec) {
		t.Errorf("written:\n%s", b)
	}
	if code, out := run("--check"); code != 0 || !strings.Contains(out, "up to date") {
		t.Errorf("check: %d %s", code, out)
	}
	if code, out := run("--out=-"); code != 0 || out != string(spec) {
		t.Errorf("stdout: %d %s", code, out)
	}
	if code, _ := run("extra"); code != 2 {
		t.Errorf("extra argument: %d", code)
	}
}

type Paging struct {
	Page int `query:"page"`
}

type Search struct {
	Paging        // embedded without tags: its query fields are the input's
	Q      string `query:"q" validate:"required"`
}

func (Search) Validate(context.Context) error { return nil }

type Note struct {
	Title string `json:"title" validate:"required"`
}

type NoteInput struct {
	Body string `json:"body"`
}

type Pair[A, B any] struct {
	First  A `json:"first"`
	Second B `json:"second"`
}

type Raw struct{}

func (Raw) MarshalJSON() ([]byte, error) { return []byte(`1`), nil }

type Created struct {
	ID int `json:"id"`
}

func (c Created) Respond(ctx *web.Ctx) error { return ctx.JSON(http.StatusCreated, c) }

// PtrResponder's Respond has a pointer receiver: web.H writes a value of
// it as JSON.
type PtrResponder struct {
	ID int `json:"id"`
}

func (p *PtrResponder) Respond(ctx *web.Ctx) error { return ctx.NoContent() }

// What the types say: names, collisions, generics, validators,
// responders, own JSON encodings.
func TestSchemas(t *testing.T) {
	r := web.NewRouter()
	r.Get("/search", web.H(func(*web.Ctx, Search) ([]other.ProductResponse, error) { return nil, nil }))
	r.Post("/notes", web.H(func(*web.Ctx, Note) (Note, error) { return Note{}, nil }))
	r.Put("/notes", web.H(func(*web.Ctx, NoteInput) (Pair[ProductResponse, *Note], error) {
		return Pair[ProductResponse, *Note]{}, nil
	}))
	r.Get("/raw", web.H(func(*web.Ctx, struct{}) (Raw, error) { return Raw{}, nil }))
	r.Post("/created", web.H(func(*web.Ctx, struct{}) (Created, error) { return Created{}, nil }))
	r.Get("/ptr", web.H(func(*web.Ctx, struct{}) (PtrResponder, error) { return PtrResponder{}, nil }))
	spec, warnings, err := openapi.Spec(r, openapi.Config{Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage
		Components struct{ Schemas map[string]json.RawMessage }
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range doc.Components.Schemas {
		names = append(names, n)
	}
	slices.Sort(names)
	want := []string{"Note", "NoteInput", "NoteInput2", "Owner", "PairProductResponseNote", "Problem", "PtrResponder", "openapi_test.ProductResponse", "other.ProductResponse"}
	if !slices.Equal(names, want) {
		t.Errorf("components: %v", names)
	}
	// The NoteInput type keeps its name; Note's body is numbered.
	if !strings.Contains(compact(string(doc.Paths["/notes"]["post"])), `"$ref":"#/components/schemas/NoteInput2"`) ||
		!strings.Contains(compact(string(doc.Paths["/notes"]["put"])), `"$ref":"#/components/schemas/NoteInput"`) {
		t.Errorf("/notes: %s", doc.Paths["/notes"])
	}
	search := string(doc.Paths["/search"]["get"])
	for _, s := range []string{`"name":"page"`, `"name":"q"`, `"required":true`, `"422"`, `"items":{"$ref":"#/components/schemas/other.ProductResponse"}`} {
		if !strings.Contains(compact(search), s) {
			t.Errorf("/search: no %s in %s", s, search)
		}
	}
	if !strings.Contains(compact(string(doc.Paths["/raw"]["get"])), `"schema":{}`) {
		t.Errorf("/raw: %s", doc.Paths["/raw"]["get"])
	}
	if !strings.Contains(compact(string(doc.Paths["/ptr"]["get"])), `"$ref":"#/components/schemas/PtrResponder"`) {
		t.Errorf("/ptr: %s", doc.Paths["/ptr"]["get"])
	}
	if !strings.Contains(compact(string(doc.Paths["/created"]["post"])), `"2XX"`) {
		t.Errorf("/created: %s", doc.Paths["/created"]["post"])
	}
	if strings.Join(warnings, "\n") != "POST /created: its result is a web.Responder, whose status and body are chosen as it runs: described as any 2XX\n"+
		"openapi_test.Raw has its own MarshalJSON: described as any value" {
		t.Errorf("warnings:\n%s", strings.Join(warnings, "\n"))
	}
}

func compact(s string) string {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

type Problem struct {
	Name string `json:"name"`
}

type Base struct {
	Created time.Time `json:"created"`
}

type WithBase struct {
	*Base
	Name string `json:"name"`
}

type Flags struct {
	Wait   time.Duration   `query:"wait"`
	OK     bool            `json:"ok" validate:"required"`
	Count  *int            `json:"count" validate:"required"`
	Nested struct{ A int } `json:"nested"`
	Plain  string
	File   *multipart.FileHeader `form:"file"`
	Title  string                `json:"title"`
}

func spec(t *testing.T, r *web.Router) (doc struct {
	Paths      map[string]map[string]json.RawMessage
	Components struct{ Schemas map[string]json.RawMessage }
}, warnings []string) {
	t.Helper()
	b, warnings, err := openapi.Spec(r, openapi.Config{Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, warnings
}

// The document says what the server does, where review51 found it
// didn't.
func TestAsServed(t *testing.T) {
	r := web.NewRouter()
	r.Get("/problem", web.H(func(*web.Ctx, struct{}) (Problem, error) { return Problem{}, nil }))
	r.Get("/base", web.H(func(*web.Ctx, struct{}) (WithBase, error) { return WithBase{}, nil }))
	r.Get("/flags", web.H(func(*web.Ctx, Flags) (web.Empty, error) { return web.Empty{}, nil }))
	r.Post("/flags", web.H(func(*web.Ctx, Flags) (web.Empty, error) { return web.Empty{}, nil }))
	r.Get("/any", web.H(func(*web.Ctx, struct{}) (any, error) { return nil, nil }))
	r.Get("/fn", web.H(handler))
	r.Post("/link", web.H(func(*web.Ctx, struct {
		Link string      `json:"link" validate:"required|url:https"`
		Due  anetos.Date `json:"due"`
	}) (web.Empty, error) {
		return web.Empty{}, nil
	}))
	doc, warnings := spec(t, r)
	// The url rule's schemes, not any URI's.
	if !strings.Contains(compact(string(doc.Paths["/link"]["post"])), `"format":"uri","minLength":1,"pattern":"^([hH][tT][tT][pP][sS]):"`) {
		t.Errorf("/link: %s", doc.Paths["/link"]["post"])
	}
	// An optional date may be empty: the zero date.
	if !strings.Contains(compact(string(doc.Paths["/link"]["post"])), `"due":{"anyOf":[{"type":"string","format":"date"},{"type":"string","maxLength":0}]}`) {
		t.Errorf("/link: %s", doc.Paths["/link"]["post"])
	}

	// A type named Problem doesn't take the problem details' name.
	if _, ok := doc.Components.Schemas["Problem2"]; !ok || !strings.Contains(compact(string(doc.Paths["/problem"]["get"])), `"#/components/schemas/Problem2"`) {
		t.Errorf("Problem: %s", doc.Paths["/problem"]["get"])
	}
	// A field promoted through a nil embedded pointer is left out.
	if b := compact(string(doc.Components.Schemas["WithBase"])); !strings.Contains(b, `"required":["name"]`) {
		t.Errorf("WithBase: %s", b)
	}
	get, post := compact(string(doc.Paths["/flags"]["get"])), compact(string(doc.Paths["/flags"]["post"]))
	// No body for GET; a duration parameter is text.
	if strings.Contains(get, "requestBody") || !strings.Contains(get, `"name":"wait","in":"query","schema":{"type":"string"`) {
		t.Errorf("GET /flags: %s", get)
	}
	// A multipart form: the fields a form binds, by their names; a
	// required bool must be true, a required pointer may be 0.
	for _, s := range []string{`"multipart/form-data"`, `"ok":{"type":"boolean","const":true}`, `"count":{"type":"integer"}`, `"title"`, `"file":{"type":"string","format":"binary"}`} {
		if !strings.Contains(post, s) {
			t.Errorf("POST /flags: no %s in %s", s, post)
		}
	}
	for _, s := range []string{`"Plain"`, `"nested"`} {
		if strings.Contains(post, s) {
			t.Errorf("POST /flags: %s in %s", s, post)
		}
	}
	if !slices.Contains(warnings, "GET /any: its result is an interface (interface {}): described as any value; a nil one answers 204") {
		t.Errorf("warnings: %v", warnings)
	}
	// A function's operationId is its name, whatever its package.
	if !strings.Contains(compact(string(doc.Paths["/fn"]["get"])), `"operationId":"handler"`) {
		t.Errorf("/fn: %s", doc.Paths["/fn"]["get"])
	}
}

func handler(*web.Ctx, struct{}) (web.Empty, error) { return web.Empty{}, nil }

// Routes OpenAPI can't tell apart are an error.
func TestSamePath(t *testing.T) {
	h := web.H(func(*web.Ctx, struct{}) (web.Empty, error) { return web.Empty{}, nil })
	r := web.NewRouter()
	r.Get("/a/{id}", h)
	r.Delete("/a/{name}", web.H(func(*web.Ctx, struct{}) (web.Empty, error) { return web.Empty{}, nil }))
	if _, _, err := openapi.Spec(r, openapi.Config{Title: "T"}); err == nil || !strings.Contains(err.Error(), "name their wildcards alike") {
		t.Errorf("wildcards: %v", err)
	}
	r = web.NewRouter()
	r.Host("a.example.com").Get("/x", h)
	r.Host("b.example.com").Get("/x", web.H(func(*web.Ctx, struct{}) (web.Empty, error) { return web.Empty{}, nil }))
	if _, _, err := openapi.Spec(r, openapi.Config{Title: "T"}); err == nil || !strings.Contains(err.Error(), "two routes are GET /x") {
		t.Errorf("hosts: %v", err)
	}
}

// A checkout with Windows line ends is up to date.
func TestCheckCRLF(t *testing.T) {
	cfg := shopConfig
	cfg.File = filepath.Join(t.TempDir(), "openapi.json")
	b, _, _ := openapi.Spec(shop(), cfg)
	if err := os.WriteFile(cfg.File, bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := openapi.Check(shop(), cfg); err != nil {
		t.Error(err)
	}
}

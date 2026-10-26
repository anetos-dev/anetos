// SPDX-License-Identifier: Apache-2.0

package vite_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/view/vite"
)

// manifest is the example of Vite's backend integration guide.
const manifest = `{
  "_shared-B7PI925R.js": {
    "file": "assets/shared-B7PI925R.js",
    "name": "shared",
    "css": ["assets/shared-ChJ_j-JJ.css"]
  },
  "_shared-ChJ_j-JJ.css": {
    "file": "assets/shared-ChJ_j-JJ.css",
    "src": "_shared-ChJ_j-JJ.css"
  },
  "logo.svg": {
    "file": "assets/logo-BuPIv-2h.svg",
    "src": "logo.svg"
  },
  "baz.js": {
    "file": "assets/baz-B2H3sXNv.js",
    "name": "baz",
    "src": "baz.js",
    "isDynamicEntry": true
  },
  "views/bar.js": {
    "file": "assets/bar-gkvgaI9m.js",
    "name": "bar",
    "src": "views/bar.js",
    "isEntry": true,
    "imports": ["_shared-B7PI925R.js"],
    "dynamicImports": ["baz.js"]
  },
  "views/foo.js": {
    "file": "assets/foo-BRBmoGS9.js",
    "name": "foo",
    "src": "views/foo.js",
    "isEntry": true,
    "imports": ["_shared-B7PI925R.js"],
    "css": ["assets/foo-5UjPuW-k.css"]
  },
  "src/app.css": {
    "file": "assets/app-Dq3Lk1Zs.css",
    "src": "src/app.css",
    "isEntry": true
  }
}`

func files() fstest.MapFS {
	big := strings.Repeat("console.log('anetos');\n", 100)
	return fstest.MapFS{
		"build/manifest.json":             {Data: []byte(manifest)},
		"build/assets/foo-BRBmoGS9.js":    {Data: []byte(big)},
		"build/assets/logo-BuPIv-2h.svg":  {Data: []byte("<svg/>")},
		"build/robots-like.txt":           {Data: []byte("x")},
		"build/.vite/manifest.json":       {Data: []byte("{}")},
		"static/app.css":                  {Data: []byte("body{}")},
		"build/assets/shared-B7PI925R.js": {Data: []byte("export {}")},
	}
}

func render(t *testing.T, ctx context.Context, c interface {
	Render(context.Context, io.Writer) error
}) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	env["APP_NAME"] = "blog"
	app, err := anetos.New(anetos.WithSource(env), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

// The tags of the guide's example: stylesheets first (the entry's, then
// its imports'), the script, then the chunks to preload.
func TestBuildTags(t *testing.T) {
	v, err := vite.NewWithFiles(files(), "")
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, context.Background(), v.Tags("views/foo.js"))
	want := `<link rel="stylesheet" href="/build/assets/foo-5UjPuW-k.css">
<link rel="stylesheet" href="/build/assets/shared-ChJ_j-JJ.css">
<script type="module" src="/build/assets/foo-BRBmoGS9.js"></script>
<link rel="modulepreload" href="/build/assets/shared-B7PI925R.js">
`
	if got != want {
		t.Errorf("foo:\n%s\nwant:\n%s", got, want)
	}
	// Two entries share their chunk once; a CSS entry is a stylesheet;
	// a leading slash is the same name.
	got = render(t, context.Background(), v.Tags("/views/foo.js", "views/bar.js", "src/app.css"))
	want = `<link rel="stylesheet" href="/build/assets/foo-5UjPuW-k.css">
<link rel="stylesheet" href="/build/assets/shared-ChJ_j-JJ.css">
<link rel="stylesheet" href="/build/assets/app-Dq3Lk1Zs.css">
<script type="module" src="/build/assets/foo-BRBmoGS9.js"></script>
<script type="module" src="/build/assets/bar-gkvgaI9m.js"></script>
<link rel="modulepreload" href="/build/assets/shared-B7PI925R.js">
`
	if got != want {
		t.Errorf("foo, bar, app.css:\n%s\nwant:\n%s", got, want)
	}
	if err := v.Tags("src/missing.ts").Render(context.Background(), io.Discard); err == nil || !strings.Contains(err.Error(), `"src/missing.ts" isn't an entry of build/manifest.json`) {
		t.Errorf("missing entry: %v", err)
	}
	if u := v.URL("logo.svg"); u != "/build/assets/logo-BuPIv-2h.svg" {
		t.Errorf("URL(logo.svg) = %q", u)
	}
	if u := v.URL("nope.png"); u != "" {
		t.Errorf("URL(nope.png) = %q", u)
	}
	if v.Dev() {
		t.Error("Dev() with a manifest")
	}
}

// Imports are followed recursively, each chunk once, dependencies first.
func TestBuildTagsNestedImports(t *testing.T) {
	m := `{
  "src/main.ts": {"file": "assets/main.js", "isEntry": true, "imports": ["_a.js", "_b.js"]},
  "_a.js": {"file": "assets/a.js", "imports": ["_c.js"], "css": ["assets/a.css"]},
  "_b.js": {"file": "assets/b.js", "imports": ["_c.js", "src/main.ts"]},
  "_c.js": {"file": "assets/c.js", "css": ["assets/c.css", "assets/a.css"]}
}`
	v, err := vite.NewWithFiles(fstest.MapFS{"build/manifest.json": {Data: []byte(m)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, context.Background(), v.Tags("src/main.ts"))
	want := `<link rel="stylesheet" href="/build/assets/c.css">
<link rel="stylesheet" href="/build/assets/a.css">
<script type="module" src="/build/assets/main.js"></script>
<link rel="modulepreload" href="/build/assets/c.js">
<link rel="modulepreload" href="/build/assets/a.js">
<link rel="modulepreload" href="/build/assets/b.js">
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDevTags(t *testing.T) {
	v, err := vite.NewWithFiles(nil, "/build/")
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, context.Background(), v.Tags("src/main.ts", "src/app.css"))
	want := `<script type="module" src="/build/@vite/client"></script>
<script type="module" src="/build/src/main.ts"></script>
<link rel="stylesheet" href="/build/src/app.css">
`
	if got != want || !v.Dev() || v.URL("/src/logo.svg") != "/build/src/logo.svg" {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	v, err = vite.NewWithFiles(nil, "http://localhost:5173/build/", vite.WithReactRefresh())
	if err != nil {
		t.Fatal(err)
	}
	got = render(t, context.Background(), v.Tags("src/main.tsx"))
	if !strings.HasPrefix(got, `<script type="module">
import RefreshRuntime from "http://localhost:5173/build/@react-refresh"
RefreshRuntime.injectIntoGlobalHook(window)`) || !strings.Contains(got, "window.__vite_plugin_react_preamble_installed__ = true\n</script>\n") ||
		!strings.HasSuffix(got, `<script type="module" src="http://localhost:5173/build/src/main.tsx"></script>
`) {
		t.Errorf("react:\n%s", got)
	}
	// The preamble's URL is a JavaScript string, whatever it holds.
	v, err = vite.NewWithFiles(nil, "http://x/</script>\u2028/", vite.WithReactRefresh())
	if err != nil {
		t.Fatal(err)
	}
	if got := render(t, context.Background(), v.Tags("src/main.tsx")); !strings.Contains(got, `import RefreshRuntime from "http://x/\u003c/script\u003e\u2028/@react-refresh"`) {
		t.Errorf("escaping:\n%s", got)
	}
	for _, bad := range []string{"build/", "/build", "ftp://x/build/", "http:///build/", "//x/build/", "/build/?x=1", "/build/#x", "http://u:p@x/build/"} {
		if _, err := vite.NewWithFiles(nil, bad); err == nil || !strings.Contains(err.Error(), "VITE_DEV_URL") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := vite.NewWithFiles(fstest.MapFS{}, ""); err == nil || !strings.Contains(err.Error(), "npm run build") {
		t.Errorf("no manifest: %v", err)
	}
	if _, err := vite.NewWithFiles(nil, ""); err == nil || !strings.Contains(err.Error(), "needs the web root's files") {
		t.Errorf("no files: %v", err)
	}
}

// New: in the app's contexts and container; the settings; what a
// missing manifest does in each environment.
func TestNew(t *testing.T) {
	app := newApp(t, config.Map{"APP_ENV": "development"})
	v, err := vite.New(app, files())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := anetos.Resolve[*vite.Vite](app); err != nil || got != v {
		t.Errorf("Resolve: %v", err)
	}
	ctx := app.Context(context.Background())
	if got, err := vite.From(ctx); err != nil || got != v {
		t.Errorf("From: %v", err)
	}
	if got := render(t, ctx, vite.Tags("views/bar.js")); !strings.Contains(got, `src="/build/assets/bar-gkvgaI9m.js"`) {
		t.Errorf("Tags from the context:\n%s", got)
	}
	if vite.URL(ctx, "logo.svg") != "/build/assets/logo-BuPIv-2h.svg" || vite.URL(context.Background(), "logo.svg") != "" {
		t.Error("URL from the context")
	}
	if _, err := vite.New(app, files()); err == nil || !strings.Contains(err.Error(), "New called twice") {
		t.Errorf("twice: %v", err)
	}
	if _, err := vite.From(context.Background()); !errors.Is(err, vite.ErrNoVite) {
		t.Errorf("From without: %v", err)
	}
	if err := vite.Tags("x.js").Render(context.Background(), io.Discard); !errors.Is(err, vite.ErrNoVite) {
		t.Errorf("Tags without: %v", err)
	}

	// No build: a comment in development and tests, and a doctor warning
	// in development; an error where the app is deployed.
	for _, env := range []string{"development", "testing"} {
		app := newApp(t, config.Map{"APP_ENV": env})
		v, err := vite.New(app, fstest.MapFS{})
		if err != nil {
			t.Fatalf("%s: %v", env, err)
		}
		if got := render(t, context.Background(), v.Tags("src/main.ts")); got != "<!-- vite: no build/manifest.json (run anetos dev, or npm run build in frontend/) -->\n" {
			t.Errorf("%s: %q", env, got)
		}
		var out bytes.Buffer
		app.ExecuteArgs(context.Background(), []string{"doctor"}, &out, &out)
		if warned := strings.Contains(out.String(), "build/manifest.json is missing and VITE_DEV_URL isn't set"); warned != (env == "development") {
			t.Errorf("%s: doctor:\n%s", env, out.String())
		}
	}
	for _, env := range []string{"production", "staging"} {
		app := newApp(t, config.Map{"APP_ENV": env, "APP_KEY": "base64:" + strings.Repeat("A", 43)})
		if _, err := vite.New(app, fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "read build/manifest.json") {
			t.Errorf("%s without a build: %v", env, err)
		}
		app = newApp(t, config.Map{"APP_ENV": env, "APP_KEY": "base64:" + strings.Repeat("A", 43), "VITE_DEV_URL": "/build/"})
		if _, err := vite.New(app, files()); err == nil || !strings.Contains(err.Error(), "VITE_DEV_URL is set with APP_ENV="+env) {
			t.Errorf("%s with VITE_DEV_URL: %v", env, err)
		}
	}
	// No files, or a manifest that isn't one: errors everywhere.
	app = newApp(t, config.Map{"APP_ENV": "development"})
	if _, err := vite.New(app, nil); err == nil || !strings.Contains(err.Error(), "needs the web root's files") {
		t.Errorf("nil files: %v", err)
	}
	if _, err := vite.New(app, fstest.MapFS{"build/manifest.json": {Data: []byte("{")}}); err == nil || !strings.Contains(err.Error(), "read build/manifest.json: unexpected end of JSON input") {
		t.Errorf("a malformed manifest: %v", err)
	}
	// VITE_DEV_URL: the development server, no manifest needed.
	app = newApp(t, config.Map{"APP_ENV": "development", "VITE_DEV_URL": "/build/"})
	if v, err := vite.New(app, fstest.MapFS{}); err != nil || !v.Dev() {
		t.Errorf("dev: %v", err)
	}
	app = newApp(t, config.Map{"APP_ENV": "development", "VITE_DEV_URL": "localhost:5173"})
	if _, err := vite.New(app, fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "VITE_DEV_URL") {
		t.Errorf("a bad VITE_DEV_URL: %v", err)
	}
}

func TestServe(t *testing.T) {
	app := newApp(t, config.Map{"APP_ENV": "testing"})
	if _, err := vite.New(app, files()); err != nil {
		t.Fatal(err)
	}
	get := func(target string, h http.Header) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(app.Context(context.Background()), http.MethodGet, target, nil)
		maps.Copy(req.Header, h)
		rec := httptest.NewRecorder()
		vite.Handler().ServeHTTP(rec, req)
		return rec
	}
	res := get("/build/assets/foo-BRBmoGS9.js", nil)
	body := res.Body.Bytes()
	if res.Code != 200 || res.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.HasPrefix(res.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(string(body), "anetos") ||
		res.Header().Get("Vary") != "Accept-Encoding" || res.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("asset: %d %v", res.Code, res.Header())
	}
	etag := res.Header().Get("ETag")
	if res := get("/build/assets/foo-BRBmoGS9.js", http.Header{"If-None-Match": {etag}}); res.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", res.Code)
	}
	res = get("/build/assets/foo-BRBmoGS9.js", http.Header{"Accept-Encoding": {"br, gzip"}})
	if res.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: %v", res.Header())
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); !bytes.Equal(b, body) {
		t.Error("the gzipped body differs")
	}
	if res := get("/build/assets/foo-BRBmoGS9.js", http.Header{"Accept-Encoding": {"gzip;q=0"}}); res.Header().Get("Content-Encoding") != "" {
		t.Error("gzip;q=0 got gzip")
	}
	if res := get("/build/robots-like.txt", nil); res.Code != 200 || res.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("a file outside assets/: %d %v", res.Code, res.Header())
	}
	for _, p := range []string{"/build/manifest.json", "/build/.vite/manifest.json", "/build/", "/build/../static/app.css",
		"/build/assets/../manifest.json", "/build/nope.js", "/static/app.css"} {
		if res := get(p, nil); res.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, res.Code)
		}
	}
	req := httptest.NewRequestWithContext(app.Context(context.Background()), http.MethodPost, "/build/assets/foo-BRBmoGS9.js", nil)
	rec := httptest.NewRecorder()
	vite.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d", rec.Code)
	}
	// Without a Vite in the request's context: 404.
	rec = httptest.NewRecorder()
	vite.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/build/assets/foo-BRBmoGS9.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("without a Vite: %d", rec.Code)
	}
	// A file system whose files can't seek; a development Vite without
	// files.
	v, err := vite.NewWithFiles(plainFS{files()}, "")
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	v.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/build/robots-like.txt", nil))
	if rec.Code != 200 || rec.Body.String() != string(files()["build/robots-like.txt"].Data) {
		t.Errorf("unseekable: %d %q", rec.Code, rec.Body.String())
	}
	if v, err = vite.NewWithFiles(nil, "/build/"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	v.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/build/robots-like.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("no files: %d", rec.Code)
	}
}

// plainFS hides its files' Seek.
type plainFS struct{ fs fs.FS }

func (p plainFS) Open(name string) (fs.File, error) {
	f, err := p.fs.Open(name)
	if err != nil {
		return nil, err
	}
	return struct{ fs.File }{f}, nil
}

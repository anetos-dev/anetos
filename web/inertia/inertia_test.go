// SPDX-License-Identifier: Apache-2.0

package inertia_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/vite"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/inertia"
)

type Post struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

type Stats struct {
	Count int `json:"count"`
}

type Base struct {
	AppName string `json:"appName"`
}

// PostsIndex has every kind of prop.
type PostsIndex struct {
	Base
	Posts    []Post                       `json:"posts"`
	Filter   string                       `json:"filter,omitempty"`
	Hidden   string                       `json:"-"`
	Lazy     inertia.Prop[int]            `json:"lazy"`
	Always   inertia.Prop[string]         `json:"always"`
	Optional inertia.Prop[[]string]       `json:"optional"`
	Stats    inertia.Prop[Stats]          `json:"stats"`
	Slow     inertia.Prop[Stats]          `json:"slow"`
	Broken   inertia.Prop[Stats]          `json:"broken"`
	Feed     inertia.Prop[map[string]any] `json:"feed"`
	Plans    inertia.Prop[[]string]       `json:"plans"`
	Unset    inertia.Prop[int]            `json:"unset"`
}

var postsIndex = inertia.NewPage[PostsIndex]("Posts/Index")

type Shared struct {
	User    string `json:"user"`
	AppName string `json:"appName"`
}

var root = view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><title>%s</title></head><body>", inertia.Component(ctx))
	if err := inertia.App().Render(ctx, w); err != nil {
		return err
	}
	_, err := io.WriteString(w, "</body></html>")
	return err
})

type fixture struct {
	srv          *httptest.Server
	client       *http.Client
	lazyRuns     atomic.Int32
	optionalRuns atomic.Int32
}

func newFixture(t *testing.T, opts ...inertia.Option) *fixture {
	t.Helper()
	opts = append([]inertia.Option{inertia.WithVersion("v1"), inertia.WithShared(func(c *web.Ctx) (Shared, error) {
		if c.Query("sharedfail") != "" {
			return Shared{}, errors.New("no user")
		}
		return Shared{User: "ada", AppName: "Shared name"}, nil
	})}, opts...)
	in, err := inertia.NewWithRoot(root, opts...)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.NewEncrypter(k)
	no := false
	cfg := session.DefaultConfig()
	cfg.Secure = &no
	sessions, err := session.NewManager(cfg, enc)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{}
	r := web.NewRouter()
	r.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(inertia.WithInertia(r.Context(), in)))
		})
	})
	r.ErrorPages(inertia.ErrorPages("Error"))
	g := r.Group("", sessions.Middleware, web.CSRF(web.WithXSRFCookie()), inertia.Middleware)
	g.Get("/posts", func(c *web.Ctx) error {
		return postsIndex.Render(c, PostsIndex{
			Base:   Base{AppName: "Page name"},
			Posts:  []Post{{1, "Hello </script>"}},
			Filter: c.Query("filter"),
			Lazy: inertia.Func(func(context.Context) (int, error) {
				f.lazyRuns.Add(1)
				return 42, nil
			}),
			Always: inertia.Always("always"),
			Optional: inertia.Optional(func(context.Context) ([]string, error) {
				f.optionalRuns.Add(1)
				return []string{"a"}, nil
			}),
			Stats: inertia.Defer(func(context.Context) (Stats, error) { return Stats{7}, nil }),
			Slow:  inertia.Defer(func(context.Context) (Stats, error) { return Stats{9}, nil }).Group("slow"),
			Broken: inertia.Defer(func(context.Context) (Stats, error) { return Stats{}, errors.New("down") }).
				Group("slow").Rescue(),
			Feed: inertia.Merge(map[string]any{"data": []int{1}, "next": 2}).Append("data").Prepend("pinned").MatchOn("data.id"),
			Plans: inertia.Once(func(context.Context) ([]string, error) { return []string{"free"}, nil }).
				ExpiresIn(time.Hour),
		})
	})
	// A typed handler's page: Page.Responder.
	g.Get("/typed", web.H(func(c *web.Ctx, _ struct{}) (web.Responder, error) {
		return postsIndex.Responder(PostsIndex{Posts: []Post{{ID: 7, Title: "Typed"}}}), nil
	}))
	g.Get("/fail", func(c *web.Ctx) error {
		return inertia.Render(c, "Fail", inertia.Props{
			"x": inertia.Func(func(context.Context) (int, error) { return 0, errors.New("boom") }),
		})
	})
	g.Get("/form", func(c *web.Ctx) error {
		return inertia.Render(c, "Form", inertia.Props{"deep": inertia.Value(1).DeepMerge()})
	})
	g.Post("/form", func(c *web.Ctx) error {
		if c.Query("ok") != "" {
			inertia.Flash(c, "toast", "Saved")
			inertia.ClearHistory(c)
			inertia.PreserveFragment(c)
			return c.Redirect(http.StatusSeeOther, "/form")
		}
		return &web.HTTPError{Status: http.StatusUnprocessableEntity, Message: "invalid", Fields: map[string]string{"title": "The title is required."}}
	})
	g.Get("/now", func(c *web.Ctx) error {
		inertia.Flash(c, "toast", "Now")
		inertia.EncryptHistory(c)
		return inertia.Render(c, "Now", nil)
	})
	g.Put("/move", func(c *web.Ctx) error { return c.Redirect(http.StatusFound, c.Query("to")) })
	g.Get("/away", func(c *web.Ctx) error { return inertia.Location(c, "https://example.com/pay") })
	// Pages that aren't Inertia's: a templ page not moved yet, a file.
	g.Get("/plain", func(c *web.Ctx) error {
		return c.HTML(http.StatusOK, "<p>"+c.Session().GetString("status")+"</p>")
	})
	g.Post("/plain", func(c *web.Ctx) error {
		c.Session().Flash("status", "Moved")
		return c.Redirect(http.StatusSeeOther, "/plain?x=1")
	})
	g.Post("/plain-page", func(c *web.Ctx) error { return c.HTML(http.StatusOK, "<p>posted</p>") })
	g.Get("/file.txt", func(c *web.Ctx) error { return c.Text(http.StatusOK, "text") })
	g.Get("/sniffed", func(c *web.Ctx) error {
		_, err := c.Writer().Write([]byte("<!doctype html><p>no type</p>"))
		return err
	})
	g.Get("/headers", func(c *web.Ctx) error {
		h := c.Writer().Header()
		h.Set("ETag", `"1"`)
		h.Set("Content-Length", "21")
		h.Set("Content-Type", "application/xhtml+xml")
		return c.HTML(http.StatusOK, "<p>xhtml, with etag</p>")
	})
	g.Get("/streamed", func(c *web.Ctx) error {
		c.Writer().Header().Set("Content-Type", "text/html")
		_ = http.NewResponseController(c.Writer()).Flush()
		_, err := c.Writer().Write([]byte("<p>streamed</p>"))
		return err
	})
	g.Get("/teapot", func(*web.Ctx) error {
		return &web.HTTPError{Status: http.StatusTeapot, Message: "Short and stout.", Fields: map[string]string{"tea": "Too hot."}}
	})
	f.srv = httptest.NewServer(r)
	t.Cleanup(f.srv.Close)
	jar, _ := cookiejar.New(nil)
	f.client = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return f
}

type response struct {
	status int
	header http.Header
	body   string
}

// do sends a request; headers are name, value pairs.
func (f *fixture) do(t *testing.T, method, path string, body io.Reader, headers ...string) response {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html, application/xhtml+xml")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return response{res.StatusCode, res.Header, string(b)}
}

// visit sends a visit of Inertia's client.
func (f *fixture) visit(t *testing.T, method, path string, headers ...string) response {
	t.Helper()
	var body io.Reader
	if method != http.MethodGet {
		body = strings.NewReader(`{}`)
		headers = append(headers, "Content-Type", "application/json", "X-XSRF-TOKEN", f.cookie("XSRF-TOKEN"))
	}
	return f.do(t, method, path, body, append([]string{"X-Inertia", "true", "X-Inertia-Version", "v1", "X-Requested-With", "XMLHttpRequest"}, headers...)...)
}

func (f *fixture) cookie(name string) string {
	u, _ := url.Parse(f.srv.URL)
	for _, c := range f.client.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func page(t *testing.T, res response) *inertia.PageObject {
	t.Helper()
	p, err := inertia.ReadPage([]byte(res.body))
	if err != nil {
		t.Fatalf("%d %v: %v\n%s", res.status, res.header, err, res.body)
	}
	return p
}

func prop[T any](t *testing.T, p *inertia.PageObject, name string) T {
	t.Helper()
	var v T
	raw, ok := p.Props[name]
	if !ok {
		t.Fatalf("no prop %q in %v", name, keys(p))
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func keys(p *inertia.PageObject) []string {
	var ks []string
	for k := range p.Props {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// The first visit: the root view's HTML with the page object; a visit of
// the client: the page object, with the props a full visit gets.
func TestFirstVisitAndVisit(t *testing.T) {
	f := newFixture(t)
	res := f.do(t, http.MethodGet, "/posts?filter=new", nil)
	if res.status != 200 || res.header.Get("Content-Type") != "text/html; charset=utf-8" || !slices.Contains(res.header.Values("Vary"), "X-Inertia") {
		t.Fatalf("first visit: %d %v", res.status, res.header)
	}
	if !strings.Contains(res.body, "<title>Posts/Index</title>") || !strings.Contains(res.body, `<script data-page="app" type="application/json">{"component":"Posts\/Index"`) ||
		!strings.Contains(res.body, "</script>\n<div id=\"app\"></div>") || strings.Contains(res.body, "Hello </script>") || !strings.Contains(res.body, `Hello \u003c\/script\u003e`) {
		t.Errorf("first visit:\n%s", res.body)
	}
	p := page(t, res)
	if p.Component != "Posts/Index" || p.URL != "/posts?filter=new" || p.Version != "v1" {
		t.Errorf("page %+v", p)
	}
	want := []string{"always", "appName", "errors", "feed", "filter", "lazy", "plans", "posts", "user"}
	if got := keys(p); !slices.Equal(got, want) {
		t.Errorf("props %v, want %v", got, want)
	}
	if prop[string](t, p, "appName") != "Page name" || prop[string](t, p, "user") != "ada" || prop[int](t, p, "lazy") != 42 ||
		prop[[]Post](t, p, "posts")[0].Title != "Hello </script>" || len(prop[map[string]string](t, p, "errors")) != 0 {
		t.Errorf("props %s", res.body)
	}
	if !slices.Equal(p.SharedProps, []string{"user"}) {
		t.Errorf("shared %v", p.SharedProps)
	}
	if fmt.Sprint(p.DeferredProps) != "map[default:[stats] slow:[slow broken]]" {
		t.Errorf("deferred %v", p.DeferredProps)
	}
	if !slices.Equal(p.MergeProps, []string{"feed.data"}) || !slices.Equal(p.PrependProps, []string{"feed.pinned"}) || !slices.Equal(p.MatchPropsOn, []string{"feed.data.id"}) {
		t.Errorf("merge %v %v %v", p.MergeProps, p.PrependProps, p.MatchPropsOn)
	}
	op := p.OnceProps["plans"]
	if op.Prop != "plans" || op.ExpiresAt == nil || *op.ExpiresAt < time.Now().Add(59*time.Minute).UnixMilli() {
		t.Errorf("once %+v", p.OnceProps)
	}
	if p.EncryptHistory || p.ClearHistory || p.Flash != nil || f.optionalRuns.Load() != 0 {
		t.Errorf("extras %+v %d", p, f.optionalRuns.Load())
	}

	res = f.visit(t, http.MethodGet, "/posts")
	if res.status != 200 || res.header.Get("X-Inertia") != "true" || !strings.HasPrefix(res.header.Get("Content-Type"), "application/json") {
		t.Fatalf("visit: %d %v", res.status, res.header)
	}
	if p := page(t, res); p.Component != "Posts/Index" || !slices.Contains(keys(p), "lazy") || p.Props["filter"] != nil {
		t.Errorf("visit: %s", res.body)
	}
	// The client holds the once prop: left out, still listed.
	res = f.visit(t, http.MethodGet, "/posts", "X-Inertia-Except-Once-Props", "plans,other")
	if p := page(t, res); p.Props["plans"] != nil || p.OnceProps["plans"].Prop != "plans" {
		t.Errorf("held once prop: %s", res.body)
	}
	if p := page(t, f.visit(t, http.MethodGet, "/typed")); p.Component != "Posts/Index" || prop[[]Post](t, p, "posts")[0].Title != "Typed" {
		t.Errorf("Responder: %+v", p)
	}
	// A failing prop, and failing shared props, fail the response.
	if res := f.visit(t, http.MethodGet, "/fail"); res.status != 500 {
		t.Errorf("failing prop: %d", res.status)
	}
	if res := f.visit(t, http.MethodGet, "/posts?sharedfail=1"); res.status != 500 {
		t.Errorf("failing shared: %d", res.status)
	}
}

// Partial reloads: only the props asked for (always props too), optional
// and deferred ones included; rescued deferred props; reset props lose
// their merge labels.
func TestPartialReload(t *testing.T) {
	f := newFixture(t)
	partial := func(only, except string, more ...string) *inertia.PageObject {
		t.Helper()
		h := append([]string{"X-Inertia-Partial-Component", "Posts/Index", "X-Inertia-Partial-Data", only, "X-Inertia-Partial-Except", except}, more...)
		return page(t, f.visit(t, http.MethodGet, "/posts", h...))
	}
	p := partial("optional,stats", "")
	if got := keys(p); !slices.Equal(got, []string{"always", "errors", "optional", "stats"}) || prop[Stats](t, p, "stats").Count != 7 || f.lazyRuns.Load() != 0 {
		t.Errorf("only: %v (lazy ran %d)", got, f.lazyRuns.Load())
	}
	if p.DeferredProps != nil {
		t.Errorf("deferred announced on a partial reload: %v", p.DeferredProps)
	}
	p = partial("slow,broken", "")
	if !slices.Equal(p.RescuedProps, []string{"broken"}) || p.Props["broken"] != nil || prop[Stats](t, p, "slow").Count != 9 {
		t.Errorf("rescued: %+v", p)
	}
	p = partial("", "posts,lazy,feed,plans,appName,user")
	if got := keys(p); !slices.Equal(got, []string{"always", "broken", "errors", "optional", "slow", "stats"}) && !slices.Equal(got, []string{"always", "errors", "optional", "slow", "stats"}) {
		t.Errorf("except: %v", got)
	}
	// A nested path asks for its top-level prop.
	if p = partial("feed.data", ""); !slices.Equal(p.MergeProps, []string{"feed.data"}) {
		t.Errorf("merge on a partial reload: %+v", p)
	}
	if p = partial("feed", "", "X-Inertia-Reset", "feed"); p.MergeProps != nil || p.Props["feed"] == nil {
		t.Errorf("reset: %+v", p)
	}
	// Once props are sent when asked for, held or not.
	if p = partial("plans", "", "X-Inertia-Except-Once-Props", "plans"); p.Props["plans"] == nil {
		t.Errorf("once prop asked for: %+v", p)
	}
	// Another component's partial reload is a full visit.
	p = page(t, f.visit(t, http.MethodGet, "/posts", "X-Inertia-Partial-Component", "Other", "X-Inertia-Partial-Data", "stats"))
	if p.Props["stats"] != nil || p.Props["posts"] == nil {
		t.Errorf("other component: %v", keys(p))
	}
}

// A page built with other assets reloads whole, keeping the flashed data.
func TestVersion(t *testing.T) {
	f := newFixture(t)
	f.do(t, http.MethodGet, "/form", nil)
	if res := f.visit(t, http.MethodPost, "/form?ok=1"); res.status != http.StatusSeeOther {
		t.Fatalf("post: %d", res.status)
	}
	res := f.visit(t, http.MethodGet, "/form?x=1", "X-Inertia-Version", "v0")
	if res.status != http.StatusConflict || res.header.Get("X-Inertia-Location") != "/form?x=1" || res.header.Get("X-Inertia-Version") != "v1" || res.header.Get("X-Inertia") != "" {
		t.Fatalf("mismatch: %d %v", res.status, res.header)
	}
	if p := page(t, f.do(t, http.MethodGet, "/form", nil)); !strings.Contains(string(p.Flash["toast"]), "Saved") {
		t.Errorf("flash lost across the reload: %+v", p)
	}
	// Non-GET visits are never refused for their version.
	f.do(t, http.MethodGet, "/form", nil)
	if res := f.visit(t, http.MethodPost, "/form?ok=1", "X-Inertia-Version", "v0"); res.status != http.StatusSeeOther {
		t.Errorf("POST with an old version: %d", res.status)
	}
}

// A failed post redirects back with its errors, under the form's error
// bag; a successful one leaves flash data and history flags for the
// page it redirects to, once.
func TestFormFlow(t *testing.T) {
	f := newFixture(t)
	f.do(t, http.MethodGet, "/form", nil)
	res := f.visit(t, http.MethodPost, "/form", "Referer", f.srv.URL+"/form")
	if res.status != http.StatusSeeOther || res.header.Get("Location") != "/form" {
		t.Fatalf("invalid post: %d %v %s", res.status, res.header, res.body)
	}
	p := page(t, f.visit(t, http.MethodGet, "/form"))
	if prop[map[string]string](t, p, "errors")["title"] != "The title is required." {
		t.Errorf("errors: %s", p.Props["errors"])
	}
	if p = page(t, f.visit(t, http.MethodGet, "/form")); len(prop[map[string]string](t, p, "errors")) != 0 {
		t.Errorf("errors kept: %s", p.Props["errors"])
	}
	f.visit(t, http.MethodPost, "/form", "Referer", f.srv.URL+"/form", "X-Inertia-Error-Bag", "createPost")
	p = page(t, f.visit(t, http.MethodGet, "/form"))
	if prop[map[string]map[string]string](t, p, "errors")["createPost"]["title"] != "The title is required." {
		t.Errorf("error bag: %s", p.Props["errors"])
	}
	if p.DeepMergeProps == nil || p.DeepMergeProps[0] != "deep" {
		t.Errorf("deep merge: %v", p.DeepMergeProps)
	}

	f.visit(t, http.MethodPost, "/form?ok=1")
	p = page(t, f.visit(t, http.MethodGet, "/form"))
	if string(p.Flash["toast"]) != `"Saved"` || !p.ClearHistory || !p.PreserveFragment {
		t.Errorf("after the redirect: %+v", p)
	}
	if p = page(t, f.visit(t, http.MethodGet, "/form")); p.Flash != nil || p.ClearHistory || p.PreserveFragment {
		t.Errorf("kept a second time: %+v", p)
	}
	// Flashed and rendered at once: on this page, not the next.
	if p = page(t, f.visit(t, http.MethodGet, "/now")); string(p.Flash["toast"]) != `"Now"` || !p.EncryptHistory {
		t.Errorf("now: %+v", p)
	}
	if p = page(t, f.visit(t, http.MethodGet, "/form")); p.Flash != nil {
		t.Errorf("flashed twice: %+v", p)
	}
	// The app-wide option.
	f = newFixture(t, inertia.WithEncryptHistory())
	if p = page(t, f.visit(t, http.MethodGet, "/form")); !p.EncryptHistory {
		t.Errorf("WithEncryptHistory: %+v", p)
	}
}

// Redirects: 303 after PUT, fragments through X-Inertia-Redirect, other
// sites through X-Inertia-Location.
func TestRedirects(t *testing.T) {
	f := newFixture(t)
	f.do(t, http.MethodGet, "/form", nil)
	if res := f.visit(t, http.MethodPut, "/move?to=/posts"); res.status != http.StatusSeeOther || res.header.Get("Location") != "/posts" {
		t.Errorf("PUT 302: %d %v", res.status, res.header)
	}
	if res := f.visit(t, http.MethodPut, "/move?to=/posts%23comments"); res.status != http.StatusConflict ||
		res.header.Get("X-Inertia-Redirect") != "/posts#comments" || res.header.Get("Location") != "" || res.body != "" {
		t.Errorf("fragment: %d %v %q", res.status, res.header, res.body)
	}
	if res := f.visit(t, http.MethodPut, "/move?to=/posts%23comments", "Purpose", "prefetch"); res.status != http.StatusSeeOther {
		t.Errorf("prefetch: %d", res.status)
	}
	// Not the client's: the browser follows the redirect itself.
	req := strings.NewReader("")
	if res := f.do(t, http.MethodPut, "/move?to=/posts%23c", req, "X-XSRF-TOKEN", f.cookie("XSRF-TOKEN")); res.status != http.StatusFound {
		t.Errorf("browser PUT: %d", res.status)
	}
	if res := f.visit(t, http.MethodGet, "/away"); res.status != http.StatusConflict || res.header.Get("X-Inertia-Location") != "https://example.com/pay" {
		t.Errorf("Location: %d %v", res.status, res.header)
	}
	if res := f.do(t, http.MethodGet, "/away", nil); res.status != http.StatusSeeOther || res.header.Get("Location") != "https://example.com/pay" {
		t.Errorf("Location for a browser: %d %v", res.status, res.header)
	}
}

// A visit to a page that isn't Inertia's loads it whole (409 with
// X-Inertia-Location), keeping what was flashed for it; other answers,
// and requests that aren't the client's visits, are left as they are.
func TestPlainPages(t *testing.T) {
	f := newFixture(t)
	f.do(t, http.MethodGet, "/form", nil)
	if res := f.visit(t, http.MethodPost, "/plain"); res.status != http.StatusSeeOther {
		t.Fatalf("post: %d", res.status)
	}
	res := f.visit(t, http.MethodGet, "/plain?x=1")
	if res.status != http.StatusConflict || res.header.Get("X-Inertia-Location") != "/plain?x=1" || res.body != "" ||
		res.header.Get("Content-Type") != "" || res.header.Get("X-Inertia") != "" {
		t.Fatalf("visit: %d %v %q", res.status, res.header, res.body)
	}
	if res := f.do(t, http.MethodGet, "/plain?x=1", nil); res.status != http.StatusOK || res.body != "<p>Moved</p>" {
		t.Errorf("the load after it: %d %q", res.status, res.body)
	}
	if res := f.do(t, http.MethodGet, "/plain", nil); res.body != "<p></p>" {
		t.Errorf("flashed twice: %q", res.body)
	}
	if res := f.visit(t, http.MethodGet, "/plain", "Purpose", "prefetch"); res.status != http.StatusOK {
		t.Errorf("prefetch: %d", res.status)
	}
	if res := f.visit(t, http.MethodPost, "/plain-page"); res.status != http.StatusOK || res.body != "<p>posted</p>" {
		t.Errorf("POST: %d %q", res.status, res.body)
	}
	if res := f.visit(t, http.MethodGet, "/file.txt"); res.status != http.StatusOK || res.body != "text" {
		t.Errorf("text: %d %q", res.status, res.body)
	}
	if res := f.visit(t, http.MethodGet, "/posts"); res.status != http.StatusOK || res.header.Get("X-Inertia") != "true" {
		t.Errorf("Inertia's page: %d %v", res.status, res.header)
	}
	if res := f.visit(t, http.MethodGet, "/teapot"); res.status != http.StatusTeapot {
		t.Errorf("error page: %d", res.status)
	}
	// HTML without a Content-Type (net/http sniffs it), XHTML, a page
	// flushed before it writes: the same; validators and lengths go.
	for _, path := range []string{"/sniffed", "/headers", "/streamed"} {
		res := f.visit(t, http.MethodGet, path)
		if res.status != http.StatusConflict || res.header.Get("X-Inertia-Location") != path || res.body != "" ||
			res.header.Get("ETag") != "" || res.header.Get("Content-Length") == "21" {
			t.Errorf("%s: %d %v %q", path, res.status, res.header, res.body)
		}
	}
	// An old asset version answers first, as for Inertia's pages.
	if res := f.visit(t, http.MethodGet, "/plain", "X-Inertia-Version", "v0"); res.status != http.StatusConflict || res.header.Get("X-Inertia-Version") != "v1" {
		t.Errorf("old version: %d %v", res.status, res.header)
	}
}

// Error pages are the frontend's page, with the error's status.
func TestErrorPages(t *testing.T) {
	f := newFixture(t)
	res := f.visit(t, http.MethodGet, "/nowhere")
	p := page(t, res)
	if res.status != 404 || res.header.Get("X-Inertia") != "true" || p.Component != "Error" || prop[int](t, p, "status") != 404 ||
		prop[string](t, p, "title") != "Not Found" || p.URL != "/nowhere" || prop[string](t, p, "user") != "ada" {
		t.Errorf("404: %d %s", res.status, res.body)
	}
	res = f.do(t, http.MethodGet, "/teapot", nil)
	p = page(t, res)
	if res.status != http.StatusTeapot || !strings.Contains(res.body, "<!doctype html>") || prop[string](t, p, "detail") != "Short and stout." ||
		prop[map[string]string](t, p, "errors")["tea"] != "Too hot." {
		t.Errorf("418: %d %s", res.status, res.body)
	}
	// JSON clients still get problem details.
	if res := f.do(t, http.MethodGet, "/nowhere", nil, "Accept", "application/json"); !strings.Contains(res.header.Get("Content-Type"), "problem+json") {
		t.Errorf("JSON client: %v", res.header)
	}
	// Without an Inertia: the built-in page.
	r := web.NewRouter()
	r.ErrorPages(inertia.ErrorPages("Error"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nowhere", nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "<!doctype html>") || strings.Contains(rec.Body.String(), "data-page") {
		t.Errorf("without Inertia: %d %s", rec.Code, rec.Body)
	}
	if err := (inertia.ErrorPages("Error")(nil, web.ErrorPage{})).Render(context.Background(), io.Discard); err == nil {
		t.Error("an error page rendered")
	}
}

func TestMistakes(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s didn't panic", name)
			}
		}()
		fn()
	}
	type errs struct {
		E string `json:"errors"`
	}
	mustPanic("empty name", func() { inertia.NewPage[PostsIndex]("") })
	mustPanic("not a struct", func() { inertia.NewPage[[]int]("X") })
	mustPanic("errors", func() { inertia.NewPage[errs]("X") })
	mustPanic("ErrorPages without a name", func() { inertia.ErrorPages("") })
	if _, err := inertia.NewWithRoot(nil); err == nil {
		t.Error("nil root")
	}
	if _, err := inertia.NewWithRoot(root, inertia.WithShared[Shared](nil)); err == nil {
		t.Error("nil shared function")
	}
	if _, err := inertia.NewWithRoot(root, inertia.WithShared(func(*web.Ctx) (int, error) { return 0, nil })); err == nil {
		t.Error("shared int")
	}
	if _, err := inertia.From(context.Background()); !errors.Is(err, inertia.ErrNoInertia) {
		t.Errorf("From: %v", err)
	}
	if err := inertia.App().Render(context.Background(), io.Discard); err == nil || inertia.Component(context.Background()) != "" {
		t.Error("App outside a page")
	}
	for _, body := range []string{"<html></html>", `<script data-page="app" type="application/json">{`, "{nope"} {
		if _, err := inertia.ReadPage([]byte(body)); err == nil {
			t.Errorf("ReadPage(%q)", body)
		}
	}
	if postsIndex.Component() != "Posts/Index" {
		t.Error("Component")
	}
	// Without an Inertia in the context, Middleware and Render fail.
	r := web.NewRouter()
	r.With(inertia.Middleware).Get("/m", func(c *web.Ctx) error { return nil })
	r.Get("/r", func(c *web.Ctx) error { return inertia.Render(c, "X", nil) })
	for _, p := range []string{"/m", "/r"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != 500 {
			t.Errorf("%s without an Inertia: %d", p, rec.Code)
		}
	}
}

// New: in the app's contexts and container; the asset version from the
// frontend's build.
func TestNew(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_NAME": "blog", "APP_ENV": "testing"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	manifest := `{"src/app.ts":{"file":"assets/app-1.js","isEntry":true}}`
	v, err := vite.New(app, fstest.MapFS{"build/manifest.json": {Data: []byte(manifest)}})
	if err != nil {
		t.Fatal(err)
	}
	in, err := inertia.New(app, root)
	if err != nil {
		t.Fatal(err)
	}
	if in.Version() != v.Version() || in.Version() == "" {
		t.Errorf("version %q, the build's %q", in.Version(), v.Version())
	}
	if got, err := anetos.Resolve[*inertia.Inertia](app); err != nil || got != in {
		t.Errorf("Resolve: %v", err)
	}
	if got, err := inertia.From(app.Context(context.Background())); err != nil || got != in {
		t.Errorf("From: %v", err)
	}
	if _, err := inertia.New(app, root); err == nil || !strings.Contains(err.Error(), "New called twice") {
		t.Errorf("twice: %v", err)
	}
	other, _ := anetos.New(anetos.WithSource(config.Map{"APP_NAME": "blog", "APP_ENV": "testing"}), anetos.WithLogOutput(io.Discard))
	t.Cleanup(func() { _ = other.Close() })
	if in, err := inertia.New(other, root, inertia.WithVersion("x")); err != nil || in.Version() != "x" {
		t.Errorf("WithVersion: %v", err)
	}
	if _, err := inertia.New(other, nil); err == nil {
		t.Error("nil root")
	}
}

// Once props' keys and expiry, a fresh value, a nil function, and the
// flash of a request without the middleware.
func TestOnceAndFlash(t *testing.T) {
	type Plans struct {
		Plans inertia.Prop[[]string] `json:"plans"`
		Keyed inertia.Prop[int]      `json:"keyed"`
		Nil   inertia.Prop[int]      `json:"nil"`
	}
	p := inertia.NewPage[Plans]("Plans")
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	in, _ := inertia.NewWithRoot(root)
	r := web.NewRouter()
	r.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(inertia.WithInertia(r.Context(), in)))
		})
	})
	r.Get("/", func(c *web.Ctx) error {
		inertia.Flash(c, "toast", "hi") // no session: nowhere to keep it
		return p.Render(c, Plans{
			Plans: inertia.Value([]string{"free"}).Once().Until(at).Fresh(),
			Keyed: inertia.Value(1).OnceAs("shared-count"),
			Nil:   inertia.Func[int](nil),
		})
	})
	get := func(headers ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Inertia", "true")
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(); rec.Code != 500 {
		t.Errorf("nil function: %d", rec.Code)
	}
	p2 := inertia.NewPage[Plans]("Plans")
	r.Get("/ok", func(c *web.Ctx) error {
		return p2.Render(c, Plans{Plans: inertia.Value([]string{"free"}).Once().Until(at).Fresh(), Keyed: inertia.Value(1).OnceAs("shared-count")})
	})
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("X-Inertia", "true")
	req.Header.Set("X-Inertia-Except-Once-Props", "plans,shared-count")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	pg, err := inertia.ReadPage(rec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if pg.Props["plans"] == nil || *pg.OnceProps["plans"].ExpiresAt != at.UnixMilli() || pg.Props["keyed"] != nil ||
		pg.OnceProps["shared-count"].Prop != "keyed" || pg.OnceProps["shared-count"].ExpiresAt != nil || pg.Flash != nil {
		t.Errorf("once: %s", rec.Body)
	}
}

// A router with the Inertia in every context, and its own sessions and
// middleware when withMiddleware.
func bareRouter(t *testing.T, withMiddleware bool) (*web.Router, *web.Router) {
	t.Helper()
	in, err := inertia.NewWithRoot(root, inertia.WithShared(func(*web.Ctx) (Shared, error) { return Shared{User: "ada"}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.NewEncrypter(k)
	no := false
	cfg := session.DefaultConfig()
	cfg.Secure = &no
	sessions, _ := session.NewManager(cfg, enc)
	r := web.NewRouter()
	r.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(inertia.WithInertia(r.Context(), in)))
		})
	})
	mws := []web.Middleware{sessions.Middleware}
	if withMiddleware {
		mws = append(mws, inertia.Middleware)
	}
	return r, r.Group("", mws...)
}

func serve(r http.Handler, method, target string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// Merge props: deep merges are listed once, nested paths in partial
// reloads, once keys with dots, an unset Prop keeps the shared prop.
func TestMergeAndPaths(t *testing.T) {
	type Feed struct {
		Items inertia.Prop[[]int]          `json:"items"`
		Chat  inertia.Prop[map[string]any] `json:"chat"`
		User  inertia.Prop[string]         `json:"user"` // unset: the shared one stays
		Perms inertia.Prop[[]string]       `json:"perms"`
	}
	feed := inertia.NewPage[Feed]("Feed")
	r, g := bareRouter(t, true)
	g.Get("/", func(c *web.Ctx) error {
		return feed.Render(c, Feed{
			Items: inertia.Merge([]int{1}).DeepMerge(),
			Chat:  inertia.Value(map[string]any{"a": 1, "b": 2}),
			Perms: inertia.Value([]string{"x"}).OnceAs("user.perms"),
		})
	})
	pg, err := inertia.ReadPage(serve(r, http.MethodGet, "/", "X-Inertia", "true").Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if pg.MergeProps != nil || !slices.Equal(pg.DeepMergeProps, []string{"items"}) || string(pg.Props["user"]) != `"ada"` || !slices.Equal(pg.SharedProps, []string{"user", "appName"}) {
		t.Errorf("deep merge / shared: %+v", pg)
	}
	// Leaving out part of a prop still sends the prop; asking for part of
	// it sends it whole.
	rec := serve(r, http.MethodGet, "/", "X-Inertia", "true", "X-Inertia-Partial-Component", "Feed", "X-Inertia-Partial-Except", "chat.a")
	if pg, _ = inertia.ReadPage(rec.Body.Bytes()); pg.Props["chat"] == nil {
		t.Errorf("except chat.a: %s", rec.Body)
	}
	rec = serve(r, http.MethodGet, "/", "X-Inertia", "true", "X-Inertia-Partial-Component", "Feed", "X-Inertia-Partial-Data", "chat.b")
	if pg, _ = inertia.ReadPage(rec.Body.Bytes()); pg.Props["chat"] == nil || pg.Props["items"] != nil {
		t.Errorf("only chat.b: %s", rec.Body)
	}
	// A once key with a dot is a key, not a path.
	rec = serve(r, http.MethodGet, "/", "X-Inertia", "true", "X-Inertia-Except-Once-Props", "user.perms")
	if pg, _ = inertia.ReadPage(rec.Body.Bytes()); pg.Props["perms"] != nil || pg.OnceProps["user.perms"].Prop != "perms" {
		t.Errorf("once key user.perms: %s", rec.Body)
	}
}

// Props as encoding/json writes them: omitempty and omitzero, the
// shallowest field of a name, a tag choosing between two.
func TestPropsLikeJSON(t *testing.T) {
	type Inner struct {
		Title string `json:"title"`
		Note  string
	}
	type Z struct{ A int }
	type P struct {
		Inner
		Title string    `json:"title"` // shallower: wins
		Tags  []string  `json:"tags,omitempty"`
		Empty []string  `json:"empty,omitempty"`
		S     Z         `json:"s,omitempty"` //nolint:modernize // omitempty keeps structs, as encoding/json does
		Zero  Z         `json:"zero,omitzero"`
		When  time.Time `json:"when,omitzero"`
	}
	p := inertia.NewPage[P]("P")
	r, g := bareRouter(t, true)
	g.Get("/", func(c *web.Ctx) error {
		return p.Render(c, P{Inner: Inner{Title: "inner", Note: "n"}, Title: "outer", Tags: []string{"a"}, Empty: []string{}})
	})
	pg, err := inertia.ReadPage(serve(r, http.MethodGet, "/", "X-Inertia", "true").Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(pg); !slices.Equal(got, []string{"Note", "appName", "errors", "s", "tags", "title", "user"}) || string(pg.Props["title"]) != `"outer"` {
		t.Errorf("props %v: %s %s", got, pg.Props["title"], pg.Props["s"])
	}
	type A struct{ Title string }
	type B struct{ Title string }
	type tied struct {
		A
		B
	}
	type twoTags struct {
		X string `json:"x"`
		Y int
	}
	_ = inertia.NewPage[tied]("tied") // both Titles left out, as encoding/json does
	_ = inertia.NewPage[twoTags]("ok")
	r.Get("/tied", func(c *web.Ctx) error { return inertia.NewPage[tied]("T").Render(c, tied{A{"a"}, B{"b"}}) })
	if pg, _ := inertia.ReadPage(serve(r, http.MethodGet, "/tied", "X-Inertia", "true").Body.Bytes()); pg.Props["Title"] != nil {
		t.Errorf("tied fields: %v", keys(pg))
	}
	// A field promoted through an embedded pointer to an unexported
	// struct is written, as encoding/json writes it; not when it's nil.
	type hidden struct {
		Hidden string `json:"hidden"`
	}
	type viaPtr struct {
		*hidden
		Name string `json:"name"`
	}
	vp := inertia.NewPage[viaPtr]("V")
	r.Get("/via", func(c *web.Ctx) error { return vp.Render(c, viaPtr{&hidden{"h"}, "n"}) })
	r.Get("/via-nil", func(c *web.Ctx) error { return vp.Render(c, viaPtr{nil, "n"}) })
	if pg, _ := inertia.ReadPage(serve(r, http.MethodGet, "/via", "X-Inertia", "true").Body.Bytes()); string(pg.Props["hidden"]) != `"h"` {
		t.Errorf("via an unexported pointer: %v", keys(pg))
	}
	if pg, _ := inertia.ReadPage(serve(r, http.MethodGet, "/via-nil", "X-Inertia", "true").Body.Bytes()); pg.Props["hidden"] != nil || pg.Props["name"] == nil {
		t.Errorf("via a nil pointer: %v", keys(pg))
	}
}

// withInertia is a router whose requests have in.
func withInertia(in *inertia.Inertia, routes func(*web.Router)) *web.Router {
	r := web.NewRouter()
	r.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(inertia.WithInertia(r.Context(), in)))
		})
	})
	routes(r)
	return r
}

// Without the middleware, Flash and the history flags do nothing, and
// pages still render; the writer's details; a props value json can't
// encode fails without X-Inertia.
func TestWithoutMiddlewareAndWriter(t *testing.T) {
	r, g := bareRouter(t, false)
	g.Get("/", func(c *web.Ctx) error {
		inertia.Flash(c, "toast", "lost")
		inertia.ClearHistory(c)
		inertia.PreserveFragment(c)
		inertia.EncryptHistory(c)
		return inertia.Render(c, "Home", nil)
	})
	g.Get("/nan", func(c *web.Ctx) error { return inertia.Render(c, "NaN", inertia.Props{"x": func() {}}) })
	rec := serve(r, http.MethodGet, "/", "X-Inertia", "true")
	pg, err := inertia.ReadPage(rec.Body.Bytes())
	if err != nil || pg.Flash != nil || pg.ClearHistory || pg.PreserveFragment || pg.EncryptHistory {
		t.Errorf("without the middleware: %v %s", err, rec.Body)
	}
	for _, h := range [][]string{{"X-Inertia", "true"}, nil} {
		if rec := serve(r, http.MethodGet, "/nan", h...); rec.Code != 500 || rec.Header().Get("X-Inertia") != "" {
			t.Errorf("unencodable props %v: %d %v", h, rec.Code, rec.Header())
		}
	}

	// The middleware's writer: a GET redirect to a fragment loses its body,
	// a 103 doesn't stop the rewriting, Flush and Unwrap reach the writer.
	r, g = bareRouter(t, true)
	g.Get("/frag", func(c *web.Ctx) error { return c.Redirect(http.StatusFound, "/a#b") })
	g.Get("/early", func(c *web.Ctx) error {
		c.Writer().WriteHeader(http.StatusEarlyHints)
		return c.Redirect(http.StatusFound, "/a#b")
	})
	g.Get("/stream", func(c *web.Ctx) error {
		rc := http.NewResponseController(c.Writer())
		if err := rc.Flush(); err != nil {
			return err
		}
		_, err := io.WriteString(c.Writer(), "streamed")
		return err
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/frag", "/early"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+p, nil)
		req.Header.Set("X-Inertia", "true")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusConflict || res.Header.Get("X-Inertia-Redirect") != "/a#b" || len(b) != 0 {
			t.Errorf("%s: %d %v %q", p, res.StatusCode, res.Header, b)
		}
	}
	if rec := serve(r, http.MethodGet, "/stream", "X-Inertia", "true"); rec.Code != 200 || !rec.Flushed || rec.Body.String() != "streamed" {
		t.Errorf("stream: %d %v %q", rec.Code, rec.Flushed, rec.Body)
	}
}

// A root view that fails fails the response.
func TestRootFails(t *testing.T) {
	in, _ := inertia.NewWithRoot(view.ComponentFunc(func(context.Context, io.Writer) error { return errors.New("broken root") }))
	r := web.NewRouter()
	r.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(inertia.WithInertia(r.Context(), in)))
		})
	})
	r.Get("/", func(c *web.Ctx) error { return inertia.Render(c, "X", nil) })
	if rec := serve(r, http.MethodGet, "/"); rec.Code != 500 {
		t.Errorf("broken root: %d", rec.Code)
	}
}

// WithPages: the list tools read; in development and tests, a page it
// doesn't list fails to render.
func TestWithPages(t *testing.T) {
	type A struct {
		X inertia.Prop[[]int] `json:"x"`
	}
	a, b := inertia.NewPage[A]("A"), inertia.NewPage[A]("B")
	if _, err := inertia.NewWithRoot(root, inertia.WithPages(a, a)); err == nil || !strings.Contains(err.Error(), `"A" is listed twice`) {
		t.Errorf("twice: %v", err)
	}
	if _, err := inertia.NewWithRoot(root, inertia.WithPages(nil)); err == nil {
		t.Error("a nil page")
	}
	in, err := inertia.NewWithRoot(root, inertia.WithPages(a), inertia.WithShared(func(*web.Ctx) (Shared, error) { return Shared{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Pages()) != 1 || in.Pages()[0].Component() != "A" || in.Pages()[0].PropsType() != reflect.TypeFor[A]() ||
		len(in.SharedTypes()) != 1 || in.SharedTypes()[0] != reflect.TypeFor[Shared]() {
		t.Errorf("pages %v, shared %v", in.Pages(), in.SharedTypes())
	}
	if vt, ok := inertia.PropType(reflect.TypeFor[inertia.Prop[[]int]]()); !ok || vt != reflect.TypeFor[[]int]() {
		t.Errorf("PropType: %v %v", vt, ok)
	}
	if _, ok := inertia.PropType(reflect.TypeFor[int]()); ok {
		t.Error("PropType(int)")
	}
	if vt, ok := inertia.PropType(reflect.TypeFor[*inertia.Prop[string]]()); !ok || vt != reflect.TypeFor[string]() {
		t.Errorf("PropType(*Prop): %v %v", vt, ok)
	}
	// An empty list checks nothing.
	if none, err := inertia.NewWithRoot(root, inertia.WithPages()); err != nil {
		t.Fatal(err)
	} else if rec := serve(withInertia(none, func(r *web.Router) {
		r.Get("/b", func(c *web.Ctx) error { return b.Render(c, A{}) })
	}), http.MethodGet, "/b", "X-Inertia", "true"); rec.Code != 200 {
		t.Errorf("WithPages(): %d", rec.Code)
	}
	r := web.NewRouter()
	r.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(inertia.WithInertia(r.Context(), in)))
		})
	})
	r.Get("/a", func(c *web.Ctx) error { return a.Render(c, A{}) })
	r.Get("/b", func(c *web.Ctx) error { return b.Render(c, A{}) })
	r.Get("/untyped", func(c *web.Ctx) error { return inertia.Render(c, "C", nil) })
	for p, want := range map[string]int{"/a": 200, "/b": 500, "/untyped": 200} {
		if rec := serve(r, http.MethodGet, p, "X-Inertia", "true"); rec.Code != want {
			t.Errorf("%s: %d, want %d", p, rec.Code, want)
		}
	}
	// In production, an app's unlisted page renders.
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_NAME": "x", "APP_ENV": "production", "APP_KEY": "base64:" + strings.Repeat("A", 43)}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	prod, err := inertia.New(app, root, inertia.WithPages(a))
	if err != nil {
		t.Fatal(err)
	}
	r2 := web.NewRouter()
	r2.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(inertia.WithInertia(r.Context(), prod)))
		})
	})
	r2.Get("/b", func(c *web.Ctx) error { return b.Render(c, A{}) })
	if rec := serve(r2, http.MethodGet, "/b", "X-Inertia", "true"); rec.Code != 200 {
		t.Errorf("production: %d", rec.Code)
	}
}

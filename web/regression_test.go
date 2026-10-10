// SPDX-License-Identifier: Apache-2.0

package web

// Regression tests for defects found in the F5 code review.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestNestedJSONFieldsAreJSONOnly(t *testing.T) {
	type meta struct {
		Color string `json:"color"`
	}
	type in struct {
		Meta  meta              `json:"meta"`
		Tags  map[string]string `json:"tags"`
		Items []meta            `json:"items"`
		Raw   json.RawMessage   `json:"raw"`
		Any   any               `json:"any"`
	}
	r := newTestRouter()
	r.Post("/x", H(func(c *Ctx, v in) (in, error) { return v, nil })) // must not panic
	got := do(t, r, "POST", "/x", strings.NewReader(`{"meta":{"color":"red"},"tags":{"a":"b"},"items":[{"color":"x"}],"raw":[1],"any":2}`),
		"Content-Type", "application/json")
	out := decode[in](t, got.body)
	if out.Meta.Color != "red" || out.Tags["a"] != "b" || len(out.Items) != 1 || string(out.Raw) != "[1]" {
		t.Errorf("nested JSON = %+v", out)
	}
}

type tenant struct {
	TenantID string `header:"X-Tenant"`
}

func TestEmbeddedPointerWithSourceTagsIsRejected(t *testing.T) {
	type in struct {
		*tenant
		Name string `json:"name"`
	}
	mustPanic(t, "embedded pointer with header tag", func() { H(func(*Ctx, in) (int, error) { return 0, nil }) })

	type byValue struct {
		tenant
		Name string `json:"name"`
	}
	r := newTestRouter()
	r.Post("/x", H(func(c *Ctx, v byValue) (string, error) { return v.TenantID, nil }))
	got := do(t, r, "POST", "/x", strings.NewReader(`{"TenantID":"victim"}`), "Content-Type", "application/json", "X-Tenant", "mine")
	if strings.TrimSpace(got.body) != `"mine"` {
		t.Errorf("tenant = %s", got.body)
	}
}

func TestPathWinsOverFormForDoubleTaggedField(t *testing.T) {
	type in struct {
		ID int `path:"id" form:"id"`
	}
	r := newTestRouter()
	r.Post("/p/{id}", H(func(c *Ctx, v in) (int, error) { return v.ID, nil }))
	got := do(t, r, "POST", "/p/7", strings.NewReader("id=999"), "Content-Type", "application/x-www-form-urlencoded")
	if strings.TrimSpace(got.body) != "7" {
		t.Errorf("ID = %s, want 7 from the path", got.body)
	}
}

func TestMultipartTempFilesAreRemoved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	type in struct {
		File *multipart.FileHeader `form:"file"`
	}
	r := newTestRouter()
	r.UseGlobal(RequestIDs) // WithContext copies of the request are what leaked
	r.Post("/up", H(func(c *Ctx, v in) (int64, error) { return v.File.Size, nil }))
	srv := httptest.NewServer(r)
	defer srv.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "big.bin")
	_, _ = fw.Write(bytes.Repeat([]byte("x"), MaxMultipartMemory+1<<20)) // forces a temp file
	_ = mw.Close()
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/up", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	srv.Close()
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("status %d; %d temp files left behind", resp.StatusCode, len(ents))
	}
}

func TestCORSRejectsWildcardWithCredentials(t *testing.T) {
	mustPanic(t, "CORS * + credentials", func() { CORS(CORSConfig{Origins: []string{"*"}, Credentials: true}) })
}

func TestInformationalResponsesDontStartTheResponse(t *testing.T) {
	r := newTestRouter()
	r.Get("/x", func(c *Ctx) error {
		c.Writer().WriteHeader(http.StatusEarlyHints)
		return Error(404, "gone")
	})
	srv := httptest.NewServer(r) // ResponseRecorder can't represent 1xx + final status
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/x", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 after 103 Early Hints", resp.StatusCode)
	}
}

func TestTrailingJSONIsRejected(t *testing.T) {
	type in struct {
		Title string `json:"title"`
	}
	r := newTestRouter()
	r.Post("/x", H(func(c *Ctx, v in) (string, error) { return v.Title, nil }))
	for _, body := range []string{`{"title":"a"}{"title":"b"}`, `{"title":"a"} garbage`} {
		got := do(t, r, "POST", "/x", strings.NewReader(body), "Content-Type", "application/json")
		if got.status != 400 {
			t.Errorf("%q: status %d", body, got.status)
		}
	}
	if got := do(t, r, "POST", "/x", strings.NewReader(`{"title":"a"}`+"\n  "), "Content-Type", "application/json"); got.status != 200 {
		t.Errorf("trailing whitespace rejected: %d", got.status)
	}
}

type slowCheck struct{}

func (slowCheck) Validate(ctx context.Context) error {
	return errors.Join(errors.New("dial tcp 10.0.0.5:5432"), context.DeadlineExceeded)
}

func TestValidateContextErrorsKeepTheirStatus(t *testing.T) {
	r := newTestRouter()
	r.Post("/x", H(func(*Ctx, slowCheck) (int, error) { return 0, nil }))
	got := do(t, r, "POST", "/x", strings.NewReader("{}"), "Content-Type", "application/json", "Accept", "application/json")
	if got.status != 503 || strings.Contains(got.body, "10.0.0.5") {
		t.Errorf("= %d %s", got.status, got.body)
	}
}

func TestUseAfterGroupRoutesPanics(t *testing.T) {
	r := newTestRouter()
	api := r.Group("/api")
	api.Get("/secret", text("x"))
	mustPanic(t, "Use after a group registered routes", func() { r.Use(func(h http.Handler) http.Handler { return h }) })

	r2 := newTestRouter()
	r2.With().Get("/x", text("x"))
	mustPanic(t, "Use after With registered routes", func() { r2.Use(func(h http.Handler) http.Handler { return h }) })
}

func TestFormFriendlyValues(t *testing.T) {
	type in struct {
		Remember bool `form:"remember"`
		Age      *int `form:"age"`
		Count    int  `query:"count"`
	}
	r := newTestRouter()
	r.Post("/x", H(func(c *Ctx, v in) (in, error) { return v, nil }))
	got := do(t, r, "POST", "/x?count=", strings.NewReader("remember=on&age="), "Content-Type", "application/x-www-form-urlencoded")
	out := decode[in](t, got.body)
	if got.status != 200 || !out.Remember || out.Age != nil || out.Count != 0 {
		t.Errorf("= %d %+v", got.status, out)
	}
}

func TestURLRejectsDotSegments(t *testing.T) {
	r := newTestRouter()
	r.Get("/files/{path...}", text("x")).Name("files")
	r.Get("/users/{id}/profile", text("x")).Name("user")
	for _, tc := range []struct {
		name string
		arg  any
	}{{"files", "../admin/delete"}, {"files", "a/./b"}, {"user", ".."}, {"user", ""}} {
		if u, err := r.URL(tc.name, tc.arg); err == nil {
			t.Errorf("URL(%s, %q) = %q, want error", tc.name, tc.arg, u)
		}
	}
}

func TestFileFieldsCantComeFromJSON(t *testing.T) {
	type in struct {
		File *multipart.FileHeader `form:"file" json:"file"`
	}
	r := newTestRouter()
	r.Post("/x", H(func(c *Ctx, v in) (bool, error) { return v.File == nil, nil }))
	got := do(t, r, "POST", "/x", strings.NewReader(`{"file":{"Filename":"../../etc/passwd","Size":5}}`), "Content-Type", "application/json")
	if strings.TrimSpace(got.body) != "true" {
		t.Errorf("file field set from JSON: %s", got.body)
	}
}

func TestAllowIncludesOptionsAndCustomMethods(t *testing.T) {
	r := newTestRouter()
	r.Options("/cors-only", text("x"))
	r.Handle("PURGE", "/cache", text("x"))
	r.Get("/cache", text("x"))

	if got := do(t, r, "GET", "/cors-only", nil); got.status != 405 || got.header.Get("Allow") != "OPTIONS" {
		t.Errorf("OPTIONS-only path: %d Allow=%q", got.status, got.header.Get("Allow"))
	}
	if got := do(t, r, "DELETE", "/cache", nil); got.status != 405 || got.header.Get("Allow") != "GET, HEAD, PURGE" {
		t.Errorf("custom method: %d Allow=%q", got.status, got.header.Get("Allow"))
	}
}

func TestFieldsHiddenOn5xxInProduction(t *testing.T) {
	r := newTestRouter()
	r.Get("/x", func(*Ctx) error {
		return &HTTPError{Status: 500, Message: "db", Fields: map[string]string{"dsn": "postgres://u:pw@h"}}
	})
	if got := do(t, r, "GET", "/x", nil, "Accept", "application/json"); strings.Contains(got.body, "pw@h") {
		t.Errorf("5xx fields leaked: %s", got.body)
	}
}

func TestWriterSupportsHijackAndReadFrom(t *testing.T) {
	var hijacked bool
	srv := httptest.NewServer(newRouterWith(func(r *Router) {
		r.Get("/ws", func(c *Ctx) error {
			hj, ok := c.Writer().(http.Hijacker)
			if !ok {
				return errors.New("not a Hijacker")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				return err
			}
			hijacked = true
			_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n\r\n"))
			return conn.Close()
		})
		r.Get("/file", func(c *Ctx) error {
			_, err := io.Copy(c.Writer(), strings.NewReader("streamed"))
			return err
		})
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("GET /ws HTTP/1.1\r\nHost: x\r\n\r\n"))
	buf := make([]byte, 12)
	_, _ = io.ReadFull(conn, buf)
	conn.Close()
	if !hijacked || string(buf) != "HTTP/1.1 101" {
		t.Errorf("hijack: %v %q", hijacked, buf)
	}

	rec := httptest.NewRecorder()
	rw := wrapWriter(rec)
	if _, err := io.Copy(rw, strings.NewReader("abc")); err != nil || rec.Body.String() != "abc" || rw.written != 3 {
		t.Errorf("ReadFrom: %v %q %d", err, rec.Body.String(), rw.written)
	}
}

func newRouterWith(fn func(*Router)) *Router {
	r := newTestRouter()
	fn(r)
	return r
}

func TestAcceptQualityValues(t *testing.T) {
	for accept, want := range map[string]bool{
		"application/json, text/html;q=0.1":         true,
		"text/html, application/json;q=0.5":         false,
		"text/html,application/xhtml+xml,*/*;q=0.8": false,
		"*/*":                      false,
		"application/problem+json": true,
		"text/html;q=0.9, application/vnd.api+json;q=1": true,
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept", accept)
		if got := wantsJSON(req); got != want {
			t.Errorf("Accept %q: WantsJSON = %v, want %v", accept, got, want)
		}
	}
}

func TestGroupSlashMeansGroupRoot(t *testing.T) {
	r := newTestRouter()
	r.Group("/posts").Get("/", text("index")).Name("posts.index")
	if got := do(t, r, "GET", "/posts", nil); got.status != 200 || got.body != "index" {
		t.Errorf("GET /posts = %d %q", got.status, got.body)
	}
	if u := r.MustURL("posts.index"); u != "/posts" {
		t.Errorf("URL = %q", u)
	}
}

func TestRealIPHardening(t *testing.T) {
	trusted, _ := ParsePrefixes([]string{"10.0.0.0/8", "::ffff:192.168.0.0/112"})
	var ip string
	h := RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ip = ClientIP(r.Context()) }))
	serve := func(remote string, headers ...string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Add(headers[i], headers[i+1])
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return ip
	}
	if got := serve("10.0.0.1:1", "X-Forwarded-For", "198.51.100.7:5555"); got != "198.51.100.7" {
		t.Errorf("hop with port = %q", got)
	}
	if got := serve("10.0.0.1:1", "X-Forwarded-For", "10.0.0.2", "X-Real-IP", "6.6.6.6"); got != "10.0.0.1" {
		t.Errorf("X-Real-IP trusted although XFF was present: %q", got)
	}
	if got := serve("192.168.0.9:1", "X-Forwarded-For", "198.51.100.1"); got != "198.51.100.1" {
		t.Errorf("IPv4-mapped CIDR not matched: %q", got)
	}
}

func TestCORSWildcardSubdomainIgnoresCase(t *testing.T) {
	h := CORS(CORSConfig{Origins: []string{"https://*.Example.com"}})(http.NotFoundHandler())
	got := do(t, h, "GET", "/", nil, "Origin", "https://App.EXAMPLE.com")
	if got.header.Get("Access-Control-Allow-Origin") == "" {
		t.Error("case-insensitive subdomain origin not allowed")
	}
}

func TestDebugPageRedactsSensitiveQuery(t *testing.T) {
	r := newTestRouter(WithDebug(true))
	r.Get("/x", func(*Ctx) error { return errors.New("boom") })
	got := do(t, r, "GET", "/x?"+url.Values{"api_key": {"sekrit"}, "page": {"2"}}.Encode(), nil, "Accept", "text/html")
	if strings.Contains(got.body, "sekrit") || !strings.Contains(got.body, "page=2") {
		t.Errorf("debug page query not redacted properly")
	}
}

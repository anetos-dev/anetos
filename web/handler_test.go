// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/validate"
)

type pagination struct {
	Page int `query:"page"`
}

type storePost struct {
	pagination
	ID        int64     `path:"id"`
	Token     string    `header:"X-Token"`
	Tags      []string  `query:"tag"`
	Title     string    `json:"title"`
	Body      string    `json:"body" form:"content"`
	Draft     bool      `json:"draft"`
	Published time.Time `json:"published_at"`
	Secret    string    `json:"-"`
}

func echo(c *Ctx, in storePost) (storePost, error) { return in, nil }

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return v
}

func TestBindJSONAndSources(t *testing.T) {
	r := newTestRouter()
	r.Post("/posts/{id}", H(echo))

	body := `{"title":"Hello","body":"World","draft":true,"published_at":"2026-09-30T10:00:00Z","ID":999,"Secret":"x"}`
	got := do(t, r, "POST", "/posts/7?page=2&tag=go&tag=web", strings.NewReader(body),
		"Content-Type", "application/json", "X-Token", "abc")
	if got.status != 200 {
		t.Fatalf("status %d: %s", got.status, got.body)
	}
	out := decode[storePost](t, got.body)
	if out.ID != 7 { // the body tried to set ID=999; path wins
		t.Errorf("ID = %d, want 7 from the path", out.ID)
	}
	if out.Page != 2 || strings.Join(out.Tags, ",") != "go,web" || out.Token != "abc" {
		t.Errorf("query/header: %+v", out)
	}
	if out.Title != "Hello" || out.Body != "World" || !out.Draft || out.Published.Year() != 2026 {
		t.Errorf("json: %+v", out)
	}
}

func TestBodyCannotSetQueryOrHeaderFields(t *testing.T) {
	r := newTestRouter()
	r.Post("/posts/{id}", H(echo))
	got := do(t, r, "POST", "/posts/1", strings.NewReader(`{"Page":5,"Token":"forged","Tags":["x"]}`),
		"Content-Type", "application/json")
	out := decode[storePost](t, got.body)
	if out.Page != 0 || out.Token != "" || out.Tags != nil {
		t.Errorf("body set non-body fields: %+v", out)
	}
}

func TestBindForm(t *testing.T) {
	r := newTestRouter()
	r.Post("/posts/{id}", H(echo))
	form := url.Values{"title": {"From form"}, "content": {"Body via form tag"}, "draft": {"true"}, "page": {"9"}}
	got := do(t, r, "POST", "/posts/3", strings.NewReader(form.Encode()),
		"Content-Type", "application/x-www-form-urlencoded")
	out := decode[storePost](t, got.body)
	if out.Title != "From form" || out.Body != "Body via form tag" || !out.Draft || out.ID != 3 {
		t.Errorf("form: %+v", out)
	}
	if out.Page != 0 {
		t.Error("form values must not fill query fields")
	}
}

type upload struct {
	Name   string                  `form:"name"`
	Avatar *multipart.FileHeader   `form:"avatar"`
	Docs   []*multipart.FileHeader `form:"docs"`
}

func TestBindMultipart(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "ada")
	fw, _ := mw.CreateFormFile("avatar", "me.png")
	_, _ = fw.Write([]byte("PNGDATA"))
	for _, n := range []string{"a.pdf", "b.pdf"} {
		fw, _ := mw.CreateFormFile("docs", n)
		_, _ = fw.Write([]byte(n))
	}
	_ = mw.Close()

	r := newTestRouter()
	r.Post("/upload", H(func(c *Ctx, in upload) (Responder, error) {
		f, err := in.Avatar.Open()
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		return Text(200, fmt.Sprintf("%s %s %s %d", in.Name, in.Avatar.Filename, data, len(in.Docs))), nil
	}))
	got := do(t, r, "POST", "/upload", &buf, "Content-Type", mw.FormDataContentType())
	if got.body != "ada me.png PNGDATA 2" {
		t.Errorf("multipart = %d %q", got.status, got.body)
	}
}

func TestBindErrors(t *testing.T) {
	r := newTestRouter()
	r.Post("/posts/{id}", H(echo))

	tests := []struct {
		name, target, ct, body string
		status                 int
		want                   string
	}{
		{"bad path int", "/posts/abc", "", "", 400, `"id": "invalid integer \"abc\""`},
		{"bad query int", "/posts/1?page=x", "", "", 400, `"page": "invalid integer \"x\""`},
		{"json type", "/posts/1", "application/json", `{"draft":"yes"}`, 400, `"draft": "must be a boolean"`},
		{"bad json", "/posts/1", "application/json", `{"title":`, 400, "not valid JSON"},
		{"unsupported type", "/posts/1", "text/csv", "a,b", 415, "unsupported content type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := []string{"Accept", "application/json"}
			if tt.ct != "" {
				hdr = append(hdr, "Content-Type", tt.ct)
			}
			got := do(t, r, "POST", tt.target, strings.NewReader(tt.body), hdr...)
			if got.status != tt.status || !strings.Contains(got.body, tt.want) {
				t.Errorf("= %d %s\nwant %d containing %s", got.status, got.body, tt.status, tt.want)
			}
		})
	}
}

func TestBodyLimitReturns413(t *testing.T) {
	r := newTestRouter()
	r.UseGlobal(BodyLimit(16))
	r.Post("/posts/{id}", H(echo))
	got := do(t, r, "POST", "/posts/1", strings.NewReader(`{"title":"this is far too long"}`),
		"Content-Type", "application/json", "Accept", "application/json")
	if got.status != 413 {
		t.Errorf("status = %d %s", got.status, got.body)
	}
}

type signup struct {
	Email string `json:"email"`
}

func (s signup) Validate(context.Context) error {
	switch s.Email {
	case "":
		return validate.Fail("email", "email is required")
	case "db@example.com":
		return errors.New("connection refused to 10.0.0.7")
	case "taken@example.com":
		return Error(http.StatusConflict, "email already registered")
	}
	return nil
}

func TestValidatorHook(t *testing.T) {
	r := newTestRouter()
	called := false
	r.Post("/signup", H(func(c *Ctx, in signup) (Responder, error) {
		called = true
		return NoContent(), nil
	}))
	post := func(body string) result {
		return do(t, r, "POST", "/signup", strings.NewReader(body), "Content-Type", "application/json")
	}
	if got := post(`{}`); got.status != 422 || !strings.Contains(got.body, "email is required") || called {
		t.Errorf("422: %d %s called=%v", got.status, got.body, called)
	}
	if got := post(`{"email":"db@example.com"}`); got.status != 500 || strings.Contains(got.body, "10.0.0.7") {
		t.Errorf("plain error from Validate: %d %s", got.status, got.body)
	}
	if got := post(`{"email":"taken@example.com"}`); got.status != 409 {
		t.Errorf("HTTPError from Validate: %d", got.status)
	}
	if got := post(`{"email":"ok@example.com"}`); got.status != 204 || !called {
		t.Errorf("valid: %d called=%v", got.status, called)
	}
}

func TestResponders(t *testing.T) {
	r := newTestRouter()
	r.Get("/target/{id}", text("t")).Name("target")
	type none struct{}
	r.Get("/created", H(func(*Ctx, none) (Responder, error) { return Created(map[string]int{"id": 1}), nil }))
	r.Get("/nil", H(func(*Ctx, none) (Responder, error) { return nil, nil }))
	r.Get("/nilptr", H(func(*Ctx, none) (*storePost, error) { return nil, nil }))
	r.Get("/struct", H(func(*Ctx, none) (map[string]string, error) { return map[string]string{"a": "b"}, nil }))
	r.Get("/redirect", H(func(*Ctx, none) (Responder, error) { return Redirect("/elsewhere"), nil }))
	r.Get("/route", H(func(*Ctx, none) (Responder, error) { return RedirectRoute("target", 5), nil }))
	r.Get("/text", H(func(*Ctx, none) (Responder, error) { return Text(418, "teapot"), nil }))
	r.Get("/json", H(func(*Ctx, none) (Responder, error) { return JSON(202, []int{1}), nil }))
	r.Get("/direct", H(func(c *Ctx, _ none) (map[string]string, error) {
		return map[string]string{"ignored": "yes"}, c.Text(200, "written directly")
	}))

	tests := []struct {
		target, want string
		status       int
	}{
		{"/created", `{"id":1}`, 201},
		{"/nil", "", 204},
		{"/nilptr", "null", 200},
		{"/struct", `{"a":"b"}`, 200},
		{"/text", "teapot", 418},
		{"/json", "[1]", 202},
		{"/direct", "written directly", 200},
	}
	for _, tt := range tests {
		got := do(t, r, "GET", tt.target, nil)
		if got.status != tt.status || strings.TrimSpace(got.body) != tt.want {
			t.Errorf("%s = %d %q, want %d %q", tt.target, got.status, got.body, tt.status, tt.want)
		}
	}
	if got := do(t, r, "GET", "/redirect", nil); got.status != 303 || got.header.Get("Location") != "/elsewhere" {
		t.Errorf("redirect: %d %q", got.status, got.header.Get("Location"))
	}
	if got := do(t, r, "GET", "/route", nil); got.status != 303 || got.header.Get("Location") != "/target/5" {
		t.Errorf("route redirect: %d %q", got.status, got.header.Get("Location"))
	}
}

func TestHPanicsOnBadInputTypes(t *testing.T) {
	mustPanic(t, "non-struct", func() { H(func(*Ctx, int) (int, error) { return 0, nil }) })
	type badField struct {
		M map[string]string `query:"m"`
	}
	mustPanic(t, "unsupported field", func() { H(func(*Ctx, badField) (int, error) { return 0, nil }) })
	type sliceParam struct {
		IDs []int `path:"ids"`
	}
	mustPanic(t, "slice path param", func() { H(func(*Ctx, sliceParam) (int, error) { return 0, nil }) })
	type emptyTag struct {
		X string `query:""`
	}
	mustPanic(t, "empty tag", func() { H(func(*Ctx, emptyTag) (int, error) { return 0, nil }) })
}

func TestCtxHelpers(t *testing.T) {
	r := newTestRouter()
	r.Get("/p/{id}", func(c *Ctx) error {
		if c.Param("id") != "9" || c.Query("q") != "x" || c.Header("X-A") != "b" {
			return errors.New("helpers wrong")
		}
		if c.App() != nil {
			return errors.New("App should be nil without WithApp")
		}
		var ctx context.Context = c // Ctx is a context.Context
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, ok := c.Deadline(); ok {
			return errors.New("unexpected deadline")
		}
		if c.Value("nope") != nil || c.Done() != c.Request().Context().Done() {
			return errors.New("context delegation")
		}
		c.SetHeader("X-Out", "1")
		return c.HTML(200, "<b>ok</b>")
	})
	got := do(t, r, "GET", "/p/9?q=x", nil, "X-A", "b")
	if got.status != 200 || got.body != "<b>ok</b>" || got.header.Get("X-Out") != "1" ||
		!strings.HasPrefix(got.header.Get("Content-Type"), "text/html") {
		t.Errorf("= %d %q %v", got.status, got.body, got.header)
	}
}

func TestWantsJSON(t *testing.T) {
	tests := []struct {
		headers []string
		want    bool
	}{
		{nil, false},
		{[]string{"Accept", "application/json"}, true},
		{[]string{"Accept", "application/problem+json"}, true},
		{[]string{"Accept", "text/html,application/json"}, false},
		{[]string{"X-Requested-With", "XMLHttpRequest"}, true},
		{[]string{"Content-Type", "application/json"}, true},
		{[]string{"Content-Type", "application/vnd.api+json"}, true},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest("GET", "/", nil)
		for i := 0; i+1 < len(tt.headers); i += 2 {
			req.Header.Set(tt.headers[i], tt.headers[i+1])
		}
		if got := wantsJSON(req); got != tt.want {
			t.Errorf("%v: WantsJSON = %v", tt.headers, got)
		}
	}
}

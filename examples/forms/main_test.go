// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
)

type browser struct {
	t      *testing.T
	base   string
	client *http.Client
	last   string // last page loaded, sent as Referer like a browser
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	app, err := anetos.New(anetos.WithSource(config.Map{
		"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "LOG_LEVEL": "error",
	}))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := setup(app)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: ts.URL, client: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type page struct {
	status   int
	body     string
	location string
}

func (b *browser) do(method, path string, body string, headers ...string) page {
	b.t.Helper()
	req, _ := http.NewRequest(method, b.base+path, strings.NewReader(body))
	req.Header.Set("Accept", "text/html")
	if b.last != "" {
		req.Header.Set("Referer", b.last)
	}
	if method == http.MethodGet {
		b.last = b.base + path
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return page{res.StatusCode, string(out), res.Header.Get("Location")}
}

var tokenRe = regexp.MustCompile(`name="_token" value="([^"]+)"`)

func (p page) token(t *testing.T) string {
	t.Helper()
	m := tokenRe.FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("no CSRF token in %s", p.body)
	}
	return m[1]
}

func form(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

func TestNotes(t *testing.T) {
	b := newBrowser(t)
	if p := b.do("GET", "/", ""); p.status != http.StatusSeeOther || p.location != "/notes" {
		t.Fatalf("home: %d %q", p.status, p.location)
	}
	p := b.do("GET", "/notes/new", "")
	if p.status != 200 || !strings.Contains(p.body, "<title>New note · Notes</title>") || !strings.Contains(p.body, `/assets/htmx.min.js?v=`) {
		t.Fatalf("new: %d %s", p.status, p.body)
	}
	tok := p.token(t)

	// Invalid: back to the form, with errors and the input kept.
	p = b.do("POST", "/notes", form("_token", tok, "title", "<Groceries>", "body", ""))
	if p.status != http.StatusSeeOther || p.location != "/notes/new" {
		t.Fatalf("invalid: %d %q %s", p.status, p.location, p.body)
	}
	p = b.do("GET", "/notes/new", "")
	for _, want := range []string{`value="&lt;Groceries&gt;"`, "The body field is required.", "Please fix the errors below."} {
		if !strings.Contains(p.body, want) {
			t.Errorf("missing %q in %s", want, p.body)
		}
	}

	// Valid: redirect to the list with a flash message.
	p = b.do("POST", "/notes", form("_token", p.token(t), "title", "Groceries", "body", "Milk"))
	if p.status != http.StatusSeeOther || p.location != "/notes" {
		t.Fatalf("create: %d %q %s", p.status, p.location, p.body)
	}
	p = b.do("GET", "/notes", "")
	if !strings.Contains(p.body, "Note created.") || !strings.Contains(p.body, "<strong>Groceries</strong>") {
		t.Fatalf("list: %s", p.body)
	}
	if p = b.do("GET", "/notes", ""); strings.Contains(p.body, "Note created.") {
		t.Error("flash shown twice")
	}

	// Edit with an HTML form (PUT through _method).
	p = b.do("GET", "/notes/1/edit", "")
	if !strings.Contains(p.body, `value="Groceries"`) || !strings.Contains(p.body, `name="_method" value="PUT"`) {
		t.Fatalf("edit: %s", p.body)
	}
	p = b.do("POST", "/notes/1", form("_token", p.token(t), "_method", "PUT", "title", "Shopping", "body", "Milk"))
	if p.status != http.StatusSeeOther {
		t.Fatalf("update: %d %s", p.status, p.body)
	}
	if p = b.do("GET", "/notes", ""); !strings.Contains(p.body, "Shopping") || !strings.Contains(p.body, "Note saved.") {
		t.Errorf("after update: %s", p.body)
	}

	// Delete with htmx: DELETE with the token in a header, empty response.
	p = b.do("DELETE", "/notes/1", "", "HX-Request", "true", "X-CSRF-Token", p.token(t))
	if p.status != 200 || p.body != "" {
		t.Errorf("htmx delete: %d %q", p.status, p.body)
	}
	if p = b.do("GET", "/notes", ""); strings.Contains(p.body, "Shopping") {
		t.Error("note not deleted")
	}

	// Without the token, nothing changes.
	if p = b.do("POST", "/notes", form("title", "x", "body", "y")); p.status != http.StatusForbidden {
		t.Errorf("no token: %d", p.status)
	}
	if p = b.do("GET", "/notes/99/edit", ""); p.status != http.StatusNotFound {
		t.Errorf("missing note: %d", p.status)
	}
}

func TestAssets(t *testing.T) {
	b := newBrowser(t)
	for _, name := range []string{"app.css", "htmx.min.js"} {
		p := b.do("GET", assets.URL(name), "")
		if p.status != 200 || p.body == "" {
			t.Errorf("%s: %d", name, p.status)
		}
	}
}

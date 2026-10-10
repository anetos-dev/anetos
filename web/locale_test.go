// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
)

var catalogs = fstest.MapFS{
	"en.yaml": {Data: []byte("hello: \"Hello\"\nvalidation:\n  attributes:\n    name: \"name\"")},
	"bn.yaml": {Data: []byte("hello: \"হ্যালো\"\nhttp:\n  status:\n    404: \"পাওয়া যায়নি\"\nvalidation:\n  required: \"{label} দিতে হবে।\"\n  attributes:\n    name: \"নাম\"")},
}

type localeClient struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
}

type reply struct {
	status int
	body   string
	header http.Header
}

func (c *localeClient) get(target string, headers ...string) reply {
	c.t.Helper()
	if !strings.HasPrefix(target, "http") {
		target = c.srv.URL + target
	}
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Accept", "text/html")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return reply{res.StatusCode, string(b), res.Header}
}

// localeServer serves /hello (a translated page and its route URL), a
// session page, /switch/{locale}, and a validated JSON endpoint, with
// the given settings.
func localeServer(t *testing.T, env config.Map) *localeClient {
	t.Helper()
	env["APP_KEY"] = encryption.GenerateKey()
	env["SESSION_SECURE"] = "false"
	app := newApp(t, env)
	if _, err := i18n.New(app, catalogs); err != nil {
		t.Fatal(err)
	}
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.New(app)
	if err != nil {
		t.Fatal(err)
	}
	r := srv.Router()
	pages := r.Group("", sessions.Middleware)
	pages.Get("/hello", func(c *web.Ctx) error {
		u, _ := c.URL("hello")
		return c.Text(http.StatusOK, i18n.Locale(c)+" "+i18n.T(c, "hello")+" "+u+" "+web.LocaleURL(c, "bn")+" "+web.LocaleURL(c, "en"))
	}).Name("hello")
	pages.Get("/alternates", func(c *web.Ctx) error {
		var b strings.Builder
		for _, a := range web.Alternates(c) {
			b.WriteString(a.Locale + "=" + a.URL + " ")
		}
		return c.Text(http.StatusOK, b.String())
	})
	pages.Get("/forget", func(c *web.Ctx) error {
		c.ForgetLocale()
		return c.NoContent()
	})
	pages.Get("/switch/{locale}", func(c *web.Ctx) error {
		// A database driver watches the context's Done from a goroutine
		// while the handler runs: SetLocale mustn't race with it.
		stop := make(chan struct{})
		watched := make(chan struct{})
		go func() {
			defer close(watched)
			for {
				select {
				case <-stop:
					return
				case <-c.Done():
					return
				default:
					_ = c.Value(struct{}{})
				}
			}
		}()
		err := c.SetLocale(c.Param("locale"))
		close(stop)
		<-watched
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, i18n.Locale(c)+" "+i18n.T(c, "hello"))
	})
	type input struct {
		Name string `json:"name" validate:"required"`
	}
	r.Get("/limited", func(*web.Ctx) error {
		return &web.HTTPError{Status: http.StatusTooManyRequests, Message: "limit", Key: "ai.usage_limit", Args: []any{"wait", "1m"}}
	})
	r.Post("/names", web.H(func(_ *web.Ctx, in input) (input, error) { return in, nil }))
	if err := app.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	hs := httptest.NewUnstartedServer(r)
	hs.Config.BaseContext = func(net.Listener) context.Context { return app.Context(context.Background()) }
	hs.Start()
	t.Cleanup(hs.Close)
	jar, _ := cookiejar.New(nil)
	return &localeClient{t: t, srv: hs, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func TestLocaleNone(t *testing.T) {
	c := localeServer(t, config.Map{})
	if got := c.get("/hello").body; got != "en Hello /hello /hello?locale=bn /hello?locale=en" {
		t.Errorf("default: %q", got)
	}
	if got := c.get("/hello", "Accept-Language", "bn-BD, en;q=0.5").body; !strings.HasPrefix(got, "bn হ্যালো /hello") {
		t.Errorf("Accept-Language: %q", got)
	}
	// ?locale= remembers the choice in a cookie and redirects.
	r := c.get("/hello?locale=bn&x=1")
	if r.status != http.StatusFound || r.header.Get("Location") != "/hello?x=1" || !strings.Contains(r.header.Get("Set-Cookie"), "locale=bn") {
		t.Fatalf("switch: %d %v", r.status, r.header)
	}
	if got := c.get("/hello").body; !strings.HasPrefix(got, "bn ") {
		t.Errorf("after the switch: %q", got)
	}
	// SetLocale: cookie and session.
	if got := c.get("/switch/en").body; got != "en Hello" {
		t.Errorf("SetLocale: %q", got)
	}
	if got := c.get("/hello", "Accept-Language", "bn").body; !strings.HasPrefix(got, "en ") {
		t.Errorf("the chosen locale beats the browser's: %q", got)
	}
	if r := c.get("/switch/fr"); r.status != http.StatusUnprocessableEntity {
		t.Errorf("unsupported locale: %d", r.status)
	}
	// ForgetLocale: the browser's again.
	c.get("/forget")
	if got := c.get("/hello", "Accept-Language", "bn").body; !strings.HasPrefix(got, "bn ") {
		t.Errorf("after ForgetLocale: %q", got)
	}

	// Validation messages and error pages in the visitor's language.
	req, _ := http.NewRequest(http.MethodPost, c.srv.URL+"/names", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "bn")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `"name": "নাম দিতে হবে।"`) {
		t.Errorf("validation in bn: %s", body)
	}
	fresh := &localeClient{t: t, srv: c.srv, client: http.DefaultClient} // no cookie
	if r := fresh.get("/nowhere", "Accept-Language", "bn"); r.status != http.StatusNotFound || !strings.Contains(r.body, "পাওয়া যায়নি") || !strings.Contains(r.body, `lang="bn"`) {
		t.Errorf("404 page in bn: %d %s", r.status, r.body)
	}
}

func TestLocalePrefix(t *testing.T) {
	c := localeServer(t, config.Map{"APP_LOCALE_STRATEGY": "prefix"})
	if got := c.get("/bn/hello").body; got != "bn হ্যালো /bn/hello /bn/hello /en/hello" {
		t.Errorf("/bn/hello: %q", got)
	}
	// The visit remembered bn: an unprefixed page redirects there.
	if r := c.get("/hello?x=1"); r.status != http.StatusFound || r.header.Get("Location") != "/bn/hello?x=1" {
		t.Errorf("redirect to the remembered locale: %d %v", r.status, r.header.Get("Location"))
	}
	// /en/… remembers en and goes to the canonical URL.
	if r := c.get("/en/hello"); r.status != http.StatusFound || r.header.Get("Location") != "/hello" {
		t.Errorf("/en/hello: %d %v", r.status, r.header.Get("Location"))
	}
	if r := c.get("/hello", "Accept-Language", "bn"); r.status != http.StatusOK || r.body != "en Hello /hello /bn/hello /en/hello" {
		t.Errorf("after choosing en: %d %q", r.status, r.body)
	}
	// A new visitor whose browser prefers bn is sent to /bn/.
	fresh := &localeClient{t: t, srv: c.srv, client: &http.Client{CheckRedirect: c.client.CheckRedirect}}
	if r := fresh.get("/hello", "Accept-Language", "bn"); r.status != http.StatusFound || r.header.Get("Location") != "/bn/hello" || !strings.Contains(r.header.Get("Vary"), "Accept-Language") {
		t.Errorf("Accept-Language redirect: %d %v", r.status, r.header)
	}
	// Not for API requests.
	if r := fresh.get("/hello", "Accept-Language", "bn", "Accept", "application/json"); r.status != http.StatusOK {
		t.Errorf("JSON request redirected: %d", r.status)
	}
	if r := fresh.get("/fr/hello"); r.status != http.StatusNotFound {
		t.Errorf("unsupported prefix: %d", r.status)
	}
}

func TestLocaleSubdomain(t *testing.T) {
	c := localeServer(t, config.Map{"APP_LOCALE_STRATEGY": "subdomain", "APP_URL": "http://example.test"})
	get := func(host, path string) reply {
		c.t.Helper()
		req, _ := http.NewRequest(http.MethodGet, c.srv.URL+path, nil)
		req.Host = host
		req.Header.Set("Accept", "text/html")
		res, err := c.client.Do(req)
		if err != nil {
			c.t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return reply{res.StatusCode, string(b), res.Header}
	}
	if got := get("bn.example.test", "/hello").body; got != "bn হ্যালো /hello http://bn.example.test/hello http://en.example.test/hello" {
		t.Errorf("bn.example.test: %q", got)
	}
	if r := get("en.example.test", "/hello"); r.status != http.StatusFound || r.header.Get("Location") != "http://example.test/hello" || !strings.Contains(r.header.Get("Set-Cookie"), "Domain=example.test") {
		t.Errorf("en.example.test: %d %v", r.status, r.header)
	}
	if got := get("example.test", "/hello").body; !strings.HasPrefix(got, "en ") {
		t.Errorf("example.test after choosing en: %q", got)
	}
	if got := get("bn.example.test", "/alternates").body; got != "en=http://example.test/alternates bn=http://bn.example.test/alternates x-default=http://example.test/alternates " {
		t.Errorf("Alternates: %q", got)
	}
	// A host below another domain is not a locale's.
	if got := get("bn.other.test", "/hello").body; !strings.HasPrefix(got, "en ") {
		t.Errorf("bn.other.test: %q", got)
	}

	app := newApp(t, config.Map{"APP_LOCALE_STRATEGY": "subdomain"})
	if _, err := i18n.New(app, catalogs); err == nil || !strings.Contains(err.Error(), "APP_URL") {
		t.Errorf("subdomain without APP_URL: %v", err)
	}
}

type prefUser struct{}

func (prefUser) PreferredLocale() string   { return "bn" }
func (prefUser) PreferredTimeZone() string { return "Asia/Dhaka" }

func TestLocaleUserPreference(t *testing.T) {
	app := newApp(t, config.Map{})
	if _, err := i18n.New(app, catalogs); err != nil {
		t.Fatal(err)
	}
	loggedIn := false
	i18n.SetCurrentUser(app, func(context.Context) (any, bool) { return prefUser{}, loggedIn })
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().Get("/me", func(c *web.Ctx) error {
		return c.Text(http.StatusOK, i18n.Locale(c)+" "+i18n.TimeZone(c).String())
	})
	if err := app.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	get := func(cookie bool) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(app.Context(context.Background()), http.MethodGet, "/me", nil)
		req.Header.Set("Accept-Language", "en")
		if cookie {
			req.AddCookie(&http.Cookie{Name: web.LocaleCookie, Value: "en"})
		}
		srv.Router().ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if got := get(false); got != "en UTC" {
		t.Errorf("logged out: %q", got)
	}
	loggedIn = true
	if got := get(false); got != "bn Asia/Dhaka" {
		t.Errorf("the user's preference beats the browser's: %q", got)
	}
	if got := get(true); got != "en Asia/Dhaka" {
		t.Errorf("the visitor's choice (the cookie) beats the user's preference: %q", got)
	}
}

// TestLocaleRedirectsStayOnSite checks that paths a browser would read as
// another host never become one in a redirect.
func TestLocaleRedirectsStayOnSite(t *testing.T) {
	for _, mode := range []string{"none", "prefix"} {
		c := localeServer(t, config.Map{"APP_LOCALE_STRATEGY": mode})
		for _, p := range []string{"//evil.example/x", "/%2Fevil.example/x", "/\\evil.example/x", "/en//evil.example/x", "/en/%5Cevil.example"} {
			target := p
			if mode == "none" {
				target += "?locale=bn"
			}
			req, _ := http.NewRequest(http.MethodGet, c.srv.URL, nil)
			req.URL.Opaque = target // sent as it is
			req.Header.Set("Accept", "text/html")
			req.Header.Set("Accept-Language", "bn")
			res, err := c.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			loc := res.Header.Get("Location")
			if strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") || strings.Contains(loc, "://") {
				t.Errorf("%s %s: redirected to %q", mode, p, loc)
			}
		}
	}
}

func TestLocalePrefixDetails(t *testing.T) {
	c := localeServer(t, config.Map{"APP_LOCALE_STRATEGY": "prefix"})
	// Escaped paths keep their escapes.
	if r := c.get("/bn/hello%20there"); r.status != http.StatusNotFound {
		t.Errorf("/bn/hello%%20there: %d", r.status)
	}
	if got := c.get("/bn/hello?q=a%26b").body; !strings.HasSuffix(got, "/bn/hello?q=a%26b /en/hello?q=a%26b") {
		t.Errorf("query kept as it was: %q", got)
	}
	if r := c.get("/BN/hello"); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/bn/hello" {
		t.Errorf("/BN/hello: %d %v", r.status, r.header.Get("Location"))
	}
	if r := c.get("/limited", "Accept", "application/json"); !strings.Contains(r.body, "Try again in 1m.") {
		t.Errorf("HTTPError Key and Args: %s", r.body)
	}
	if got := c.get("/bn/alternates?q=1").body; got != "en=/alternates?q=1 bn=/bn/alternates?q=1 x-default=/alternates?q=1 " {
		t.Errorf("Alternates: %q", got)
	}
	// /en/… is not redirected for a POST: its body would be lost.
	req, _ := http.NewRequest(http.MethodPost, c.srv.URL+"/en/names", strings.NewReader(`{"name":"Ada"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("POST /en/names: %d", res.StatusCode)
	}
}

func TestLocaleQueryParameter(t *testing.T) {
	c := localeServer(t, config.Map{})
	// Not for API requests, nor for locales the app doesn't support.
	if r := c.get("/hello?locale=bn", "Accept", "application/json"); r.status != http.StatusOK {
		t.Errorf("API request: %d", r.status)
	}
	if r := c.get("/hello?locale=fr"); r.status != http.StatusOK || !strings.HasPrefix(r.body, "en ") {
		t.Errorf("unsupported: %d %q", r.status, r.body)
	}
	if r := c.get("/hello"); !strings.Contains(r.header.Get("Vary"), "Accept-Language") {
		t.Errorf("Vary: %v", r.header.Get("Vary"))
	}
	if got := c.get("/alternates").body; got != "" {
		t.Errorf("Alternates with APP_LOCALE_STRATEGY=none: %q", got)
	}
}

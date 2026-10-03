// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/session"
)

// LocaleCookie is the cookie that remembers a visitor's choice of locale.
const LocaleCookie = "locale"

// localeSessionKey is the session key of the visitor's locale.
const localeSessionKey = "locale"

// localeSetup is what the locale middleware needs from the app's
// translator, computed once.
type localeSetup struct {
	tr     *i18n.Translator
	mode   string
	lower  []string // the supported locales, lower case, as in URLs
	tags   []string // the same, canonical
	secure bool     // APP_URL is https
	base   *url.URL // APP_URL, if set (subdomain URLs, Alternates)
	domain string   // the locale cookie's domain (subdomain URLs)
}

// localeState is what the locale middleware learned about a request.
type localeState struct {
	*localeSetup
	path   string // the request's escaped path, without its locale prefix
	query  string // the request's raw query
	url    string // the locale in the URL (prefix or subdomain), if any
	secure bool
}

type localeStateKey struct{}

func localeFrom(ctx context.Context) *localeState {
	s, _ := ctx.Value(localeStateKey{}).(*localeState)
	return s
}

// localize resolves each request's locale, when the app has a translator
// (i18n.ForApp). With LOCALE_URL=prefix or subdomain, it takes the locale
// out of the URL, which decides it, and sends visitors asking for a page
// without one to the locale they chose before (the cookie) or their
// browser prefers. With none, ?locale= on a page switches, and the
// request gets a lazy resolver: the locale cookie, the session, the
// signed-in user's preference, Accept-Language, then APP_LOCALE.
func localize(app *anetos.App) Middleware {
	var cached atomic.Pointer[localeSetup]
	var none atomic.Bool
	setup := func() *localeSetup {
		if s := cached.Load(); s != nil {
			return s
		}
		if none.Load() {
			return nil
		}
		tr, err := anetos.Resolve[*i18n.Translator](app)
		if err != nil {
			if app.Booted() {
				none.Store(true) // set up after the app booted: never
			}
			return nil
		}
		s := &localeSetup{tr: tr, mode: tr.Config().URL, tags: tr.Supported(), secure: strings.HasPrefix(app.Config().URL, "https://")}
		for _, l := range s.tags {
			s.lower = append(s.lower, strings.ToLower(l))
		}
		if u, err := url.Parse(app.Config().URL); err == nil && u.Host != "" {
			s.base = u
			if s.mode == i18n.URLSubdomain {
				s.domain = strings.ToLower(u.Hostname())
			}
		}
		cached.Store(s)
		return s
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ls := setup()
			if ls == nil {
				next.ServeHTTP(w, r)
				return
			}
			tr := ls.tr
			st := &localeState{localeSetup: ls, path: safePath(r.URL.EscapedPath()), query: r.URL.RawQuery, secure: ls.secure || r.TLS != nil}
			switch ls.mode {
			case i18n.URLPrefix:
				if loc, rest, ok := ls.splitPrefix(st.path); ok {
					if loc == tr.Default() && readOnly(r) { // /en/about: remember, then the canonical URL
						setLocaleCookie(w, st, loc)
						http.Redirect(w, r, withQuery(rest, r.URL.RawQuery), http.StatusFound)
						return
					}
					if lower := "/" + strings.ToLower(loc); !strings.HasPrefix(st.path, lower) && readOnly(r) { // /BN/about
						http.Redirect(w, r, withQuery(lower+rest, r.URL.RawQuery), http.StatusMovedPermanently)
						return
					}
					st.url, st.path = loc, rest
					u := *r.URL
					u.Path, u.RawPath = rest, rest
					if p, err := url.PathUnescape(rest); err == nil {
						u.Path = p
					}
					if u.Path == u.RawPath {
						u.RawPath = ""
					}
					r = r.WithContext(r.Context())
					r.URL = &u
				}
			case i18n.URLSubdomain:
				if loc, ok := ls.subdomain(r.Host); ok {
					if loc == tr.Default() && readOnly(r) { // en.example.com: remember, then the canonical host
						setLocaleCookie(w, st, loc)
						http.Redirect(w, r, st.canonicalURL(loc, withQuery(st.path, r.URL.RawQuery)), http.StatusFound)
						return
					}
					st.url = loc
				}
			default:
				w.Header().Add("Vary", "Accept-Language, Cookie") // one URL, every language
				if strings.Contains(r.URL.RawQuery, "locale=") && pageRequest(r) {
					if loc, ok := tr.Match(r.URL.Query().Get("locale")); ok {
						setLocaleCookie(w, st, loc)
						http.Redirect(w, r, withQuery(st.path, dropParam(r.URL.RawQuery, "locale")), http.StatusFound)
						return
					}
				}
			}
			if ls.mode != i18n.URLNone {
				if st.url != "" {
					if c, err := r.Cookie(LocaleCookie); err != nil || c.Value != st.url {
						setLocaleCookie(w, st, st.url) // remember the visitor's choice
					}
				} else if pageRequest(r) {
					w.Header().Add("Vary", "Accept-Language, Cookie")
					if loc := preferred(tr, r); loc != "" && loc != tr.Default() {
						http.Redirect(w, r, LocaleURL(context.WithValue(r.Context(), localeStateKey{}, st), loc), http.StatusFound)
						return
					}
				}
			}
			ctx := i18n.WithTranslator(context.WithValue(r.Context(), localeStateKey{}, st), tr)
			ctx = i18n.WithResolver(ctx, func(ctx context.Context) (string, *time.Location) {
				user, zone := i18n.Preferences(ctx)
				switch {
				case ls.mode == i18n.URLNone:
				case st.url != "":
					return st.url, zone
				default:
					return tr.Default(), zone
				}
				if c, err := r.Cookie(LocaleCookie); err == nil {
					if loc, ok := tr.Match(c.Value); ok {
						return loc, zone
					}
				}
				if s := session.From(ctx); s != nil {
					var v string
					if s.Get(localeSessionKey, &v) {
						if loc, ok := tr.Match(v); ok {
							return loc, zone
						}
					}
				}
				if user != "" {
					return user, zone
				}
				if loc, ok := tr.MatchAcceptLanguage(r.Header.Get("Accept-Language")); ok {
					return loc, zone
				}
				return tr.Default(), zone
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// readOnly reports whether r can be redirected without losing its body.
func readOnly(r *http.Request) bool { return r.Method == http.MethodGet || r.Method == http.MethodHead }

// pageRequest reports whether r is a browser asking for a page.
func pageRequest(r *http.Request) bool {
	return readOnly(r) && strings.Contains(r.Header.Get("Accept"), "text/html") && r.Header.Get("HX-Request") == ""
}

// safePath returns an escaped request path with one leading slash, so it
// can't be read as another host in a Location header ("//evil.com" and
// "/\evil.com" become "/evil.com").
func safePath(p string) string { return "/" + strings.TrimLeft(p, `/\`) }

// dropParam removes name's values from a raw query, keeping the other
// parameters as they were.
func dropParam(rawQuery, name string) string {
	var out []string
	for p := range strings.SplitSeq(rawQuery, "&") {
		if k, _, _ := strings.Cut(p, "="); k != name && p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "&")
}

// preferred returns the locale a visitor chose (the cookie) or their
// browser prefers; "" if neither is supported.
func preferred(tr *i18n.Translator, r *http.Request) string {
	if c, err := r.Cookie(LocaleCookie); err == nil {
		if loc, ok := tr.Match(c.Value); ok {
			return loc
		}
	}
	loc, _ := tr.MatchAcceptLanguage(r.Header.Get("Accept-Language"))
	return loc
}

// splitPrefix splits an escaped path's leading locale segment
// ("/bn/about" → bn, "/about") when it names a supported locale.
func (ls *localeSetup) splitPrefix(path string) (string, string, bool) {
	seg, rest, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if seg == "" {
		return "", path, false
	}
	seg = strings.ToLower(seg)
	for i, l := range ls.lower {
		if seg == l {
			return ls.tags[i], safePath(rest), true
		}
	}
	return "", path, false
}

// subdomain returns the locale named by host's first label, below the
// app's host.
func (ls *localeSetup) subdomain(host string) (string, bool) {
	host = strings.ToLower(stripPort(host))
	label, parent, ok := strings.Cut(host, ".")
	if !ok || (ls.domain != "" && parent != ls.domain) {
		return "", false
	}
	for i, l := range ls.lower {
		if label == l {
			return ls.tags[i], true
		}
	}
	return "", false
}

func stripPort(host string) string {
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
		return host[:i]
	}
	return host
}

func withQuery(path, query string) string {
	if path == "" {
		path = "/"
	}
	if query == "" {
		return path
	}
	return path + "?" + query
}

func setLocaleCookie(w http.ResponseWriter, st *localeState, loc string) {
	http.SetCookie(w, &http.Cookie{
		Name: LocaleCookie, Value: loc, Path: "/", Domain: st.domain,
		MaxAge: 365 * 24 * 3600, HttpOnly: true, Secure: st.secure, SameSite: http.SameSiteLaxMode,
	})
}

// ErrUnsupportedLocale is returned by [Ctx.SetLocale] for a locale the app
// doesn't support.
var ErrUnsupportedLocale = &HTTPError{Status: http.StatusUnprocessableEntity, Message: "unsupported locale", Key: "http.unsupported_locale"}

// SetLocale makes locale the visitor's: it is stored in the locale cookie
// and the session (if the route has one), which come before a signed-in
// user's preference, and used for the rest of the request. A settings
// page that saves a user's preferred locale calls it too, so the change
// shows at once. With LOCALE_URL=prefix or subdomain, send the visitor
// to the page in that locale afterwards ([LocaleURL]). It returns
// [ErrUnsupportedLocale] for a locale the app doesn't support, and an
// error without a translator (i18n.ForApp).
func (c *Ctx) SetLocale(locale string) error {
	st := localeFrom(c.r.Context())
	if st == nil {
		return errors.New("web: SetLocale needs the app's translator (i18n.ForApp, before the server serves)")
	}
	loc, ok := st.tr.Match(locale)
	if !ok {
		return ErrUnsupportedLocale
	}
	setLocaleCookie(c.w, st, loc)
	if s := session.From(c.r.Context()); s != nil {
		s.Put(localeSessionKey, loc)
	}
	c.r = c.r.WithContext(i18n.WithLocale(c.r.Context(), loc))
	return nil
}

// LocaleURL returns the URL of the current page (its path and query) in
// locale, for a language switcher; following it remembers the choice:
// /bn/about with LOCALE_URL=prefix (/en/about for the default locale,
// which redirects to /about), https://bn.example.com/about with
// subdomain (en.example.com for the default, which redirects to
// example.com), and /about?locale=bn with none (which redirects to
// /about). It returns "" for a context without a translator
// (i18n.ForApp) or outside a request.
func LocaleURL(ctx context.Context, locale string) string {
	st := localeFrom(ctx)
	if st == nil {
		return ""
	}
	loc, ok := st.tr.Match(locale)
	if !ok {
		loc = st.tr.Default()
	}
	switch st.mode {
	case i18n.URLPrefix:
		return withQuery("/"+strings.ToLower(loc)+st.path, st.query)
	case i18n.URLSubdomain:
		if st.base == nil || st.base.Host == "" {
			return withQuery(st.path, st.query)
		}
		return st.base.Scheme + "://" + strings.ToLower(loc) + "." + st.base.Host + withQuery(st.path, st.query)
	}
	q := dropParam(st.query, "locale")
	if q != "" {
		q += "&"
	}
	return withQuery(st.path, q+"locale="+url.QueryEscape(loc))
}

// Alternate is the address of the current page in one locale, for a
// <link rel="alternate" hreflang> element.
type Alternate struct {
	Locale string // a supported locale ("bn"), or "x-default"
	URL    string // absolute when APP_URL is set
}

// Alternates returns the current page's address in each supported
// locale, then "x-default" (the default locale's), for search engines,
// with LOCALE_URL=prefix or subdomain:
//
//	for _, a := range web.Alternates(ctx) {
//		<link rel="alternate" hreflang={ a.Locale } href={ a.URL }/>
//	}
//
// Unlike [LocaleURL]'s links, these are the canonical addresses (no
// /en/ prefix for the default locale). With LOCALE_URL=none, a page has
// one address for every language, so it returns nil, as it does
// without a translator.
func Alternates(ctx context.Context) []Alternate {
	st := localeFrom(ctx)
	if st == nil || st.mode == i18n.URLNone {
		return nil
	}
	pq := withQuery(st.path, st.query)
	addr := func(loc string) string {
		if st.mode == i18n.URLSubdomain {
			return st.canonicalURL(loc, pq)
		}
		p := pq
		if loc != st.tr.Default() {
			p = "/" + strings.ToLower(loc) + pq
		}
		if st.base != nil {
			return st.base.Scheme + "://" + st.base.Host + p
		}
		return p
	}
	out := make([]Alternate, 0, len(st.tags)+1)
	for _, loc := range st.tags {
		out = append(out, Alternate{Locale: loc, URL: addr(loc)})
	}
	return append(out, Alternate{Locale: "x-default", URL: addr(st.tr.Default())})
}

// canonicalURL returns path on loc's canonical host: APP_URL's for the
// default locale, loc.<APP_URL's host> for the others.
func (st *localeState) canonicalURL(loc, path string) string {
	if st.base == nil || st.base.Host == "" {
		return path
	}
	host := st.base.Host
	if loc != st.tr.Default() {
		host = strings.ToLower(loc) + "." + host
	}
	return st.base.Scheme + "://" + host + path
}

// LocalePath returns path (a path of the app, "/dashboard") in the
// current request's locale: with LOCALE_URL=prefix, prefixed by a
// locale other than the default ("/bn/dashboard"); otherwise unchanged.
// Route URLs ([URL], [Ctx.URL]) do it already.
func LocalePath(ctx context.Context, path string) string {
	st := localeFrom(ctx)
	if st == nil || st.mode != i18n.URLPrefix || !strings.HasPrefix(path, "/") {
		return path
	}
	loc := i18n.Locale(ctx)
	if loc == st.tr.Default() {
		return path
	}
	return "/" + strings.ToLower(loc) + path
}

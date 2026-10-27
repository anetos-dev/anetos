// SPDX-License-Identifier: Apache-2.0

package vite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/web"
)

// SPADir is the folder of the web root (public/) a single-page app's
// build is in: the frontend's vite.config writes it there (outDir
// ../public/spa, base /), and [SPA] serves it at the site's root.
const SPADir = "spa"

// spaIndex is the SPA's page, in files.
const spaIndex = SPADir + "/index.html"

// spa serves a single-page app's build (see [SPA]).
type spa struct {
	files   fs.FS
	cache   fileCache
	missing bool     // no index.html: not built (development and tests)
	except  []string // path prefixes left to the routes (Except)
}

// SPAOption configures [SPA].
type SPAOption func(*spa)

// Except leaves the paths under prefix ("/api/": /api and below) to the
// app's routes: a request there that no route takes gets 404 (problem
// details, with web.JSONErrors), from a browser too, rather than the
// SPA's page.
func Except(prefix string) SPAOption {
	if !strings.HasPrefix(prefix, "/") {
		panic("vite: Except(" + strconv.Quote(prefix) + "): the prefix is a path, starting with /")
	}
	prefix = path.Clean(prefix) // as the request's path is: "//api/" is /api
	return func(s *spa) { s.except = append(s.except, prefix) }
}

// SPA returns the handler of a single-page app that Vite built into
// files' spa/ folder (files is the app's web root, the embedded public/
// folder), for the requests the app's routes don't take
// ([web.Router.Fallback]):
//
//	spa, err := vite.SPA(app, public.Files, vite.Except("/api/"))
//	…
//	srv.Router().Fallback(spa)
//
// A GET or HEAD request for a file of the build gets it (the files under
// assets/, which Vite names with a hash of their content, cached for a
// year, so keep files of frontend/public out of an assets/ folder; the
// others revalidated); one for any other path that a browser opens (it
// accepts text/html), the root, and /index.html, gets index.html, whose
// router shows the page (with Vary: Accept); any other request, and any
// under a prefix of [Except], gets 404, as problem details with
// web.JSONErrors. In development, the SPA comes from Vite's server
// instead, which forwards /api to the app.
//
// Without the build (spa/index.html), SPA fails where the app is deployed
// (APP_ENV production or staging): build the frontend (anetos build does)
// before the binary. Elsewhere, so that tests run without it, a page
// says so.
func SPA(app *anetos.App, files fs.FS, opts ...SPAOption) (http.Handler, error) {
	if files == nil {
		return nil, errors.New("vite: SPA needs the web root's files (public.Files)")
	}
	s := &spa{files: files}
	for _, o := range opts {
		o(s)
	}
	if _, err := fs.Stat(files, spaIndex); err != nil {
		if app.Config().Env.Deployed() {
			return nil, fmt.Errorf("vite: SPA: public/%s is missing: build the frontend (anetos build, or npm run build in frontend/) before the binary", spaIndex)
		}
		s.missing = true
	}
	app.AddCheck(anetos.Check{Name: "spa", Run: spaCheck(app)})
	return s, nil
}

// spaCheck is the doctor's: where the app is deployed, the SPA it serves
// is at APP_URL, whose origin must be first-party (HTTP_SPA_ORIGINS) for
// its login to use the session.
func spaCheck(app *anetos.App) func(context.Context) []anetos.Finding {
	return func(context.Context) []anetos.Finding {
		cfg := app.Config()
		if !cfg.Env.Deployed() || cfg.URL == "" {
			return nil
		}
		wc, err := web.LoadConfig(app.Source())
		if err != nil {
			return nil // reported by web's own check
		}
		origin := originOf(cfg.URL)
		if origin == "" {
			return nil
		}
		for _, o := range wc.SPAOrigins {
			if originOf(o) == origin {
				return nil
			}
		}
		return []anetos.Finding{{Severity: anetos.Warning,
			Message: "the app serves its SPA at APP_URL, but HTTP_SPA_ORIGINS doesn't list " + origin + ": the SPA's login answers with a token instead of a session cookie; add it"}}
	}
}

// originOf is a URL's origin as browsers send it: lower case, without
// the scheme's default port; "" if it has none.
func originOf(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	switch scheme {
	case "https":
		host = strings.TrimSuffix(host, ":443")
	case "http":
		host = strings.TrimSuffix(host, ":80")
	}
	return scheme + "://" + host
}

func (s *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !s.excepted(r.URL.Path) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" && fs.ValidPath(name) && !hiddenPath(name) &&
			serveFile(w, r, s.files, &s.cache, SPADir+"/"+name, name) {
			return
		}
		// The page or a 404, by the Accept header: caches keep them apart.
		w.Header().Add("Vary", "Accept")
		if name == "" || acceptsHTML(r) {
			s.index(w, r)
			return
		}
	}
	web.WriteError(w, r, &web.HTTPError{Status: http.StatusNotFound})
}

// excepted reports whether p is under a prefix of [Except] (the prefix
// without its final slash too: /api for /api/).
func (s *spa) excepted(p string) bool {
	p = path.Clean("/" + p)
	for _, prefix := range s.except {
		if strings.HasPrefix(p+"/", strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

// index serves the SPA's page, whose router shows the path's view.
func (s *spa) index(w http.ResponseWriter, r *http.Request) {
	if s.missing {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(notBuilt))
		return
	}
	if !serveFile(w, r, s.files, &s.cache, spaIndex, "index.html") {
		web.WriteError(w, r, &web.HTTPError{Status: http.StatusNotFound})
	}
}

// notBuilt is the page of an SPA without its build, outside production.
const notBuilt = `<!doctype html>
<meta charset="utf-8">
<title>The frontend isn't built</title>
<p>The frontend isn't built here (public/spa). In development, open Vite's
address (http://localhost:5173, which anetos dev runs); to serve it from
the app, build it: <code>npm run build</code> in frontend/, or
<code>anetos build</code>.</p>
`

// acceptsHTML reports whether the request is a browser opening a page:
// its Accept header names text/html (not with q=0).
func acceptsHTML(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		for media := range strings.SplitSeq(v, ",") {
			typ, params, _ := strings.Cut(media, ";")
			if !strings.EqualFold(strings.TrimSpace(typ), "text/html") {
				continue
			}
			refused := false
			for p := range strings.SplitSeq(params, ";") {
				k, v, _ := strings.Cut(p, "=")
				if strings.EqualFold(strings.TrimSpace(k), "q") {
					q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
					refused = err == nil && q == 0
				}
			}
			if !refused {
				return true
			}
		}
	}
	return false
}

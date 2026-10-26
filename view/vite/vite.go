// SPDX-License-Identifier: Apache-2.0

package vite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/view"
)

// Base is the URL path the build output is served under, and the folder
// of the web root it is in ("build", beside static/ in public/). The
// frontend's vite.config sets the same base, so the files Vite writes
// link to each other there.
const Base = "/build/"

// manifestFile is the manifest's path in the web root: vite.config's
// build.manifest names it "manifest.json" (Vite's default,
// .vite/manifest.json, is in a dot folder, which //go:embed leaves out).
const manifestFile = "build/manifest.json"

// Config holds the VITE_* settings.
type Config struct {
	// DevURL, when set, is where the browser loads the modules of Vite's
	// development server: the tags point there instead of the build.
	// anetos dev sets it to "/build/", which its proxy forwards to Vite.
	// Running Vite without anetos dev, it is Vite's own address with the
	// base ("http://localhost:5173/build/"), and vite.config's
	// server.origin is that address without the base, for the URLs in
	// the frontend's CSS. Refused where the app is deployed (APP_ENV
	// production or staging). VITE_DEV_URL.
	DevURL string `env:"VITE_DEV_URL"`
}

// LoadConfig reads the VITE_* settings.
func LoadConfig(src config.Source) (Config, error) {
	return config.Get[Config](src)
}

// Validate implements config.Validator.
func (c Config) Validate() error {
	if c.DevURL == "" {
		return nil
	}
	u, err := url.Parse(c.DevURL)
	if err != nil || !strings.HasPrefix(u.Path, "/") || !strings.HasSuffix(u.Path, "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil ||
		(u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || (u.Scheme == "") != (u.Host == "") {
		return fmt.Errorf("VITE_DEV_URL %q must be a path or an http(s) URL ending with /, without a query (\"/build/\", \"http://localhost:5173/build/\")", c.DevURL)
	}
	return nil
}

// chunk is an entry of Vite's manifest: a file of the build and what it
// needs.
type chunk struct {
	// File is the built file's path, relative to the build folder
	// ("assets/main-B7PI925R.js").
	File string `json:"file"`
	// Src is the source file's path, relative to the frontend folder.
	Src string `json:"src,omitempty"`
	// IsEntry says the chunk is an entry of the build (an input of
	// vite.config).
	IsEntry bool `json:"isEntry,omitempty"`
	// CSS are the stylesheets the chunk imports.
	CSS []string `json:"css,omitempty"`
	// Imports are the keys of the chunks it imports statically.
	Imports []string `json:"imports,omitempty"`
}

// Vite writes the tags that load a frontend's entries: from the build
// embedded in the app, or from Vite's development server. Make it with
// [New]; it is safe for concurrent use.
type Vite struct {
	files    fs.FS // the web root
	manifest map[string]chunk
	missing  error // why there is no manifest, outside deployed environments
	devURL   string
	react    bool
	cache    fileCache
}

// Option configures [New].
type Option func(*Vite)

// WithReactRefresh adds the preamble @vitejs/plugin-react needs in
// development (an inline module script before the entries), for a React
// frontend's hot reload.
func WithReactRefresh() Option {
	return func(v *Vite) { v.react = true }
}

type ctxKey struct{}

// ErrNoVite is [From]'s error for a context without a Vite: New wasn't
// called for the app.
var ErrNoVite = errors.New("vite: no Vite in the context (call vite.New in setup)")

// New sets up the app's frontend build from the VITE_* settings and
// files, the app's web root (the embedded public/ folder, whose build/
// folder holds what `vite build` wrote): it reads build/manifest.json,
// makes the Vite available in every context the app creates (for
// [Tags] and [URL], and the [Handler] handler) and to [anetos.Resolve].
//
//	v, err := vite.New(app, public.Files)
//
// Without a manifest it fails where the app is deployed (APP_ENV
// production or staging): run `npm run build` (anetos build does) before
// building the binary. Elsewhere, so that tests run without a build, the
// tags are an HTML comment saying so. With VITE_DEV_URL set, the tags
// load Vite's development server instead; New refuses it where the app
// is deployed. Calling New a second time for one app is an error.
func New(app *anetos.App, files fs.FS, opts ...Option) (*Vite, error) {
	if files == nil {
		return nil, errors.New("vite: New needs the web root's files (public.Files)")
	}
	if _, ok := anetos.Lookup[*Vite](app); ok {
		return nil, errors.New("vite: New called twice for one app")
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	env := app.Config().Env
	if cfg.DevURL != "" && env.Deployed() {
		return nil, fmt.Errorf("vite: VITE_DEV_URL is set with APP_ENV=%s: the pages would load their scripts from a development server; remove it, and build the frontend (npm run build) instead", env)
	}
	v := &Vite{files: files, devURL: cfg.DevURL}
	for _, opt := range opts {
		opt(v)
	}
	if v.devURL == "" {
		if err := v.readManifest(); err != nil {
			if env.Deployed() || !errors.Is(err, fs.ErrNotExist) {
				return nil, err // a malformed manifest is a broken build
			}
			v.missing = err
		}
	}
	anetos.Provide(app, v)
	app.AddContextValue(ctxKey{}, v)
	app.AddCheck(anetos.Check{Name: "vite", Run: v.check(env)})
	return v, nil
}

// NewWithFiles returns a Vite for files, without an app, for tests and
// tools: devURL "" reads files' build/manifest.json (an error when it
// can't), otherwise the tags load Vite's development server there, and
// files may be nil.
func NewWithFiles(files fs.FS, devURL string, opts ...Option) (*Vite, error) {
	if err := (Config{DevURL: devURL}).Validate(); err != nil {
		return nil, fmt.Errorf("vite: %w", err)
	}
	if files == nil && devURL == "" {
		return nil, errors.New("vite: NewWithFiles needs the web root's files without a development server")
	}
	v := &Vite{files: files, devURL: devURL}
	for _, opt := range opts {
		opt(v)
	}
	if devURL == "" {
		if err := v.readManifest(); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (v *Vite) readManifest() error {
	b, err := fs.ReadFile(v.files, manifestFile)
	if err != nil {
		return fmt.Errorf("vite: read %s: %w (build the frontend: npm run build, or anetos build)", manifestFile, err)
	}
	if err := json.Unmarshal(b, &v.manifest); err != nil {
		return fmt.Errorf("vite: read %s: %w", manifestFile, err)
	}
	return nil
}

// check is the doctor's: a development app without a build or Vite's
// server gets pages without their scripts.
func (v *Vite) check(env anetos.Environment) func(context.Context) []anetos.Finding {
	return func(context.Context) []anetos.Finding {
		if v.missing == nil || !env.IsDevelopment() {
			return nil
		}
		return []anetos.Finding{{Severity: anetos.Warning, Message: manifestFile + " is missing and VITE_DEV_URL isn't set: pages load no scripts or styles of the frontend; run anetos dev (which runs Vite), or npm run build in frontend/"}}
	}
}

// WithVite returns ctx carrying v, for [Tags], [URL] and [Handler].
func WithVite(ctx context.Context, v *Vite) context.Context {
	return context.WithValue(ctx, ctxKey{}, v)
}

// From returns the Vite in ctx, or [ErrNoVite].
func From(ctx context.Context) (*Vite, error) {
	if v, ok := ctx.Value(ctxKey{}).(*Vite); ok && v != nil {
		return v, nil
	}
	return nil, ErrNoVite
}

// Dev reports whether the tags load Vite's development server
// (VITE_DEV_URL is set).
func (v *Vite) Dev() bool { return v.devURL != "" }

// Tags returns the tags that load the entries, the paths of vite.config's
// inputs relative to the frontend folder ("src/main.ts", "src/app.css"):
// in a templ layout's head,
//
//	@vite.Tags("src/main.ts")
//
// From the build, an entry's stylesheets and those of the chunks it
// imports, its script (a module) or stylesheet, and modulepreload links
// for the chunks it imports; in development, Vite's client and the
// entries' sources. Rendering fails when an entry isn't in the build's
// manifest. A page calls it once, with all its entries: separate calls
// repeat what their entries share, such as a stylesheet. The Vite is the
// context's ([From]).
func Tags(entries ...string) view.Component {
	return view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		v, err := From(ctx)
		if err != nil {
			return err
		}
		return v.render(w, entries)
	})
}

// Tags is the package's [Tags] with v.
func (v *Vite) Tags(entries ...string) view.Component {
	return view.ComponentFunc(func(_ context.Context, w io.Writer) error {
		return v.render(w, entries)
	})
}

func (v *Vite) render(w io.Writer, entries []string) error {
	var b strings.Builder
	switch {
	case v.devURL != "":
		v.devTags(&b, entries)
	case v.manifest == nil:
		fmt.Fprintf(&b, "<!-- vite: no %s (run anetos dev, or npm run build in frontend/) -->\n", manifestFile)
	default:
		if err := v.buildTags(&b, entries); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (v *Vite) devTags(b *strings.Builder, entries []string) {
	if v.react {
		// A JSON string is a JavaScript one, and json escapes <, > and &.
		refresh, _ := json.Marshal(v.devURL + "@react-refresh")
		fmt.Fprintf(b, `<script type="module">
import RefreshRuntime from %s
RefreshRuntime.injectIntoGlobalHook(window)
window.$RefreshReg$ = () => {}
window.$RefreshSig$ = () => (type) => type
window.__vite_plugin_react_preamble_installed__ = true
</script>
`, refresh)
	}
	fmt.Fprintf(b, "<script type=\"module\" src=\"%s\"></script>\n", attr(v.devURL+"@vite/client"))
	for _, e := range entries {
		u := attr(v.devURL + strings.TrimPrefix(e, "/"))
		if isCSS(e) {
			fmt.Fprintf(b, "<link rel=\"stylesheet\" href=\"%s\">\n", u)
		} else {
			fmt.Fprintf(b, "<script type=\"module\" src=\"%s\"></script>\n", u)
		}
	}
}

// buildTags writes the tags of Vite's backend integration guide: every
// stylesheet first, then the entries, then the chunks to preload.
func (v *Vite) buildTags(b *strings.Builder, entries []string) error {
	seenCSS := map[string]bool{}
	var css, scripts, preloads []string
	seenPreload := map[string]bool{}
	for _, e := range entries {
		e = strings.TrimPrefix(e, "/")
		c, ok := v.manifest[e]
		if !ok {
			return fmt.Errorf("vite: %q isn't an entry of %s: add it to vite.config's build input, and build again", e, manifestFile)
		}
		addCSS := func(files []string) {
			for _, f := range files {
				if !seenCSS[f] {
					seenCSS[f] = true
					css = append(css, f)
				}
			}
		}
		addCSS(c.CSS)
		imported := v.imported(e)
		for _, key := range imported {
			addCSS(v.manifest[key].CSS)
		}
		if isCSS(c.File) {
			addCSS([]string{c.File})
		} else {
			scripts = append(scripts, c.File)
		}
		for _, key := range imported {
			if f := v.manifest[key].File; !seenPreload[f] && !isCSS(f) {
				seenPreload[f] = true
				preloads = append(preloads, f)
			}
		}
	}
	for _, f := range css {
		fmt.Fprintf(b, "<link rel=\"stylesheet\" href=\"%s\">\n", attr(Base+f))
	}
	for _, f := range scripts {
		fmt.Fprintf(b, "<script type=\"module\" src=\"%s\"></script>\n", attr(Base+f))
	}
	for _, f := range preloads {
		fmt.Fprintf(b, "<link rel=\"modulepreload\" href=\"%s\">\n", attr(Base+f))
	}
	return nil
}

// imported returns the keys of the chunks name imports, directly or
// not, each once, dependencies before the chunks importing them.
func (v *Vite) imported(name string) []string {
	var out []string
	seen := map[string]bool{name: true}
	var walk func(string)
	walk = func(n string) {
		for _, key := range v.manifest[n].Imports {
			if seen[key] {
				continue
			}
			seen[key] = true
			walk(key)
			out = append(out, key)
		}
	}
	walk(name)
	return out
}

// URL returns the URL of a file of the frontend, by its source path
// ("src/images/logo.svg"): the built file's, from the manifest, or the
// development server's. "" when the build has no such file. The Vite is
// the context's ([From]).
func URL(ctx context.Context, src string) string {
	v, err := From(ctx)
	if err != nil {
		return ""
	}
	return v.URL(src)
}

// URL is the package's [URL] with v.
func (v *Vite) URL(src string) string {
	src = strings.TrimPrefix(src, "/")
	if v.devURL != "" {
		return v.devURL + src
	}
	if c, ok := v.manifest[src]; ok {
		return Base + c.File
	}
	return ""
}

func isCSS(name string) bool {
	switch path.Ext(name) {
	case ".css", ".scss", ".sass", ".less", ".styl", ".stylus", ".pcss", ".postcss":
		return true
	}
	return false
}

func attr(s string) string { return html.EscapeString(s) }

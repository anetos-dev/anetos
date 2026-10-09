// SPDX-License-Identifier: Apache-2.0

package view

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Assets serves static files (CSS, JavaScript, images) and builds their
// URLs with a content hash, so browsers can cache them for a year and
// still get a new version as soon as a file changes:
//
//	//go:embed public
//	var public embed.FS
//
//	files, _ := fs.Sub(public, "public")
//	assets, err := view.NewAssets("/assets", files, htmx.FS)
//	r.HandleStd("GET", "/assets/{path...}", assets)
//
//	<link rel="stylesheet" href={ assets.URL("app.css") }>
//
// Files are hashed once, when NewAssets runs (anetos dev restarts the app
// when they change). A text file (CSS, JavaScript, SVG, JSON…) of 1 KiB
// or more is gzipped once, the first time it's requested, and sent
// compressed to clients that accept it. An Assets is safe for concurrent
// use.
type Assets struct {
	prefix string
	files  map[string]*assetFile
}

type assetFile struct {
	fsys fs.FS
	hash string
	text bool // a text file of 1 KiB or more, which may shrink gzipped

	gzOnce sync.Once
	gz     []byte // the file gzipped, if it shrinks by a tenth; else nil
}

// gzipped returns the file gzipped, compressing it the first time, or
// nil when it isn't worth it.
func (f *assetFile) gzipped(name string) []byte {
	if !f.text {
		return nil
	}
	f.gzOnce.Do(func() {
		if b, err := fs.ReadFile(f.fsys, name); err == nil {
			f.gz = compress(b)
		}
	})
	return f.gz
}

// NewAssets returns the files of the given file systems served under the
// URL prefix ("/assets"). When a name exists in several, the first file
// system wins. Names starting with a dot are not served.
func NewAssets(prefix string, fsys ...fs.FS) (*Assets, error) {
	if !strings.HasPrefix(prefix, "/") {
		return nil, fmt.Errorf("view: asset prefix %q must start with /", prefix)
	}
	a := &Assets{prefix: strings.TrimSuffix(prefix, "/"), files: map[string]*assetFile{}}
	for _, f := range fsys {
		err := fs.WalkDir(f, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if hidden(name) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if _, dup := a.files[name]; dup {
				return nil
			}
			b, err := fs.ReadFile(f, name)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			a.files[name] = &assetFile{fsys: f, hash: hex.EncodeToString(sum[:5]), text: len(b) >= 1024 && compressible(name)}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("view: reading assets: %w", err)
		}
	}
	return a, nil
}

// compress returns b gzipped, or nil when that doesn't shrink it by a
// tenth.
func compress(b []byte) []byte {
	var out bytes.Buffer
	w, _ := gzip.NewWriterLevel(&out, gzip.BestCompression) // a valid level: no error
	if _, err := w.Write(b); err != nil {
		return nil
	}
	if err := w.Close(); err != nil || out.Len() > len(b)*9/10 {
		return nil
	}
	return bytes.Clone(out.Bytes())
}

// compressible reports whether a file's type is text, which gzip shrinks.
func compressible(name string) bool {
	t, _, _ := strings.Cut(mime.TypeByExtension(path.Ext(name)), ";")
	switch t {
	case "application/javascript", "text/javascript", "application/json", "application/manifest+json",
		"image/svg+xml", "application/xml", "application/wasm":
		return true
	}
	return strings.HasPrefix(t, "text/")
}

// acceptsGzip reports whether a request's Accept-Encoding takes gzip:
// it names gzip with a weight above 0 (a "*" alone doesn't count).
func acceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for part := range strings.SplitSeq(v, ",") {
			coding, params, _ := strings.Cut(part, ";")
			if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
				continue
			}
			for param := range strings.SplitSeq(params, ";") {
				name, value, _ := strings.Cut(param, "=")
				if strings.EqualFold(strings.TrimSpace(name), "q") {
					q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
					return err == nil && q > 0
				}
			}
			return true
		}
	}
	return false
}

// gzipWriter marks a response gzipped when it has content (200, 206):
// http.ServeContent sets the length of the gzipped bytes only while the
// header has no Content-Encoding, and a 304 or an error isn't gzipped.
type gzipWriter struct{ http.ResponseWriter }

func (w gzipWriter) WriteHeader(code int) {
	if code == http.StatusOK || code == http.StatusPartialContent {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap returns the response writer, for http.ResponseController.
func (w gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func hidden(name string) bool {
	for p := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(p, ".") && p != "." {
			return true
		}
	}
	return false
}

// URL returns the URL of the named file ("css/app.css"), with its content
// hash: "/assets/css/app.css?v=3f2a9c01d4". A name that doesn't exist gets
// no hash, so the browser reports the missing file.
func (a *Assets) URL(name string) string {
	name = strings.TrimPrefix(name, "/")
	segs := strings.Split(name, "/")
	for i, p := range segs {
		segs[i] = url.PathEscape(p)
	}
	u := a.prefix + "/" + strings.Join(segs, "/")
	if f, ok := a.files[name]; ok {
		u += "?v=" + f.hash
	}
	return u
}

// Has reports whether the named file exists.
func (a *Assets) Has(name string) bool {
	_, ok := a.files[strings.TrimPrefix(name, "/")]
	return ok
}

// ServeHTTP serves the file named by the request path after the prefix.
// Requests with the current hash in "v" may be cached for a year; others
// must revalidate (ETag). A text file goes gzipped to a client whose
// Accept-Encoding takes gzip.
func (a *Assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	name, ok := strings.CutPrefix(r.URL.Path, a.prefix+"/")
	f, found := a.files[name]
	if !ok || name != path.Clean(name) || !found {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("ETag", `"`+f.hash+`"`)
	if r.URL.Query().Get("v") == f.hash {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("X-Content-Type-Options", "nosniff")
	if gz := f.gzipped(name); gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("ETag", `"`+f.hash+`-gz"`) // another representation
			if t := mime.TypeByExtension(path.Ext(name)); t != "" {
				h.Set("Content-Type", t)
			}
			http.ServeContent(gzipWriter{w}, r, name, time.Time{}, bytes.NewReader(gz))
			return
		}
	}
	file, err := f.fsys.Open(name)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	content, ok := file.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		content = bytes.NewReader(b)
	}
	http.ServeContent(w, r, name, time.Time{}, content)
}

// SPDX-License-Identifier: Apache-2.0

package vite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos/internal/gzipfile"
)

// Handler returns the handler that serves the build's files under
// [Base], for the router:
//
//	r.Get("/build/{path...}", web.WrapHandler(vite.Handler()))
//
// The files Vite names with a hash of their content (under assets/) may
// be cached for a year; others must revalidate (ETag). A text file of 1
// KiB or more goes gzipped to clients that take it. The Vite is the
// request context's ([From]); without one, the handler answers 404.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, err := From(r.Context())
		if err != nil {
			http.NotFound(w, r)
			return
		}
		v.ServeHTTP(w, r)
	})
}

// ServeHTTP serves the build's files under [Base] (see [Handler]); the
// manifest and dot files aren't served.
func (v *Vite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	name, ok := strings.CutPrefix(r.URL.Path, Base)
	if !ok || name == "" || name != path.Clean(name) || hiddenPath(name) || name == path.Base(manifestFile) || v.files == nil {
		http.NotFound(w, r)
		return
	}
	full := strings.Trim(Base, "/") + "/" + name
	f, err := v.cache.get(v.files, full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("ETag", `"`+f.hash+`"`)
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("X-Content-Type-Options", "nosniff")
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		h.Set("Content-Type", t)
	}
	if f.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if gzipfile.AcceptsGzip(r) {
			h.Set("ETag", `"`+f.hash+`-gz"`) // another representation
			http.ServeContent(gzipfile.Writer{ResponseWriter: w}, r, name, time.Time{}, bytes.NewReader(f.gz))
			return
		}
	}
	file, err := v.files.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	if rs, ok := file.(io.ReadSeeker); ok { // embed's files: no copy
		http.ServeContent(w, r, name, time.Time{}, rs)
		return
	}
	b, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}

// hiddenPath reports whether a part of name starts with a dot.
func hiddenPath(name string) bool {
	for p := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(p, ".") {
			return true
		}
	}
	return false
}

// fileCache holds what is computed once for each of the build's files
// (the files of an embedded file system don't change while the app runs):
// their hash, and their gzipped form.
type fileCache struct {
	m sync.Map // name → *cachedFile
}

type cachedFile struct {
	hash string
	gz   []byte // the file gzipped, if it shrinks by a tenth; else nil
}

func (c *fileCache) get(files fs.FS, name string) (*cachedFile, error) {
	if f, ok := c.m.Load(name); ok {
		return f.(*cachedFile), nil
	}
	b, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	f := &cachedFile{hash: hex.EncodeToString(sum[:5])}
	if len(b) >= 1024 && gzipfile.Compressible(name) {
		f.gz = gzipfile.Compress(b)
	}
	actual, _ := c.m.LoadOrStore(name, f)
	return actual.(*cachedFile), nil
}

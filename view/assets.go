// SPDX-License-Identifier: Apache-2.0

package view

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
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
// when they change). An Assets is safe for concurrent use.
type Assets struct {
	prefix string
	files  map[string]assetFile
}

type assetFile struct {
	fsys fs.FS
	hash string
}

// NewAssets returns the files of the given file systems served under the
// URL prefix ("/assets"). When a name exists in several, the first file
// system wins. Names starting with a dot are not served.
func NewAssets(prefix string, fsys ...fs.FS) (*Assets, error) {
	if !strings.HasPrefix(prefix, "/") {
		return nil, fmt.Errorf("view: asset prefix %q must start with /", prefix)
	}
	a := &Assets{prefix: strings.TrimSuffix(prefix, "/"), files: map[string]assetFile{}}
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
			a.files[name] = assetFile{fsys: f, hash: hex.EncodeToString(sum[:5])}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("view: reading assets: %w", err)
		}
	}
	return a, nil
}

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
// must revalidate (ETag).
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
	h := w.Header()
	h.Set("ETag", `"`+f.hash+`"`)
	if r.URL.Query().Get("v") == f.hash {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, time.Time{}, content)
}

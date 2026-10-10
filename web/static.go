// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
)

// static is a file system [Router.Static] serves.
type static struct {
	host, prefix string // prefix ends with "/"
	fsys         fs.FS
}

// Static serves the files of fsys below prefix, for GET and HEAD requests
// no route matches: with r.Static("/", public.Files), a request for
// /robots.txt gets the file robots.txt, and /.well-known/security.txt the
// file .well-known/security.txt. Routes win, and a path that has routes
// for other methods still answers 405. It never serves a directory, a
// hidden file or folder (a name starting with "." or "_") other than
// .well-known, or a Go source file; those, and missing files, are 404
// with the router's error pages. Global middleware applies; route and
// group middleware don't. On a group or a [Router.Host] router, prefix is
// below the group's path and only that host is served; a host's file
// systems are tried before the others.
//
// Content-hashed assets (CSS, scripts, images the pages link to) are
// better served with view.Assets, whose URLs can be cached for a year.
func (r *Router) Static(prefix string, fsys fs.FS) {
	if fsys == nil {
		panic("web: Static with a nil file system")
	}
	checkPattern(prefix)
	full := joinPath(r.prefix, prefix)
	if !strings.HasSuffix(full, "/") {
		full += "/"
	}
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	r.core.statics = append(r.core.statics, static{host: hostName(r.host), prefix: full, fsys: fsys})
	// A host's file systems first, as a host's routes win over others.
	slices.SortStableFunc(r.core.statics, func(a, b static) int {
		switch {
		case a.host != "" && b.host == "":
			return -1
		case a.host == "" && b.host != "":
			return 1
		}
		return 0
	})
}

// serveStatic serves req from the first of the router's static file
// systems that has the file, and reports whether one did.
func (r *Router) serveStatic(w http.ResponseWriter, req *http.Request) bool {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false
	}
	r.core.mu.RLock()
	statics := r.core.statics
	r.core.mu.RUnlock()
	for _, s := range statics {
		if s.host != "" && !strings.EqualFold(s.host, hostName(req.Host)) {
			continue
		}
		name, ok := strings.CutPrefix(req.URL.Path, s.prefix)
		if !ok || !servable(name) {
			continue
		}
		if serveFile(w, req, s.fsys, name) {
			return true
		}
	}
	return false
}

// servable reports whether Static may serve the file name.
func servable(name string) bool {
	if name == "" || !fs.ValidPath(name) || strings.EqualFold(path.Ext(name), ".go") {
		return false
	}
	for i, part := range strings.Split(name, "/") {
		if (strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_")) && (i > 0 || part != ".well-known") {
			return false
		}
	}
	return true
}

// serveFile serves the regular file name of fsys, and reports whether
// there was one.
func serveFile(w http.ResponseWriter, req *http.Request, fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		content = bytes.NewReader(b)
	}
	http.ServeContent(w, req, path.Base(name), info.ModTime(), content)
	return true
}

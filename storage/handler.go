// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// Serve writes the file at path to w: its content type, length, ETag and
// modification time, with conditional requests and a single range when
// the file can seek (a request for several ranges gets the whole file).
// Files a browser would run ([IsActive]: HTML, SVG, XML, JavaScript,
// CSS) are sent as application/octet-stream attachments in a sandbox,
// so an uploaded file can't run on the app's origin, not even through
// <script src>; an attachment Content-Disposition already set on w (to
// name the download) is kept. A missing file is a 404; other errors are
// logged and a 500.
//
//	disk.Serve(c.Writer(), c.Request(), invoice.Path) // in a handler, after checking access
func (d *Disk) Serve(w http.ResponseWriter, r *http.Request, p string) {
	f, err := d.Open(r.Context(), p)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalidPath):
		http.NotFound(w, r)
		return
	case err != nil:
		d.log.ErrorContext(r.Context(), "storage: serve a file", "disk", d.name, "path", p, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info := f.Info()
	h := w.Header()
	ct := info.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("X-Content-Type-Options", "nosniff")
	if IsActive(ct) {
		ct = "application/octet-stream"
		h.Set("Content-Security-Policy", "sandbox")
		// An attachment disposition the caller set (with the uploaded
		// file's name) stays; anything else becomes one.
		if d, _, err := mime.ParseMediaType(h.Get("Content-Disposition")); err != nil || d != "attachment" {
			h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": baseName(p)}))
		}
	}
	h.Set("Content-Type", ct)
	if strings.Count(r.Header.Get("Range"), ",") > 0 {
		r.Header.Del("Range") // several ranges: each would be a read, a request to S3
	}
	if info.ETag != "" {
		h.Set("ETag", info.ETag)
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "", info.ModTime, rs)
		return
	}
	if !info.ModTime.IsZero() {
		h.Set("Last-Modified", info.ModTime.UTC().Format(http.TimeFormat))
	}
	if info.Size >= 0 {
		h.Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, f)
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Handler returns a handler that serves the disk's files: the path is
// the request's "path" wildcard, or its URL path without the leading
// "/". Mount it at STORAGE_URL's path:
//
//	r.HandleStd(http.MethodGet, "/files/{path...}", disk.Handler())
//
// On a disk that isn't public, it serves only requests with a valid
// token from [Disk.TemporaryURL] (others get a 403), privately cached.
func (d *Disk) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		p := r.PathValue("path")
		if p == "" {
			p = strings.TrimPrefix(r.URL.Path, "/")
		}
		if CheckPath(p) != nil {
			http.NotFound(w, r)
			return
		}
		if !d.public {
			if !d.checkToken(r.URL.Query().Get("token"), p) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
		}
		d.Serve(w, r, p)
	})
}

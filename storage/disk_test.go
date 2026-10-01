// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/storage"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var ctx = context.Background()

func newSigner(t *testing.T) *encryption.Encrypter {
	t.Helper()
	k, err := encryption.ParseKey(encryption.GenerateKey())
	check(t, err)
	e, err := encryption.New(k)
	check(t, err)
	return e
}

func TestDisk(t *testing.T) {
	d := storage.NewDisk("files", storage.NewMemoryBackend())
	check(t, d.PutBytes(ctx, "notes/a.txt", []byte("hello")))
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 600))
	check(t, d.PutBytes(ctx, "noext", png))
	check(t, d.PutBytes(ctx, "data.bin", []byte{1, 2}, storage.ContentType("application/x-thing")))
	for p, want := range map[string]string{"notes/a.txt": "text/plain; charset=utf-8", "noext": "image/png", "data.bin": "application/x-thing"} {
		info, err := d.Stat(ctx, p)
		check(t, err)
		if info.ContentType != want {
			t.Errorf("%s: %q, want %q", p, info.ContentType, want)
		}
	}
	if data, err := d.Get(ctx, "noext"); err != nil || !bytes.Equal(data, png) {
		t.Errorf("sniffed file changed: %v", err)
	}
	if err := d.PutBytes(ctx, "x.txt", nil, storage.ContentType("not a type")); err == nil {
		t.Error("an invalid content type: no error")
	}
	for _, p := range []string{"", "/abs", "../up", "a/../b", "a//b", `a\b`, "dir/", "nul\x00", "./a", strings.Repeat("a", 1025)} {
		if err := d.PutBytes(ctx, p, nil); !errors.Is(err, storage.ErrInvalidPath) {
			t.Errorf("Put(%q) = %v", p, err)
		}
		if _, err := d.Get(ctx, p); !errors.Is(err, storage.ErrInvalidPath) {
			t.Errorf("Get(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"..", "a/..", "/a", "a//"} {
		for _, err := range d.List(ctx, p) {
			if !errors.Is(err, storage.ErrInvalidPath) {
				t.Errorf("List(%q) = %v", p, err)
			}
		}
	}
	ok, err := d.Exists(ctx, "notes/a.txt")
	if !ok || err != nil {
		t.Errorf("Exists = %v, %v", ok, err)
	}
	if ok, err := d.Exists(ctx, "nope"); ok || err != nil {
		t.Errorf("Exists(nope) = %v, %v", ok, err)
	}
	if _, err := d.Get(ctx, "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get(nope) = %v", err)
	}
	check(t, d.Move(ctx, "notes/a.txt", "notes/b.txt"))
	check(t, d.Copy(ctx, "notes/b.txt", "notes/c.txt"))
	check(t, d.Copy(ctx, "notes/c.txt", "notes/c.txt"))
	if ok, _ := d.Exists(ctx, "notes/a.txt"); ok {
		t.Error("Move left the source")
	}
	n, err := d.DeleteAll(ctx, "notes/")
	if n != 2 || err != nil {
		t.Errorf("DeleteAll = %d, %v", n, err)
	}
	if _, err := d.DeleteAll(ctx, ""); err == nil {
		t.Error("DeleteAll(\"\"): no error")
	}
	check(t, d.Delete(ctx, "noext", "data.bin", "missing"))
	for _, err := range d.List(ctx, "") {
		t.Errorf("a file is left: %v", err)
	}
	if d.Name() != "files" || d.Backend() == nil {
		t.Error("Name, Backend")
	}
}

func TestPutUpload(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("avatar", "../../evil.png")
	check(t, err)
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\nimage"))
	check(t, w.Close())
	r := httptest.NewRequest(http.MethodPost, "/", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	check(t, r.ParseMultipartForm(1<<20))
	fh := r.MultipartForm.File["avatar"][0]

	d := storage.NewDisk("files", storage.NewMemoryBackend())
	check(t, d.PutUpload(ctx, "avatars/42", fh))
	info, err := d.Stat(ctx, "avatars/42")
	check(t, err)
	if info.ContentType != "image/png" || info.Size != 13 {
		t.Errorf("info %+v", info)
	}
	if err := d.PutUpload(ctx, "x", nil); err == nil {
		t.Error("PutUpload(nil): no error")
	}
}

func TestURLs(t *testing.T) {
	signer := newSigner(t)
	mem := storage.NewMemoryBackend()
	d := storage.NewDisk("files", mem)
	if _, err := d.URL("a.txt"); !errors.Is(err, storage.ErrNoURL) {
		t.Errorf("URL without a base = %v", err)
	}
	if _, err := d.TemporaryURL(ctx, "a.txt", time.Minute); !errors.Is(err, storage.ErrNoURL) {
		t.Errorf("TemporaryURL without a base = %v", err)
	}
	private := storage.NewDisk("files", mem, storage.BaseURL("https://example.com/files/"), storage.SignWith(signer))
	if _, err := private.URL("a.txt"); err == nil || errors.Is(err, storage.ErrNoURL) {
		t.Errorf("URL of a private disk = %v", err)
	}
	if _, err := private.TemporaryURL(ctx, "a.txt", 0); err == nil {
		t.Error("TemporaryURL(0): no error")
	}
	public := storage.NewDisk("files", mem, storage.BaseURL("https://cdn.example.com"), storage.Public())
	u, err := public.URL("photos/my cat#1.jpg")
	check(t, err)
	if u != "https://cdn.example.com/photos/my%20cat%231.jpg" {
		t.Errorf("URL = %s", u)
	}
	if _, err := public.URL("../x"); err == nil {
		t.Error("URL(../x): no error")
	}
}

func TestHandler(t *testing.T) {
	signer := newSigner(t)
	mem := storage.NewMemoryBackend()
	private := storage.NewDisk("private", mem, storage.BaseURL("https://example.com/files"), storage.SignWith(signer))
	now := time.Now()
	storage.SetNow(private, func() time.Time { return now })
	check(t, private.PutBytes(ctx, "invoices/42 final.pdf", []byte("%PDF-1.7 invoice")))
	check(t, private.PutBytes(ctx, "page.html", []byte("<script>alert(1)</script>")))

	mux := http.NewServeMux()
	mux.Handle("/files/{path...}", private.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	type result struct {
		StatusCode    int
		Header        http.Header
		ContentLength int64
		body          string
	}
	fetch := func(method, rawURL string, header ...string) result {
		t.Helper()
		u, err := url.Parse(rawURL)
		check(t, err)
		req, err := http.NewRequestWithContext(ctx, method, srv.URL+u.RequestURI(), nil)
		check(t, err)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		check(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return result{resp.StatusCode, resp.Header, resp.ContentLength, string(b)}
	}
	body := func(r result) string { return r.body }

	u, err := private.TemporaryURL(ctx, "invoices/42 final.pdf", time.Minute)
	check(t, err)
	if !strings.HasPrefix(u, "https://example.com/files/invoices/42%20final.pdf?token=") {
		t.Errorf("TemporaryURL = %s", u)
	}
	resp := fetch("GET", u)
	if resp.StatusCode != 200 || body(resp) != "%PDF-1.7 invoice" || resp.Header.Get("Content-Type") != "application/pdf" ||
		resp.Header.Get("Cache-Control") != "private, no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("ETag") == "" {
		t.Errorf("GET: %d %v", resp.StatusCode, resp.Header)
	}
	if resp := fetch("GET", u, "Range", "bytes=0-3"); resp.StatusCode != http.StatusPartialContent || body(resp) != "%PDF" {
		t.Errorf("range: %d", resp.StatusCode)
	}
	if resp := fetch("GET", u, "If-None-Match", resp.Header.Get("ETag")); resp.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", resp.StatusCode)
	}
	if resp := fetch("HEAD", u); resp.StatusCode != 200 || resp.ContentLength != 16 {
		t.Errorf("HEAD: %d %d", resp.StatusCode, resp.ContentLength)
	}
	// Without a token, with another file's, an expired one, or another
	// disk's: 403.
	other, err := private.TemporaryURL(ctx, "page.html", time.Minute)
	check(t, err)
	otherDisk := storage.NewDisk("public", mem, storage.BaseURL("https://example.com/files"), storage.SignWith(signer))
	foreign, err := otherDisk.TemporaryURL(ctx, "invoices/42 final.pdf", time.Minute)
	check(t, err)
	_, otherQuery, _ := strings.Cut(other, "?")
	_, foreignQuery, _ := strings.Cut(foreign, "?")
	for name, target := range map[string]string{
		"no token":      "/files/invoices/42%20final.pdf",
		"another file":  "/files/invoices/42%20final.pdf?" + otherQuery,
		"another disk":  "/files/invoices/42%20final.pdf?" + foreignQuery,
		"garbage token": "/files/invoices/42%20final.pdf?token=abc",
	} {
		if resp := fetch("GET", target); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	now = now.Add(2 * time.Minute)
	if resp := fetch("GET", u); resp.StatusCode != http.StatusForbidden {
		t.Errorf("expired: %d", resp.StatusCode)
	}
	now = now.Add(-2 * time.Minute)
	// Missing files, bad paths, other methods.
	missing, err := private.TemporaryURL(ctx, "missing.pdf", time.Minute)
	check(t, err)
	if resp := fetch("GET", missing); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing: %d", resp.StatusCode)
	}
	if resp := fetch("GET", "/files/a/..%2F..%2Fetc"); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Errorf("bad path: %d", resp.StatusCode)
	}
	if resp := fetch("POST", u); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
	// HTML is an attachment in a sandbox.
	resp = fetch("GET", other)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Security-Policy") != "sandbox" || resp.Header.Get("Content-Disposition") != "attachment; filename=page.html" {
		t.Errorf("HTML: %d %v", resp.StatusCode, resp.Header)
	}

	// A public disk serves without a token; its handler works without a
	// path wildcard too.
	public := storage.NewDisk("public", mem, storage.BaseURL("https://example.com/pub"), storage.Public())
	psrv := httptest.NewServer(http.StripPrefix("/pub", public.Handler()))
	defer psrv.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, psrv.URL+"/pub/invoices/42%20final.pdf", nil)
	check(t, err)
	r, err := http.DefaultClient.Do(req)
	check(t, err)
	defer r.Body.Close()
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "" {
		t.Errorf("public: %d %v", r.StatusCode, r.Header)
	}
}

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestForApp(t *testing.T) {
	dir := t.TempDir()
	for name, env := range map[string]config.Map{
		"unknown driver":     {"STORAGE_DRIVER": "floppy"},
		"bad URL":            {"STORAGE_DRIVER": "memory", "STORAGE_URL": "ftp://x"},
		"bad disk name":      {"STORAGE_DRIVER": "memory", "STORAGE_DISKS": "Avatars"},
		"repeated disk":      {"STORAGE_DRIVER": "memory", "STORAGE_DISKS": "a,a"},
		"default disk name":  {"STORAGE_DRIVER": "memory", "STORAGE_DISKS": "default"},
		"named disk's error": {"STORAGE_DRIVER": "memory", "STORAGE_DISKS": "a", "STORAGE_A_DRIVER": "floppy"},
	} {
		if _, err := storage.ForApp(newApp(t, env)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	app := newApp(t, config.Map{
		"STORAGE_ROOT": filepath.Join(dir, "app"), "STORAGE_URL": "https://example.com/files",
		"STORAGE_DISKS": "avatars, exports", "STORAGE_AVATARS_ROOT": filepath.Join(dir, "avatars"),
		"STORAGE_AVATARS_URL": "https://cdn.example.com/avatars", "STORAGE_AVATARS_PUBLIC": "true",
		"STORAGE_EXPORTS_DRIVER": "memory",
	})
	st, err := storage.ForApp(app)
	check(t, err)
	if _, err := storage.ForApp(app); err == nil {
		t.Error("ForApp twice: no error")
	}
	actx := app.Context(ctx)
	def, err := storage.From(actx)
	check(t, err)
	avatars, err := storage.From(actx, "avatars")
	check(t, err)
	exports, err := storage.From(actx, "exports")
	check(t, err)
	if d, _ := st.Disk("default"); d != def || st.Default() != def {
		t.Error("Disk(default) isn't the default disk")
	}
	if _, err := storage.From(actx, "nope"); err == nil {
		t.Error("From(nope): no error")
	}
	if _, err := storage.From(actx, "a", "b"); err == nil {
		t.Error("From with two names: no error")
	}
	if _, err := storage.From(ctx); !errors.Is(err, storage.ErrNoStorage) {
		t.Errorf("From without storage = %v", err)
	}
	check(t, def.PutBytes(actx, "a.txt", []byte("default")))
	check(t, avatars.PutBytes(actx, "1.png", []byte("avatar")))
	check(t, exports.PutBytes(actx, "e.csv", []byte("export")))
	for f, want := range map[string]string{"app/a.txt": "default", "avatars/1.png": "avatar"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err != nil || string(b) != want {
			t.Errorf("%s: %q, %v", f, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "exports")); err == nil {
		t.Error("the memory disk wrote files")
	}
	// The default disk's URL settings aren't inherited; the driver is.
	if _, err := def.TemporaryURL(actx, "a.txt", time.Minute); err != nil {
		t.Errorf("TemporaryURL on the default disk: %v", err)
	}
	if u, err := avatars.URL("1.png"); err != nil || u != "https://cdn.example.com/avatars/1.png" {
		t.Errorf("avatars URL = %q, %v", u, err)
	}
	if _, err := exports.TemporaryURL(actx, "e.csv", time.Minute); !errors.Is(err, storage.ErrNoURL) {
		t.Errorf("exports TemporaryURL = %v", err)
	}
	names := []string{def.Name(), avatars.Name(), exports.Name()}
	if !slices.Equal(names, []string{"default", "avatars", "exports"}) {
		t.Errorf("names %q", names)
	}
	// Named disks inherit the driver.
	app = newApp(t, config.Map{"STORAGE_DRIVER": "memory", "STORAGE_DISKS": "x"})
	st, err = storage.ForApp(app)
	check(t, err)
	x, err := st.Disk("x")
	check(t, err)
	if _, ok := x.Backend().(*storage.MemoryBackend); !ok {
		t.Errorf("x's backend is a %T", x.Backend())
	}
}

func TestHardening(t *testing.T) {
	d := storage.NewDisk("files", storage.NewMemoryBackend())
	// Paths.
	for _, p := range []string{strings.Repeat("a", 256), "x/\u202eevil", "c1\u0085", ".anetos-tmp-x", "a/.anetos-tmp-b/c"} {
		if err := storage.CheckPath(p); !errors.Is(err, storage.ErrInvalidPath) {
			t.Errorf("CheckPath(%q) = %v", p, err)
		}
	}
	// Sniffing never yields active content, and keeps a seekable reader's
	// position.
	check(t, d.PutBytes(ctx, "noext", []byte("<html><script>alert(1)</script>")))
	if info, _ := d.Stat(ctx, "noext"); info.ContentType != "application/octet-stream" {
		t.Errorf("sniffed HTML: %q", info.ContentType)
	}
	r := strings.NewReader("skip:\x89PNG\r\n\x1a\nrest")
	_, _ = r.Seek(5, io.SeekStart)
	check(t, d.Put(ctx, "img", r))
	if b, _ := d.Get(ctx, "img"); string(b) != "\x89PNG\r\n\x1a\nrest" {
		t.Errorf("content %q", b)
	}
	if info, _ := d.Stat(ctx, "img"); info.ContentType != "image/png" {
		t.Errorf("sniffed PNG: %q", info.ContentType)
	}
	for ct, want := range map[string]bool{"text/html; charset=utf-8": true, "application/x-javascript": true, "text/css": true,
		"image/svg+xml": true, "application/atom+xml": true, "image/png": false, "application/pdf": false, "text/plain": false, "": true} {
		if storage.IsActive(ct) != want {
			t.Errorf("IsActive(%q) = %v", ct, !want)
		}
	}
	// Copy and Move onto themselves check the file.
	if err := d.Copy(ctx, "missing", "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Copy(missing, missing) = %v", err)
	}
	if err := d.Move(ctx, "img", "img"); err != nil {
		t.Errorf("Move(img, img) = %v", err)
	}
	if _, err := d.DeleteAll(ctx, "users/1"); err == nil {
		t.Error("DeleteAll without a trailing /: no error")
	}
	// Temporary URLs need APP_KEY.
	nokey := storage.NewDisk("files", storage.NewMemoryBackend(), storage.BaseURL("https://example.com/files"))
	if _, err := nokey.TemporaryURL(ctx, "a", time.Minute); err == nil || !strings.Contains(err.Error(), "APP_KEY") {
		t.Errorf("TemporaryURL without a key = %v", err)
	}
}

func TestServeHardening(t *testing.T) {
	mem := storage.NewMemoryBackend()
	d := storage.NewDisk("public", mem, storage.Public())
	check(t, d.PutBytes(ctx, "x.js", []byte("alert(1)")))
	check(t, d.PutBytes(ctx, "data.txt", []byte("0123456789")))
	serve := func(p string, header ...string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/"+p, nil)
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		d.Handler().ServeHTTP(w, r)
		return w
	}
	if w := serve("x.js"); w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Content-Disposition") != "attachment; filename=x.js" {
		t.Errorf("JavaScript served as %v", w.Header())
	}
	if w := serve("data.txt", "Range", "bytes=0-1"); w.Code != http.StatusPartialContent || w.Body.String() != "01" {
		t.Errorf("one range: %d %q", w.Code, w.Body.String())
	}
	if w := serve("data.txt", "Range", "bytes=0-1,3-4"); w.Code != http.StatusOK || w.Body.String() != "0123456789" {
		t.Errorf("two ranges: %d %q", w.Code, w.Body.String())
	}
}

func TestLocalHardening(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	check(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o644))
	check(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "link")))
	b, err := storage.NewLocalBackend(dir)
	check(t, err)
	t.Cleanup(func() { _ = b.Close() })
	d := storage.NewDisk("local", b, storage.Public())
	if _, err := d.Get(ctx, "link"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a link out of the root = %v", err)
	}
	w := httptest.NewRecorder()
	d.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/link", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("serving a link out of the root: %d", w.Code)
	}
	// Same-size rewrites change the ETag.
	seen := map[string]bool{}
	for i := range 50 {
		check(t, d.PutBytes(ctx, "e.txt", []byte{byte('a' + i%26)}))
		info, err := d.Stat(ctx, "e.txt")
		check(t, err)
		if seen[info.ETag] {
			t.Fatalf("the ETag %s came back after a rewrite", info.ETag)
		}
		seen[info.ETag] = true
	}
}

func TestDiskSource(t *testing.T) {
	src := config.Map{"STORAGE_DRIVER": "s3", "STORAGE_S3_ACCESS_KEY": "k", "STORAGE_S3_SECRET_KEY": "s", "STORAGE_S3_REGION": "eu",
		"STORAGE_A_S3_BUCKET": "a", "STORAGE_B_S3_ENDPOINT": "https://other"}
	group := []string{"S3_REGION", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY"}
	a := storage.DiskSource(src, "a", group)
	if v, _ := a.Lookup("STORAGE_S3_ACCESS_KEY"); v != "k" {
		t.Errorf("a inherits the keys: %q", v)
	}
	if v, _ := a.Lookup("STORAGE_DRIVER"); v != "s3" {
		t.Errorf("a inherits the driver: %q", v)
	}
	b := storage.DiskSource(src, "b", group)
	if _, ok := b.Lookup("STORAGE_S3_ACCESS_KEY"); ok {
		t.Error("b, with an endpoint of its own, inherits the keys")
	}
	if _, ok := b.Lookup("STORAGE_S3_BUCKET"); ok {
		t.Error("b inherits a location")
	}
}

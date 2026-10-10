// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos/encryption"
)

// Disk is a named place for files, on a [Backend]: the app's local
// directory, an S3 bucket. Get the app's disks with [From].
type Disk struct {
	name    string
	backend Backend
	url     string // the files' base URL, without a trailing slash
	public  bool   // files are readable at url without a signature
	signer  *encryption.Encrypter
	log     *slog.Logger
	now     func() time.Time
}

// DiskOption configures a [Disk] made with [NewDisk].
type DiskOption func(*Disk)

// BaseURL sets the URL the disk's files are served at: a CDN, a public
// bucket, or the route of the disk's [Disk.Handler] ("https://example.com/files").
func BaseURL(u string) DiskOption { return func(d *Disk) { d.url = strings.TrimSuffix(u, "/") } }

// Public makes the files readable at the base URL by anyone, so
// [Disk.URL] works. Without it, only [Disk.TemporaryURL]'s signed URLs
// are.
func Public() DiskOption { return func(d *Disk) { d.public = true } }

// SignWith sets the encrypter that signs the temporary URLs the disk's
// handler serves (for backends without signed URLs of their own).
// [New] uses the app's APP_KEY.
func SignWith(e *encryption.Encrypter) DiskOption { return func(d *Disk) { d.signer = e } }

// WithLogger sets the logger of the disk's handler errors. Default
// slog.Default().
func WithLogger(l *slog.Logger) DiskOption { return func(d *Disk) { d.log = l } }

// NewDisk returns a disk named name on b.
func NewDisk(name string, b Backend, opts ...DiskOption) *Disk {
	d := &Disk{name: name, backend: b, log: slog.Default(), now: time.Now}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Name returns the disk's name.
func (d *Disk) Name() string { return d.name }

// Backend returns the disk's backend.
func (d *Disk) Backend() Backend { return d.backend }

// PutOption configures a Put.
type PutOption func(*PutOptions)

// ContentType sets the file's media type. Default: from the path's
// extension, or sniffed from the content. Local disks don't keep it:
// they give the extension's.
func ContentType(t string) PutOption { return func(o *PutOptions) { o.ContentType = t } }

// Put stores what r reads at path, replacing a file there:
//
//	err := disk.Put(ctx, "reports/2026-09.csv", r)
func (d *Disk) Put(ctx context.Context, p string, r io.Reader, opts ...PutOption) error {
	if err := CheckPath(p); err != nil {
		return err
	}
	var o PutOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.ContentType == "" {
		o.ContentType = mime.TypeByExtension(path.Ext(p))
	}
	if o.ContentType == "" {
		var err error
		if o.ContentType, r, err = sniff(r); err != nil {
			return err
		}
	} else if _, _, err := mime.ParseMediaType(o.ContentType); err != nil {
		return fmt.Errorf("storage: content type %q: %w", o.ContentType, err)
	}
	if err := d.backend.Put(ctx, p, r, o); err != nil {
		return fmt.Errorf("storage: put %s on %s: %w", p, d.name, err)
	}
	return nil
}

// sniff returns the media type of r's first bytes, and a reader of all of
// r: r itself, sought back, if it can seek (so backends still see its
// size). A type that would run in a browser is application/octet-stream:
// it takes an extension to store HTML.
func sniff(r io.Reader) (string, io.Reader, error) {
	if s, ok := r.(io.ReadSeeker); ok {
		start, err := s.Seek(0, io.SeekCurrent)
		if err == nil {
			_, err = s.Seek(start, io.SeekStart) // it can seek back
		}
		if err == nil {
			head := make([]byte, 512)
			n, err := io.ReadFull(s, head)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return "", nil, err
			}
			if _, err := s.Seek(start, io.SeekStart); err != nil {
				return "", nil, err
			}
			return safeType(http.DetectContentType(head[:n])), s, nil
		}
	}
	br := bufio.NewReaderSize(r, 512)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return "", nil, err
	}
	return safeType(http.DetectContentType(head)), br, nil
}

func safeType(ct string) string {
	if IsActive(ct) {
		return "application/octet-stream"
	}
	return ct
}

// PutBytes stores data at path.
func (d *Disk) PutBytes(ctx context.Context, p string, data []byte, opts ...PutOption) error {
	return d.Put(ctx, p, bytes.NewReader(data), opts...)
}

// PutUpload stores an uploaded file at path: a *multipart.FileHeader
// field of a handler's input. Choose the path yourself (a random name,
// the record's ID), not from the upload's filename, which the client
// chose; validate the file's type and size first (the mimes and max
// rules).
func (d *Disk) PutUpload(ctx context.Context, p string, fh *multipart.FileHeader, opts ...PutOption) error {
	if fh == nil {
		return errors.New("storage: no uploaded file")
	}
	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("storage: open the upload: %w", err)
	}
	defer f.Close()
	return d.Put(ctx, p, f, opts...)
}

// Open returns the file at path for reading, or an error matching
// ErrNotFound. Close it when done.
func (d *Disk) Open(ctx context.Context, p string) (File, error) {
	if err := CheckPath(p); err != nil {
		return nil, err
	}
	f, err := d.backend.Open(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s on %s: %w", p, d.name, err)
	}
	return f, nil
}

// Get returns the content of the file at path.
func (d *Disk) Get(ctx context.Context, p string) ([]byte, error) {
	f, err := d.Open(ctx, p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Stat returns the file's information, or an error matching ErrNotFound.
func (d *Disk) Stat(ctx context.Context, p string) (FileInfo, error) {
	if err := CheckPath(p); err != nil {
		return FileInfo{}, err
	}
	info, err := d.backend.Stat(ctx, p)
	if err != nil {
		return FileInfo{}, fmt.Errorf("storage: stat %s on %s: %w", p, d.name, err)
	}
	return info, nil
}

// Exists reports whether there is a file at path.
func (d *Disk) Exists(ctx context.Context, p string) (bool, error) {
	_, err := d.Stat(ctx, p)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Delete removes the files at paths; missing ones are no error.
func (d *Disk) Delete(ctx context.Context, paths ...string) error {
	for _, p := range paths {
		if err := CheckPath(p); err != nil {
			return err
		}
		if err := d.backend.Delete(ctx, p); err != nil {
			return fmt.Errorf("storage: delete %s on %s: %w", p, d.name, err)
		}
	}
	return nil
}

// DeleteAll removes the files in the directory prefix ("exports/",
// ending in "/") and its subdirectories, and returns how many it
// removed. Delete a whole disk's files by listing them.
func (d *Disk) DeleteAll(ctx context.Context, prefix string) (int, error) {
	if !strings.HasSuffix(prefix, "/") {
		return 0, fmt.Errorf("storage: DeleteAll(%q): the prefix must be a directory, ending in /", prefix)
	}
	var paths []string
	for info, err := range d.List(ctx, prefix) {
		if err != nil {
			return 0, err
		}
		paths = append(paths, info.Path)
	}
	for i, p := range paths {
		if err := d.backend.Delete(ctx, p); err != nil {
			return i, fmt.Errorf("storage: delete %s on %s: %w", p, d.name, err)
		}
	}
	return len(paths), nil
}

// List yields the files whose paths start with prefix ("" for all), in
// byte order of their paths:
//
//	for f, err := range disk.List(ctx, "exports/") { … }
func (d *Disk) List(ctx context.Context, prefix string) iter.Seq2[FileInfo, error] {
	if err := CheckPrefix(prefix); err != nil {
		return func(yield func(FileInfo, error) bool) { yield(FileInfo{}, err) }
	}
	return d.backend.List(ctx, prefix)
}

// Copy copies the file at src to dst, replacing a file there.
func (d *Disk) Copy(ctx context.Context, src, dst string) error {
	for _, p := range []string{src, dst} {
		if err := CheckPath(p); err != nil {
			return err
		}
	}
	if src == dst {
		_, err := d.Stat(ctx, src)
		return err
	}
	if err := d.backend.Copy(ctx, src, dst); err != nil {
		return fmt.Errorf("storage: copy %s to %s on %s: %w", src, dst, d.name, err)
	}
	return nil
}

// Move copies the file at src to dst, then deletes src.
func (d *Disk) Move(ctx context.Context, src, dst string) error {
	if src == dst {
		return d.Copy(ctx, src, dst)
	}
	if err := d.Copy(ctx, src, dst); err != nil {
		return err
	}
	return d.Delete(ctx, src)
}

// URL returns the permanent URL of the file at path on a public disk
// (STORAGE_PUBLIC=true, with STORAGE_URL): for files anyone may read.
// It doesn't check that the file exists.
func (d *Disk) URL(p string) (string, error) {
	if err := CheckPath(p); err != nil {
		return "", err
	}
	if d.url == "" {
		return "", ErrNoURL
	}
	if !d.public {
		return "", fmt.Errorf("storage: the disk %s isn't public (STORAGE_PUBLIC): use TemporaryURL", d.name)
	}
	return d.url + "/" + escapePath(p), nil
}

// TemporaryURL returns a URL that reads the file at path for ttl: the
// backend's own (S3's presigned URLs), or one signed with APP_KEY that
// the disk's [Disk.Handler] serves, at STORAGE_URL. It doesn't check
// that the file exists.
func (d *Disk) TemporaryURL(ctx context.Context, p string, ttl time.Duration) (string, error) {
	if err := CheckPath(p); err != nil {
		return "", err
	}
	if ttl <= 0 {
		return "", fmt.Errorf("storage: TemporaryURL(%s): the ttl must be positive", ttl)
	}
	if s, ok := d.backend.(URLSigner); ok {
		// The store checks it against its own clock: real time.
		return s.SignedURL(ctx, p, time.Now().Add(ttl))
	}
	expires := d.now().Add(ttl) // checked by Handler, on the app's clock
	if d.url == "" {
		return "", ErrNoURL
	}
	if d.signer == nil {
		return "", fmt.Errorf("storage: the disk %s signs temporary URLs with APP_KEY, which isn't set", d.name)
	}
	return d.url + "/" + escapePath(p) + "?token=" + d.token(p, expires), nil
}

// tokenContext separates the disk's tokens from other encrypted data.
const tokenContext = "anetos storage url"

// token returns the signed token for reading p until expires.
func (d *Disk) token(p string, expires time.Time) string {
	return d.signer.EncryptString(d.name+"\x00"+p+"\x00"+strconv.FormatInt(expires.Unix(), 10), tokenContext)
}

// checkToken reports whether token lets a request read p now.
func (d *Disk) checkToken(token, p string) bool {
	if d.signer == nil || token == "" {
		return false
	}
	plain, err := d.signer.DecryptString(token, tokenContext)
	if err != nil {
		return false
	}
	parts := strings.Split(plain, "\x00")
	if len(parts) != 3 || parts[0] != d.name || parts[1] != p {
		return false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	return err == nil && d.now().Unix() < exp
}

// escapePath escapes each segment of p for a URL path.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

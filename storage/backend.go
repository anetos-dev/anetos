// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Backend keeps files: a directory, a bucket, memory. Drivers implement
// it; apps use it through a [Disk]. storagetest is its conformance suite.
// Paths are checked by the Disk before they reach the backend (see
// [CheckPath]).
type Backend interface {
	// Put stores what r reads at path, replacing a file there. Readers
	// see the old file or the new one, never part of it. If r fails or
	// ctx ends, nothing is stored.
	Put(ctx context.Context, path string, r io.Reader, opts PutOptions) error
	// Open returns the file at path for reading, or ErrNotFound. Close
	// it when done.
	Open(ctx context.Context, path string) (File, error)
	// Stat returns the file's information, or ErrNotFound.
	Stat(ctx context.Context, path string) (FileInfo, error)
	// Delete removes the file at path; a missing file is no error.
	Delete(ctx context.Context, path string) error
	// List yields the files whose paths start with prefix ("" for all),
	// in byte order of their paths. A prefix ending in "/" lists a
	// directory, recursively; "avatars/1" lists avatars/1.png and
	// avatars/10.png. The FileInfos have a Path, Size and ModTime; the
	// ContentType and ETag may be empty (S3 doesn't list them).
	List(ctx context.Context, prefix string) iter.Seq2[FileInfo, error]
	// Copy copies the file at src to dst, replacing a file there, or
	// returns ErrNotFound.
	Copy(ctx context.Context, src, dst string) error
}

// TemporaryURLBackend is implemented by backends with URLs of their own
// for reading a file for a while (S3's presigned URLs), for
// [Disk.TemporaryURL]. Other disks sign URLs to their [Disk.Handler].
type TemporaryURLBackend interface {
	Backend
	// TemporaryURL returns a URL that reads the file at path until
	// expires.
	TemporaryURL(ctx context.Context, path string, expires time.Time) (string, error)
}

// URLSigner is the interface [TemporaryURLBackend] replaces: disks still
// use it until v0.6.
//
// Deprecated: Implement TemporaryURLBackend (rename the method SignedURL
// to TemporaryURL); URLSigner is removed in v0.6.
type URLSigner interface {
	// SignedURL returns a URL that reads the file at path until
	// expires.
	SignedURL(ctx context.Context, path string, expires time.Time) (string, error)
}

// PutOptions are a Put's options.
type PutOptions struct {
	// ContentType is the file's media type. The Disk sets it from the
	// path's extension when it isn't given. Backends that don't keep it
	// (local) give the extension's in FileInfo.
	ContentType string
}

// File is a file open for reading. Backends whose files can seek
// (local, memory, S3) return a File that is also an io.Seeker, which
// lets [Disk.Serve] answer range requests.
type File interface {
	io.ReadCloser
	// Info returns the file's information.
	Info() FileInfo
}

// FileInfo describes a file.
type FileInfo struct {
	// Path is the file's path on the disk.
	Path string
	// Size is its length in bytes. A File's Info may say -1 when the
	// length isn't known before reading (a GCS object stored
	// gzip-compressed, which is read decompressed).
	Size int64
	// ModTime is when it was last written.
	ModTime time.Time
	// ContentType is its media type.
	ContentType string
	// ETag identifies its content, for HTTP caching; it changes when
	// the content does. It may be empty.
	ETag string
}

// ErrNotFound is returned for a file that doesn't exist. It reports
// status 404 to the web package, so a handler can return it.
var ErrNotFound error = statusError{"storage: file not found", http.StatusNotFound}

// ErrNoURL is returned by [Disk.URL] and [Disk.TemporaryURL] when the
// disk has no URL for its files.
var ErrNoURL = errors.New("storage: the disk has no URL (set STORAGE_URL)")

// ErrInvalidPath is matched by the errors of paths [CheckPath] refuses.
// It reports status 404 to the web package: a path from a URL that
// isn't valid names no file.
var ErrInvalidPath error = statusError{"storage: invalid path", http.StatusNotFound}

type statusError struct {
	msg    string
	status int
}

func (e statusError) Error() string   { return e.msg }
func (e statusError) HTTPStatus() int { return e.status }

// MaxPath is the longest path, in bytes; MaxSegment the longest name
// between slashes (most file systems' limit).
const (
	MaxPath    = 1024
	MaxSegment = 255
)

// CheckPath reports whether path is a valid file path: slash-separated,
// relative, at most MaxPath bytes of UTF-8 (MaxSegment per name), without
// empty, "." or ".." segments, backslashes, control characters or
// bidirectional overrides, and no name starting with ".anetos-tmp-"
// (the local backend's temporary files). Paths aren't cleaned: one that
// isn't valid is an error, so a path built from user input can't escape
// the disk.
//
// Backends differ beyond it: a local disk can't hold both a file "a" and
// a file "a/b", and on a case-insensitive file system "A.txt" is
// "a.txt". Build paths from IDs and random names to stay clear of both.
func CheckPath(path string) error {
	if err := checkChars(path); err != nil {
		return err
	}
	if path == "" || strings.HasSuffix(path, "/") {
		return fmt.Errorf("%w %q: it must name a file", ErrInvalidPath, path)
	}
	return checkSegments(path, path)
}

// CheckPrefix is [CheckPath] for a List prefix: it may be empty, end
// with "/", or end in the middle of a name.
func CheckPrefix(prefix string) error {
	if err := checkChars(prefix); err != nil {
		return err
	}
	i := strings.LastIndexByte(prefix, '/')
	// The last segment may be partial ("avatars/1"), but not "." or "..".
	if last := prefix[i+1:]; last == "." || last == ".." {
		return fmt.Errorf("%w: prefix %q", ErrInvalidPath, prefix)
	}
	if i < 0 {
		return nil
	}
	return checkSegments(prefix, prefix[:i])
}

func checkChars(path string) error {
	if len(path) > MaxPath || !utf8.ValidString(path) {
		return fmt.Errorf("%w %q: at most %d bytes of UTF-8", ErrInvalidPath, path, MaxPath)
	}
	for _, r := range path {
		if r < 0x20 || r >= 0x7f && r <= 0x9f || r == '\\' || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return fmt.Errorf("%w %q: no control characters, bidirectional overrides or backslashes", ErrInvalidPath, path)
		}
	}
	return nil
}

func checkSegments(path, dir string) error {
	for seg := range strings.SplitSeq(dir, "/") {
		if seg == "" || seg == "." || seg == ".." || len(seg) > MaxSegment || strings.HasPrefix(seg, tmpPrefix) {
			return fmt.Errorf("%w %q: no leading /, //, . or .. segments, names over %d bytes, or names starting with %s",
				ErrInvalidPath, path, MaxSegment, tmpPrefix)
		}
	}
	return nil
}

// IsActive reports whether a browser would run content of media type
// ct (HTML, SVG and other XML, JavaScript, CSS, WebAssembly) rather than
// display it. (Serve the app's own scripts and styles as assets, with
// view.NewAssets, not from a disk.)
// Such files are served as downloads, so an uploaded file can't run on
// a site: [Disk.Serve] serves them as application/octet-stream
// attachments, and drivers whose stores serve files themselves (S3)
// store them with Content-Disposition: attachment.
func IsActive(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil && !errors.Is(err, mime.ErrInvalidMediaParameter) {
		return true // unparsable: assume the worst
	}
	switch mt {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "text/xml", "application/xml", "text/xsl",
		"text/css", "application/wasm",
		"text/javascript", "application/javascript", "application/x-javascript", "text/x-javascript",
		"text/ecmascript", "application/ecmascript", "application/x-ecmascript", "text/jscript", "text/livescript",
		"text/vbscript", "application/x-shockwave-flash", "multipart/x-mixed-replace":
		return true
	}
	return strings.HasSuffix(mt, "+xml") || strings.HasSuffix(mt, "+javascript")
}

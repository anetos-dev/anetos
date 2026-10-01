// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"mime"
	"os"
	"path"
	"slices"
	"strings"
)

// tmpPrefix starts the names of files being written: List skips them,
// and paths can't use them.
const tmpPrefix = ".anetos-tmp-"

// LocalBackend keeps files in a directory. It opens them through an
// os.Root, so neither a path nor a symbolic link can reach outside the
// directory. Content types come from the files' extensions.
type LocalBackend struct {
	root *os.Root
}

// NewLocalBackend returns a backend for dir, which it creates if
// needed. Close it when done.
func NewLocalBackend(dir string) (*LocalBackend, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", dir, err)
	}
	return &LocalBackend{root: root}, nil
}

// Close closes the directory.
func (l *LocalBackend) Close() error { return l.root.Close() }

// Dir returns the directory's name.
func (l *LocalBackend) Dir() string { return l.root.Name() }

// checkLocal checks a path given to the backend directly, not through a
// Disk.
func checkLocal(p string) error { return CheckPath(p) }

// Put implements [Backend]: it writes a temporary file next to path,
// syncs it, renames it over path and syncs the directory. Content types
// aren't kept: [FileInfo] gives the path's extension's. If the process
// dies during a Put, its temporary file (".anetos-tmp-…") stays; List
// doesn't show it.
func (l *LocalBackend) Put(ctx context.Context, p string, r io.Reader, _ PutOptions) (err error) {
	if err := checkLocal(p); err != nil {
		return err
	}
	dir := path.Dir(p)
	if dir != "." {
		if err := l.root.MkdirAll(dir, 0o755); err != nil {
			return localErr(err)
		}
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	tmp := path.Join(dir, tmpPrefix+hex.EncodeToString(b))
	f, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return localErr(err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = l.root.Remove(tmp)
		}
	}()
	if _, err := io.Copy(f, ctxReader{ctx, r}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := l.root.Rename(tmp, p); err != nil {
		return localErr(err)
	}
	if d, err := l.root.Open(dir); err == nil { // make the rename durable
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// localErr maps a missing file, or a symbolic link leading out of the
// directory, to ErrNotFound.
func localErr(err error) error {
	if errors.Is(err, fs.ErrNotExist) || err != nil && strings.Contains(err.Error(), "path escapes from parent") {
		return ErrNotFound
	}
	return err
}

// Open implements [Backend].
func (l *LocalBackend) Open(ctx context.Context, p string) (File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkLocal(p); err != nil {
		return nil, err
	}
	f, err := l.root.Open(p)
	if err != nil {
		return nil, localErr(err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrNotFound
	}
	return &localFile{File: f, info: localInfo(p, st)}, nil
}

type localFile struct {
	*os.File
	info FileInfo
}

func (f *localFile) Info() FileInfo { return f.info }

func localInfo(p string, st fs.FileInfo) FileInfo {
	// Put renames a new file into place, so its file number changes even
	// when the modification time (coarse on some file systems) and size
	// don't.
	return FileInfo{Path: p, Size: st.Size(), ModTime: st.ModTime().UTC(), ContentType: typeByExt(p),
		ETag: fmt.Sprintf(`"%x-%x-%x"`, fileID(st), st.ModTime().UnixNano(), st.Size())}
}

// typeByExt returns the media type of p's extension, or
// application/octet-stream.
func typeByExt(p string) string {
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// Stat implements [Backend].
func (l *LocalBackend) Stat(ctx context.Context, p string) (FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return FileInfo{}, err
	}
	if err := checkLocal(p); err != nil {
		return FileInfo{}, err
	}
	st, err := l.root.Stat(p)
	if err != nil {
		return FileInfo{}, localErr(err)
	}
	if !st.Mode().IsRegular() {
		return FileInfo{}, ErrNotFound
	}
	return localInfo(p, st), nil
}

// Delete implements [Backend]. Empty directories are left.
func (l *LocalBackend) Delete(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkLocal(p); err != nil {
		return err
	}
	st, err := l.root.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.IsDir() {
		return nil // not a file
	}
	if err := l.root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List implements [Backend]: it walks the directory of prefix, in the
// byte order of the paths (a directory sorts as its name followed by
// "/"), skipping symbolic links and files being written.
func (l *LocalBackend) List(ctx context.Context, prefix string) iter.Seq2[FileInfo, error] {
	return func(yield func(FileInfo, error) bool) {
		dir := "."
		if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
			dir = prefix[:i]
		}
		l.walk(ctx, dir, prefix, yield)
	}
}

// walk yields the files under dir whose paths start with prefix; it
// returns false when yield does.
func (l *LocalBackend) walk(ctx context.Context, dir, prefix string, yield func(FileInfo, error) bool) bool {
	if err := ctx.Err(); err != nil {
		yield(FileInfo{}, err)
		return false
	}
	f, err := l.root.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return yield(FileInfo{}, err)
	}
	st, err := f.Stat()
	if err != nil || !st.IsDir() {
		_ = f.Close()
		return err == nil || yield(FileInfo{}, err) // a file where the prefix's directory would be
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close() // before walking subdirectories
	if err != nil {
		return yield(FileInfo{}, err)
	}
	type entry struct {
		key, path string
		dir       bool
	}
	var list []entry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, tmpPrefix) || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		p := name
		if dir != "." {
			p = dir + "/" + name
		}
		switch {
		case e.IsDir():
			// Only directories that can hold matching paths.
			if !strings.HasPrefix(p+"/", prefix) && !strings.HasPrefix(prefix, p+"/") {
				continue
			}
			list = append(list, entry{p + "/", p, true})
		case e.Type().IsRegular() && strings.HasPrefix(p, prefix):
			list = append(list, entry{p, p, false})
		}
	}
	slices.SortFunc(list, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	for _, e := range list {
		if e.dir {
			if !l.walk(ctx, e.path, prefix, yield) {
				return false
			}
			continue
		}
		st, err := l.root.Lstat(e.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted meanwhile
		}
		if err != nil {
			if !yield(FileInfo{}, err) {
				return false
			}
			continue
		}
		if !yield(localInfo(e.path, st), nil) {
			return false
		}
	}
	return true
}

// Copy implements [Backend].
func (l *LocalBackend) Copy(ctx context.Context, src, dst string) error {
	f, err := l.Open(ctx, src)
	if err != nil {
		return err
	}
	defer f.Close()
	return l.Put(ctx, dst, f, PutOptions{})
}

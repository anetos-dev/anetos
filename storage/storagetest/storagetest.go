// SPDX-License-Identifier: Apache-2.0

// Package storagetest is the conformance suite of storage.Backend: every
// backend (local, memory, drivers/s3) runs it.
//
//	func TestConformance(t *testing.T) {
//		storagetest.Run(t, newBackend(t), storagetest.Features{ContentTypes: true})
//	}
package storagetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos/storage"
)

// Features says what the backend does beyond the contract.
type Features struct {
	// ContentTypes: the backend keeps the content type given to Put
	// (otherwise it gives the path's extension's).
	ContentTypes bool
	// TemporaryURLs: the backend implements storage.TemporaryURLBackend,
	// with URLs the test can fetch.
	TemporaryURLs bool
	// SignedURLs is TemporaryURLs.
	//
	// Deprecated: Use TemporaryURLs; SignedURLs is removed in v0.6.
	SignedURLs bool
	// Client fetches temporary URLs. Default http.DefaultClient.
	Client *http.Client
}

// Run runs the suite against b. Each test works under a prefix of its
// own, so b can hold other files.
func Run(t *testing.T, b storage.Backend, f Features) {
	tests := []struct {
		name string
		fn   func(t *testing.T, ctx context.Context, b storage.Backend, p string)
	}{
		{"PutOpen", testPutOpen},
		{"Replace", testReplace},
		{"NotFound", testNotFound},
		{"Paths", testPaths},
		{"List", testList},
		{"CopyDelete", testCopyDelete},
		{"FailedPut", testFailedPut},
		{"Concurrency", testConcurrency},
		{"Large", testLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fn(t, context.Background(), b, prefix())
		})
	}
	t.Run("ContentTypes", func(t *testing.T) { testContentTypes(t, context.Background(), b, prefix(), f) })
	if f.TemporaryURLs || f.SignedURLs {
		client := f.Client
		if client == nil {
			client = http.DefaultClient
		}
		t.Run("TemporaryURLs", func(t *testing.T) { testTemporaryURLs(t, context.Background(), b, prefix(), client) })
	}
}

func prefix() string {
	r := make([]byte, 6)
	_, _ = rand.Read(r)
	return "storagetest-" + hex.EncodeToString(r) + "/"
}

func put(t *testing.T, ctx context.Context, b storage.Backend, p, content string) {
	t.Helper()
	if err := b.Put(ctx, p, strings.NewReader(content), storage.PutOptions{ContentType: "text/plain; charset=utf-8"}); err != nil {
		t.Fatalf("Put(%s): %v", p, err)
	}
}

func get(t *testing.T, ctx context.Context, b storage.Backend, p string) (string, storage.FileInfo) {
	t.Helper()
	f, err := b.Open(ctx, p)
	if err != nil {
		t.Fatalf("Open(%s): %v", p, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(data), f.Info()
}

func testPutOpen(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	before := time.Now().Add(-time.Minute)
	put(t, ctx, b, p+"hello.txt", "hello, world")
	data, info := get(t, ctx, b, p+"hello.txt")
	if data != "hello, world" {
		t.Errorf("content %q", data)
	}
	st, err := b.Stat(ctx, p+"hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []storage.FileInfo{info, st} {
		if i.Path != p+"hello.txt" || i.Size != 12 || i.ModTime.Before(before) || i.ModTime.After(time.Now().Add(time.Minute)) {
			t.Errorf("info %+v", i)
		}
	}
	if info.ETag != st.ETag {
		t.Errorf("ETags differ: %q and %q", info.ETag, st.ETag)
	}
	// Binary and empty files.
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	if err := b.Put(ctx, p+"bin", bytes.NewReader(all), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if data, _ := get(t, ctx, b, p+"bin"); data != string(all) {
		t.Error("binary content changed")
	}
	put(t, ctx, b, p+"empty", "")
	if data, info := get(t, ctx, b, p+"empty"); data != "" || info.Size != 0 {
		t.Errorf("empty file: %q, %+v", data, info)
	}
	// A file that can seek seeks.
	f, err := b.Open(ctx, p+"hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if s, ok := f.(io.Seeker); ok {
		if _, err := s.Seek(7, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if rest, _ := io.ReadAll(f); string(rest) != "world" {
			t.Errorf("after Seek(7): %q", rest)
		}
	}
}

func testReplace(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	put(t, ctx, b, p+"f.txt", "one")
	_, first := get(t, ctx, b, p+"f.txt")
	put(t, ctx, b, p+"f.txt", "two, longer")
	data, second := get(t, ctx, b, p+"f.txt")
	if data != "two, longer" || second.Size != 11 {
		t.Errorf("after replacing: %q, %+v", data, second)
	}
	if first.ETag != "" && first.ETag == second.ETag {
		t.Errorf("the ETag %q didn't change with the content", first.ETag)
	}
}

func testNotFound(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	if _, err := b.Open(ctx, p+"missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Open = %v", err)
	}
	if _, err := b.Stat(ctx, p+"missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat = %v", err)
	}
	if err := b.Copy(ctx, p+"missing", p+"x"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Copy = %v", err)
	}
	if err := b.Delete(ctx, p+"missing"); err != nil {
		t.Errorf("Delete = %v", err)
	}
	// A "directory" isn't a file.
	put(t, ctx, b, p+"dir/file", "x")
	if _, err := b.Stat(ctx, p+"dir"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat of a directory = %v", err)
	}
	if _, err := b.Open(ctx, p+"dir"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Open of a directory = %v", err)
	}
	if err := b.Delete(ctx, p+"dir"); err != nil {
		t.Errorf("Delete of a directory = %v", err)
	}
	if _, err := b.Stat(ctx, p+"dir/file"); err != nil {
		t.Errorf("deleting the directory removed its file: %v", err)
	}
	// Canceled contexts.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.Open(canceled, p+"dir/file"); err == nil {
		t.Error("Open with a canceled context: no error")
	}
}

func testPaths(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	for _, name := range []string{"spaces in name.txt", "ünïcödé/日本語.txt", "a+b=c&d;e,f@g$h!.txt", "percent%20name", "deep/a/b/c/d/e/f/g.txt", "-dash", "x.y.z", "#hash?q"} {
		put(t, ctx, b, p+name, name)
		if data, info := get(t, ctx, b, p+name); data != name || info.Path != p+name {
			t.Errorf("%q: %q, %+v", name, data, info)
		}
	}
}

func list(t *testing.T, ctx context.Context, b storage.Backend, prefix string) []string {
	t.Helper()
	var out []string
	for info, err := range b.List(ctx, prefix) {
		if err != nil {
			t.Fatalf("List(%q): %v", prefix, err)
		}
		out = append(out, info.Path)
		if info.ModTime.IsZero() || info.Size < 0 {
			t.Errorf("List info %+v", info)
		}
	}
	return out
}

func testList(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	files := []string{"b.txt", "a/c/d.txt", "ab.txt", "a.txt", "a-b.txt", "a/b.txt", "a/ü.txt"}
	for _, f := range files {
		put(t, ctx, b, p+f, "x")
	}
	want := func(names ...string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = p + n
		}
		return out
	}
	for prefix, w := range map[string][]string{
		"":     want("a-b.txt", "a.txt", "a/b.txt", "a/c/d.txt", "a/ü.txt", "ab.txt", "b.txt"),
		"a/":   want("a/b.txt", "a/c/d.txt", "a/ü.txt"),
		"a":    want("a-b.txt", "a.txt", "a/b.txt", "a/c/d.txt", "a/ü.txt", "ab.txt"),
		"a/c":  want("a/c/d.txt"),
		"a/c/": want("a/c/d.txt"),
		"a/b":  want("a/b.txt"),
		"z":    nil,
		"a/z/": nil,
	} {
		if got := list(t, ctx, b, p+prefix); !slices.Equal(got, w) {
			t.Errorf("List(%q) =\n %q\nwant\n %q", prefix, got, w)
		}
	}
	// Stopping early.
	n := 0
	for _, err := range b.List(ctx, p) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		if n == 2 {
			break
		}
	}
	// Deleted files are gone from the list.
	if err := b.Delete(ctx, p+"a/c/d.txt"); err != nil {
		t.Fatal(err)
	}
	if got := list(t, ctx, b, p+"a/"); !slices.Equal(got, want("a/b.txt", "a/ü.txt")) {
		t.Errorf("after Delete: %q", got)
	}
}

func testCopyDelete(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	put(t, ctx, b, p+"src.txt", "content")
	if err := b.Copy(ctx, p+"src.txt", p+"copies/dst.txt"); err != nil {
		t.Fatal(err)
	}
	if data, info := get(t, ctx, b, p+"copies/dst.txt"); data != "content" || info.Path != p+"copies/dst.txt" {
		t.Errorf("copy: %q, %+v", data, info)
	}
	put(t, ctx, b, p+"other.txt", "other")
	if err := b.Copy(ctx, p+"src.txt", p+"other.txt"); err != nil {
		t.Fatal(err)
	}
	if data, _ := get(t, ctx, b, p+"other.txt"); data != "content" {
		t.Errorf("copy over a file: %q", data)
	}
	if err := b.Delete(ctx, p+"src.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(ctx, p+"src.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat after Delete = %v", err)
	}
	if data, _ := get(t, ctx, b, p+"copies/dst.txt"); data != "content" {
		t.Errorf("deleting the source changed the copy: %q", data)
	}
}

// failing reads some bytes, then fails.
type failing struct{ n int }

func (f *failing) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("the upload broke")
	}
	n := min(len(p), f.n)
	for i := range n {
		p[i] = 'x'
	}
	f.n -= n
	return n, nil
}

func testFailedPut(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	put(t, ctx, b, p+"f.txt", "old")
	if err := b.Put(ctx, p+"f.txt", &failing{n: 100_000}, storage.PutOptions{}); err == nil {
		t.Error("a Put whose reader failed: no error")
	}
	if data, _ := get(t, ctx, b, p+"f.txt"); data != "old" {
		t.Errorf("a failed Put changed the file: %d bytes", len(data))
	}
	if err := b.Put(ctx, p+"new.txt", &failing{n: 10}, storage.PutOptions{}); err == nil {
		t.Error("a Put whose reader failed: no error")
	}
	if _, err := b.Stat(ctx, p+"new.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a failed Put stored a file: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := b.Put(canceled, p+"canceled.txt", strings.NewReader("x"), storage.PutOptions{}); err == nil {
		t.Error("Put with a canceled context: no error")
	}
	if _, err := b.Stat(ctx, p+"canceled.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a canceled Put stored a file: %v", err)
	}
	if got := list(t, ctx, b, p); !slices.Equal(got, []string{p + "f.txt"}) {
		t.Errorf("files after failed Puts: %q", got)
	}
}

func testConcurrency(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	const n = 8
	contents := make([]string, n)
	for i := range contents {
		contents[i] = strings.Repeat(fmt.Sprint(i), 64<<10)
	}
	put(t, ctx, b, p+"shared", contents[0])
	var wg sync.WaitGroup
	errs := make(chan error, 4*n)
	for i := range n {
		wg.Go(func() {
			if err := b.Put(ctx, p+"shared", strings.NewReader(contents[i]), storage.PutOptions{}); err != nil {
				errs <- err
			}
		})
		wg.Go(func() {
			f, err := b.Open(ctx, p+"shared")
			if err != nil {
				errs <- err
				return
			}
			defer f.Close()
			data, err := io.ReadAll(f)
			if err != nil {
				errs <- err
				return
			}
			if !slices.Contains(contents, string(data)) {
				errs <- fmt.Errorf("a reader saw %d bytes of no single Put", len(data))
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if data, _ := get(t, ctx, b, p+"shared"); !slices.Contains(contents, data) {
		t.Error("the final content is no single Put's")
	}
}

// onlyReader hides everything but Read.
type onlyReader struct{ r io.Reader }

func (o onlyReader) Read(p []byte) (int, error) { return o.r.Read(p) }

func testLarge(t *testing.T, ctx context.Context, b storage.Backend, p string) {
	const size = 20 << 20 // over S3 drivers' part sizes
	data := make([]byte, size)
	_, _ = rand.Read(data)
	for name, r := range map[string]io.Reader{
		"seekable":     bytes.NewReader(data),
		"not seekable": onlyReader{bytes.NewReader(data)}, // unknown size
	} {
		if err := b.Put(ctx, p+"large.bin", r, storage.PutOptions{ContentType: "application/octet-stream"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, info := get(t, ctx, b, p+"large.bin")
		if info.Size != size || got != string(data) {
			t.Errorf("%s: %d bytes back, info %+v", name, len(got), info)
		}
	}
	// A reader already partly read: only the rest is stored.
	r := bytes.NewReader(data)
	if _, err := r.Seek(1<<20, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, p+"rest.bin", r, storage.PutOptions{ContentType: "application/octet-stream"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := get(t, ctx, b, p+"rest.bin"); got != string(data[1<<20:]) {
		t.Errorf("a partly read reader: %d bytes stored, want the %d after the first MiB", len(got), size-1<<20)
	}
}

func testContentTypes(t *testing.T, ctx context.Context, b storage.Backend, p string, f Features) {
	if err := b.Put(ctx, p+"photo.png", strings.NewReader("x"), storage.PutOptions{ContentType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, p+"data.json", strings.NewReader("{}"), storage.PutOptions{ContentType: "text/plain; charset=utf-8"}); err != nil {
		t.Fatal(err)
	}
	st, err := b.Stat(ctx, p+"photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if st.ContentType != "image/png" {
		t.Errorf("photo.png: %q", st.ContentType)
	}
	st, err = b.Stat(ctx, p+"data.json")
	if err != nil {
		t.Fatal(err)
	}
	want := "application/json"
	if f.ContentTypes {
		want = "text/plain; charset=utf-8"
	}
	if st.ContentType != want {
		t.Errorf("data.json: %q, want %q", st.ContentType, want)
	}
}

func testTemporaryURLs(t *testing.T, ctx context.Context, b storage.Backend, p string, client *http.Client) {
	urlOf := func(path string, expires time.Time) (string, error) {
		switch s := b.(type) {
		case storage.TemporaryURLBackend:
			return s.TemporaryURL(ctx, path, expires)
		case storage.URLSigner: //nolint:staticcheck // the interface before v0.5, until v0.6
			return s.SignedURL(ctx, path, expires)
		}
		t.Fatal("the backend isn't a storage.TemporaryURLBackend")
		return "", nil
	}
	put(t, ctx, b, p+"signed file.txt", "secret")
	u, err := urlOf(p+"signed file.txt", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "secret" {
		t.Errorf("GET the signed URL: %d %q", resp.StatusCode, body)
	}
}

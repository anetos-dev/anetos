// SPDX-License-Identifier: Apache-2.0

package s3_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/s3"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/storage/storagetest"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fake starts an in-process S3 server with the bucket "test", over TLS
// (over HTTP, minio-go signs uploads in chunks, which the fake doesn't
// read), and returns its URL and an option trusting it.
func fake(t *testing.T) (string, *http.Client) {
	t.Helper()
	backend := s3mem.New()
	check(t, backend.CreateBucket("test"))
	srv := httptest.NewTLSServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	return srv.URL, srv.Client()
}

func TestConformanceFake(t *testing.T) {
	endpoint, client := fake(t)
	b, err := s3.New(s3.Config{Bucket: "test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "key", SecretKey: "secret",
		PathStyle: true, Prefix: "disk/"}, s3.Transport(client.Transport))
	check(t, err)
	storagetest.Run(t, b, storagetest.Features{ContentTypes: true, SignedURLs: true, Client: client})
}

// TestConformance runs against a real S3-compatible server when
// ANETOS_TEST_S3_URL is set: http://ACCESS:SECRET@host:port/bucket
// (path-style), with the bucket existing.
func TestConformance(t *testing.T) {
	raw := os.Getenv("ANETOS_TEST_S3_URL")
	if raw == "" {
		t.Skip("ANETOS_TEST_S3_URL not set")
	}
	u, err := url.Parse(raw)
	check(t, err)
	secret, _ := u.User.Password()
	b, err := s3.New(s3.Config{Bucket: strings.Trim(u.Path, "/"), Region: "us-east-1", Endpoint: u.Scheme + "://" + u.Host,
		AccessKey: u.User.Username(), SecretKey: anetos.Secret(secret), PathStyle: true, Prefix: "anetos-test/"})
	check(t, err)
	storagetest.Run(t, b, storagetest.Features{ContentTypes: true, SignedURLs: true})
	var paths []string
	for info, err := range b.List(context.Background(), "storagetest-") {
		check(t, err)
		paths = append(paths, info.Path)
	}
	check(t, storage.NewDisk("t", b).Delete(context.Background(), paths...))
}

func TestNew(t *testing.T) {
	for name, c := range map[string]s3.Config{
		"no bucket":     {},
		"bad endpoint":  {Bucket: "b", Endpoint: "ftp://x"},
		"endpoint path": {Bucket: "b", Endpoint: "https://x/path"},
		"half the keys": {Bucket: "b", AccessKey: "k"},
		"bad prefix":    {Bucket: "b", Prefix: "../x"},
	} {
		if _, err := s3.New(c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	b, err := s3.New(s3.Config{Bucket: "b", Region: "us-east-1"}) // AWS, credential chain
	check(t, err)
	if b.Client() == nil {
		t.Error("no client")
	}
	if _, err := b.SignedURL(context.Background(), "x", time.Now().Add(8*24*time.Hour)); err == nil {
		t.Error("an 8-day signed URL: no error")
	}
}

func TestForApp(t *testing.T) {
	endpoint, client := fake(t)
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(),
		"STORAGE_DRIVER": "s3", "STORAGE_S3_BUCKET": "test", "STORAGE_S3_ENDPOINT": endpoint, "STORAGE_S3_PATH_STYLE": "true",
		"STORAGE_S3_ACCESS_KEY": "key", "STORAGE_S3_SECRET_KEY": "secret",
		"STORAGE_DISKS": "avatars", "STORAGE_AVATARS_S3_BUCKET": "test", "STORAGE_AVATARS_S3_PREFIX": "avatars/"}
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	if _, err := storage.ForApp(app); err == nil || !strings.Contains(err.Error(), "s3.Driver()") {
		t.Errorf("ForApp without the driver = %v", err)
	}
	app2, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app2.Close() })
	_, err = storage.ForApp(app2, s3.Driver(s3.Transport(client.Transport)))
	check(t, err)
	ctx := app2.Context(context.Background())
	def, err := storage.From(ctx)
	check(t, err)
	avatars, err := storage.From(ctx, "avatars")
	check(t, err)
	check(t, avatars.PutBytes(ctx, "1.png", []byte("png")))
	// The avatars disk inherits the endpoint and keys, with its prefix.
	data, err := def.Get(ctx, "avatars/1.png")
	check(t, err)
	if string(data) != "png" {
		t.Errorf("data %q", data)
	}
	u, err := avatars.TemporaryURL(ctx, "1.png", time.Minute)
	check(t, err)
	if !strings.HasPrefix(u, endpoint+"/test/avatars/1.png?") || !strings.Contains(u, "X-Amz-Signature=") {
		t.Errorf("TemporaryURL = %s", u)
	}
	// Missing bucket setting for a named disk.
	bad := maps.Clone(src)
	delete(bad, "STORAGE_AVATARS_S3_BUCKET")
	app3, err := anetos.New(anetos.WithSource(bad), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app3.Close() })
	if _, err := storage.ForApp(app3, s3.Driver(s3.Transport(client.Transport))); err == nil || !strings.Contains(err.Error(), "STORAGE_S3_BUCKET") {
		t.Errorf("ForApp without the avatars bucket = %v", err)
	}
}

// Breaking out of a List loop doesn't leave minio-go's goroutine behind.
func TestListBreak(t *testing.T) {
	endpoint, client := fake(t)
	b, err := s3.New(s3.Config{Bucket: "test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "key", SecretKey: "secret", PathStyle: true}, s3.Transport(client.Transport))
	check(t, err)
	ctx := context.Background()
	for i := range 5 {
		check(t, b.Put(ctx, fmt.Sprintf("l/%d", i), strings.NewReader("x"), storage.PutOptions{}))
	}
	before := runtime.NumGoroutine()
	for range 20 {
		for _, err := range b.List(ctx, "l/") {
			check(t, err)
			break
		}
	}
	time.Sleep(100 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+3 {
		t.Errorf("%d goroutines before, %d after 20 broken loops", before, after)
	}
}

func TestMissingBucket(t *testing.T) {
	endpoint, client := fake(t)
	b, err := s3.New(s3.Config{Bucket: "nope", Region: "us-east-1", Endpoint: endpoint, AccessKey: "key", SecretKey: "secret", PathStyle: true}, s3.Transport(client.Transport))
	check(t, err)
	if _, err := b.Stat(context.Background(), "x"); err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat on a missing bucket = %v", err)
	}
}

// Active content is stored as an attachment: the bucket serves it as is.
func TestActiveContent(t *testing.T) {
	endpoint, client := fake(t)
	b, err := s3.New(s3.Config{Bucket: "test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "key", SecretKey: "secret", PathStyle: true}, s3.Transport(client.Transport))
	check(t, err)
	ctx := context.Background()
	check(t, storage.NewDisk("d", b).PutBytes(ctx, "page.html", []byte("<script>alert(1)</script>")))
	u, err := b.SignedURL(ctx, "page.html", time.Now().Add(time.Minute))
	check(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	check(t, err)
	resp, err := client.Do(req)
	check(t, err)
	defer resp.Body.Close()
	if resp.Header.Get("Content-Disposition") != "attachment" {
		t.Errorf("headers %v", resp.Header)
	}
}

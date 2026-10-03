// SPDX-License-Identifier: Apache-2.0

package gcs_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"maps"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsouza/fake-gcs-server/fakestorage"
	"google.golang.org/api/option"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/gcs"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/storage/storagetest"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fake starts an in-process GCS server with the bucket "test", and
// returns the option that sends the client's requests to it.
func fake(t *testing.T) (*fakestorage.Server, gcs.Option) {
	t.Helper()
	srv, err := fakestorage.NewServerWithOptions(fakestorage.Options{NoListener: true, Writer: io.Discard})
	check(t, err)
	srv.CreateBucketWithOpts(fakestorage.CreateBucketOpts{Name: "test"})
	t.Cleanup(srv.Stop)
	return srv, gcs.ClientOptions(option.WithHTTPClient(srv.HTTPClient()))
}

// serviceAccount returns a service account key with a new private key,
// which signs URLs without the network.
func serviceAccount(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	check(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	check(t, err)
	j, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "anetos-test",
		"private_key_id": "1",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   "files@anetos-test.iam.gserviceaccount.com",
		"client_id":      "1",
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	check(t, err)
	return string(j)
}

func TestConformanceFake(t *testing.T) {
	srv, opt := fake(t)
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "test", Prefix: "disk/", Credentials: anetos.Secret(serviceAccount(t))}, opt)
	check(t, err)
	t.Cleanup(func() { _ = b.Close() })
	storagetest.Run(t, b, storagetest.Features{ContentTypes: true, SignedURLs: true, Client: srv.HTTPClient()})
}

// TestConformance runs against Cloud Storage when ANETOS_TEST_GCS_BUCKET
// names a bucket the Application Default Credentials can write (and,
// for the signed URLs, sign for). It works under anetos-test/.
func TestConformance(t *testing.T) {
	bucket := os.Getenv("ANETOS_TEST_GCS_BUCKET")
	if bucket == "" {
		t.Skip("ANETOS_TEST_GCS_BUCKET not set")
	}
	ctx := context.Background()
	b, err := gcs.New(ctx, gcs.Config{Bucket: bucket, Prefix: "anetos-test/", Signer: os.Getenv("ANETOS_TEST_GCS_SIGNER")})
	check(t, err)
	t.Cleanup(func() { _ = b.Close() })
	storagetest.Run(t, b, storagetest.Features{ContentTypes: true, SignedURLs: true})
	var paths []string
	for info, err := range b.List(ctx, "storagetest-") {
		check(t, err)
		paths = append(paths, info.Path)
	}
	check(t, storage.NewDisk("t", b).Delete(ctx, paths...))
}

func TestNew(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]gcs.Config{
		"no bucket":        {},
		"bad prefix":       {Bucket: "b", Prefix: "../x"},
		"prefix without /": {Bucket: "b", Prefix: "x"},
		"both credentials": {Bucket: "b", CredentialsFile: "key.json", Credentials: "{}"},
	} {
		if _, err := gcs.New(ctx, c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_, opt := fake(t)
	b, err := gcs.New(ctx, gcs.Config{Bucket: "test"}, opt)
	check(t, err)
	defer b.Close()
	if b.Client() == nil || b.Bucket() == nil {
		t.Error("no client")
	}
	if _, err := b.SignedURL(ctx, "x", time.Now().Add(8*24*time.Hour)); err == nil {
		t.Error("an 8-day signed URL: no error")
	}
}

func TestSignedURL(t *testing.T) {
	_, opt := fake(t)
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "test", Prefix: "p/", Credentials: anetos.Secret(serviceAccount(t))}, opt)
	check(t, err)
	defer b.Close()
	u, err := b.SignedURL(context.Background(), "a b.txt", time.Now().Add(time.Hour))
	check(t, err)
	for _, want := range []string{"https://storage.googleapis.com/test/p/a%20b.txt?", "X-Goog-Algorithm=GOOG4-RSA-SHA256",
		"X-Goog-Credential=files%40anetos-test.iam.gserviceaccount.com", "X-Goog-Expires=3", "X-Goog-Signature="} {
		if !strings.Contains(u, want) {
			t.Errorf("SignedURL = %s, lacks %s", u, want)
		}
	}
}

func TestForApp(t *testing.T) {
	_, opt := fake(t)
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(),
		"STORAGE_DRIVER": "gcs", "STORAGE_GCS_BUCKET": "test", "STORAGE_GCS_CREDENTIALS": serviceAccount(t),
		"STORAGE_DISKS": "avatars", "STORAGE_AVATARS_GCS_BUCKET": "test", "STORAGE_AVATARS_GCS_PREFIX": "avatars/"}
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	if _, err := storage.ForApp(app); err == nil || !strings.Contains(err.Error(), "gcs.Driver()") {
		t.Errorf("ForApp without the driver = %v", err)
	}
	app2, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app2.Close() })
	_, err = storage.ForApp(app2, gcs.Driver(opt))
	check(t, err)
	ctx := app2.Context(context.Background())
	def, err := storage.From(ctx)
	check(t, err)
	avatars, err := storage.From(ctx, "avatars")
	check(t, err)
	check(t, avatars.PutBytes(ctx, "1.png", []byte("png")))
	data, err := def.Get(ctx, "avatars/1.png")
	check(t, err)
	if string(data) != "png" {
		t.Errorf("data %q", data)
	}
	// The avatars disk inherits the credentials: it signs URLs.
	u, err := avatars.TemporaryURL(ctx, "1.png", time.Minute)
	check(t, err)
	if !strings.HasPrefix(u, "https://storage.googleapis.com/test/avatars/1.png?") {
		t.Errorf("TemporaryURL = %s", u)
	}
	bad := maps.Clone(src)
	delete(bad, "STORAGE_AVATARS_GCS_BUCKET")
	app3, err := anetos.New(anetos.WithSource(bad), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app3.Close() })
	if _, err := storage.ForApp(app3, gcs.Driver(opt)); err == nil || !strings.Contains(err.Error(), "STORAGE_GCS_BUCKET") {
		t.Errorf("ForApp without the avatars bucket = %v", err)
	}
}

func TestMissingBucket(t *testing.T) {
	_, opt := fake(t)
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "nope"}, opt)
	check(t, err)
	defer b.Close()
	if _, err := b.Stat(context.Background(), "x"); err == nil || errors.Is(err, storage.ErrNotFound) || !strings.Contains(err.Error(), "the bucket nope doesn't exist") {
		t.Errorf("Stat on a missing bucket = %v", err)
	}
}

// Active content is stored as an attachment: the bucket serves it as is.
func TestActiveContent(t *testing.T) {
	srv, opt := fake(t)
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "test"}, opt)
	check(t, err)
	defer b.Close()
	ctx := context.Background()
	check(t, storage.NewDisk("d", b).PutBytes(ctx, "page.html", []byte("<script>alert(1)</script>")))
	obj, err := srv.GetObject("test", "page.html")
	check(t, err)
	if obj.ContentDisposition != "attachment" || obj.ContentType != "text/html; charset=utf-8" {
		t.Errorf("stored with %q, %q", obj.ContentDisposition, obj.ContentType)
	}
}

// A file reads the generation it opened: replacing the object makes a
// later ranged read fail rather than mix two versions.
func TestOpenGeneration(t *testing.T) {
	_, opt := fake(t)
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "test"}, opt)
	check(t, err)
	defer b.Close()
	ctx := context.Background()
	check(t, b.Put(ctx, "g.txt", strings.NewReader("0123456789"), storage.PutOptions{}))
	f, err := b.Open(ctx, "g.txt")
	check(t, err)
	defer f.Close()
	buf := make([]byte, 3)
	_, err = io.ReadFull(f, buf)
	check(t, err)
	check(t, b.Put(ctx, "g.txt", strings.NewReader("abcdefghij"), storage.PutOptions{}))
	if _, err := f.(io.Seeker).Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(f); err == nil || !strings.Contains(err.Error(), "changed since it was opened") {
		t.Errorf("reading a replaced object = %v", err)
	}
}

func TestCredentialChecks(t *testing.T) {
	ctx := context.Background()
	user := `{"type": "authorized_user", "client_id": "x", "client_secret": "y", "refresh_token": "z"}`
	if _, err := gcs.New(ctx, gcs.Config{Bucket: "b", Credentials: anetos.Secret(user)}); err == nil || !strings.Contains(err.Error(), `"authorized_user", not a service account key`) {
		t.Errorf("an authorized_user JSON = %v", err)
	}
	if _, err := gcs.New(ctx, gcs.Config{Bucket: "b", Credentials: "nope"}); err == nil {
		t.Error("not JSON: no error")
	}
	if _, err := gcs.New(ctx, gcs.Config{Bucket: "b", Credentials: anetos.Secret(serviceAccount(t)), Signer: "other@x.iam.gserviceaccount.com"}); err == nil || !strings.Contains(err.Error(), "STORAGE_GCS_SIGNER is for credentials without a key") {
		t.Errorf("a key and a signer = %v", err)
	}
	if _, err := gcs.New(ctx, gcs.Config{Bucket: "b", CredentialsFile: "/nonexistent/key.json"}); err == nil {
		t.Error("a missing key file: no error")
	}

	// No key: URLs are signed by the IAM API as an account, which
	// must be found.
	_, opt := fake(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", t.TempDir()+"/missing.json")
	b, err := gcs.New(ctx, gcs.Config{Bucket: "test"}, opt)
	check(t, err)
	defer b.Close()
	if _, err := b.SignedURL(ctx, "x", time.Now().Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "no credentials to sign URLs") {
		t.Errorf("SignedURL without credentials = %v", err)
	}
}

// A small upload doesn't take an 8 MB buffer.
func TestSmallPutMemory(t *testing.T) {
	_, opt := fake(t)
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "test"}, opt)
	check(t, err)
	defer b.Close()
	ctx := context.Background()
	check(t, b.Put(ctx, "warm", strings.NewReader("x"), storage.PutOptions{}))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range 4 {
		check(t, b.Put(ctx, "small", strings.NewReader("hello"), storage.PutOptions{}))
		check(t, b.Put(ctx, "pipe", io.MultiReader(strings.NewReader("hello")), storage.PutOptions{ContentType: "text/plain"}))
		_ = i
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / 8; per > 2<<20 {
		t.Errorf("%d bytes allocated per small Put", per)
	}
}

// An object stored gzip-compressed (gsutil -z) is read decompressed,
// without a known size.
func TestGzipObject(t *testing.T) {
	srv, opt := fake(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("id,name\n1,Ada\n"))
	check(t, zw.Close())
	srv.CreateObject(fakestorage.Object{
		ObjectAttrs: fakestorage.ObjectAttrs{BucketName: "test", Name: "data.csv", ContentType: "text/csv", ContentEncoding: "gzip"},
		Content:     gz.Bytes(),
	})
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "test"}, opt)
	check(t, err)
	defer b.Close()
	data, err := storage.NewDisk("d", b).Get(context.Background(), "data.csv")
	check(t, err)
	if string(data) != "id,name\n1,Ada\n" {
		t.Errorf("Get = %q", data)
	}
}

// With STORAGE_EMULATOR_HOST (fake-gcs-server over HTTP), files and
// signed URLs work, the URLs on the emulator.
func TestEmulator(t *testing.T) {
	srv, err := fakestorage.NewServerWithOptions(fakestorage.Options{Scheme: "http", Host: "127.0.0.1", Port: 0, Writer: io.Discard})
	check(t, err)
	t.Cleanup(srv.Stop)
	srv.CreateBucketWithOpts(fakestorage.CreateBucketOpts{Name: "test"})
	t.Setenv("STORAGE_EMULATOR_HOST", strings.TrimPrefix(srv.URL(), "http://"))
	b, err := gcs.New(context.Background(), gcs.Config{Bucket: "test", Credentials: anetos.Secret(serviceAccount(t))})
	check(t, err)
	defer b.Close()
	ctx := context.Background()
	check(t, b.Put(ctx, "e.txt", strings.NewReader("emulated"), storage.PutOptions{}))
	u, err := b.SignedURL(ctx, "e.txt", time.Now().Add(time.Minute))
	check(t, err)
	if !strings.HasPrefix(u, srv.URL()+"/test/e.txt?") {
		t.Errorf("SignedURL = %s", u)
	}
}

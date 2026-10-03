// SPDX-License-Identifier: Apache-2.0

// Package gcs is the storage package's backend for Google Cloud Storage,
// on the official client (cloud.google.com/go/storage):
//
//	st, err := storage.ForApp(app, gcs.Driver())
//
// Settings (STORAGE_<NAME>_GCS_* for a named disk): STORAGE_DRIVER=gcs,
// STORAGE_GCS_BUCKET, STORAGE_GCS_PREFIX (a prefix for the disk's
// objects in the bucket, ending in "/"), STORAGE_GCS_CREDENTIALS_FILE or
// STORAGE_GCS_CREDENTIALS (a service account key, its file or its JSON;
// without them, Application Default Credentials: the service account of
// Cloud Run, GKE or Compute Engine, GOOGLE_APPLICATION_CREDENTIALS, or
// gcloud auth application-default login), and STORAGE_GCS_SIGNER (the
// service account that signs temporary URLs when the credentials have
// no key of their own; default: the credentials' account). A named disk
// that sets none of the credentials and signer takes the default
// disk's; one that sets any of them takes none.
//
// With STORAGE_EMULATOR_HOST set (fake-gcs-server, the Firebase
// emulator), the client talks to the emulator without credentials; a
// service account key still signs temporary URLs.
package gcs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cloud.google.com/go/auth/credentials"
	"cloud.google.com/go/compute/metadata"
	gcstorage "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/storage"
)

// Config is the backend's settings.
type Config struct {
	// Bucket is the bucket's name. STORAGE_GCS_BUCKET, required.
	Bucket string `env:"STORAGE_GCS_BUCKET"`
	// Prefix starts the disk's object names in the bucket ("uploads/"),
	// so disks can share a bucket. STORAGE_GCS_PREFIX.
	Prefix string `env:"STORAGE_GCS_PREFIX"`
	// CredentialsFile is the path of a service account key (JSON).
	// STORAGE_GCS_CREDENTIALS_FILE; default Application Default
	// Credentials, which also take other kinds of credentials
	// (workload identity federation, impersonation) through
	// GOOGLE_APPLICATION_CREDENTIALS.
	CredentialsFile string `env:"STORAGE_GCS_CREDENTIALS_FILE"`
	// Credentials is a service account key's JSON, for platforms that
	// keep secrets in the environment. STORAGE_GCS_CREDENTIALS.
	Credentials anetos.Secret `env:"STORAGE_GCS_CREDENTIALS"`
	// Signer is the email of the service account that signs temporary
	// URLs, through the IAM API (its signBlob method), when the
	// credentials have no private key: Cloud Run's or a user's
	// credentials. STORAGE_GCS_SIGNER; default the credentials' account
	// (on Google Cloud, the instance's service account). A key signs
	// for its own account: set one or the other.
	Signer string `env:"STORAGE_GCS_SIGNER"`
}

// Backend is a bucket, or a prefix in one.
type Backend struct {
	client   *gcstorage.Client
	bucket   *gcstorage.BucketHandle
	name     string
	prefix   string
	bucketOK atomic.Bool // the bucket was seen to exist (or can't be checked)
	insecure bool        // an emulator over plain HTTP: signed URLs too

	signMu sync.Mutex
	signer signer // how temporary URLs are signed, found on the first one
}

// signer signs URLs: with a private key, or by the IAM API.
type signer struct {
	email string
	key   []byte                  // a service account's private key (PEM)
	iam   *iamcredentials.Service // without a key
	ready bool
}

// Option configures a [Backend].
type Option func(*options)

type options struct{ client []option.ClientOption }

// ClientOptions adds options to the client's (an HTTP client with a
// proxy or custom TLS roots, an endpoint, a quota project).
func ClientOptions(opts ...option.ClientOption) Option {
	return func(o *options) { o.client = append(o.client, opts...) }
}

// serviceAccountKey is what the backend reads of a key's JSON.
type serviceAccountKey struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// New returns a backend for c. It reads the credentials but doesn't
// contact the bucket; ctx is only for that.
func New(ctx context.Context, c Config, opts ...Option) (*Backend, error) {
	if c.Bucket == "" {
		return nil, errors.New("gcs: STORAGE_GCS_BUCKET is required")
	}
	if c.Prefix != "" && (storage.CheckPrefix(c.Prefix) != nil || !strings.HasSuffix(c.Prefix, "/")) {
		return nil, fmt.Errorf("gcs: invalid STORAGE_GCS_PREFIX %q: a directory, ending in / (uploads/)", c.Prefix)
	}
	if c.CredentialsFile != "" && c.Credentials != "" {
		return nil, errors.New("gcs: set STORAGE_GCS_CREDENTIALS_FILE or STORAGE_GCS_CREDENTIALS, not both")
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	b := &Backend{name: c.Bucket, prefix: c.Prefix, signer: signer{email: c.Signer}}
	keyJSON := []byte(c.Credentials)
	if c.CredentialsFile != "" {
		data, err := os.ReadFile(c.CredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("gcs: STORAGE_GCS_CREDENTIALS_FILE: %w", err)
		}
		keyJSON = data
	}
	var copts []option.ClientOption
	if len(keyJSON) > 0 {
		var key serviceAccountKey
		if err := json.Unmarshal(keyJSON, &key); err != nil {
			return nil, fmt.Errorf("gcs: the credentials aren't a service account key's JSON: %w", err)
		}
		if key.Type != "service_account" || key.ClientEmail == "" || key.PrivateKey == "" {
			return nil, fmt.Errorf("gcs: the credentials are %q, not a service account key: use Application Default Credentials (GOOGLE_APPLICATION_CREDENTIALS) for other kinds", key.Type)
		}
		if c.Signer != "" {
			return nil, errors.New("gcs: STORAGE_GCS_SIGNER is for credentials without a key: a service account key signs for its own account")
		}
		b.signer = signer{email: key.ClientEmail, key: []byte(key.PrivateKey), ready: true}
		copts = append(copts, option.WithAuthCredentialsJSON(option.ServiceAccount, keyJSON))
	}
	if host := os.Getenv("STORAGE_EMULATOR_HOST"); host != "" {
		b.insecure = !strings.HasPrefix(host, "https://")
	}
	client, err := gcstorage.NewClient(ctx, append(copts, o.client...)...)
	if err != nil {
		return nil, fmt.Errorf("gcs: %w", err)
	}
	b.client, b.bucket = client, client.Bucket(c.Bucket)
	return b, nil
}

// Client returns the Cloud Storage client, for what the backend doesn't
// do (metadata, lifecycle rules, other buckets).
func (b *Backend) Client() *gcstorage.Client { return b.client }

// Bucket returns the bucket's handle.
func (b *Backend) Bucket() *gcstorage.BucketHandle { return b.bucket }

// Close closes the client; the app does it when it shuts down.
func (b *Backend) Close() error { return b.client.Close() }

// object returns p's object. Writes replace the whole object, so
// retrying them on a transient error (429, 503) is safe.
func (b *Backend) object(p string) *gcstorage.ObjectHandle {
	return b.bucket.Object(b.prefix + p).Retryer(gcstorage.WithPolicy(gcstorage.RetryAlways))
}

// notFound turns a missing object into storage.ErrNotFound, after
// checking once that the bucket exists: a misspelled bucket is an
// error, not missing files.
func (b *Backend) notFound(ctx context.Context, err error) error {
	if !errors.Is(err, gcstorage.ErrObjectNotExist) {
		return err
	}
	if !b.bucketOK.Load() {
		_, berr := b.bucket.Attrs(ctx)
		var gerr *googleapi.Error
		switch {
		case errors.Is(berr, gcstorage.ErrBucketNotExist):
			return fmt.Errorf("gcs: the bucket %s doesn't exist", b.name)
		case errors.As(berr, &gerr) && gerr.Code == http.StatusForbidden:
			// Object roles (Storage Object Admin) can't read the bucket's
			// metadata: it can't be checked, so don't try again.
			b.bucketOK.Store(true)
		case berr != nil:
			return berr
		default:
			b.bucketOK.Store(true)
		}
	}
	return fmt.Errorf("%w (%w)", storage.ErrNotFound, err)
}

// Upload chunks: files up to maxChunk go in one request, with a buffer
// rounded up to the client's 256 KiB unit; larger ones in maxChunk
// chunks of a resumable upload.
const (
	minChunk = 256 << 10
	maxChunk = 8 << 20
)

// peekPool holds the buffers that read the start of uploads of unknown
// size.
var peekPool = sync.Pool{New: func() any {
	b := make([]byte, minChunk)
	return &b
}}

// chunkFor returns the chunk size for n bytes.
func chunkFor(n int64) int {
	if n >= maxChunk {
		return maxChunk
	}
	return int((n/minChunk + 1) * minChunk)
}

// Put implements storage.Backend. The object is replaced atomically when
// the upload completes; files over 8 MB go in 8 MB chunks of a
// resumable upload. If r fails or ctx ends, the upload is abandoned and
// nothing is stored. Active content ([storage.IsActive]) is stored with
// Content-Disposition: attachment, since the bucket serves it as is.
func (b *Backend) Put(ctx context.Context, p string, r io.Reader, opts storage.PutOptions) error {
	chunk := maxChunk
	if n, ok := sizeOf(r); ok {
		chunk = chunkFor(n)
	} else {
		buf := peekPool.Get().(*[]byte)
		defer peekPool.Put(buf)
		head := *buf
		n, err := io.ReadFull(r, head)
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			r, chunk = bytes.NewReader(head[:n]), chunkFor(int64(n))
		case err != nil:
			return err
		default:
			r = io.MultiReader(bytes.NewReader(head), r)
		}
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := b.object(p).NewWriter(wctx)
	w.ChunkSize = chunk
	w.ContentType = opts.ContentType
	if w.ContentType == "" {
		w.ContentType = "application/octet-stream" // not sniffed
	}
	if storage.IsActive(w.ContentType) {
		w.ContentDisposition = "attachment"
	}
	abandon := func(err error) error {
		// Close the upload's stream with the error before Close, which
		// would end it normally and store what was written (canceling
		// ctx alone aborts it only eventually).
		cancel()
		_ = w.CloseWithError(err) //nolint:staticcheck // the synchronous abort we need; the context's is asynchronous
		_ = w.Close()
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		return abandon(err)
	}
	if err := ctx.Err(); err != nil {
		return abandon(err)
	}
	return w.Close()
}

// sizeOf returns how many bytes r has left, if it can seek.
func sizeOf(r io.Reader) (int64, bool) {
	s, ok := r.(io.Seeker)
	if !ok {
		return -1, false
	}
	cur, err := s.Seek(0, io.SeekCurrent)
	if err != nil {
		return -1, false
	}
	end, err := s.Seek(0, io.SeekEnd)
	if err != nil {
		return -1, false
	}
	if _, err := s.Seek(cur, io.SeekStart); err != nil {
		return -1, false
	}
	return end - cur, true
}

// Open implements storage.Backend: the file reads the object's
// generation that was current when it was opened, so a Put meanwhile
// doesn't change what it reads. It can seek: a read after a Seek
// fetches the rest from there, and fails if that generation was replaced
// or deleted since. An object stored with Content-Encoding: gzip (gsutil
// -z, gcloud storage cp --gzip-local) is read decompressed, without
// seeking, and its Info's Size is -1 (unknown before reading).
func (b *Backend) Open(ctx context.Context, p string) (storage.File, error) {
	r, err := b.object(p).NewReader(ctx)
	if err != nil {
		return nil, b.notFound(ctx, err)
	}
	b.bucketOK.Store(true)
	a := r.Attrs
	info := storage.FileInfo{Path: p, Size: a.Size, ModTime: a.LastModified.UTC(), ContentType: a.ContentType, ETag: etag(a.Generation)}
	if a.Decompressed {
		info.Size = -1
		return &stream{Reader: r, info: info}, nil
	}
	return &file{b: b, ctx: ctx, p: p, gen: a.Generation, body: r, info: info}, nil
}

// etag is the ETag of an object's generation: a new one for each write.
func etag(gen int64) string { return `"` + strconv.FormatInt(gen, 10) + `"` }

// stream is an object read decompressed: it can't seek.
type stream struct {
	*gcstorage.Reader
	info storage.FileInfo
}

func (s *stream) Info() storage.FileInfo { return s.info }

// file is an object being read.
type file struct {
	b       *Backend
	ctx     context.Context
	p       string
	gen     int64
	body    io.ReadCloser // reads from bodyPos
	bodyPos int64
	pos     int64 // where the next Read reads, after a Seek
	info    storage.FileInfo
}

func (f *file) Info() storage.FileInfo { return f.info }

func (f *file) Read(p []byte) (int, error) {
	if f.pos != f.bodyPos || f.body == nil {
		if f.body != nil {
			_ = f.body.Close()
			f.body = nil
		}
		if f.pos >= f.info.Size {
			return 0, io.EOF
		}
		r, err := f.b.object(f.p).Generation(f.gen).NewRangeReader(f.ctx, f.pos, -1)
		if err != nil {
			if errors.Is(err, gcstorage.ErrObjectNotExist) {
				return 0, fmt.Errorf("gcs: %s changed since it was opened: %w", f.info.Path, err)
			}
			return 0, err
		}
		f.body, f.bodyPos = r, f.pos
	}
	n, err := f.body.Read(p)
	f.pos += int64(n)
	f.bodyPos += int64(n)
	return n, err
}

// Seek moves where the next Read reads; it doesn't contact the server.
func (f *file) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.pos
	case io.SeekEnd:
		offset += f.info.Size
	default:
		return 0, errors.New("gcs: invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("gcs: seek before the start of the file")
	}
	f.pos = offset
	return offset, nil
}

func (f *file) Close() error {
	if f.body == nil {
		return nil
	}
	return f.body.Close()
}

// info maps an object's attributes. ModTime is to the second, as Open's
// (an HTTP date).
func (b *Backend) info(p string, a *gcstorage.ObjectAttrs) storage.FileInfo {
	return storage.FileInfo{Path: p, Size: a.Size, ModTime: a.Updated.UTC().Truncate(time.Second), ContentType: a.ContentType, ETag: etag(a.Generation)}
}

// Stat implements storage.Backend.
func (b *Backend) Stat(ctx context.Context, p string) (storage.FileInfo, error) {
	a, err := b.object(p).Attrs(ctx)
	if err != nil {
		return storage.FileInfo{}, b.notFound(ctx, err)
	}
	b.bucketOK.Store(true)
	return b.info(p, a), nil
}

// Delete implements storage.Backend. A missing object, or bucket, is no
// error.
func (b *Backend) Delete(ctx context.Context, p string) error {
	if err := b.object(p).Delete(ctx); err != nil && !errors.Is(err, gcstorage.ErrObjectNotExist) {
		return err
	}
	return nil
}

// List implements storage.Backend: the bucket lists names in byte
// order, with their content types.
func (b *Backend) List(ctx context.Context, prefix string) iter.Seq2[storage.FileInfo, error] {
	return func(yield func(storage.FileInfo, error) bool) {
		q := &gcstorage.Query{Prefix: b.prefix + prefix, Projection: gcstorage.ProjectionNoACL}
		if err := q.SetAttrSelection([]string{"Name", "Size", "Updated", "ContentType", "Generation"}); err != nil {
			yield(storage.FileInfo{}, err)
			return
		}
		it := b.bucket.Objects(ctx, q)
		for {
			a, err := it.Next()
			if errors.Is(err, iterator.Done) {
				return
			}
			if err != nil {
				yield(storage.FileInfo{}, err)
				return
			}
			p := strings.TrimPrefix(a.Name, b.prefix)
			if p == "" || strings.HasSuffix(p, "/") {
				continue // a "folder" placeholder the console creates
			}
			if !yield(b.info(p, a), nil) {
				return
			}
		}
	}
}

// Copy implements storage.Backend, on the server (any size: large
// objects take several rewrite calls), keeping the content type.
func (b *Backend) Copy(ctx context.Context, src, dst string) error {
	_, err := b.object(dst).CopierFrom(b.bucket.Object(b.prefix + src)).Run(ctx)
	if errors.Is(err, gcstorage.ErrObjectNotExist) {
		return fmt.Errorf("%w (%w)", storage.ErrNotFound, err)
	}
	return err
}

// SignedURL implements storage.URLSigner: a V4 signed GET URL, valid for
// at most 7 days. A service account key (the settings', or Application
// Default Credentials') signs it locally. Without one, each URL takes a
// call to the IAM API (iamcredentials.googleapis.com, which must be
// enabled), signing as STORAGE_GCS_SIGNER or the instance's service
// account, which needs the iam.serviceAccounts.signBlob permission on
// that account (the Service Account Token Creator role). The call uses
// ctx.
func (b *Backend) SignedURL(ctx context.Context, p string, expires time.Time) (string, error) {
	ttl := time.Until(expires).Round(time.Second)
	if ttl < time.Second || ttl > 7*24*time.Hour {
		return "", fmt.Errorf("gcs: a signed URL lasts from a second to 7 days, not %s", ttl)
	}
	s, err := b.signerFor(ctx)
	if err != nil {
		return "", err
	}
	opts := &gcstorage.SignedURLOptions{
		Scheme:         gcstorage.SigningSchemeV4,
		Method:         http.MethodGet,
		Expires:        expires,
		GoogleAccessID: s.email,
		Insecure:       b.insecure,
	}
	if s.key != nil {
		opts.PrivateKey = s.key
	} else {
		name := "projects/-/serviceAccounts/" + s.email
		opts.SignBytes = func(data []byte) ([]byte, error) {
			resp, err := s.iam.Projects.ServiceAccounts.SignBlob(name, &iamcredentials.SignBlobRequest{
				Payload: base64.StdEncoding.EncodeToString(data),
			}).Context(ctx).Do()
			if err != nil {
				return nil, err
			}
			return base64.StdEncoding.DecodeString(resp.SignedBlob)
		}
	}
	u, err := b.bucket.SignedURL(b.prefix+p, opts)
	if err != nil {
		return "", fmt.Errorf("gcs: signing a URL as %s: %w (the account needs the Service Account Token Creator role, and the IAM Service Account Credentials API must be enabled)", s.email, err)
	}
	return u, nil
}

// signerFor returns how URLs are signed, found once (again after a
// failure): the settings' key; else Application Default Credentials'
// key; else the IAM API, as STORAGE_GCS_SIGNER, the credentials'
// account or the instance's.
func (b *Backend) signerFor(ctx context.Context) (signer, error) {
	b.signMu.Lock()
	defer b.signMu.Unlock()
	if b.signer.ready {
		return b.signer, nil
	}
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}})
	if err != nil {
		return signer{}, fmt.Errorf("gcs: no credentials to sign URLs with: %w (set STORAGE_GCS_CREDENTIALS_FILE to a service account key)", err)
	}
	s := signer{email: b.signer.email}
	if j := creds.JSON(); len(j) > 0 {
		var key serviceAccountKey
		if json.Unmarshal(j, &key) == nil && key.Type == "service_account" && key.PrivateKey != "" && s.email == "" {
			b.signer = signer{email: key.ClientEmail, key: []byte(key.PrivateKey), ready: true}
			return b.signer, nil
		}
		if s.email == "" {
			s.email = impersonated(j)
		}
	}
	if s.email == "" {
		if !metadata.OnGCE() {
			return signer{}, errors.New("gcs: no service account to sign URLs as: set STORAGE_GCS_SIGNER, or STORAGE_GCS_CREDENTIALS_FILE to a service account key")
		}
		email, err := metadata.EmailWithContext(ctx, "default")
		if err != nil {
			return signer{}, fmt.Errorf("gcs: finding the instance's service account: %w", err)
		}
		s.email = email
	}
	svc, err := iamcredentials.NewService(ctx, option.WithAuthCredentials(creds))
	if err != nil {
		return signer{}, fmt.Errorf("gcs: %w", err)
	}
	s.iam, s.ready = svc, true
	b.signer = s
	return s, nil
}

// impersonated returns the service account of impersonated or external
// account credentials, from their impersonation URL, or "".
func impersonated(j []byte) string {
	var c struct {
		URL string `json:"service_account_impersonation_url"`
	}
	if json.Unmarshal(j, &c) != nil {
		return ""
	}
	start, end := strings.LastIndex(c.URL, "/"), strings.LastIndex(c.URL, ":")
	if start < 0 || end <= start {
		return ""
	}
	return c.URL[start+1 : end]
}

// Driver is the backend's driver (STORAGE_DRIVER=gcs); opts configure
// each disk's backend.
func Driver(opts ...Option) storage.Driver {
	return storage.Driver{
		Name:    "gcs",
		Inherit: []string{"GCS_CREDENTIALS_FILE", "GCS_CREDENTIALS", "GCS_SIGNER"},
		Open: func(app *anetos.App, _ string, src config.Source, _ storage.Config) (storage.Backend, error) {
			c, err := config.Get[Config](src)
			if err != nil {
				return nil, err
			}
			return New(app.Context(context.Background()), c, opts...)
		},
	}
}

// SPDX-License-Identifier: Apache-2.0

// Package s3 is the storage package's backend for S3 and S3-compatible
// object stores (Cloudflare R2, MinIO, Backblaze B2, …), on minio-go:
//
//	st, err := storage.New(app, s3.Driver())
//
// Settings (STORAGE_<NAME>_S3_* for a named disk): STORAGE_DRIVER=s3,
// STORAGE_S3_BUCKET, STORAGE_S3_REGION (default us-east-1; "auto" for
// R2), STORAGE_S3_ENDPOINT (the server's URL, for other stores than
// AWS), STORAGE_S3_ACCESS_KEY and STORAGE_S3_SECRET_KEY (without them,
// the AWS environment variables, shared credentials file or instance
// role), STORAGE_S3_PATH_STYLE (MinIO), and STORAGE_S3_PREFIX (a prefix
// for the disk's keys in the bucket, ending in "/"). A named disk that
// sets none of the region, endpoint, keys and path style takes the
// default disk's; one that sets any of them takes none.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config is the backend's settings.
type Config struct {
	// Bucket is the bucket's name. STORAGE_S3_BUCKET, required.
	Bucket string `env:"STORAGE_S3_BUCKET"`
	// Region is the bucket's region. STORAGE_S3_REGION, default
	// us-east-1.
	Region string `env:"STORAGE_S3_REGION" default:"us-east-1"`
	// Endpoint is the server's URL, for stores other than AWS
	// ("https://<account>.r2.cloudflarestorage.com",
	// "http://127.0.0.1:9000"). STORAGE_S3_ENDPOINT, default AWS's.
	Endpoint string `env:"STORAGE_S3_ENDPOINT"`
	// AccessKey and SecretKey are the credentials; without them, the
	// AWS environment variables, shared credentials file or instance
	// role are used. STORAGE_S3_ACCESS_KEY, STORAGE_S3_SECRET_KEY.
	AccessKey string `env:"STORAGE_S3_ACCESS_KEY"`
	// SecretKey is the access key's secret. STORAGE_S3_SECRET_KEY.
	SecretKey anetos.Secret `env:"STORAGE_S3_SECRET_KEY"`
	// PathStyle puts the bucket in the URL's path rather than its host
	// (MinIO and other self-hosted stores). STORAGE_S3_PATH_STYLE,
	// default false.
	PathStyle bool `env:"STORAGE_S3_PATH_STYLE" default:"false"`
	// Prefix starts the disk's keys in the bucket ("uploads/"), so
	// disks can share a bucket. STORAGE_S3_PREFIX.
	Prefix string `env:"STORAGE_S3_PREFIX"`
}

// Driver is the backend's driver (STORAGE_DRIVER=s3); opts configure
// each disk's backend.
func Driver(opts ...Option) storage.Driver {
	return storage.Driver{
		Name:    "s3",
		Inherit: []string{"S3_REGION", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_PATH_STYLE"},
		Open: func(_ *anetos.App, _ string, src config.Source, _ storage.Config) (storage.Backend, error) {
			c, err := config.Get[Config](src)
			if err != nil {
				return nil, err
			}
			return New(c, opts...)
		},
	}
}

// Backend is a bucket, or a prefix in one.
type Backend struct {
	client   *minio.Client
	bucket   string
	prefix   string
	bucketOK atomic.Bool // the bucket was seen to exist
}

// Option configures a [Backend].
type Option func(*options)

type options struct{ transport http.RoundTripper }

// WithTransport sets the HTTP transport (proxies, custom TLS roots).
func WithTransport(rt http.RoundTripper) Option { return func(o *options) { o.transport = rt } }

// Transport is [WithTransport].
//
// Deprecated: Use WithTransport; Transport is removed in v0.6.
//
//go:fix inline
func Transport(rt http.RoundTripper) Option { return WithTransport(rt) }

// New returns a backend for c. It doesn't contact the server.
func New(c Config, opts ...Option) (*Backend, error) {
	if c.Bucket == "" {
		return nil, errors.New("s3: STORAGE_S3_BUCKET is required")
	}
	if c.Prefix != "" && (storage.CheckPrefix(c.Prefix) != nil || !strings.HasSuffix(c.Prefix, "/")) {
		return nil, fmt.Errorf("s3: invalid STORAGE_S3_PREFIX %q: a directory, ending in / (uploads/)", c.Prefix)
	}
	if (c.AccessKey == "") != (c.SecretKey == "") {
		return nil, errors.New("s3: set both STORAGE_S3_ACCESS_KEY and STORAGE_S3_SECRET_KEY, or neither")
	}
	host, secure := "s3.amazonaws.com", true
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("s3: STORAGE_S3_ENDPOINT %q must be an http or https URL without a path", c.Endpoint)
		}
		host, secure = u.Host, u.Scheme == "https"
	}
	creds := credentials.NewStaticV4(c.AccessKey, string(c.SecretKey), "")
	if c.AccessKey == "" {
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{}, &credentials.FileAWSCredentials{}, &credentials.IAM{Client: &http.Client{Timeout: 10 * time.Second}},
		})
	}
	lookup := minio.BucketLookupAuto
	if c.PathStyle {
		lookup = minio.BucketLookupPath
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	mo := &minio.Options{Creds: creds, Secure: secure, Region: c.Region, BucketLookup: lookup, Transport: o.transport}
	client, err := minio.New(host, mo)
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	return &Backend{client: client, bucket: c.Bucket, prefix: c.Prefix}, nil
}

// Client returns the minio-go client, for what the backend doesn't do.
func (b *Backend) Client() *minio.Client { return b.client }

func (b *Backend) key(p string) string { return b.prefix + p }

// mapErr turns a missing object into storage.ErrNotFound.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	r := minio.ToErrorResponse(err)
	if r.Code == "NoSuchKey" || r.StatusCode == http.StatusNotFound && r.Code != "NoSuchBucket" {
		return fmt.Errorf("%w (%w)", storage.ErrNotFound, err)
	}
	return err
}

// Put implements storage.Backend. A reader that can seek is sent with
// its size (from where it is), up to S3's 5 TB; others are read up to
// 8 MB first, then uploaded in 8 MB parts if they are longer, up to
// S3's 10,000 parts (78 GB). Active content ([storage.IsActive]) is
// stored with Content-Disposition: attachment, since the bucket serves
// it as is. A Put canceled during a multipart upload removes the
// incomplete uploads of its path (another's too, if two uploads of one
// path run at once).
func (b *Backend) Put(ctx context.Context, p string, r io.Reader, opts storage.PutOptions) error {
	po := minio.PutObjectOptions{ContentType: opts.ContentType}
	if opts.ContentType != "" && storage.IsActive(opts.ContentType) {
		po.ContentDisposition = "attachment"
	}
	size, known := sizeOf(r)
	if known {
		if cur, ok := r.(interface {
			io.ReaderAt
			io.Seeker
		}); ok {
			// minio-go reads a ReaderAt from offset 0: give it the rest only.
			off, _ := cur.Seek(0, io.SeekCurrent)
			r = io.NewSectionReader(cur, off, size)
		}
	} else {
		buf := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf)
		head := *buf
		n, err := io.ReadFull(r, head)
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			r, size = bytes.NewReader(head[:n]), int64(n)
		case err != nil:
			return err
		default:
			r = io.MultiReader(bytes.NewReader(head), r)
			po.PartSize = partSize // otherwise minio-go buffers parts of ~530 MB
		}
	}
	if size == 0 {
		// An empty body must be sent with "Content-Length: 0", which
		// net/http does only for http.NoBody (S3 refuses a chunked upload).
		r = http.NoBody
	}
	_, err := b.client.PutObject(ctx, b.bucket, b.key(p), r, size, po)
	if err != nil && ctx.Err() != nil && (size < 0 || size > partSize) {
		// minio-go aborts a failed multipart upload with ctx, which is
		// done: abort it here, so its parts aren't kept (and billed).
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = b.client.RemoveIncompleteUpload(actx, b.bucket, b.key(p))
	}
	return err
}

// partSize is the size of the parts of uploads of unknown size, and of
// the buffer each needs.
const partSize = 8 << 20

// bufPool holds the buffers that read the start of uploads of unknown
// size.
var bufPool = sync.Pool{New: func() any {
	b := make([]byte, partSize)
	return &b
}}

// sizeOf returns how many bytes r has left, if it can seek (files,
// uploads, bytes and strings readers): a known size is sent in one
// request (up to minio-go's multipart threshold) rather than buffered
// in parts.
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

// Open implements storage.Backend: one GET, whose body the file reads,
// so a Put meanwhile doesn't change what it reads. The file can seek:
// a read after a Seek fetches the rest from there, and fails if the
// object was replaced since it was opened.
func (b *Backend) Open(ctx context.Context, p string) (storage.File, error) {
	body, st, _, err := minio.Core{Client: b.client}.GetObject(ctx, b.bucket, b.key(p), minio.GetObjectOptions{})
	if err != nil {
		return nil, mapErr(err)
	}
	return &file{b: b, ctx: ctx, key: b.key(p), body: body, etag: st.ETag, info: b.info(p, st)}, nil
}

// file is an object being read.
type file struct {
	b         *Backend
	ctx       context.Context
	key, etag string
	body      io.ReadCloser // reads from bodyPos
	bodyPos   int64
	pos       int64 // where the next Read reads, after a Seek
	info      storage.FileInfo
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
		opts := minio.GetObjectOptions{}
		if err := opts.SetRange(f.pos, 0); err != nil {
			return 0, err
		}
		if err := opts.SetMatchETag(f.etag); err != nil {
			return 0, err
		}
		body, _, _, err := minio.Core{Client: f.b.client}.GetObject(f.ctx, f.b.bucket, f.key, opts)
		if err != nil {
			if minio.ToErrorResponse(err).StatusCode == http.StatusPreconditionFailed {
				return 0, fmt.Errorf("s3: %s changed since it was opened: %w", f.info.Path, err)
			}
			return 0, mapErr(err)
		}
		f.body, f.bodyPos = body, f.pos
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
		return 0, errors.New("s3: invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("s3: seek before the start of the file")
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

func (b *Backend) info(p string, st minio.ObjectInfo) storage.FileInfo {
	etag := st.ETag
	if etag != "" && !strings.HasPrefix(etag, `"`) {
		etag = `"` + etag + `"`
	}
	return storage.FileInfo{Path: p, Size: st.Size, ModTime: st.LastModified.UTC(), ContentType: st.ContentType, ETag: etag}
}

// Stat implements storage.Backend. A HEAD request can't tell a missing
// object from a missing bucket, so the first "not found" checks the
// bucket.
func (b *Backend) Stat(ctx context.Context, p string) (storage.FileInfo, error) {
	st, err := b.client.StatObject(ctx, b.bucket, b.key(p), minio.StatObjectOptions{})
	if err != nil {
		err = mapErr(err)
		if errors.Is(err, storage.ErrNotFound) && !b.bucketOK.Load() {
			ok, berr := b.client.BucketExists(ctx, b.bucket)
			switch {
			case berr != nil && minio.ToErrorResponse(berr).StatusCode == http.StatusForbidden:
				// Credentials for a prefix may not see the bucket: unknown.
			case berr != nil:
				return storage.FileInfo{}, berr
			case !ok:
				return storage.FileInfo{}, fmt.Errorf("s3: the bucket %s doesn't exist", b.bucket)
			default:
				b.bucketOK.Store(true)
			}
		}
		return storage.FileInfo{}, err
	}
	b.bucketOK.Store(true)
	return b.info(p, st), nil
}

// Delete implements storage.Backend.
func (b *Backend) Delete(ctx context.Context, p string) error {
	return mapIgnoreNotFound(b.client.RemoveObject(ctx, b.bucket, b.key(p), minio.RemoveObjectOptions{}))
}

func mapIgnoreNotFound(err error) error {
	if errors.Is(mapErr(err), storage.ErrNotFound) {
		return nil
	}
	return err
}

// List implements storage.Backend: the bucket lists keys in byte order.
// It doesn't give content types.
func (b *Backend) List(ctx context.Context, prefix string) iter.Seq2[storage.FileInfo, error] {
	return func(yield func(storage.FileInfo, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		ch := b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Prefix: b.key(prefix), Recursive: true})
		defer func() {
			// Stop the listing if the loop broke off, and let its
			// goroutine finish: it blocks until its channel is read.
			cancel()
			for range ch {
			}
		}()
		for obj := range ch {
			if obj.Err != nil {
				yield(storage.FileInfo{}, mapErr(obj.Err))
				return
			}
			p := strings.TrimPrefix(obj.Key, b.prefix)
			if p == "" || strings.HasSuffix(p, "/") {
				continue // a "folder" placeholder some tools create
			}
			if !yield(b.info(p, obj), nil) {
				return
			}
		}
		if err := ctx.Err(); err != nil {
			yield(storage.FileInfo{}, err)
		}
	}
}

// Copy implements storage.Backend, on the server (up to 5 GB).
func (b *Backend) Copy(ctx context.Context, src, dst string) error {
	_, err := b.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: b.bucket, Object: b.key(dst)},
		minio.CopySrcOptions{Bucket: b.bucket, Object: b.key(src)})
	return mapErr(err)
}

// TemporaryURL implements storage.TemporaryURLBackend: a presigned GET URL, valid
// for at most 7 days.
func (b *Backend) TemporaryURL(ctx context.Context, p string, expires time.Time) (string, error) {
	ttl := time.Until(expires).Round(time.Second)
	if ttl < time.Second || ttl > 7*24*time.Hour {
		return "", fmt.Errorf("s3: a signed URL lasts from a second to 7 days, not %s", ttl)
	}
	u, err := b.client.PresignedGetObject(ctx, b.bucket, b.key(p), ttl, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// SignedURL is [Backend.TemporaryURL].
//
// Deprecated: Use TemporaryURL; SignedURL is removed in v0.6.
//
//go:fix inline
func (b *Backend) SignedURL(ctx context.Context, p string, expires time.Time) (string, error) {
	return b.TemporaryURL(ctx, p, expires)
}

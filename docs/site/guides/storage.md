---
title: Store files
since: v0.2.0
group: "Features"
weight: 501
---

# Store files

Store files your app receives or makes: uploads, avatars, exports,
invoices. Files go on **disks**: a local directory by default, or an
S3-compatible bucket (Amazon S3, Cloudflare R2, MinIO, …) or a Google
Cloud Storage bucket, with the same code. Private files are read through temporary signed URLs. The complete
app is [`examples/files`](../../../examples/files).

## Before you start

You have an app created with `anetos.New()`. Choose where files are kept
with `STORAGE_DRIVER`:

| Driver | Files are kept | Use it for |
|---|---|---|
| `local` (default) | In the directory `STORAGE_ROOT` (default `storage/app`) | One server, development |
| `s3` | In an S3-compatible bucket (`drivers/s3`, `STORAGE_S3_*`) | Several servers, production |
| `gcs` | In a Google Cloud Storage bucket (`drivers/gcs`, `STORAGE_GCS_*`) | Apps on Google Cloud |
| `memory` | In the process | Tests (`anetostest` sets it) |

A disk can also have a URL its files are served at, `STORAGE_URL`, and be
**public** (`STORAGE_PUBLIC=true`: anyone can read its files at that URL)
or private (the default: files are read through temporary URLs only).

```env
STORAGE_DRIVER=local
STORAGE_URL=https://example.com/files
```

## Steps

### 1. Set up storage

In `setup`:

```go
st, err := storage.ForApp(app, s3.Driver()) // STORAGE_DRIVER: local, memory or s3
if err != nil {
	return nil, err
}
avatars, err := st.Disk("avatars") // STORAGE_DISKS=avatars, STORAGE_AVATARS_*
if err != nil {
	return nil, err
}
```

(Copied from [`examples/files`](../../../examples/files/main.go), region `setup`.)

Pass the drivers of the stores you use: `s3.Driver()` for S3,
`gcs.Driver()` for Google Cloud Storage (step 6), or both.

The default disk is configured with `STORAGE_*`. Name more disks in
`STORAGE_DISKS`, and configure each with `STORAGE_<NAME>_*`: the example's
public avatars disk is

```env
STORAGE_DISKS=avatars
STORAGE_AVATARS_URL=https://example.com/avatars
STORAGE_AVATARS_PUBLIC=true
```

A named disk takes the default disk's driver unless it sets its own, and
its S3 connection (region, endpoint, keys, path style) or GCS
credentials and signer unless it sets any of them; its directory defaults to `storage/<name>`, and its bucket must
be set.

### 2. Store uploads

Bind the upload to a `*multipart.FileHeader` field, validate it, and
store it with `PutUpload`:

```go
// UploadInput is a document to store: a PDF, PNG or JPEG of at most 5 MB.
// mimetypes checks the file's content, extensions its name.
type UploadInput struct {
	File *multipart.FileHeader `form:"file" validate:"required|max_size:5MB|mimetypes:application/pdf,image/png,image/jpeg|extensions:pdf,png,jpg,jpeg"`
}

// UploadDocument stores the file under a name of its own: a random one,
// with the upload's extension. The client's file name isn't used in the
// path.
func UploadDocument(c *web.Ctx, in UploadInput) (web.Responder, error) {
	disk, err := storage.From(c)
	if err != nil {
		return nil, err
	}
	name := randomName() + strings.ToLower(path.Ext(in.File.Filename))
	if err := disk.PutUpload(c, "documents/"+name, in.File); err != nil {
		return nil, err
	}
	doc, err := document(c, disk, name)
	if err != nil {
		return nil, err
	}
	return web.JSON(http.StatusCreated, doc), nil
}
```

(Copied from [`examples/files/documents.go`](../../../examples/files/documents.go), region `upload`.)

`storage.From(ctx)` returns the default disk; `storage.From(ctx, "avatars")`
a named one. Choose the path yourself: the file name an upload comes with
is the client's, so it may be anything.

Paths are relative and slash-separated: `documents/42.pdf`. A path with
`..`, a leading `/`, a backslash or a control character is an error
(`storage.ErrInvalidPath`), not cleaned, so a path built from user input
can't reach outside the disk. Other ways to store:

| Method | Stores |
|---|---|
| `disk.Put(ctx, path, r)` | What an `io.Reader` reads |
| `disk.PutBytes(ctx, path, data)` | A byte slice |
| `disk.PutUpload(ctx, path, fileHeader)` | An upload |
| `disk.Copy(ctx, src, dst)`, `disk.Move(ctx, src, dst)` | Another file's content |

A file is replaced whole: readers see the old content or the new, never
part. The content type comes from the path's extension (or, without one,
the content, never as HTML or another type a browser would run);
`storage.ContentType("…")` sets it on S3 and memory disks. Local disks
keep no content types: they give the extension's, so give files the
right extension.

### 3. Read and list files

```go
// ListDocuments lists the documents, in order of name.
func ListDocuments(c *web.Ctx) error {
	disk, err := storage.From(c)
	if err != nil {
		return err
	}
	docs := []Document{}
	for info, err := range disk.List(c, "documents/") {
		if err != nil {
			return err
		}
		url, err := disk.TemporaryURL(c, info.Path, 15*time.Minute)
		if err != nil {
			return err
		}
		docs = append(docs, Document{Name: strings.TrimPrefix(info.Path, "documents/"), Size: info.Size, UploadedAt: info.ModTime, URL: url})
	}
	return c.JSON(http.StatusOK, docs)
}
```

(Copied from [`examples/files/documents.go`](../../../examples/files/documents.go), region `list`.)

| Method | Returns |
|---|---|
| `disk.Get(ctx, path)` | The content, `[]byte` |
| `disk.Open(ctx, path)` | A `storage.File` to read (and `Close`), with its `Info()` |
| `disk.Stat(ctx, path)` | A `storage.FileInfo`: `Path`, `Size`, `ModTime`, `ContentType`, `ETag` |
| `disk.Exists(ctx, path)` | Whether there is a file |
| `disk.List(ctx, prefix)` | The files whose paths start with `prefix`, in order of path (`"documents/"`: a directory and its subdirectories) |
| `disk.Delete(ctx, paths…)`, `disk.DeleteAll(ctx, "dir/")` | Removes files (`DeleteAll`: a directory's, recursively); a missing file is no error |

A missing file is `storage.ErrNotFound`, which a handler can return: it
becomes a 404, as does an invalid path.

### 4. Give out URLs

A private file is read through a temporary URL:

```go
// document describes the stored document name, with a URL that reads it
// for 15 minutes: signed by the app for a local disk, presigned by the
// bucket on S3.
func document(c *web.Ctx, disk *storage.Disk, name string) (Document, error) {
	info, err := disk.Stat(c, "documents/"+name)
	if err != nil {
		return Document{}, err // 404 if there is no such document
	}
	url, err := disk.TemporaryURL(c, info.Path, 15*time.Minute)
	if err != nil {
		return Document{}, err
	}
	return Document{Name: name, Size: info.Size, UploadedAt: info.ModTime, URL: url}, nil
}
```

(Copied from [`examples/files/documents.go`](../../../examples/files/documents.go), region `temporary-url`.)

On S3, `TemporaryURL` is a URL presigned by the bucket (up to 7 days).
On a local disk, it is `STORAGE_URL` with the path and a token signed
with `APP_KEY`, which the disk's handler checks: mount it at
`STORAGE_URL`'s path.

```go
// Local disks' files, at their STORAGE_URL. The default disk isn't
// public: its handler serves only temporary URLs. (Files on S3 are
// served by the bucket.)
r.HandleStd(http.MethodGet, "/files/{path...}", st.Default().Handler())
r.HandleStd(http.MethodGet, "/avatars/{path...}", avatars.Handler())
```

(Copied from [`examples/files`](../../../examples/files/main.go), region `serve`.)

The handler serves a file with its content type, length, `ETag` and
modification time, and answers conditional requests and requests for one
range (a request for several gets the whole file). Files a browser would
run (`storage.IsActive`: HTML, SVG and other XML, JavaScript, CSS) are
sent as `application/octet-stream` downloads in a sandbox, so an
uploaded file can't run on your site, not even through `<script src>`.
Without a valid token, a private disk's handler answers 403; tokens
expire, and work only for their file and disk.

A public disk's files have permanent URLs, `disk.URL(path)`:

```go
// AvatarInput is an image of at most 1 MB. extensions matters: the
// stored file's type comes from its extension.
type AvatarInput struct {
	Avatar *multipart.FileHeader `form:"avatar" validate:"required|image|extensions:png,jpg,jpeg,gif,webp|max_size:1MB"`
}

// UploadAvatar stores the image on the public avatars disk and returns
// its permanent URL.
func UploadAvatar(c *web.Ctx, in AvatarInput) (web.Responder, error) {
	avatars, err := storage.From(c, "avatars")
	if err != nil {
		return nil, err
	}
	p := randomName() + strings.ToLower(path.Ext(in.Avatar.Filename))
	if err := avatars.PutUpload(c, p, in.Avatar); err != nil {
		return nil, err
	}
	url, err := avatars.URL(p)
	if err != nil {
		return nil, err
	}
	return web.JSON(http.StatusCreated, map[string]string{"url": url}), nil
}
```

(Copied from [`examples/files/documents.go`](../../../examples/files/documents.go), region `avatar`.)

To check who may read a file before sending it, serve it from your own
handler with `disk.Serve(c.Writer(), c.Request(), path)`.

### 5. Use S3

Pass `s3.Driver()` to `storage.ForApp` (from `drivers/s3`) and set:

```env
STORAGE_DRIVER=s3
STORAGE_S3_BUCKET=example-uploads
STORAGE_S3_REGION=eu-west-1
STORAGE_S3_ACCESS_KEY=AKIA…
STORAGE_S3_SECRET_KEY=…
```

For other S3-compatible stores, set the endpoint:

| Store | Settings |
|---|---|
| Cloudflare R2 | `STORAGE_S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com`, `STORAGE_S3_REGION=auto` |
| MinIO | `STORAGE_S3_ENDPOINT=http://127.0.0.1:9000`, `STORAGE_S3_PATH_STYLE=true` |
| Backblaze B2, DigitalOcean Spaces, … | Their S3 endpoint and region |

Without keys, the AWS environment variables, shared credentials file or
the instance's role are used. `STORAGE_S3_PREFIX` (a directory, ending
in `/`) puts the disk's files under a prefix of the bucket, so disks can
share one. For a public disk on S3, make the bucket (or the prefix)
readable with a bucket policy, or put a CDN in front, and set
`STORAGE_URL` to the URL of the disk's files (the prefix included:
`https://cdn.example.com/avatars`).

The bucket serves files itself, so the S3 driver stores the types a
browser would run with `Content-Disposition: attachment`. A missing
bucket is an error, not a missing file.

### 6. Use Google Cloud Storage

Pass `gcs.Driver()` to `storage.ForApp` (from `drivers/gcs`) and set:

```env
STORAGE_DRIVER=gcs
STORAGE_GCS_BUCKET=example-uploads
```

On Cloud Run, GKE or Compute Engine that is all: the service account of
the service or instance is used (Application Default Credentials).
Elsewhere:

- On your machine, `gcloud auth application-default login`, or
- a service account key: `STORAGE_GCS_CREDENTIALS_FILE=/secrets/key.json`,
  or its JSON in `STORAGE_GCS_CREDENTIALS` for platforms that keep
  secrets in the environment. These two take service account keys only;
  other credentials (workload identity federation, impersonation) go
  through `GOOGLE_APPLICATION_CREDENTIALS`.

The account needs the Storage Object Admin role on the bucket (Storage
Object Viewer for a read-only disk).

Temporary URLs are V4 signed URLs (up to 7 days). A service account key
signs them itself, with no network call. Without one (Cloud Run, gcloud
credentials), each URL is signed by a call to the IAM API (enable the
IAM Service Account Credentials API, `iamcredentials.googleapis.com`), as
the service's account or `STORAGE_GCS_SIGNER`:

- On Cloud Run, give the service's account the Service Account Token
  Creator role on itself.
- With your gcloud credentials, set `STORAGE_GCS_SIGNER` to a service
  account's email, on which *you* need that role.

A page that links many files makes as many calls: sign links when they
are clicked (a route that redirects to `TemporaryURL`) rather than for
every row.

`STORAGE_GCS_PREFIX` (a directory, ending in `/`) puts the disk's files
under a prefix of the bucket, as with S3: a disk can read a folder of a
bucket other tools write to (`STORAGE_REPORTS_GCS_PREFIX=exports/`).

For local development without Google Cloud, run
[fake-gcs-server](https://github.com/fsouza/fake-gcs-server) over HTTP
and point the client at it; a service account key (any, even one of a
test project) signs temporary URLs on the emulator:

```sh
fake-gcs-server -scheme http -port 4443 -external-url http://localhost:4443
export STORAGE_EMULATOR_HOST=localhost:4443
```

Like S3, the bucket serves files itself: active types are stored with
`Content-Disposition: attachment`, and a missing bucket is an error
(when a file is read or listed). Objects other tools stored compressed
(`Content-Encoding: gzip`, from `gsutil -z` or
`gcloud storage cp --gzip-local`) are read decompressed, without range
requests. The Cloud Storage client is large: the driver adds about
40 MB to a binary, which matters for container images.

### 7. Test

`anetostest` sets `STORAGE_DRIVER=memory`: each test app's files are in
memory. `app.PostMultipart` uploads files, and the temporary URLs are on
the test client's site (`http://example.test`), so the test can fetch
them:

```go
// Upload a document, then read it through its temporary URL, which the
// default disk's handler serves.
func TestDocuments(t *testing.T) {
	app := anetostest.New(t, setup, env)

	var doc Document
	app.PostMultipart("/documents", nil, anetostest.Upload{Field: "file", Filename: "Invoice.PDF", Content: pdf}).
		AssertStatus(http.StatusCreated).
		JSON(&doc)
	if !strings.HasSuffix(doc.Name, ".pdf") || doc.Size != int64(len(pdf)) {
		t.Errorf("document %+v", doc)
	}
	app.Get(doc.URL).AssertOK().AssertHeader("Content-Type", "application/pdf").AssertSee("a tiny document")
	app.Get("/files/documents/" + doc.Name).AssertStatus(http.StatusForbidden) // no token

	var docs []Document
	app.GetJSON("/documents").AssertOK().JSON(&docs)
	if len(docs) != 1 || docs[0].Name != doc.Name {
		t.Errorf("documents %+v", docs)
	}
	app.Delete("/documents/" + doc.Name).AssertNoContent()
	app.Get(doc.URL).AssertNotFound()
}
```

(Copied from [`examples/files/main_test.go`](../../../examples/files/main_test.go), region `test`.)

`app.Disk()` and `app.Disk("avatars")` check the disks' files, and
`app.Travel` moves the clock past a temporary URL's expiry:

```go
// The upload is on the default disk, and its temporary URL stops working
// after 15 minutes: the test travels in time instead of waiting.
func TestDocumentLink(t *testing.T) {
	app := anetostest.New(t, setup, env)
	uploaded := app.Freeze(time.Time{})

	var doc Document
	app.PostMultipart("/documents", nil, anetostest.Upload{Field: "file", Filename: "q3.pdf", Content: pdf}).
		AssertStatus(http.StatusCreated).
		JSON(&doc)
	app.Disk().AssertContent("documents/"+doc.Name, string(pdf))
	app.Disk("avatars").AssertMissing("documents/" + doc.Name)
	if !doc.UploadedAt.Equal(uploaded) {
		t.Errorf("uploaded at %v, want %v", doc.UploadedAt, uploaded)
	}

	app.Travel(14 * time.Minute)
	app.Get(doc.URL).AssertOK()
	app.Travel(2 * time.Minute)
	app.Get(doc.URL).AssertStatus(http.StatusForbidden)
}
```

(Region `test-disk`.)

## How it works

A `storage.Disk` checks paths, sets content types and builds URLs; its
`storage.Backend` keeps the files. Backends implement `Put`, `Open`,
`Stat`, `Delete`, `List` and `Copy`, and `storage/storagetest` is the
conformance suite each runs.

- **Local** files are opened through an `os.Root`, so neither a path nor
  a symbolic link reaches outside `STORAGE_ROOT`. `Put` writes a
  temporary file, syncs it, renames it over the old one and syncs the
  directory; if the process dies meanwhile, the temporary file
  (`.anetos-tmp-…`) stays behind, unlisted. Content types come from
  extensions; empty directories are left after deletes. A local disk
  can't hold both a file `a` and a file `a/b`.
- **S3** reads an object in one request (a read after a `Seek` asks for
  a range, and fails if the object was replaced since); a reader whose
  size is known is uploaded in one request (or parts, when large, up to
  S3's 5 TB), others in 8 MB parts (up to 78 GB). An upload canceled
  midway removes its parts. `List` doesn't give content types.
- **Temporary URLs** of local and memory disks carry the disk's name, the
  path and the expiry, encrypted with `APP_KEY` (so rotating it, with
  `APP_PREVIOUS_KEYS`, keeps them working).

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `the disk has no URL (set STORAGE_URL)` | `TemporaryURL` or `URL` on a disk without a URL | Set `STORAGE_URL` (or `STORAGE_<NAME>_URL`), and mount the disk's handler there |
| `isn't public: use TemporaryURL` | `URL` on a private disk | Use `TemporaryURL`, or make the disk public |
| 403 from the handler | No token, an expired one, or one for another file | Use a fresh `TemporaryURL` |
| `invalid path` | `..`, a leading `/`, `//`, a backslash or a control character | Build paths from IDs and random names |
| Files missing after a deploy | A local disk on a server whose disk is replaced | Use a persistent volume, or S3 |
| Files differ between servers | A local disk on each server | Use S3 when several servers run the app |
| `no disk named …` | `From(ctx, name)` for a disk not in `STORAGE_DISKS` | Add it to `STORAGE_DISKS` |
| `gcs: signing a URL: …` | GCS credentials without a private key, and no account allowed to sign | Give the service account the Service Account Token Creator role on itself, or set `STORAGE_GCS_SIGNER` |
| `could not find default credentials` (GCS) | No credentials outside Google Cloud | `gcloud auth application-default login`, or `STORAGE_GCS_CREDENTIALS_FILE` |
| A stylesheet, script or SVG from a disk downloads instead of loading | Disks serve active types as downloads | Serve your own assets with `view.NewAssets`; disks are for files users give you |

## Next steps

- [Validation](validation.md): `max_size`, `mimetypes`, `extensions` and
  `image` for uploads.
- [Configuration reference](../reference/configuration.md#storage): the
  `STORAGE_*` settings.

> **Coming from Laravel?** `storage.From(ctx)` is `Storage::disk()`, and
> `STORAGE_DISKS` with `STORAGE_<NAME>_*` replaces `config/filesystems.php`.
> `PutUpload` is `$file->storeAs()`, `TemporaryURL` is
> `temporaryUrl()` (for local disks too), and `URL` is `url()`. There is
> no `storage:link`: the disk's handler serves local files. Visibility is
> per disk (`STORAGE_PUBLIC`), not per file, since S3 buckets now block
> per-object ACLs by default.

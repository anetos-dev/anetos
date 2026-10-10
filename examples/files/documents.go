// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"

	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/web"
)

// Document is a stored document.
type Document struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploaded_at"`
	URL        string    `json:"url"` // a temporary URL
}

// region: upload
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

// endregion

func randomName() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// region: temporary-url
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

// endregion

// region: list
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

// endregion

// DocumentName is the document in the path.
type DocumentName struct {
	Name string `path:"name"`
}

// DownloadDocument redirects to a temporary URL of the document.
func DownloadDocument(c *web.Ctx, in DocumentName) (web.Responder, error) {
	disk, err := storage.From(c)
	if err != nil {
		return nil, err
	}
	doc, err := document(c, disk, in.Name) // an invalid name is a 404 too
	if err != nil {
		return nil, err
	}
	return web.Redirect(doc.URL), nil
}

// DeleteDocument deletes the document.
func DeleteDocument(c *web.Ctx, in DocumentName) (web.Responder, error) {
	disk, err := storage.From(c)
	if err != nil {
		return nil, err
	}
	if err := disk.Delete(c, "documents/"+in.Name); err != nil {
		return nil, err
	}
	return web.NoContent(), nil
}

// region: avatar
// AvatarInput is an image of at most 1 MB. extensions matters: the
// stored file's type comes from its extension.
type AvatarInput struct {
	Avatar *multipart.FileHeader `form:"avatar" validate:"required|image|extensions:png,jpg,jpeg,gif,webp|max_size:1MB"`
}

// UploadAvatar stores the image on the public avatars disk and returns
// its permanent URL.
func UploadAvatar(c *web.Ctx, in AvatarInput) (web.Responder, error) {
	avatars, err := storage.DiskFrom(c, "avatars")
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

// endregion

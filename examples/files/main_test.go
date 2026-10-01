// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
	"testing"

	"anetos.dev/anetos/anetostest"
)

// env is the disks' settings: anetostest keeps their files in memory
// (STORAGE_DRIVER=memory) and serves the app at http://example.test.
var env = anetostest.Env(map[string]string{
	"STORAGE_URL":   "http://example.test/files",
	"STORAGE_DISKS": "avatars", "STORAGE_AVATARS_URL": "http://example.test/avatars", "STORAGE_AVATARS_PUBLIC": "true",
})

var pdf = []byte("%PDF-1.7\n% a tiny document\n")

// region: test
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

// endregion

func TestUploadErrors(t *testing.T) {
	app := anetostest.New(t, setup, env)
	app.PostMultipart("/documents", nil, anetostest.Upload{Field: "file", Filename: "run.exe", Content: []byte("MZ\x90\x00binary")}).
		AssertUnprocessable()
	app.PostMultipart("/documents", nil, anetostest.Upload{Field: "file", Filename: "fake.pdf", Content: []byte("<html>not a pdf")}).
		AssertUnprocessable()
	app.GetJSON("/documents/missing.pdf").AssertNotFound()
	app.GetJSON("/documents/..").AssertNotFound()
}

func TestDownloadRedirects(t *testing.T) {
	app := anetostest.New(t, setup, env)
	var doc Document
	app.PostMultipart("/documents", nil, anetostest.Upload{Field: "file", Filename: "a.pdf", Content: pdf}).
		AssertStatus(http.StatusCreated).JSON(&doc)
	res := app.Get("/documents/" + doc.Name).AssertStatus(http.StatusSeeOther)
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "http://example.test/files/documents/"+doc.Name+"?token=") {
		t.Errorf("Location %s", loc)
	}
}

func TestAvatar(t *testing.T) {
	app := anetostest.New(t, setup, env)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	var out map[string]string
	app.PostMultipart("/avatar", nil, anetostest.Upload{Field: "avatar", Filename: "me.png", Content: png}).
		AssertStatus(http.StatusCreated).JSON(&out)
	if !strings.HasPrefix(out["url"], "http://example.test/avatars/") {
		t.Fatalf("url %q", out["url"])
	}
	app.Get(out["url"]).AssertOK().AssertHeader("Content-Type", "image/png") // public: no token
}

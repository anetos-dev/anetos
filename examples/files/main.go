// SPDX-License-Identifier: Apache-2.0

// Command files stores documents and avatars on disks: the default disk
// (private: documents are read through temporary URLs) and the public
// "avatars" disk. Disks are local directories by default, or S3 buckets
// (STORAGE_DRIVER=s3); the app serves local files itself.
//
//	go tool anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080 \
//	  STORAGE_URL=http://localhost:8080/files \
//	  STORAGE_DISKS=avatars STORAGE_AVATARS_URL=http://localhost:8080/avatars STORAGE_AVATARS_PUBLIC=true
//	go run .
//	curl -F file=@invoice.pdf localhost:8080/documents
//	curl localhost:8080/documents         # with temporary URLs
//	curl -F avatar=@me.png localhost:8080/avatar
package main

import (
	"log"
	"net/http"

	"anetos.dev/anetos"
	"anetos.dev/anetos/drivers/s3"
	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/web"
)

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute()
}

func setup(app *anetos.App) (*web.Server, error) {
	// region: setup
	st, err := storage.ForApp(app, s3.Driver()) // STORAGE_DRIVER: local, memory or s3
	if err != nil {
		return nil, err
	}
	avatars, err := st.Disk("avatars") // STORAGE_DISKS=avatars, STORAGE_AVATARS_*
	if err != nil {
		return nil, err
	}
	// endregion
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	r := srv.Router()
	r.Post("/documents", web.H(UploadDocument))
	r.Get("/documents", ListDocuments)
	r.Get("/documents/{name}", web.H(DownloadDocument))
	r.Delete("/documents/{name}", web.H(DeleteDocument))
	r.Post("/avatar", web.H(UploadAvatar))
	// region: serve
	// Local disks' files, at their STORAGE_URL. The default disk isn't
	// public: its handler serves only temporary URLs. (Files on S3 are
	// served by the bucket.)
	r.HandleStd(http.MethodGet, "/files/{path...}", st.Default().Handler())
	r.HandleStd(http.MethodGet, "/avatars/{path...}", avatars.Handler())
	// endregion
	return srv, nil
}

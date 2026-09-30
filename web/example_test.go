// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"anetos.dev/anetos/web"
)

type CreatePost struct {
	AuthorID int64  `path:"id"`
	Title    string `json:"title" validate:"required|max:200"`
}

type Post struct {
	ID       int64  `json:"id"`
	AuthorID int64  `json:"author_id"`
	Title    string `json:"title"`
}

func ExampleH() {
	r := web.NewRouter(web.WithLogger(slog.New(slog.DiscardHandler)))
	r.Post("/authors/{id}/posts", web.H(func(c *web.Ctx, in CreatePost) (web.Responder, error) {
		// in is bound and valid here.
		return web.Created(Post{ID: 1, AuthorID: in.AuthorID, Title: in.Title}), nil
	}))

	for _, body := range []string{`{"title":"Hello"}`, `{"title":""}`} {
		req := httptest.NewRequest(http.MethodPost, "/authors/7/posts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out bytes.Buffer
		_ = json.Compact(&out, rec.Body.Bytes())
		fmt.Println(rec.Code, rec.Header().Get("Content-Type"), out.String())
	}
	// Output:
	// 201 application/json; charset=utf-8 {"id":1,"author_id":7,"title":"Hello"}
	// 422 application/problem+json {"type":"about:blank","title":"Unprocessable Entity","status":422,"detail":"The given data was invalid.","errors":{"title":"The title field is required."}}
}

func ExampleRouter_URL() {
	r := web.NewRouter()
	r.Get("/posts/{id}/comments", func(c *web.Ctx) error { return nil }).Name("posts.comments")

	u, _ := r.URL("posts.comments", 42, url.Values{"page": {"2"}})
	fmt.Println(u)
	// Output: /posts/42/comments?page=2
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

func TestBlog(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"DB_DATABASE": ":memory:"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := setup(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	h := srv.Router()
	call := func(method, target, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequestWithContext(app.Context(t.Context()), method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	if code, body := call("POST", "/authors", `{"name":"Ada","email":"ada@example.com"}`); code != 201 {
		t.Fatalf("author: %d %s", code, body)
	}
	if code, body := call("POST", "/authors", `{"name":"Ada 2","email":"ada@example.com"}`); code != 422 || !strings.Contains(body, "already been taken") {
		t.Errorf("duplicate email: %d %s", code, body)
	}
	if code, body := call("POST", "/posts", `{"author_id":9,"title":"x","body":"y"}`); code != 422 || !strings.Contains(body, "selected author id is invalid") {
		t.Errorf("unknown author: %d %s", code, body)
	}
	for _, p := range []string{
		`{"author_id":1,"title":"Hello Go","body":"a","tags":["go"],"publish":true}`,
		`{"author_id":1,"title":"Draft","body":"b"}`,
		`{"author_id":1,"title":"Go tips","body":"c","publish":true}`,
	} {
		if code, body := call("POST", "/posts", p); code != 201 {
			t.Fatalf("post: %d %s", code, body)
		}
	}
	code, body := call("GET", "/posts?q=Go&per_page=1", "")
	var page struct {
		Data  []Post `json:"data"`
		Total int64  `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	if page.Total != 2 || len(page.Data) != 1 || page.Data[0].Title != "Go tips" {
		t.Errorf("page = %+v", page)
	}
	call("GET", "/posts/1", "")
	code, body = call("GET", "/posts/1", "")
	if code != 200 || !strings.Contains(body, `"views":2`) || !strings.Contains(body, `"tags":["go"]`) {
		t.Errorf("show: %d %s", code, body)
	}
	if code, _ := call("DELETE", "/posts/1", ""); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := call("GET", "/posts/1", ""); code != 404 {
		t.Errorf("deleted post: %d", code)
	}
	code, body = call("GET", "/stats", "")
	if code != 200 || !strings.Contains(body, `"posts":2`) {
		t.Errorf("stats: %d %s", code, body)
	}
}

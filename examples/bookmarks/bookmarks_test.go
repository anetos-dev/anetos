// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"testing"

	"bookmarks/app/handlers"
)

// region: test-crud

// TestBookmarks creates, lists, shows, replaces, archives and deletes a
// bookmark through the API, with a login's token (auth_test.go's
// authRegister).
func TestBookmarks(t *testing.T) {
	app, _ := authRegister(t)
	app.GetJSON("/api/v1/bookmarks").AssertOK().AssertJSONPath("total", 0).AssertJSONPath("data", []any{})
	app.PostJSON("/api/v1/bookmarks", map[string]any{}).AssertValidationErrors("url", "title")
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "not a link", "title": "x"}).AssertValidationErrors("url")

	body := map[string]any{"url": "https://go.dev/doc/effective_go", "title": "Effective Go", "notes": "Read again"}
	var created handlers.BookmarkResponse
	res := app.PostJSON("/api/v1/bookmarks", body).AssertCreated().JSON(&created)
	path := fmt.Sprintf("/api/v1/bookmarks/%d", created.ID)
	res.AssertHeader("Location", path).AssertJSONPath("title", "Effective Go").AssertJSONPath("archived", false)

	app.GetJSON(path).AssertOK().AssertJSONPath("id", created.ID)
	app.GetJSON("/api/v1/bookmarks?sort=title").AssertOK().AssertJSONPath("total", 1).AssertJSONPath("data.0.id", created.ID)
	app.GetJSON("/api/v1/bookmarks?sort=nonsense").AssertValidationErrors("sort")
	app.PutJSON(path, map[string]any{"url": "https://go.dev/doc/effective_go", "title": "Effective Go, again"}).
		AssertOK().AssertJSONPath("title", "Effective Go, again").AssertJSONPath("notes", "")

	// Archiving answers 204; the list filters on it.
	app.PostJSON(path+"/archive", nil).AssertNoContent()
	app.GetJSON("/api/v1/bookmarks?archived=true").AssertOK().AssertJSONPath("total", 1)
	app.GetJSON("/api/v1/bookmarks?archived=false").AssertOK().AssertJSONPath("total", 0)

	app.DeleteJSON(path).AssertNoContent()
	app.GetJSON(path).AssertNotFound()
	app.DeleteJSON(path).AssertStatus(http.StatusNotFound)
}

// endregion

// region: test-owner
// A user sees their own bookmarks only: another's is a 404.
func TestBookmarksAreTheUsers(t *testing.T) {
	app, _ := authRegister(t)
	var ada handlers.BookmarkResponse
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "https://ada.example.com", "title": "Ada's"}).
		AssertCreated().JSON(&ada)

	var bob handlers.SignInResponse
	app.PostJSON("/api/v1/register", map[string]any{
		"name": "Bob", "email": "bob@example.com", "password": "correct horse", "password_confirmation": "correct horse",
	}).AssertCreated().JSON(&bob)
	app.WithHeader("Authorization", "Bearer "+bob.Token)
	app.GetJSON("/api/v1/bookmarks").AssertOK().AssertJSONPath("total", 0)
	path := fmt.Sprintf("/api/v1/bookmarks/%d", ada.ID)
	app.GetJSON(path).AssertNotFound()
	app.PutJSON(path, map[string]any{"url": "https://bob.example.com", "title": "Bob's"}).AssertNotFound()
	app.PostJSON(path+"/archive", nil).AssertNotFound()
	app.DeleteJSON(path).AssertNotFound()
}

// endregion

// region: test-abilities
// A token made for reading reads, and can't change bookmarks: 403.
// Without a token, 401.
func TestBookmarkAbilities(t *testing.T) {
	app, _ := authRegister(t)
	var mine handlers.BookmarkResponse
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "https://go.dev", "title": "Go"}).AssertCreated().JSON(&mine)
	path := fmt.Sprintf("/api/v1/bookmarks/%d", mine.ID)
	var reader handlers.NewTokenResponse
	app.PostJSON("/api/v1/tokens", map[string]any{
		"name": "reader", "abilities": []string{"bookmarks:read"}, "password": "correct horse",
	}).AssertCreated().JSON(&reader)

	app.WithHeader("Authorization", "Bearer "+reader.Token)
	app.GetJSON("/api/v1/bookmarks").AssertOK()
	app.GetJSON(path).AssertOK()
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "https://go.dev", "title": "Go"}).AssertForbidden()
	app.PutJSON(path, map[string]any{"url": "https://go.dev", "title": "Go!"}).AssertForbidden()
	app.PostJSON(path+"/archive", nil).AssertForbidden()
	app.DeleteJSON(path).AssertForbidden()

	app.WithHeader("Authorization", "")
	app.GetJSON("/api/v1/bookmarks").AssertStatus(http.StatusUnauthorized)
}

// endregion

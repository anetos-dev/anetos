// SPDX-License-Identifier: Apache-2.0

package naming

import "testing"

func TestNaming(t *testing.T) {
	for in, want := range map[string]string{
		"AuthorID": "author_id", "HTTPStatus": "http_status", "UserIDs": "user_ids", "ID": "id",
		"IDs": "ids", "Title": "title", "Page2Title": "page2_title", "APIKey": "api_key", "createdAt": "created_at",
	} {
		if got := Snake(in); got != want {
			t.Errorf("Snake(%s) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"post": "posts", "category": "categories", "day": "days", "box": "boxes", "match": "matches",
		"person": "people", "blog_post": "blog_posts", "user_status": "user_statuses", "news": "news",
		"bus": "buses", "posts": "posts", "class": "classes", "alias": "aliases", "axis": "axes",
		"analysis": "analyses", "hero": "heroes", "matrix": "matrices", "settings": "settings",
	} {
		if got := Plural(in); got != want {
			t.Errorf("Plural(%s) = %s, want %s", in, got, want)
		}
	}
}

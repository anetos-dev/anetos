// SPDX-License-Identifier: Apache-2.0

package dbutil

import (
	"strings"
	"testing"
)

func TestIndexName(t *testing.T) {
	if got := IndexName("Blog.Posts", []string{"author-id"}, "index"); got != "blog_posts_author_id_index" {
		t.Errorf("IndexName = %q", got)
	}
	long := strings.Repeat("t", 60)
	a, b := IndexName(long, []string{"search"}, "bm25"), IndexName(long, []string{"search"}, "index")
	if len(a) != MaxIdent || len(b) != MaxIdent || a == b || !strings.HasPrefix(a, long[:MaxIdent-9]) {
		t.Errorf("shortened names: %q, %q", a, b)
	}
	if a != IndexName(long, []string{"search"}, "bm25") {
		t.Error("not stable")
	}
}

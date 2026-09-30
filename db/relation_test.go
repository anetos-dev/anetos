// SPDX-License-Identifier: Apache-2.0

package db

import (
	"reflect"
	"strings"
	"testing"
)

type relAuthor struct {
	Model
	Name     string
	Posts    []relBlogPost `rel:"has_many"`
	Profile  *relProfile   `rel:"has_one,fk=owner_id"`
	Articles []relBlogPost `rel:"has_many,fk=writer_id,local=id"`
}

type relProfile struct {
	Model
	OwnerID int64
}

type relBlogPost struct {
	Model
	RelAuthorID int64
	WriterID    int64
	Author      *relAuthor `rel:"belongs_to,fk=rel_author_id"`
	RelAuthor   *relAuthor `rel:"belongs_to"`
	Tags        []relTag   `rel:"many_to_many"`
	Labels      []relTag   `rel:"many_to_many,pivot=post_labels,fk=post,related_fk=label"`
}

type relTag struct {
	Model
	Label string
}

func TestRelationKeys(t *testing.T) {
	resolved := func(owner reflect.Type, field string) *relMeta {
		t.Helper()
		m, err := metaOf(owner)
		if err != nil {
			t.Fatal(err)
		}
		r := m.rels[field]
		if r == nil {
			t.Fatalf("%s.%s: no relation", owner, field)
		}
		if err := r.resolve(m); err != nil {
			t.Fatal(err)
		}
		return r
	}
	author, post := reflect.TypeFor[relAuthor](), reflect.TypeFor[relBlogPost]()
	for _, c := range []struct {
		owner       reflect.Type
		field       string
		parent, rel string
	}{
		{author, "Posts", "id", "rel_author_id"},
		{author, "Profile", "id", "owner_id"},
		{author, "Articles", "id", "writer_id"},
		{post, "Author", "rel_author_id", "id"},
		{post, "RelAuthor", "rel_author_id", "id"},
	} {
		r := resolved(c.owner, c.field)
		if got := r.owner.cols[r.parentKey].name + " " + r.rm.cols[r.relatedKey].name; got != c.parent+" "+c.rel {
			t.Errorf("%s.%s keys: %s", c.owner.Name(), c.field, got)
		}
	}
	tags := resolved(post, "Tags")
	if tags.pivot != "rel_blog_post_rel_tag" || tags.pivotFK != "rel_blog_post_id" || tags.pivotRelFK != "rel_tag_id" {
		t.Errorf("default pivot: %s %s %s", tags.pivot, tags.pivotFK, tags.pivotRelFK)
	}
	labels := resolved(post, "Labels")
	if labels.pivot != "post_labels" || labels.pivotFK != "post" || labels.pivotRelFK != "label" {
		t.Errorf("pivot options: %s %s %s", labels.pivot, labels.pivotFK, labels.pivotRelFK)
	}
	// Relation fields aren't columns.
	m, _ := metaOf(post)
	for _, c := range m.cols {
		if c.name == "author" || c.name == "tags" {
			t.Errorf("relation field mapped to column %s", c.name)
		}
	}
}

func TestRelationTagErrors(t *testing.T) {
	type unknownKind struct {
		Model
		Posts []relBlogPost `rel:"owns_many"`
	}
	type badOption struct {
		Model
		Posts []relBlogPost `rel:"has_many,pivot=x"`
	}
	type pointerHasMany struct {
		Model
		Posts *relBlogPost `rel:"has_many"`
	}
	type sliceBelongsTo struct {
		Model
		Author []relAuthor `rel:"belongs_to"`
	}
	type bothTags struct {
		Model
		Author *relAuthor `db:"author" rel:"belongs_to"`
	}
	type missingColumn struct {
		Model
		Author *relAuthor `rel:"belongs_to"` // no author_id column
	}
	for _, c := range []struct {
		t    reflect.Type
		want string
	}{
		{reflect.TypeFor[unknownKind](), "unknown relation"},
		{reflect.TypeFor[badOption](), "bad has_many option"},
		{reflect.TypeFor[pointerHasMany](), "slice of models"},
		{reflect.TypeFor[sliceBelongsTo](), "pointer to a model"},
		{reflect.TypeFor[bothTags](), "not both"},
	} {
		if _, err := metaOf(c.t); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.t, err, c.want)
		}
	}
	m, err := metaOf(reflect.TypeFor[missingColumn]())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.rels["Author"].resolve(m); err == nil || !strings.Contains(err.Error(), `no column "author_id"`) {
		t.Errorf("missing fk column: %v", err)
	}
	if err := RelOf[relAuthor, relTag]("Posts").Err(); err == nil || !strings.Contains(err.Error(), "holds") {
		t.Errorf("RelOf with the wrong type: %v", err)
	}
}

func TestSelfManyToManyNeedsKeys(t *testing.T) {
	type friend struct {
		Model
		Friends []friend `rel:"many_to_many"`
	}
	m, err := metaOf(reflect.TypeFor[friend]())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.rels["Friends"].resolve(m); err == nil || !strings.Contains(err.Error(), "both pivot columns") {
		t.Errorf("self many_to_many: %v", err)
	}
}

func TestKeyOf(t *testing.T) {
	i32, i64 := int32(7), int64(7)
	for _, v := range []any{7, int8(7), uint(7), &i32, &i64} {
		if k, ok := keyOf(reflect.ValueOf(v)); !ok || k != int64(7) {
			t.Errorf("keyOf(%T) = %v %v", v, k, ok)
		}
	}
	var nilPtr *int64
	if _, ok := keyOf(reflect.ValueOf(nilPtr)); ok {
		t.Error("nil pointer is a key")
	}
	if k, _ := keyOf(reflect.ValueOf([]byte("ab"))); k != "ab" {
		t.Errorf("[]byte key = %v", k)
	}
}

---
title: Relations and eager loading
since: v0.1.1
---

# Relations and eager loading

Declare how models relate (a post belongs to an author, an author has many
posts, posts have many tags), load related rows in one query per relation,
and filter rows by what they relate to.

## Before you start

[Define your models](models.md) and generate their typed columns with
[`anetos gen`](code-generation.md). The foreign key columns must exist in
your tables ([migrations](migrations.md)).

## Steps

### 1. Declare the relations

A relation is a field with a `rel` tag: a pointer for `belongs_to` and
`has_one`, a slice for `has_many` and `many_to_many`:

```go
// Author writes posts.
type Author struct {
	db.Model        // id, created_at, updated_at
	Name     string `db:"name" json:"name"`
	Email    string `db:"email" json:"email"`

	Posts []Post `rel:"has_many" json:"posts,omitzero"` // posts.author_id; loaded with With or Load
}

// Post is a blog post. Deleting one only marks it deleted.
type Post struct {
	db.Model
	db.SoftDeletes            // deleted_at
	AuthorID       int64      `db:"author_id" json:"author_id"`
	Title          string     `db:"title" json:"title"`
	Body           string     `db:"body" json:"body"`
	Tags           []string   `db:"tags,json" json:"tags"` // stored as JSON text
	Views          int        `db:"views" json:"views"`
	PublishedAt    *time.Time `db:"published_at" json:"published_at"` // nullable

	Author *Author `rel:"belongs_to" json:"author,omitzero"` // by AuthorID
}

// AuthorCols and PostCols, the typed columns of the models, and
// AuthorRels and PostRels, their relations, are in models_gen.go, written
// by `go tool anetos gen` (or go generate).
//
//go:generate go tool anetos gen
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `models`.)

The keys follow conventions, which tag options override:

| Kind | Field | Keys by default | Options |
|---|---|---|---|
| `belongs_to` | `Author *Author` | this model's `author_id` → the author's primary key | `fk=writer_id`, `references=uuid` |
| `has_one` | `Profile *Profile` | `profiles.author_id` → this model's primary key | `fk=`, `local=` |
| `has_many` | `Posts []Post` | `posts.author_id` → this model's primary key | `fk=`, `local=` |
| `many_to_many` | `Tags []Tag` | pivot table `post_tag` (both names, sorted) with `post_id` and `tag_id` | `pivot=post_tags`, `fk=post_id`, `related_fk=tag_id` |

A many-to-many relation needs its pivot table, created in a migration,
with a primary key (or unique index) on the two columns, which `Attach`
relies on:

```go
// illustrative
type Post struct {
	db.Model
	Tags []Tag `rel:"many_to_many"` // post_tag (post_id, tag_id)
}

return s.Create("post_tag", func(t *migrate.Table) {
	t.ForeignID("post_id").Constrained().CascadeOnDelete()
	t.ForeignID("tag_id").Constrained().CascadeOnDelete()
	t.Primary("post_id", "tag_id")
})
```

Run `go tool anetos gen` (or `go generate ./...`): next to `PostCols`, it
writes `PostRels`, one handle per relation field (`PostRels.Author`,
`AuthorRels.Posts`).

### 2. Load them with the query

`With` loads relations of the rows a query returns, with one extra query
per relation however many rows there are:

```go
func (Blog) ListPosts(c *web.Ctx, in ListPosts) (db.Page[Post], error) {
	q := db.Query[Post](c).Where(PostCols.PublishedAt.NotNull())
	if in.Search != "" {
		q = q.Where(PostCols.Title.Like("%" + in.Search + "%"))
	}
	if in.Author != nil {
		q = q.Where(PostCols.AuthorID.Eq(*in.Author))
	}
	return q.With(PostRels.Author).Latest().Paginate(in.Page, in.PerPage) // one query for all the authors
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `list-posts`.)

`Get`, `First`, `Find`, `Paginate` and `CursorPaginate` load relations.
Load several at once, and relations of the related rows with `With` on
the handle:

```go
// illustrative
posts, err := db.Query[Post](ctx).
	With(PostRels.Author, PostRels.Comments.With(CommentRels.Author), PostRels.Tags).
	Get()
```

A relation that finds nothing is `nil` (pointers) or an empty slice
(slices). A field that wasn't loaded stays `nil`. With `json:",omitzero"`,
as in the models above, an unloaded relation is left out of JSON while a
loaded empty list encodes as `[]`.

### 3. Or load them afterwards

`db.Load` loads relations of one row, `db.LoadMany` of a slice, in place:

```go
// illustrative
post, err := db.Find[Post](ctx, id)
err = db.Load(ctx, &post, PostRels.Author, PostRels.Tags)

posts, err := db.Query[Post](ctx).Get()
err = db.LoadMany(ctx, posts, PostRels.Author)
```

### 4. Limit and order the related rows

`Where`, `OrderBy` and `WithTrashed` on a handle apply to the relation's
query. Without `OrderBy`, related rows come in primary key order (and a
`has_one` gets the first match); soft-deleted related rows are left out
unless `WithTrashed`. A many-to-many query joins the pivot table, so
qualify columns the pivot also has: `TagCols.Name.Of("tags")`.

```go
// illustrative
db.Query[Post](ctx).With(
	PostRels.Comments.Where(CommentCols.Approved.Eq(true)).OrderBy(CommentCols.CreatedAt.Desc()),
)
```

### 5. Filter by relations

`WhereHas` keeps the rows that have a related row matching the conditions,
`WhereDoesntHave` those that don't. Combined with `With`:

```go
// ListAuthors returns the authors with a published post, with those posts.
func (Blog) ListAuthors(c *web.Ctx, _ struct{}) ([]Author, error) {
	published := PostCols.PublishedAt.NotNull()
	return db.Query[Author](c).
		WhereHas(AuthorRels.Posts, published).
		With(AuthorRels.Posts.Where(published).OrderBy(PostCols.PublishedAt.Desc())).
		OrderBy(AuthorCols.Name.Asc()).
		Get()
}
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `where-has`.)

The conditions use the related model's columns. `WhereHas` is an
`EXISTS` subquery, so it adds no rows and needs no `Distinct`.

### 6. Link many-to-many rows

`db.Attach`, `db.Detach`, `db.DetachAll` and `db.Sync` write the pivot
table of a `many_to_many` relation, by the related rows' primary keys:

```go
// illustrative
err := db.Attach(ctx, &post, PostRels.Tags, goTag.ID, webTag.ID) // adds missing links
err = db.Detach(ctx, &post, PostRels.Tags, webTag.ID)           // no ids: nothing to do
err = db.DetachAll(ctx, &post, PostRels.Tags)                   // every link of the post
err = db.Sync(ctx, &post, PostRels.Tags, goTag.ID)              // exactly these (none: none)
```

Each runs in a transaction (joining yours, if the context has one).
`Attach` skips links that exist, also ones another request adds at the
same moment.

## Complete example

[`examples/database`](../../../examples/database/main.go) loads each
post's author (`GET /posts`, `GET /posts/{id}`) and lists the authors with
published posts (`GET /authors`).

## How it works

Relations are loaded **only when you ask**: there is no lazy loading, so
reading `post.Author` never runs a query behind your back, and the N+1
pattern (one query per row) has to be written out to happen, and then
it is [reported](n-plus-one.md) in development and tests. `With`
collects the keys of the rows it got, runs one `WHERE key IN (…)` query per
relation (in chunks of 1,000 keys), and hands out the results; a
many-to-many relation reads the pivot table first. Rows that belong to the
same parent share one value: posts by the same author get the same
`*Author`.

Handles are typed: `PostRels.Author` is a `db.Rel[Post, Author]`, and
`With` on a `Post` query only takes `Post` relations. Handles written by
hand (`db.RelOf[Post, Author]("Author")`) are checked when used: a wrong
field or type is an error from the query.

> **Coming from Laravel?** `rel:"has_many"` is `hasMany`, `With` is
> `with()`, `WhereHas` is `whereHas()`, and `Attach`/`Detach`/`Sync` are
> the `belongsToMany` methods. Nested loads chain handles
> (`PostRels.Comments.With(CommentRels.Author)`) instead of dotted strings
> (`'comments.author'`), and there are no dynamic properties: an unloaded
> relation is `nil`, never a hidden query.

## Testing it

Create rows with [factories](testing.md#4-make-rows-with-factories) and
check what a page or API returns:

```go
func TestAuthorsWithPublishedPosts(t *testing.T) {
	app := anetostest.New(t, setup)
	now := time.Now().UTC()
	ada := anetostest.Create(app, Authors.With(func(a *Author) { a.Name = "Ada" }))
	grace := anetostest.Create(app, Authors.With(func(a *Author) { a.Name = "Grace" }))
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID, p.Title, p.PublishedAt = ada.ID, "Published", &now }))
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID, p.Title = ada.ID, "Draft" }))
	anetostest.Create(app, Posts.With(func(p *Post) { p.AuthorID = grace.ID })) // Grace has drafts only

	app.GetJSON("/authors").
		AssertOK().
		AssertJSONPath("0.name", "Ada").
		AssertJSONPath("0.posts.0.title", "Published").
		AssertDontSee("Grace", "Draft")
}
```

(Copied from [`examples/database/main_test.go`](../../../examples/database/main_test.go), region `test-relations`.)

To check that a page loads its relations without an N+1, call
`app.AssertNoRepeatedQueries()` after its requests: see
[Find N+1 queries](n-plus-one.md#3-keep-it-fixed-with-a-test).

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `has no column "author_id"` | The key column's name differs from the convention | Name it in the tag: `rel:"belongs_to,fk=writer_id"` |
| `has no column "blog_post_id" (fk)` | A `has_many` or `has_one` looks for `<type>_id` on the related model | `rel:"has_many,fk=post_id"` |
| `no such table: post_tag` | The pivot table has another name, or no migration | `rel:"many_to_many,pivot=post_tags"`, and create it |
| `All can't load relations` | Streaming can't batch rows | Use `Get` or `Paginate`, or `db.LoadMany` on batches |
| `a relation is a pointer to a model` / `a slice of models` | The field's type doesn't match the kind | `*Author` for `belongs_to`/`has_one`, `[]Post` for `has_many`/`many_to_many` |
| Changing `post.Author` changes other posts' authors | Posts by one author share the loaded `*Author` | Copy the value before changing it |
| An API returns `"author": null` for unloaded rows | The field isn't loaded | Load it, or add `omitzero` to its json tag |
| `relation … is loaded twice` | The same relation passed twice to `With` | Pass it once, with all its nested relations |
| A `repeated query: an N+1?` warning | A query in a loop, one per row | Load the relation with `With` or `db.LoadMany`: [Find N+1 queries](n-plus-one.md) |
| `both pivot columns are "user_id"` | A many-to-many from a model to itself | Name them: `rel:"many_to_many,pivot=friendships,fk=user_id,related_fk=friend_id"` |
| An `ON CONFLICT` error from `Attach` (no matching unique constraint) | The pivot table has no primary key or unique index on its two columns | Add one in a migration |
| MySQL error 1093 from `Update` or `Delete` with `WhereHas` | MySQL can't read a table it writes in a subquery (a relation to the same table) | Select the keys first, then update `WhereIn` them |

## Next steps

- [Query the database](queries.md)
- [Models reference](../reference/models.md#relations)
- [Code generation](code-generation.md)

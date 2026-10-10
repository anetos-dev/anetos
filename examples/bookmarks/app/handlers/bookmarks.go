// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"cmp"
	"strings"
	"time"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"

	"bookmarks/app/models"
)

// Bookmarks serves the bookmarks API (anetos make:crud): the list, a
// bookmark, and creating, replacing and deleting one. Yours to change;
// routes/bookmarks.go routes them.
type Bookmarks struct{}

// BookmarkResponse is how the API shows a bookmark: the fields chosen
// here, so a new column of the bookmarks table never shows by accident.
type BookmarkResponse struct {
	ID        int64     `json:"id"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Notes     string    `json:"notes"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// bookmarkResponse is row as the API shows it.
func bookmarkResponse(row models.Bookmark) BookmarkResponse {
	res := BookmarkResponse{
		ID:        row.ID,
		URL:       row.URL,
		Title:     row.Title,
		Notes:     row.Notes,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	return res
}

// BookmarkList is the list's query: ?page=, ?per_page= (20 by default,
// at most 100), ?sort= (a column, - first for descending; -id, the newest
// first, by default), and exact filters by field
// (?url=Example+url; an empty value doesn't filter).
type BookmarkList struct {
	Page     int     `query:"page"`                              // 1 if absent or below
	PerPage  int     `query:"per_page" validate:"min:0|max:100"` // 20 if absent
	Sort     string  `query:"sort" validate:"in:id,-id,created_at,-created_at,updated_at,-updated_at,url,-url,title,-title,archived,-archived"`
	URL      *string `query:"url"`
	Title    *string `query:"title"`
	Archived *bool   `query:"archived"`
}

// bookmarkSorts are the orders ?sort= may name: ascending, then
// descending.
var bookmarkSorts = map[string][2]db.Order{
	"id":         {models.BookmarkCols.ID.Asc(), models.BookmarkCols.ID.Desc()},
	"created_at": {models.BookmarkCols.CreatedAt.Asc(), models.BookmarkCols.CreatedAt.Desc()},
	"updated_at": {models.BookmarkCols.UpdatedAt.Asc(), models.BookmarkCols.UpdatedAt.Desc()},
	"url":        {models.BookmarkCols.URL.Asc(), models.BookmarkCols.URL.Desc()},
	"title":      {models.BookmarkCols.Title.Asc(), models.BookmarkCols.Title.Desc()},
	"archived":   {models.BookmarkCols.Archived.Asc(), models.BookmarkCols.Archived.Desc()},
}

// BookmarkID reads {id} from the path.
type BookmarkID struct {
	ID int64 `path:"id"`
}

// BookmarkInput is a bookmark sent to the API, as JSON. Its validate
// tags are checked before the handler runs: an invalid one is a 422 with
// the fields' errors.
type BookmarkInput struct {
	ID       int64  `path:"id"` // when replacing
	URL      string `json:"url" validate:"required|url|max:255"`
	Title    string `json:"title" validate:"required|max:255"`
	Notes    string `json:"notes" validate:"max:10000"`
	Archived bool   `json:"archived"`
}

// fill copies the input to the row.
func (in BookmarkInput) fill(row *models.Bookmark) {
	row.URL = in.URL
	row.Title = in.Title
	row.Notes = in.Notes
	row.Archived = in.Archived
}

// region: owner

// owner is the logged-in user's ID: a request sees their bookmarks only.
func owner(c *web.Ctx) (int64, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return 0, err
	}
	return u.ID, nil
}

// find returns the user's bookmark id. Another user's is a 404, like
// one that doesn't exist (db.ErrNotFound): it isn't theirs to know of.
func find(c *web.Ctx, id int64) (models.Bookmark, error) {
	userID, err := owner(c)
	if err != nil {
		return models.Bookmark{}, err
	}
	return db.Query[models.Bookmark](c).Where(models.BookmarkCols.UserID.Eq(userID)).Find(id)
}

// endregion

// Index lists the bookmarks, a page at a time.
func (Bookmarks) Index(c *web.Ctx, in BookmarkList) (db.Page[BookmarkResponse], error) {
	// region: index
	userID, err := owner(c)
	if err != nil {
		return db.Page[BookmarkResponse]{}, err
	}
	q := db.Query[models.Bookmark](c).Where(models.BookmarkCols.UserID.Eq(userID))
	// endregion
	if in.URL != nil && *in.URL != "" {
		q = q.Where(models.BookmarkCols.URL.Eq(*in.URL))
	}
	if in.Title != nil && *in.Title != "" {
		q = q.Where(models.BookmarkCols.Title.Eq(*in.Title))
	}
	if in.Archived != nil {
		q = q.Where(models.BookmarkCols.Archived.Eq(*in.Archived))
	}
	key := strings.TrimPrefix(in.Sort, "-")
	order, ok := bookmarkSorts[key]
	if !ok {
		key, order, in.Sort = "id", bookmarkSorts["id"], "-id"
	}
	desc := 0
	if strings.HasPrefix(in.Sort, "-") {
		desc = 1
	}
	q = q.OrderBy(order[desc])
	if key != "id" {
		q = q.OrderBy(models.BookmarkCols.ID.Desc()) // then the ID: a stable order across pages
	}
	page, err := q.Paginate(in.Page, cmp.Or(in.PerPage, 20))
	if err != nil {
		return db.Page[BookmarkResponse]{}, err
	}
	return db.MapPage(page, bookmarkResponse), nil
}

// Show answers a bookmark; one that doesn't exist is a 404
// (db.ErrNotFound).
func (Bookmarks) Show(c *web.Ctx, in BookmarkID) (BookmarkResponse, error) {
	row, err := find(c, in.ID)
	if err != nil {
		return BookmarkResponse{}, err
	}
	return bookmarkResponse(row), nil
}

// Create saves a new bookmark and answers it (201: the route's Status),
// with its address in the Location header.
func (Bookmarks) Create(c *web.Ctx, in BookmarkInput) (BookmarkResponse, error) {
	// region: create
	userID, err := owner(c)
	if err != nil {
		return BookmarkResponse{}, err
	}
	row := models.Bookmark{UserID: userID}
	// endregion
	in.fill(&row)
	if err := db.Create(c, &row); err != nil {
		return BookmarkResponse{}, err
	}
	if loc, err := c.URL("api.bookmarks.show", row.ID); err == nil {
		c.SetHeader("Location", loc)
	}
	return bookmarkResponse(row), nil
}

// Update replaces a bookmark with the input: every field.
func (Bookmarks) Update(c *web.Ctx, in BookmarkInput) (BookmarkResponse, error) {
	row, err := find(c, in.ID)
	if err != nil {
		return BookmarkResponse{}, err
	}
	in.fill(&row)
	if err := db.Update(c, &row); err != nil {
		return BookmarkResponse{}, err
	}
	return bookmarkResponse(row), nil
}

// Delete deletes a bookmark: 204.
func (Bookmarks) Delete(c *web.Ctx, in BookmarkID) (web.Empty, error) {
	row, err := find(c, in.ID)
	if err != nil {
		return web.Empty{}, err
	}
	return web.Empty{}, db.Delete(c, &row)
}

// region: archive

// Archive marks a bookmark archived: 204, nothing to answer.
func (Bookmarks) Archive(c *web.Ctx, in BookmarkID) (web.Empty, error) {
	row, err := find(c, in.ID)
	if err != nil {
		return web.Empty{}, err
	}
	row.Archived = true
	return web.Empty{}, db.Update(c, &row)
}

// endregion

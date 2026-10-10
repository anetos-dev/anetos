package handlers

// region: imports
import (
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"

	"tracker/app/models"
	"tracker/views"
)

// endregion

// Issues serves the issue pages.
type Issues struct{}

// region: index

// IssueList is the input of GET /issues: the page number, from ?page=.
type IssueList struct {
	Page int `query:"page"`
}

// Index lists the issues, newest first, 20 a page.
func (Issues) Index(c *web.Ctx, in IssueList) (web.Responder, error) {
	page, err := db.Query[models.Issue](c).
		With(models.IssueRels.Author).
		OrderBy(models.IssueCols.ID.Desc()).
		Paginate(in.Page, 20)
	if err != nil {
		return nil, err
	}
	return web.View(views.IssuesPage(page)), nil
}

// endregion

// region: create

// IssueInput is the issue form. Its validate tags are checked before the
// handler runs: an invalid form goes back with its errors.
type IssueInput struct {
	Title string `json:"title" validate:"required|max:200"`
	Body  string `json:"body" validate:"max:10000"`
}

// New shows the form for a new issue.
func (Issues) New(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.IssueForm(models.Issue{}))
}

// Create opens an issue by the logged-in user.
func (Issues) Create(c *web.Ctx, in IssueInput) (web.Responder, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return nil, err
	}
	issue := models.Issue{Title: in.Title, Body: in.Body, Status: "open", AuthorID: u.ID}
	if err := db.Create(c, &issue); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Issue opened.")
	return web.RedirectRoute("issues.index"), nil
}

// endregion

// region: edit

// IssueID reads {id} from the path.
type IssueID struct {
	ID int64 `path:"id"`
}

// UpdateIssue is the input of PUT /issues/{id}: the path and the form.
type UpdateIssue struct {
	IssueID
	IssueInput
}

// own returns the issue if the logged-in user wrote it: others get a
// 403, and an issue that doesn't exist is a 404 (db.ErrNotFound).
func own(c *web.Ctx, id int64) (models.Issue, error) {
	issue, err := db.Find[models.Issue](c, id)
	if err != nil {
		return issue, err
	}
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return issue, err
	}
	if issue.AuthorID != u.ID {
		return issue, web.Error(http.StatusForbidden, "Only the issue's author can change it.")
	}
	return issue, nil
}

// Edit shows the form to edit an issue.
func (Issues) Edit(c *web.Ctx, in IssueID) (web.Responder, error) {
	issue, err := own(c, in.ID)
	if err != nil {
		return nil, err
	}
	return web.View(views.IssueForm(issue)), nil
}

// Update saves the issue.
func (Issues) Update(c *web.Ctx, in UpdateIssue) (web.Responder, error) {
	issue, err := own(c, in.ID)
	if err != nil {
		return nil, err
	}
	issue.Title, issue.Body = in.Title, in.Body
	if err := db.Update(c, &issue); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Issue saved.")
	return web.RedirectRoute("issues.index"), nil
}

// endregion

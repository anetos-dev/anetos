package handlers

// region: imports
import (
	"context"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/web"

	"tracker/app/models"
	"tracker/views"
)

// endregion

// region: show

// Show is an issue's page: the issue and its comments, oldest first.
func (Issues) Show(c *web.Ctx, in IssueID) (web.Responder, error) {
	issue, err := db.Query[models.Issue](c).With(models.IssueRels.Author).Find(in.ID)
	if err != nil {
		return nil, err // db.ErrNotFound: 404
	}
	comments, err := db.Query[models.Comment](c).
		Where(models.CommentCols.IssueID.Eq(issue.ID)).
		With(models.CommentRels.Author).
		OrderBy(models.CommentCols.ID.Asc()).
		Get()
	if err != nil {
		return nil, err
	}
	return web.View(views.IssuePage(issue, comments)), nil
}

// endregion

// region: comment

// CommentInput is a new comment on the issue {id}.
type CommentInput struct {
	IssueID
	Body string `json:"body" validate:"required|max:10000"`
}

// Comment adds a comment, and emits CommentAdded. With htmx, the comment
// is sent back to add to the page; otherwise the browser reloads it.
func (Issues) Comment(c *web.Ctx, in CommentInput) (web.Responder, error) {
	issue, err := db.Find[models.Issue](c, in.ID)
	if err != nil {
		return nil, err
	}
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return nil, err
	}
	comment := models.Comment{IssueID: issue.ID, AuthorID: u.ID, Body: in.Body}
	err = db.Tx(c, func(ctx context.Context) error { // the comment and its event, or neither
		if err := db.Create(ctx, &comment); err != nil {
			return err
		}
		return events.Emit(ctx, models.CommentAdded{CommentID: comment.ID})
	})
	if err != nil {
		return nil, err
	}
	if c.IsHTMX() {
		comment.Author = u
		return web.View(views.CommentItem(comment)), nil
	}
	return web.RedirectRoute("issues.show", issue.ID), nil
}

// endregion

// region: status

// StatusInput closes or reopens the issue {id}.
type StatusInput struct {
	IssueID
	Status string `json:"status" validate:"required|in:open,closed"`
}

// SetStatus closes or reopens an issue (its author only), and goes back
// to its page.
func (Issues) SetStatus(c *web.Ctx, in StatusInput) (web.Responder, error) {
	issue, err := own(c, in.ID)
	if err != nil {
		return nil, err
	}
	issue.Status = in.Status
	if err := db.Update(c, &issue); err != nil {
		return nil, err
	}
	return web.RedirectRoute("issues.show", issue.ID), nil
}

// endregion

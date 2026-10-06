// Package listeners holds the app's event listeners, added in main.go.
package listeners

// region: imports
import (
	"context"
	"errors"
	"fmt"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/mailer"

	"tracker/app/mailers"
	"tracker/app/models"
)

// endregion

// region: listener

// EmailAuthor emails an issue's author about a new comment, unless they
// wrote it. It runs as a queue job (events.OnQueued), after the comment
// is committed: retried if the mail server fails.
func EmailAuthor(ctx context.Context, e models.CommentAdded) error {
	comment, err := db.Query[models.Comment](ctx).With(models.CommentRels.Author).Find(e.CommentID)
	if errors.Is(err, db.ErrNotFound) {
		return nil // deleted since
	}
	if err != nil {
		return err
	}
	issue, err := db.Query[models.Issue](ctx).With(models.IssueRels.Author).Find(comment.IssueID)
	if err != nil {
		return err
	}
	if issue.AuthorID == comment.AuthorID {
		return nil
	}
	url, err := mailer.URL(ctx, fmt.Sprintf("/issues/%d", issue.ID)) // on APP_URL
	if err != nil {
		return err
	}
	return mailer.Send(ctx, mailers.NewComment{
		Name: issue.Author.Name, Email: issue.Author.Email, Issue: issue.Title,
		By: comment.Author.Name, Body: comment.Body, URL: url,
	})
}

// endregion

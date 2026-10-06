package mailers

// region: imports
import (
	"context"

	"anetos.dev/anetos/mailer"

	"tracker/views"
)

// endregion

// region: mailable

// NewComment tells an issue's author that someone commented on it.
type NewComment struct {
	Name, Email string // the author's
	Issue       string // the issue's title
	By, Body    string // the comment's author and text
	URL         string // the issue's page
}

// Build implements mailer.Mailable.
func (m NewComment) Build(context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: "New comment on " + m.Issue,
		HTML:    views.NewCommentMail(m.Name, m.Issue, m.By, m.Body, m.URL),
	}, nil
}

// endregion

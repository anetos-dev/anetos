// SPDX-License-Identifier: Apache-2.0

package mailers

import (
	"context"

	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/mailer"

	"anetos.dev/anetos/examples/tracker/views"
)

// Assigned tells a user an issue was assigned to them.
type Assigned struct {
	Name, Email string
	Ref, Title  string // WEB-12, its title
	By          string // who assigned it
	URL         string // the issue's page
}

// Build implements mailer.Mailable.
func (m Assigned) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: i18n.T(ctx, "mail.assigned.subject", "ref", m.Ref, "title", m.Title),
		HTML:    views.AssignedMail(m.Name, m.Ref, m.Title, m.By, m.URL),
	}, nil
}

// Commented tells a user someone commented on an issue they follow.
type Commented struct {
	Name, Email string
	Ref, Title  string
	By          string // the comment's author
	Body        string // the comment
	URL         string
}

// Build implements mailer.Mailable.
func (m Commented) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: i18n.T(ctx, "mail.commented.subject", "ref", m.Ref, "title", m.Title),
		HTML:    views.CommentedMail(m.Name, m.Ref, m.Title, m.By, m.Body, m.URL),
	}, nil
}

// Digest is a user's morning list of the open issues assigned to them.
type Digest struct {
	Name, Email string
	Issues      []views.DigestLine
	URL         string // the dashboard
}

// Build implements mailer.Mailable.
func (m Digest) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: i18n.Plural(ctx, "mail.digest.subject", len(m.Issues)),
		HTML:    views.DigestMail(m.Name, m.Issues, m.URL),
	}, nil
}

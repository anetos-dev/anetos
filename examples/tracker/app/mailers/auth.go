// SPDX-License-Identifier: Apache-2.0

// Package mailers holds the app's emails: mailables, sent with
// mailer.Send or mailer.Queue, in the language of the context they are
// built with (i18n.ForUser for the recipient's).
package mailers

import (
	"context"

	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/mailer"

	"anetos.dev/anetos/examples/tracker/views"
)

// VerifyEmail asks a new user to confirm their address (anetos make:auth).
type VerifyEmail struct {
	Name, Email string
	URL         string // the verification link
}

// Build implements mailer.Mailable.
func (m VerifyEmail) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}}, // the name is in the body: any text, safe there
		Subject: i18n.T(ctx, "auth.mail.verify.subject"),
		HTML:    views.VerifyEmailMail(m.Name, m.URL),
	}, nil
}

// ResetPassword sends a password reset link (anetos make:auth).
type ResetPassword struct {
	Name, Email string
	URL         string // the reset link
}

// Build implements mailer.Mailable.
func (m ResetPassword) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}}, // the name is in the body: any text, safe there
		Subject: i18n.T(ctx, "auth.mail.reset.subject"),
		HTML:    views.ResetPasswordMail(m.Name, m.URL),
	}, nil
}

// ChangeEmail asks the new address of an account to confirm it (anetos
// make:auth): it becomes the account's once the link is followed.
type ChangeEmail struct {
	Name, Email string // Email is the new address
	URL         string // the confirmation link
}

// Build implements mailer.Mailable.
func (m ChangeEmail) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: i18n.T(ctx, "auth.mail.change_email.subject"),
		HTML:    views.ChangeEmailMail(m.Name, m.URL),
	}, nil
}

// EmailChanging tells an account's address that the user asked to change
// it (anetos make:auth), with a link that undoes the change if it wasn't
// them.
type EmailChanging struct {
	Name, Email string // Email is the current address
	NewEmail    string
	RevertURL   string // the link that undoes the change
}

// Build implements mailer.Mailable.
func (m EmailChanging) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: i18n.T(ctx, "auth.mail.email_changing.subject"),
		HTML:    views.EmailChangingMail(m.Name, m.NewEmail, m.RevertURL),
	}, nil
}

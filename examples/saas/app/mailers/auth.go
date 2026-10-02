// SPDX-License-Identifier: Apache-2.0

// Package mailers holds the app's emails: mailables, sent with
// mailer.Send or mailer.Queue.
package mailers

import (
	"context"

	"anetos.dev/anetos/mailer"

	"anetos.dev/anetos/examples/saas/views"
)

// VerifyEmail asks a new user to confirm their address (anetos make:auth).
type VerifyEmail struct {
	Name, Email string
	URL         string // the verification link
}

// Build implements mailer.Mailable.
func (m VerifyEmail) Build(context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}}, // the name is in the body: any text, safe there
		Subject: "Verify your email address",
		HTML:    views.VerifyEmailMail(m.Name, m.URL),
	}, nil
}

// ResetPassword sends a password reset link (anetos make:auth).
type ResetPassword struct {
	Name, Email string
	URL         string // the reset link
}

// Build implements mailer.Mailable.
func (m ResetPassword) Build(context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}}, // the name is in the body: any text, safe there
		Subject: "Reset your password",
		HTML:    views.ResetPasswordMail(m.Name, m.URL),
	}, nil
}

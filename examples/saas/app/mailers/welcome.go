// SPDX-License-Identifier: Apache-2.0

package mailers

import (
	"context"
	"time"

	"anetos.dev/anetos/mailer"

	"anetos.dev/anetos/examples/saas/views"
)

// Welcome greets a new user, sent by the jobs.SendWelcome job.
type Welcome struct {
	Name, Email string
	TrialEnds   time.Time
	URL         string // the dashboard
}

// Build implements mailer.Mailable.
func (m Welcome) Build(context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: "Welcome to SaaS",
		HTML:    views.WelcomeMail(m.Name, m.TrialEnds.Format("January 2"), m.URL),
	}, nil
}

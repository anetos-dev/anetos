// SPDX-License-Identifier: Apache-2.0

// Package jobs holds the app's queue jobs, registered in setup and run by
// the workers (run --only=workers).
package jobs

import (
	"context"
	"errors"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"

	"anetos.dev/anetos/examples/saas/app/mailers"
	"anetos.dev/anetos/examples/saas/app/models"
)

// region: job

// SendWelcome emails a new user the welcome email. Registration and the
// first login with Google or GitHub dispatch it once the user is
// committed; a worker runs it, with the queue's retries.
type SendWelcome struct {
	UserID int64 `json:"user_id"`
}

// Handle sends the email.
func (j SendWelcome) Handle(ctx context.Context) error {
	u, err := db.Find[models.User](ctx, j.UserID)
	if errors.Is(err, db.ErrNotFound) {
		return queue.Permanent(err) // deleted since: retrying won't help
	}
	if err != nil {
		return err
	}
	url, err := mailer.URL(ctx, "/dashboard")
	if err != nil {
		return err
	}
	m := mailers.Welcome{Name: u.Name, Email: u.Email, URL: url}
	if u.TrialEndsAt != nil {
		m.TrialEnds = *u.TrialEndsAt
	}
	return mailer.Send(ctx, m)
}

// endregion

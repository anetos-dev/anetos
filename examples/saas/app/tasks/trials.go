// SPDX-License-Identifier: Apache-2.0

// Package tasks holds the app's scheduled tasks, run by the scheduler
// (run --only=scheduler).
package tasks

import (
	"context"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"

	"anetos.dev/anetos/examples/saas/app/models"
)

// EndTrials moves users whose trial has ended to the free plan. It runs
// every minute, so a trial ends within a minute of its end, on one
// instance at a time; it changes only what is due when it runs.
func EndTrials(ctx context.Context) error {
	now := anetos.Now(ctx).UTC()
	n, err := db.Query[models.User](ctx).
		Where(models.UserCols.Plan.Eq("trial"), models.UserCols.TrialEndsAt.Lte(&now)).
		Update(models.UserCols.Plan.Set("free"), models.UserCols.TrialEndsAt.Set(nil))
	if err != nil {
		return err
	}
	if n > 0 {
		anetos.Logger(ctx).InfoContext(ctx, "trials ended", "users", n)
	}
	return nil
}
